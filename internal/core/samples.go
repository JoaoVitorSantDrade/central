package core

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"maps"
	"math"
	"slices"
	"strconv"
	"time"
)

const (
	// TipoRecursoSemUso é a mensagem da CENTRAL para o dono de uma concessão
	// que não usa o que pediu (Decisão 7 do plano).
	TipoRecursoSemUso = "recurso.sem_uso"
	// Eventos do medidor.
	EventoMedidorSemLeitura = "medidor.sem_leitura"
	EventoMedidorFalhou     = "medidor.falhou"
)

const (
	// leiturasValidas é quantos intervalos do medidor uma leitura vale para a
	// admissão (Decisão 8 do plano).
	leiturasValidas = 3
	// avisoMedidorACada limita os eventos medidor.sem_leitura e medidor.falhou
	// a um por minuto por recurso.
	avisoMedidorACada = time.Minute
	// GuardarAmostras é quanto tempo as amostras ficam no banco.
	GuardarAmostras = 7 * 24 * time.Hour
	// limparAmostrasACada é de quanto em quanto tempo o Tick apaga as velhas.
	limparAmostrasACada = time.Hour
)

// RecordSample grava uma leitura do medidor do recurso. Quando é o início da
// medição (zero usa o relógio do núcleo). Na mesma transação: atualiza o pico
// das concessões ativas (leitura − reservado, nunca negativo); acompanha o uso
// das que declararam a dimensão de sem_uso e avisa o dono, uma vez por
// concessão, quando o uso fica abaixo do limite por sem_uso.apos (Decisão 7);
// e roda a admissão do recurso. Uma concessão ativada depois de Quando não é
// afetada: a leitura não a viu.
func (c *Core) RecordSample(ctx context.Context, recurso string, s Sample) error {
	spec, ok := c.currentSettings().Resources[recurso]
	if !ok {
		return invalid("recurso %q não está no central.toml", recurso)
	}
	if len(s.Valores) == 0 {
		return invalid("amostra de %s sem valores", recurso)
	}
	for _, d := range slices.Sorted(maps.Keys(s.Valores)) {
		if _, ok := spec.Dimensoes[d]; !ok {
			return invalid("dimensão desconhecida %q no recurso %s", d, recurso)
		}
		if v := s.Valores[d]; math.IsNaN(v) || math.IsInf(v, 0) {
			return invalid("%s precisa ser um número", d)
		}
	}
	if s.Quando.IsZero() {
		s.Quando = c.now()
	}
	quando := s.Quando.UTC().Format(TimeLayout)
	valores, err := json.Marshal(s.Valores)
	if err != nil {
		return err
	}
	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	lacuna, err := sampleGap(ctx, tx, spec, quando, s.Quando)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO amostras (recurso, quando, valores) VALUES (?, ?, ?)`, recurso, quando, string(valores)); err != nil {
		return err
	}
	ativas, err := queryGrants(ctx, tx, grantSelect+` WHERE recurso = ? AND estado = '`+GrantAtiva+`' ORDER BY id`, recurso)
	if err != nil {
		return err
	}
	n := 0
	for _, g := range ativas {
		if g.Inicio != nil && g.Inicio.After(s.Quando) {
			continue
		}
		if err := updatePeak(ctx, tx, spec, g, s.Valores); err != nil {
			return err
		}
		avisou, err := c.checkIdle(ctx, tx, spec, g, s, lacuna)
		if err != nil {
			return err
		}
		if avisou {
			n++
		}
	}
	k, err := c.admitTx(ctx, tx, spec)
	if err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	if n+k > 0 {
		c.notify.Broadcast()
	}
	return nil
}

// updatePeak leva o pico de g ao máximo entre ele e a leitura menos o
// reservado, por dimensão (Decisão 6).
func updatePeak(ctx context.Context, tx *sql.Tx, spec ResourceSpec, g Grant, valores Dims) error {
	pico := maps.Clone(g.Pico)
	if pico == nil {
		pico = Dims{}
	}
	mudou := false
	for d, v := range valores {
		uso := max(v-spec.Reservado[d], 0)
		if atual, ok := pico[d]; !ok || uso > atual {
			pico[d] = uso
			mudou = true
		}
	}
	if !mudou {
		return nil
	}
	b, err := json.Marshal(pico)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE concessoes SET pico = ? WHERE id = ?`, string(b), g.ID)
	return err
}

