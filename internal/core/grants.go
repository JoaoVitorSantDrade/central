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

// GrantRequest é um pedido de concessão. Declarado vazio e Exclusivo falso
// deixam a estimativa para o núcleo; Duracao zero usa o teto da prioridade;
// Prioridade vazia é a menor do recurso.
type GrantRequest struct {
	Recurso, Vaga, Tipo, Prioridade, Motivo, Chave string
	Declarado                                      Dims
	Exclusivo                                      bool
	Duracao                                        time.Duration
}

// Motivos de fim de uma concessão (fim_motivo); o evento é recurso.<motivo>.
const (
	FimSolta    = "solta"
	FimExpirada = "expirada"
)

// Tipos das mensagens da CENTRAL sobre concessões (Decisão 2 do plano).
const (
	TipoRecursoConcedida = "recurso.concedida"
	TipoRecursoPrazo     = "recurso.prazo"
	TipoRecursoLiberada  = "recurso.liberada"
)

const (
	// LiberarAposPrazo é quanto uma concessão vencida espera um batimento do
	// dono antes de ser liberada (spec §5.3).
	LiberarAposPrazo = 10 * time.Minute
	// padraoPrazoMax vale para um ResourceSpec sem PrazoMax (a configuração
	// já o preenche).
	padraoPrazoMax = 30 * time.Minute
	// historicoN é quantas execuções do mesmo tipo a estimativa olha.
	historicoN = 5
)

const grantSelect = `SELECT id, recurso, vaga, tipo, prioridade, motivo, declarado, exclusivo, duracao_ms,
	estimativa, estado, pedido_em, inicio, prazo, fim, fim_motivo, sozinha, COALESCE(pico, '') FROM concessoes`

func queryGrants(ctx context.Context, q querier, query string, args ...any) ([]Grant, error) {
	rows, err := q.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	gs := []Grant{}
	for rows.Next() {
		var g Grant
		var decl, pedido, pico string
		var durMs int64
		var inicio, prazo, fim sql.NullString
		if err := rows.Scan(&g.ID, &g.Recurso, &g.Vaga, &g.Tipo, &g.Prioridade, &g.Motivo, &decl, &g.Exclusivo, &durMs,
			&g.Estimativa, &g.Estado, &pedido, &inicio, &prazo, &fim, &g.FimMotivo, &g.Sozinha, &pico); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(decl), &g.Declarado); err != nil {
			return nil, fmt.Errorf("concessão %d: declarado: %w", g.ID, err)
		}
		if g.Declarado == nil {
			g.Declarado = Dims{}
		}
		if pico != "" {
			if err := json.Unmarshal([]byte(pico), &g.Pico); err != nil {
				return nil, fmt.Errorf("concessão %d: pico: %w", g.ID, err)
			}
		}
		g.Duracao = time.Duration(durMs) * time.Millisecond
		g.PedidoEm = parseTime(pedido)
		g.Inicio, g.Prazo, g.Fim = nullTime(inicio), nullTime(prazo), nullTime(fim)
		gs = append(gs, g)
	}
	return gs, rows.Err()
}

func nullTime(s sql.NullString) *time.Time {
	if !s.Valid {
		return nil
	}
	t := parseTime(s.String)
	return &t
}

func grantByID(ctx context.Context, q querier, id int64) (Grant, error) {
	gs, err := queryGrants(ctx, q, grantSelect+` WHERE id = ?`, id)
	if err != nil {
		return Grant{}, err
	}
	if len(gs) == 0 {
		return Grant{}, fmt.Errorf("concessão %d: %w", id, ErrNotFound)
	}
	return gs[0], nil
}

// GrantByID devolve a concessão id em qualquer estado.
func (c *Core) GrantByID(ctx context.Context, id int64) (Grant, error) {
	return grantByID(ctx, c.db, id)
}

// livre é a capacidade que o declarado pode ocupar numa dimensão.
func livre(spec ResourceSpec, d string) float64 { return spec.Dimensoes[d] - spec.Reservado[d] }

// teto é o prazo máximo de uma concessão da prioridade p.
func teto(spec ResourceSpec, p string) time.Duration {
	if d, ok := spec.PrazoMaxPorPrioridade[p]; ok {
		return d
	}
	if spec.PrazoMax > 0 {
		return spec.PrazoMax
	}
	return padraoPrazoMax
}