// checkIdle aplica a regra de concessão sem uso (Decisão 7) a uma ativa g com
// a leitura s: só vale com sem_uso no recurso, para g que declarou valor > 0
// na dimensão e ainda não foi avisada. Uma leitura com uso ≥ limite zera a
// contagem; depois de uma lacuna sem leituras (sampleGap), ela recomeça nesta
// leitura, porque o uso contínuo não foi observado. Diz se avisou o dono.
func (c *Core) checkIdle(ctx context.Context, tx *sql.Tx, spec ResourceSpec, g Grant, s Sample, lacuna bool) (bool, error) {
	su := spec.SemUso
	if su == nil || g.Declarado[su.Dimensao] <= 0 {
		return false, nil
	}
	v, ok := s.Valores[su.Dimensao]
	if !ok {
		return false, nil
	}
	var desde sql.NullString
	var avisado bool
	if err := tx.QueryRowContext(ctx, `SELECT abaixo_desde, sem_uso_avisado FROM concessoes WHERE id = ?`, g.ID).Scan(&desde, &avisado); err != nil {
		return false, err
	}
	if avisado {
		return false, nil
	}
	uso := v - spec.Reservado[su.Dimensao]
	if uso >= su.Limite {
		if desde.Valid {
			_, err := tx.ExecContext(ctx, `UPDATE concessoes SET abaixo_desde = NULL WHERE id = ?`, g.ID)
			return false, err
		}
		return false, nil
	}
	quando := s.Quando.UTC()
	if !desde.Valid || lacuna {
		_, err := tx.ExecContext(ctx, `UPDATE concessoes SET abaixo_desde = ? WHERE id = ?`, quando.Format(TimeLayout), g.ID)
		return false, err
	}
	inicio := parseTime(desde.String)
	if quando.Sub(inicio) < su.Apos {
		return false, nil
	}
	if _, err := tx.ExecContext(ctx, `UPDATE concessoes SET sem_uso_avisado = 1 WHERE id = ?`, g.ID); err != nil {
		return false, err
	}
	stamp := c.stamp()
	if err := event(ctx, tx, stamp, g.Vaga, TipoRecursoSemUso, g.Recurso, map[string]any{
		"id": g.ID, "dimensao": su.Dimensao, "uso": uso, "limite": su.Limite, "desde": desde.String}); err != nil {
		return false, err
	}
	corpo := fmt.Sprintf("a concessão %d em %s está sem uso: %s ficou abaixo de %g (além do reservado) desde %s UTC; "+
		"confira se o trabalho caiu num caminho sem o recurso, ou solte com central recurso soltar %s",
		g.ID, g.Recurso, su.Dimensao, su.Limite, inicio.Format("15:04"), g.Recurso)
	return true, c.sendSystem(ctx, tx, g.Vaga, TipoRecursoSemUso, strconv.FormatInt(g.ID, 10), corpo)
}

// sampleGap diz se a leitura em quando vem depois de uma lacuna: com medidor,
// a anterior do recurso tem mais de 3 intervalos (ou não existe).
func sampleGap(ctx context.Context, tx *sql.Tx, spec ResourceSpec, quandoStamp string, quando time.Time) (bool, error) {
	if spec.MedidorIntervalo <= 0 {
		return false, nil
	}
	var anterior sql.NullString
	if err := tx.QueryRowContext(ctx, `SELECT max(quando) FROM amostras WHERE recurso = ? AND quando < ?`,
		spec.Nome, quandoStamp).Scan(&anterior); err != nil {
		return false, err
	}
	return !anterior.Valid || quando.Sub(parseTime(anterior.String)) > leiturasValidas*spec.MedidorIntervalo, nil
}

const sampleSelect = `SELECT quando, valores FROM amostras`

func querySamples(ctx context.Context, q querier, query string, args ...any) ([]Sample, error) {
	rows, err := q.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ss := []Sample{}
	for rows.Next() {
		var quando, valores string
		if err := rows.Scan(&quando, &valores); err != nil {
			return nil, err
		}
		s := Sample{Quando: parseTime(quando)}
		if err := json.Unmarshal([]byte(valores), &s.Valores); err != nil {
			return nil, fmt.Errorf("amostra de %s: %w", quando, err)
		}
		ss = append(ss, s)
	}
	return ss, rows.Err()
}

// latestSample é a leitura mais recente do recurso com idade até maxAge
// (maxAge ≤ 0: qualquer idade), ou nil.
func latestSample(ctx context.Context, q querier, recurso string, now time.Time, maxAge time.Duration) (*Sample, error) {
	query, args := sampleSelect+` WHERE recurso = ?`, []any{recurso}
	if maxAge > 0 {
		query += ` AND quando >= ?`
		args = append(args, now.Add(-maxAge).UTC().Format(TimeLayout))
	}
	ss, err := querySamples(ctx, q, query+` ORDER BY quando DESC LIMIT 1`, args...)
	if err != nil || len(ss) == 0 {
		return nil, err
	}
	return &ss[0], nil
}