// validGrant confere o pedido contra o recurso e completa a prioridade e a
// duração. Um declarado acima da capacidade livre nunca entraria, então a
// cabeça da fila sempre cabe sozinha.
func validGrant(spec ResourceSpec, r *GrantRequest) error {
	if r.Prioridade == "" && len(spec.Prioridades) > 0 {
		r.Prioridade = spec.Prioridades[len(spec.Prioridades)-1]
	}
	if !slices.Contains(spec.Prioridades, r.Prioridade) {
		return invalid("prioridade %q desconhecida no recurso %s (use %v)", r.Prioridade, spec.Nome, spec.Prioridades)
	}
	for _, d := range slices.Sorted(maps.Keys(r.Declarado)) {
		v := r.Declarado[d]
		if _, ok := spec.Dimensoes[d]; !ok {
			return invalid("dimensão desconhecida %q no recurso %s", d, spec.Nome)
		}
		if v < 0 || math.IsNaN(v) || math.IsInf(v, 0) {
			return invalid("%s precisa ser um número não negativo", d)
		}
		if v > livre(spec, d) {
			return invalid("%s = %g passa da capacidade livre do recurso %s (%g)", d, v, spec.Nome, livre(spec, d))
		}
	}
	limite := teto(spec, r.Prioridade)
	switch {
	case r.Duracao < 0:
		return invalid("duração negativa")
	case r.Duracao == 0:
		r.Duracao = limite
	case r.Duracao > limite:
		return invalid("prazo de %v passa do teto de %v da prioridade %s: divida em etapas", r.Duracao, limite, r.Prioridade)
	}
	return nil
}

// RequestGrant pede uma concessão: valida, estima o declarado, põe na fila e
// roda a admissão do recurso na mesma transação. Com chave repetida da mesma
// vaga devolve o pedido original, em qualquer estado (a chave usada em outro
// recurso dá ErrConflict); outro pedido não encerrado da vaga no mesmo
// recurso dá ErrConflict.
func (c *Core) RequestGrant(ctx context.Context, r GrantRequest) (Grant, error) {
	spec, ok := c.currentSettings().Resources[r.Recurso]
	if !ok {
		return Grant{}, invalid("recurso %q não está no central.toml", r.Recurso)
	}
	if err := validGrant(spec, &r); err != nil {
		return Grant{}, err
	}
	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return Grant{}, err
	}
	defer tx.Rollback()
	if _, err := scanAgent(tx.QueryRowContext(ctx, agentSelect+` WHERE vaga = ?`, r.Vaga)); err != nil {
		return Grant{}, fmt.Errorf("vaga %q: %w", r.Vaga, err)
	}
	if r.Chave != "" {
		gs, err := queryGrants(ctx, tx, grantSelect+` WHERE vaga = ? AND chave = ?`, r.Vaga, r.Chave)
		if err != nil {
			return Grant{}, err
		}
		if len(gs) == 1 {
			if g := gs[0]; g.Recurso != r.Recurso {
				return Grant{}, fmt.Errorf("%w: a chave %q de %s já foi usada no pedido %d do recurso %s",
					ErrConflict, r.Chave, r.Vaga, g.ID, g.Recurso)
			}
			return gs[0], nil
		}
	}
	abertos, err := queryGrants(ctx, tx, grantSelect+` WHERE vaga = ? AND recurso = ? AND estado <> '`+GrantEncerrada+`'`, r.Vaga, r.Recurso)
	if err != nil {
		return Grant{}, err
	}
	if len(abertos) > 0 {
		g := abertos[0]
		return Grant{}, fmt.Errorf("%w: %s já tem o pedido %d (%s) no recurso %s; solte-o com central recurso soltar %s",
			ErrConflict, r.Vaga, g.ID, g.Estado, r.Recurso, r.Recurso)
	}
	decl, exclusivo, estimativa, err := estimate(ctx, tx, spec, r)
	if err != nil {
		return Grant{}, err
	}
	declJSON, err := json.Marshal(decl)
	if err != nil {
		return Grant{}, err
	}
	var chave any
	if r.Chave != "" {
		chave = r.Chave
	}
	stamp := c.stamp()
	res, err := tx.ExecContext(ctx, `INSERT INTO concessoes (recurso, vaga, tipo, prioridade, motivo, declarado, exclusivo,
		duracao_ms, estimativa, estado, pedido_em, chave) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		r.Recurso, r.Vaga, r.Tipo, r.Prioridade, r.Motivo, string(declJSON), exclusivo,
		r.Duracao.Milliseconds(), estimativa, GrantFila, stamp, chave)
	if err != nil {
		return Grant{}, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return Grant{}, err
	}
	if err := event(ctx, tx, stamp, r.Vaga, "recurso.pedida", r.Recurso, map[string]any{
		"id": id, "tipo": r.Tipo, "prioridade": r.Prioridade, "estimativa": estimativa,
		"declarado": decl, "exclusivo": exclusivo, "duracao_ms": r.Duracao.Milliseconds()}); err != nil {
		return Grant{}, err
	}
	n, err := c.admitTx(ctx, tx, spec)
	if err != nil {
		return Grant{}, err
	}
	g, err := grantByID(ctx, tx, id)
	if err != nil {
		return Grant{}, err
	}
	if err := tx.Commit(); err != nil {
		return Grant{}, err
	}
	if n > 0 {
		c.notify.Broadcast()
	}
	return g, nil
}

// estimate decide o declarado do pedido (spec §5.3, Decisão 6): o declarado
// pelo agente; senão o maior pico das últimas historicoN execuções do tipo
// com +20 % (limitado à capacidade livre); senão o valor do tipo; senão exclusivo.
func estimate(ctx context.Context, tx *sql.Tx, spec ResourceSpec, r GrantRequest) (Dims, bool, string, error) {
	if len(r.Declarado) > 0 || r.Exclusivo {
		d := maps.Clone(r.Declarado)
		if d == nil {
			d = Dims{}
		}
		return d, r.Exclusivo, EstimativaDeclarada, nil
	}
	if r.Tipo == "" {
		return Dims{}, true, EstimativaExclusivo, nil
	}
	rows, err := tx.QueryContext(ctx, `SELECT pico FROM historico_recursos WHERE recurso = ? AND tipo = ? ORDER BY id DESC LIMIT ?`,
		spec.Nome, r.Tipo, historicoN)
	if err != nil {
		return nil, false, "", err
	}
	est := Dims{}
	for rows.Next() {
		var raw string
		var pico Dims
		if err := rows.Scan(&raw); err != nil {
			rows.Close()
			return nil, false, "", err
		}
		if json.Unmarshal([]byte(raw), &pico) != nil {
			continue // linha ilegível não deve travar o pedido
		}
		for d, v := range pico {
			if _, ok := spec.Dimensoes[d]; !ok {
				continue // dimensão que saiu do central.toml
			}
			// +20 % como v × 12 / 10, exato para picos inteiros.
			if f := min(v*12/10, livre(spec, d)); f > est[d] || !hasKey(est, d) {
				est[d] = max(f, 0)
			}
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, false, "", err
	}
	if len(est) > 0 {
		return est, false, EstimativaHistorico, nil
	}
	if t, ok := spec.Tipos[r.Tipo]; ok {
		d := maps.Clone(t.Dims)
		if d == nil {
			d = Dims{}
		}
		return d, t.Exclusivo, EstimativaTipo, nil
	}
	return Dims{}, true, EstimativaExclusivo, nil
}

func hasKey(d Dims, k string) bool {
	_, ok := d[k]
	return ok
}

// admitTx roda a admissão do recurso dentro de tx e ativa quem entra, em
// ordem; devolve quantas ativou. Com medidor, usa a leitura recente (measured).
func (c *Core) admitTx(ctx context.Context, tx *sql.Tx, spec ResourceSpec) (int, error) {
	fila, err := queryGrants(ctx, tx, grantSelect+` WHERE recurso = ? AND estado = '`+GrantFila+`'`, spec.Nome)
	if err != nil || len(fila) == 0 {
		return 0, err
	}
	ativas, err := queryGrants(ctx, tx, grantSelect+` WHERE recurso = ? AND estado = '`+GrantAtiva+`'`, spec.Nome)
	if err != nil {
		return 0, err
	}
	now := c.now().UTC()
	medido, err := c.measured(ctx, tx, spec, ativas, now)
	if err != nil {
		return 0, err
	}
	ids := Admit(AdmissionInput{Spec: spec, Ativas: ativas, Fila: fila, Medido: medido, Now: now})
	porID := map[int64]Grant{}
	for _, g := range fila {
		porID[g.ID] = g
	}
	nAtivas := len(ativas)
	for _, id := range ids {
		if err := c.activate(ctx, tx, porID[id], nAtivas == 0, now); err != nil {
			return 0, err
		}
		nAtivas++
	}
	return len(ids), nil
}

// activate liga a concessão g: início agora, prazo pela duração, evento e
// mensagem recurso.concedida para a vaga. Sem sozinha, as outras ativas do
// recurso também deixam de estar sozinhas.
func (c *Core) activate(ctx context.Context, tx *sql.Tx, g Grant, sozinha bool, now time.Time) error {
	if !sozinha {
		if _, err := tx.ExecContext(ctx, `UPDATE concessoes SET sozinha = 0 WHERE recurso = ? AND estado = '`+GrantAtiva+`'`, g.Recurso); err != nil {
			return err
		}
	}
	stamp := now.Format(TimeLayout)
	prazo := now.Add(g.Duracao)
	prazoStamp := prazo.Format(TimeLayout)
	if _, err := tx.ExecContext(ctx, `UPDATE concessoes SET estado = '`+GrantAtiva+`', inicio = ?, prazo = ?, sozinha = ? WHERE id = ?`,
		stamp, prazoStamp, sozinha, g.ID); err != nil {
		return err
	}
	if err := event(ctx, tx, stamp, g.Vaga, TipoRecursoConcedida, g.Recurso, map[string]any{
		"id": g.ID, "prazo": prazoStamp, "sozinha": sozinha}); err != nil {
		return err
	}
	corpo := fmt.Sprintf("sua vez em %s: concessão %d ativa até %s UTC; solte com central recurso soltar %s",
		g.Recurso, g.ID, prazo.Format("15:04"), g.Recurso)
	// O ref das mensagens de recurso é sempre o id da concessão (concedida, prazo, liberada, sem_uso).
	return c.sendSystem(ctx, tx, g.Vaga, TipoRecursoConcedida, strconv.FormatInt(g.ID, 10), corpo)
}

// endGrant encerra g com o motivo dado (evento recurso.<motivo>). Uma ativa
// que ficou sozinha a vigência toda e tem pico alimenta o histórico do tipo.
func endGrant(ctx context.Context, tx *sql.Tx, g Grant, motivo, stamp string) error {
	if _, err := tx.ExecContext(ctx, `UPDATE concessoes SET estado = '`+GrantEncerrada+`', fim = ?, fim_motivo = ? WHERE id = ?`,
		stamp, motivo, g.ID); err != nil {
		return err
	}
	historico := g.Estado == GrantAtiva && g.Sozinha && len(g.Pico) > 0 && g.Tipo != ""
	if historico {
		pico, err := json.Marshal(g.Pico)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO historico_recursos (recurso, tipo, pico, fim) VALUES (?, ?, ?, ?)`,
			g.Recurso, g.Tipo, string(pico), stamp); err != nil {
			return err
		}
	}
	return event(ctx, tx, stamp, g.Vaga, "recurso."+motivo, g.Recurso, map[string]any{
		"id": g.ID, "estado_anterior": g.Estado, "historico": historico})
}