// LatestSample devolve a leitura mais recente do recurso com idade até maxAge
// (maxAge ≤ 0: qualquer idade), ou nil sem nenhuma. A admissão usa
// maxAge = 3 × o intervalo do medidor.
func (c *Core) LatestSample(ctx context.Context, recurso string, maxAge time.Duration) (*Sample, error) {
	return latestSample(ctx, c.db, recurso, c.now(), maxAge)
}

// Samples devolve as leituras do recurso de de até ate (inclusive), em ordem.
func (c *Core) Samples(ctx context.Context, recurso string, de, ate time.Time) ([]Sample, error) {
	return querySamples(ctx, c.db, sampleSelect+` WHERE recurso = ? AND quando >= ? AND quando <= ? ORDER BY quando`,
		recurso, de.UTC().Format(TimeLayout), ate.UTC().Format(TimeLayout))
}

// measured é o medido que a admissão usa (spec §5.3, Decisão 8): a leitura dos
// últimos 3 intervalos mais o declarado das ativas que começaram depois dela
// (que a leitura ainda não viu). Recurso sem medidor: nil. Sem leitura
// recente: nil e o evento medidor.sem_leitura (no máximo um por minuto).
func (c *Core) measured(ctx context.Context, tx *sql.Tx, spec ResourceSpec, ativas []Grant, now time.Time) (Dims, error) {
	if spec.MedidorIntervalo <= 0 {
		return nil, nil
	}
	maxAge := leiturasValidas * spec.MedidorIntervalo
	s, err := latestSample(ctx, tx, spec.Nome, now, maxAge)
	if err != nil {
		return nil, err
	}
	if s == nil {
		if !c.allowNotice(EventoMedidorSemLeitura+"/"+spec.Nome, now) {
			return nil, nil
		}
		return nil, event(ctx, tx, now.Format(TimeLayout), Sistema, EventoMedidorSemLeitura, spec.Nome,
			map[string]any{"max_idade_ms": maxAge.Milliseconds()})
	}
	medido := maps.Clone(s.Valores)
	for _, g := range ativas {
		if g.Inicio != nil && !g.Inicio.Before(s.Quando) {
			for d, v := range g.Declarado {
				medido[d] += v
			}
		}
	}
	return medido, nil
}

// MeterFailed grava o evento medidor.falhou do recurso com o motivo, no
// máximo um por minuto por recurso. Diz se gravou.
func (c *Core) MeterFailed(ctx context.Context, recurso, motivo string) (bool, error) {
	now := c.now().UTC()
	if !c.allowNotice(EventoMedidorFalhou+"/"+recurso, now) {
		return false, nil
	}
	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	if err := event(ctx, tx, now.Format(TimeLayout), Sistema, EventoMedidorFalhou, recurso, map[string]string{"motivo": motivo}); err != nil {
		return false, err
	}
	return true, tx.Commit()
}

// allowNotice diz se o aviso chave pode sair agora (no máximo um por
// avisoMedidorACada) e, se pode, marca a hora. Guardado em memória: um
// reinício do daemon pode repetir um aviso, o que é inofensivo.
func (c *Core) allowNotice(chave string, now time.Time) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.avisos == nil {
		c.avisos = map[string]time.Time{}
	}
	if ultimo, ok := c.avisos[chave]; ok && now.Sub(ultimo) < avisoMedidorACada {
		return false
	}
	c.avisos[chave] = now
	return true
}

// pruneSamples apaga, dentro de tx, as amostras mais velhas que
// GuardarAmostras, no máximo uma vez por limparAmostrasACada. Devolve a hora
// a marcar como última limpeza depois do commit (zero: não limpou).
func (c *Core) pruneSamples(ctx context.Context, tx *sql.Tx, now time.Time) (time.Time, error) {
	c.mu.Lock()
	ultima := c.limpouAmostras
	c.mu.Unlock()
	if !ultima.IsZero() && now.Sub(ultima) < limparAmostrasACada {
		return time.Time{}, nil
	}
	_, err := tx.ExecContext(ctx, `DELETE FROM amostras WHERE quando < ?`, now.Add(-GuardarAmostras).UTC().Format(TimeLayout))
	if err != nil {
		return time.Time{}, err
	}
	return now, nil
}