// ReleaseGrant encerra o pedido ou a concessão da vaga no recurso
// (fim_motivo solta) e roda a admissão, numa transação.
func (c *Core) ReleaseGrant(ctx context.Context, vaga, recurso string) (Grant, error) {
	return c.releaseOne(ctx, func(tx *sql.Tx) (Grant, error) {
		gs, err := queryGrants(ctx, tx, grantSelect+` WHERE vaga = ? AND recurso = ? AND estado <> '`+GrantEncerrada+`'`, vaga, recurso)
		if err != nil {
			return Grant{}, err
		}
		if len(gs) == 0 {
			return Grant{}, fmt.Errorf("%s não tem pedido nem concessão em %s: %w", vaga, recurso, ErrNotFound)
		}
		return gs[0], nil
	})
}

// ReleaseGrantByID encerra o pedido ou a concessão id da vaga (fim_motivo
// solta) e roda a admissão, numa transação. De outra vaga ou já encerrado:
// ErrNotFound. É o soltar de quem sabe qual pedido fez (central rodar), que
// não deve soltar outro pedido da mesma vaga no recurso.
func (c *Core) ReleaseGrantByID(ctx context.Context, vaga string, id int64) (Grant, error) {
	return c.releaseOne(ctx, func(tx *sql.Tx) (Grant, error) {
		gs, err := queryGrants(ctx, tx, grantSelect+` WHERE id = ? AND vaga = ? AND estado <> '`+GrantEncerrada+`'`, id, vaga)
		if err != nil {
			return Grant{}, err
		}
		if len(gs) == 0 {
			return Grant{}, fmt.Errorf("%s não tem o pedido %d aberto: %w", vaga, id, ErrNotFound)
		}
		return gs[0], nil
	})
}

// releaseOne encerra, numa transação, o pedido que find acha (fim_motivo
// solta) e roda a admissão do recurso dele.
func (c *Core) releaseOne(ctx context.Context, find func(*sql.Tx) (Grant, error)) (Grant, error) {
	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return Grant{}, err
	}
	defer tx.Rollback()
	g, err := find(tx)
	if err != nil {
		return Grant{}, err
	}
	if err := endGrant(ctx, tx, g, FimSolta, c.stamp()); err != nil {
		return Grant{}, err
	}
	if spec, ok := c.currentSettings().Resources[g.Recurso]; ok {
		if _, err = c.admitTx(ctx, tx, spec); err != nil {
			return Grant{}, err
		}
	}
	g, err = grantByID(ctx, tx, g.ID)
	if err != nil {
		return Grant{}, err
	}
	if err := tx.Commit(); err != nil {
		return Grant{}, err
	}
	// Sempre acorda: além de quem foi admitido, quem espera pelo pedido que
	// acabou de ser encerrado precisa saber disso agora (WaitGrant → encerrada).
	c.notify.Broadcast()
	return g, nil
}

// releaseGrants solta, dentro de tx, os pedidos e concessões da vaga e roda a
// admissão dos recursos afetados. Devolve os IDs soltos e quantas ativou.
func (c *Core) releaseGrants(ctx context.Context, tx *sql.Tx, vaga string) ([]int64, int, error) {
	gs, err := queryGrants(ctx, tx, grantSelect+` WHERE vaga = ? AND estado <> '`+GrantEncerrada+`' ORDER BY id`, vaga)
	if err != nil {
		return nil, 0, err
	}
	stamp := c.stamp()
	ids := []int64{}
	recursos := map[string]bool{}
	for _, g := range gs {
		if err := endGrant(ctx, tx, g, FimSolta, stamp); err != nil {
			return nil, 0, err
		}
		ids = append(ids, g.ID)
		recursos[g.Recurso] = true
	}
	n, specs := 0, c.currentSettings().Resources
	for _, r := range slices.Sorted(maps.Keys(recursos)) {
		if spec, ok := specs[r]; ok {
			k, err := c.admitTx(ctx, tx, spec)
			if err != nil {
				return nil, 0, err
			}
			n += k
		}
	}
	return ids, n, nil
}

// tickRecursos é a parte do Tick das concessões: avisa uma vez cada prazo
// vencido; libera a concessão vencida há mais de LiberarAposPrazo cujo dono
// não deu batimento desde o prazo; e roda a admissão de cada recurso.
// Devolve quantas mensagens gravou.
func (c *Core) tickRecursos(ctx context.Context, tx *sql.Tx) (int, error) {
	now := c.now().UTC()
	stamp := now.Format(TimeLayout)
	n := 0
	vencidas, err := queryGrants(ctx, tx, grantSelect+` WHERE estado = '`+GrantAtiva+`' AND prazo_avisado = 0 AND prazo <= ? ORDER BY id`, stamp)
	if err != nil {
		return 0, err
	}
	for _, g := range vencidas {
		if _, err := tx.ExecContext(ctx, `UPDATE concessoes SET prazo_avisado = 1 WHERE id = ?`, g.ID); err != nil {
			return 0, err
		}
		corpo := fmt.Sprintf("o prazo da concessão %d em %s venceu às %s UTC; solte com central recurso soltar %s (sem batimento, ela é liberada em %d min)",
			g.ID, g.Recurso, g.Prazo.UTC().Format("15:04"), g.Recurso, int(LiberarAposPrazo/time.Minute))
		if err := c.sendSystem(ctx, tx, g.Vaga, TipoRecursoPrazo, strconv.FormatInt(g.ID, 10), corpo); err != nil {
			return 0, err
		}
		n++
	}
	limite := now.Add(-LiberarAposPrazo).Format(TimeLayout)
	expiradas, err := queryGrants(ctx, tx, grantSelect+` WHERE estado = '`+GrantAtiva+`' AND prazo < ? ORDER BY id`, limite)
	if err != nil {
		return 0, err
	}
	for _, g := range expiradas {
		var bat string
		err := tx.QueryRowContext(ctx, `SELECT batimento FROM agentes WHERE vaga = ?`, g.Vaga).Scan(&bat)
		if err != nil && err != sql.ErrNoRows {
			return 0, err
		}
		if bat != "" && !parseTime(bat).Before(*g.Prazo) {
			continue // o dono deu sinal de vida depois do prazo
		}
		if err := endGrant(ctx, tx, g, FimExpirada, stamp); err != nil {
			return 0, err
		}
		corpo := fmt.Sprintf("a concessão %d em %s foi liberada: prazo vencido às %s UTC e nenhum batimento seu desde então",
			g.ID, g.Recurso, g.Prazo.UTC().Format("15:04"))
		if err := c.sendSystem(ctx, tx, g.Vaga, TipoRecursoLiberada, strconv.FormatInt(g.ID, 10), corpo); err != nil {
			return 0, err
		}
		n++
	}
	specs := c.currentSettings().Resources
	for _, nome := range slices.Sorted(maps.Keys(specs)) {
		k, err := c.admitTx(ctx, tx, specs[nome])
		if err != nil {
			return 0, err
		}
		n += k
	}
	return n, nil
}

// ResourceView é o estado de um recurso: a configuração, as concessões ativas,
// a fila em ordem de admissão, a última leitura do medidor (nil sem leitura) e
// a capacidade livre na conta declarada.
type ResourceView struct {
	Spec           ResourceSpec `json:"spec"`
	Ativas         []Grant      `json:"ativas"`
	Fila           []Grant      `json:"fila"`
	Ultima         *Sample      `json:"ultima"`
	LivreDeclarado Dims         `json:"livre_declarado"`
}

// ResourceStatus devolve o estado do recurso nome; fora do central.toml, ErrNotFound.
func (c *Core) ResourceStatus(ctx context.Context, nome string) (ResourceView, error) {
	spec, ok := c.currentSettings().Resources[nome]
	if !ok {
		return ResourceView{}, fmt.Errorf("recurso %q não está no central.toml: %w", nome, ErrNotFound)
	}
	ativas, err := queryGrants(ctx, c.db, grantSelect+` WHERE recurso = ? AND estado = '`+GrantAtiva+`' ORDER BY id`, nome)
	if err != nil {
		return ResourceView{}, err
	}
	fila, err := queryGrants(ctx, c.db, grantSelect+` WHERE recurso = ? AND estado = '`+GrantFila+`'`, nome)
	if err != nil {
		return ResourceView{}, err
	}
	SortQueue(spec, fila)
	ultima, err := c.LatestSample(ctx, nome, 0)
	if err != nil {
		return ResourceView{}, err
	}
	exclusiva := slices.ContainsFunc(ativas, func(g Grant) bool { return g.Exclusivo })
	livres := Dims{}
	for d := range spec.Dimensoes {
		v := livre(spec, d)
		for _, g := range ativas {
			v -= g.Declarado[d]
		}
		if exclusiva || v < 0 {
			v = 0
		}
		livres[d] = v
	}
	return ResourceView{Spec: spec, Ativas: ativas, Fila: fila, Ultima: ultima, LivreDeclarado: livres}, nil
}
