package core

import (
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
)

// Estados gravados de uma vaga executada (spec do executor §3.2).
const (
	VagaAberta   = "aberta"
	VagaFechada  = "fechada"
	VagaSuspensa = "suspensa"
)

// nomeVagaExec é o nome aceito para uma vaga executada: ele vira o nome do
// worktree (-w) e uma pasta dos logs, então não pode ter separadores nem começar por -.
var nomeVagaExec = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,39}$`)

// TipoVagaSuspensa é a mensagem da CENTRAL ao GERENTE quando uma vaga é suspensa.
const TipoVagaSuspensa = "vaga.suspensa"

// VagaExec é uma vaga que a Central abre e conduz como sessão do Claude Code.
// TurnoDesde e PID só existem com um turno em curso.
type VagaExec struct {
	Vaga          string     `json:"vaga"`
	Papel         string     `json:"papel"`
	Area          string     `json:"area"`
	Nota          string     `json:"nota"`
	Sessao        string     `json:"sessao"`
	Pasta         string     `json:"pasta"`
	Modelo        string     `json:"modelo"`
	Esforco       string     `json:"esforco"`
	Estado        string     `json:"estado"`
	Sprint        string     `json:"sprint"`
	Falhas        int        `json:"falhas"`
	FalhaAnterior string     `json:"falha_anterior"`
	NTurnos       int        `json:"n_turnos"`
	PID           int        `json:"pid"`
	ProximoEm     *time.Time `json:"proximo_em"`
	TurnoDesde    *time.Time `json:"turno_desde"`
	TurnoMotivo   string     `json:"turno_motivo"`
	// UltimoTurno é o início do turno mais recente: só mensagens dali em diante acordam a vaga.
	UltimoTurno *time.Time `json:"ultimo_turno"`
	UltimaNegacao string     `json:"ultima_negacao"`
	CustoDia      float64    `json:"custo_dia"`
	AbertaEm      *time.Time `json:"aberta_em"`
	FechadaEm     *time.Time `json:"fechada_em"`
}

// TurnoFim é o que o escalonador grava ao fim de um turno. Falha vazia é ok e
// zera as falhas seguidas; limite_plano não conta; as demais somam uma.
type TurnoFim struct {
	Falha, Texto, Stderr, Sessao, Pasta string
	CustoUSD                            float64
	TokensEntrada, TokensSaida, Codigo  int
	Negacoes                            []string
	ProximoEm                           *time.Time
	Suspender                           bool
	Dur                                 time.Duration
}

// FalhaLimitePlano é a falha que pausa o plano sem contar contra a vaga.
const FalhaLimitePlano = "limite_plano"

const vagaExecCols = `vaga, papel, area, nota, sessao, pasta, modelo, esforco, estado, sprint, falhas, falha_anterior,
	n_turnos, pid, proximo_em, turno_desde, turno_motivo, ultimo_turno, ultima_negacao, custo_dia, custo_dia_em, aberta_em, fechada_em`

func timePtr(s sql.NullString) *time.Time {
	if !s.Valid || s.String == "" {
		return nil
	}
	t := parseTime(s.String)
	return &t
}

// diaLocal é o dia do custo diário, no fuso da máquina (o teto vira à meia-noite local).
func diaLocal(t time.Time) string { return t.Local().Format("2006-01-02") }

func (c *Core) scanVagaExec(row scanner) (VagaExec, error) {
	var v VagaExec
	var prox, desde, ultimo, aberta, fechada sql.NullString
	var custoEm string
	err := row.Scan(&v.Vaga, &v.Papel, &v.Area, &v.Nota, &v.Sessao, &v.Pasta, &v.Modelo, &v.Esforco, &v.Estado, &v.Sprint,
		&v.Falhas, &v.FalhaAnterior, &v.NTurnos, &v.PID, &prox, &desde, &v.TurnoMotivo, &ultimo, &v.UltimaNegacao, &v.CustoDia, &custoEm,
		&aberta, &fechada)
	if errors.Is(err, sql.ErrNoRows) {
		return VagaExec{}, ErrNotFound
	}
	if err != nil {
		return VagaExec{}, err
	}
	if custoEm != diaLocal(c.now()) {
		v.CustoDia = 0
	}
	v.ProximoEm, v.TurnoDesde, v.AbertaEm, v.FechadaEm = timePtr(prox), timePtr(desde), timePtr(aberta), timePtr(fechada)
	v.UltimoTurno = timePtr(ultimo)
	return v, nil
}

func (c *Core) vagaExecTx(ctx context.Context, tx *sql.Tx, vaga string) (VagaExec, error) {
	return c.scanVagaExec(tx.QueryRowContext(ctx, `SELECT `+vagaExecCols+` FROM vagas_exec WHERE vaga = ?`, vaga))
}

// novaSessao gera o id de sessão do Claude Code (UUID v4).
func novaSessao() string {
	var b [16]byte
	rand.Read(b[:])
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// AbrirVagaExec abre a vaga, ou reabre uma fechada ou suspensa mantendo a
// sessão, a pasta e a contagem de turnos (evento vaga.retomada). Uma vaga já
// aberta dá ErrConflict. Quem pode abrir e o teto do cargo são do processo.
func (c *Core) AbrirVagaExec(ctx context.Context, quem string, v VagaExec) (VagaExec, error) {
	if v.Vaga == "" || v.Papel == "" || v.Modelo == "" || v.Esforco == "" {
		return VagaExec{}, invalid("vaga, papel, modelo e esforço são obrigatórios")
	}
	if !nomeVagaExec.MatchString(v.Vaga) {
		return VagaExec{}, invalid("vaga %q: use letras, dígitos, _ e -, começando por letra ou dígito (até 40)", v.Vaga)
	}
	if v.Vaga == Todos || v.Vaga == Sistema || v.Vaga == Humano {
		return VagaExec{}, invalid("%s é reservado", v.Vaga)
	}
	now := c.stamp()
	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return VagaExec{}, err
	}
	defer tx.Rollback()
	atual, err := c.vagaExecTx(ctx, tx, v.Vaga)
	tipo := "vaga.aberta"
	switch {
	case errors.Is(err, ErrNotFound):
		if v.Sessao == "" {
			v.Sessao = novaSessao()
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO vagas_exec (vaga, papel, area, nota, sessao, modelo, esforco, estado, sprint, aberta_em)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, v.Vaga, v.Papel, v.Area, v.Nota, v.Sessao, v.Modelo, v.Esforco, VagaAberta, v.Sprint, now)
	case err != nil:
		return VagaExec{}, err
	case atual.Estado == VagaAberta:
		return VagaExec{}, fmt.Errorf("%w: a vaga %s já está aberta", ErrConflict, v.Vaga)
	default:
		tipo = "vaga.retomada"
		_, err = tx.ExecContext(ctx, `UPDATE vagas_exec SET papel = ?, area = ?, nota = ?, modelo = ?, esforco = ?, estado = ?, sprint = ?,
			falhas = 0, falha_anterior = '', proximo_em = NULL, aberta_em = ?, fechada_em = NULL WHERE vaga = ?`,
			v.Papel, v.Area, v.Nota, v.Modelo, v.Esforco, VagaAberta, v.Sprint, now, v.Vaga)
	}
	if err != nil {
		return VagaExec{}, err
	}
	dados := map[string]any{"papel": v.Papel, "area": v.Area, "modelo": v.Modelo, "esforco": v.Esforco, "sprint": v.Sprint}
	if err := event(ctx, tx, now, quem, tipo, v.Vaga, dados); err != nil {
		return VagaExec{}, err
	}
	return c.commitVaga(ctx, tx, v.Vaga)
}

// commitVaga lê a vaga, confirma a transação e acorda quem espera (o escalonador).
func (c *Core) commitVaga(ctx context.Context, tx *sql.Tx, vaga string) (VagaExec, error) {
	out, err := c.vagaExecTx(ctx, tx, vaga)
	if err != nil {
		return VagaExec{}, err
	}
	if err := tx.Commit(); err != nil {
		return VagaExec{}, err
	}
	c.notify.Broadcast()
	return out, nil
}

// FecharVagaExec fecha a vaga: um turno em curso termina, e nenhum novo começa.
// Fechar uma vaga já fechada não muda nada.
func (c *Core) FecharVagaExec(ctx context.Context, quem, vaga string) (VagaExec, error) {
	now := c.stamp()
	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return VagaExec{}, err
	}
	defer tx.Rollback()
	atual, err := c.vagaExecTx(ctx, tx, vaga)
	if err != nil {
		return VagaExec{}, err
	}
	if atual.Estado == VagaFechada {
		return atual, nil
	}
	if _, err := tx.ExecContext(ctx, `UPDATE vagas_exec SET estado = ?, fechada_em = ?, proximo_em = NULL WHERE vaga = ?`, VagaFechada, now, vaga); err != nil {
		return VagaExec{}, err
	}
	if err := event(ctx, tx, now, quem, "vaga.fechada", vaga, map[string]any{"estado_anterior": atual.Estado, "em_turno": atual.TurnoDesde != nil}); err != nil {
		return VagaExec{}, err
	}
	return c.commitVaga(ctx, tx, vaga)
}

// VagaExec lê uma vaga executada.
func (c *Core) VagaExec(ctx context.Context, vaga string) (VagaExec, error) {
	return c.scanVagaExec(c.db.QueryRowContext(ctx, `SELECT `+vagaExecCols+` FROM vagas_exec WHERE vaga = ?`, vaga))
}

// VagasExec lista as vagas executadas em ordem de vaga.
func (c *Core) VagasExec(ctx context.Context) ([]VagaExec, error) {
	rows, err := c.db.QueryContext(ctx, `SELECT `+vagaExecCols+` FROM vagas_exec ORDER BY vaga`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	vs := []VagaExec{}
	for rows.Next() {
		v, err := c.scanVagaExec(rows)
		if err != nil {
			return nil, err
		}
		vs = append(vs, v)
	}
	return vs, rows.Err()
}

// IniciarTurno reserva o turno n da vaga aberta e sem turno em curso (senão
// ErrConflict) e consome os despertares vencidos dela. O pid pode vir depois,
// por RegistrarPID, quando o processo já começou.
func (c *Core) IniciarTurno(ctx context.Context, vaga, motivo string, pid int) (int, error) {
	now := c.stamp()
	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	v, err := c.vagaExecTx(ctx, tx, vaga)
	if err != nil {
		return 0, err
	}
	if v.Estado != VagaAberta {
		return 0, fmt.Errorf("%w: a vaga %s está %s", ErrConflict, vaga, v.Estado)
	}
	if v.TurnoDesde != nil {
		return 0, fmt.Errorf("%w: a vaga %s já tem um turno em curso", ErrConflict, vaga)
	}
	n := v.NTurnos + 1
	if _, err := tx.ExecContext(ctx, `UPDATE vagas_exec SET n_turnos = ?, turno_desde = ?, ultimo_turno = ?, pid = ?, turno_motivo = ? WHERE vaga = ?`,
		n, now, now, pid, motivo, vaga); err != nil {
		return 0, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE despertares SET feito = 1 WHERE vaga = ? AND feito = 0 AND quando <= ?`, vaga, now); err != nil {
		return 0, err
	}
	if err := event(ctx, tx, now, Sistema, "turno.iniciado", vaga, map[string]any{"n": n, "motivo": motivo}); err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	c.notify.Broadcast()
	return n, nil
}

// RegistrarPID grava o pid do turno em curso (para a recuperação).
func (c *Core) RegistrarPID(ctx context.Context, vaga string, pid int) error {
	res, err := c.db.ExecContext(ctx, `UPDATE vagas_exec SET pid = ? WHERE vaga = ? AND turno_desde IS NOT NULL`, pid, vaga)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("%w: a vaga %s não tem turno em curso", ErrConflict, vaga)
	}
	return nil
}

// TerminarTurno grava o fim do turno em curso: custo do dia, sessão e pasta
// informadas, falhas seguidas, próximo turno permitido e, com Suspender, a
// suspensão (evento vaga.suspensa e mensagem ao GERENTE). Uma vaga fechada
// durante o turno continua fechada.
func (c *Core) TerminarTurno(ctx context.Context, vaga string, r TurnoFim) (VagaExec, error) {
	agora := c.now()
	now := agora.UTC().Format(TimeLayout)
	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return VagaExec{}, err
	}
	defer tx.Rollback()
	v, err := c.vagaExecTx(ctx, tx, vaga)
	if err != nil {
		return VagaExec{}, err
	}
	if v.TurnoDesde == nil {
		return VagaExec{}, fmt.Errorf("%w: a vaga %s não tem turno em curso", ErrConflict, vaga)
	}
	falhas := v.Falhas
	switch r.Falha {
	case "":
		falhas = 0
	case FalhaLimitePlano:
	default:
		falhas++
	}
	sessao, pasta, negacao := v.Sessao, v.Pasta, v.UltimaNegacao
	if r.Sessao != "" {
		sessao = r.Sessao
	}
	if r.Pasta != "" {
		pasta = r.Pasta
	}
	if len(r.Negacoes) > 0 {
		negacao = r.Negacoes[len(r.Negacoes)-1]
	}
	var prox any
	if r.ProximoEm != nil {
		prox = r.ProximoEm.UTC().Format(TimeLayout)
	}
	estado := v.Estado
	suspensa := r.Suspender && estado == VagaAberta
	if suspensa {
		estado = VagaSuspensa
	}
	if _, err := tx.ExecContext(ctx, `UPDATE vagas_exec SET turno_desde = NULL, pid = 0, sessao = ?, pasta = ?, falhas = ?, falha_anterior = ?,
		proximo_em = ?, ultima_negacao = ?, estado = ?, custo_dia = ?, custo_dia_em = ? WHERE vaga = ?`,
		sessao, pasta, falhas, r.Falha, prox, negacao, estado, v.CustoDia+r.CustoUSD, diaLocal(agora), vaga); err != nil {
		return VagaExec{}, err
	}
	if r.Falha == "" {
		err = event(ctx, tx, now, Sistema, "turno.terminou", vaga, map[string]any{"n": v.NTurnos, "dur_s": int(r.Dur.Seconds()),
			"custo_usd": r.CustoUSD, "tokens_entrada": r.TokensEntrada, "tokens_saida": r.TokensSaida,
			"negacoes": nonNil(r.Negacoes), "resultado": cortar(r.Texto, 300)})
	} else {
		err = event(ctx, tx, now, Sistema, "turno.falhou", vaga, map[string]any{"n": v.NTurnos, "dur_s": int(r.Dur.Seconds()),
			"motivo": r.Falha, "codigo": r.Codigo, "stderr": cortar(r.Stderr, 500), "custo_usd": r.CustoUSD,
			"negacoes": nonNil(r.Negacoes)})
	}
	if err != nil {
		return VagaExec{}, err
	}
	if suspensa {
		if err := event(ctx, tx, now, Sistema, "vaga.suspensa", vaga, map[string]any{"falhas": falhas, "motivo": r.Falha}); err != nil {
			return VagaExec{}, err
		}
		corpo := fmt.Sprintf("A vaga %s (%s) foi suspensa depois de %d falhas seguidas (a última: %s). Reabra com central vaga abrir %s --papel %s.",
			vaga, v.Papel, falhas, r.Falha, vaga, v.Papel)
		if err := c.sendSystem(ctx, tx, PapelGerente, TipoVagaSuspensa, vaga, corpo); err != nil {
			return VagaExec{}, err
		}
	}
	return c.commitVaga(ctx, tx, vaga)
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// cortar limita s a max bytes, sem cortar um caractere ao meio.
func cortar(s string, max int) string {
	s = strings.TrimSpace(s)
	if len(s) <= max {
		return s
	}
	for max > 0 && (s[max]&0xC0) == 0x80 {
		max--
	}
	return s[:max] + "…"
}

// PausarPlano pausa todas as vagas executadas até ate (limite do plano ou teto de custo).
func (c *Core) PausarPlano(ctx context.Context, ate time.Time, motivo string) error {
	now := c.stamp()
	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	ateS := ate.UTC().Format(TimeLayout)
	if _, err := tx.ExecContext(ctx, `UPDATE executor_estado SET plano_pausado_ate = ?, motivo = ? WHERE id = 1`, ateS, motivo); err != nil {
		return err
	}
	if err := event(ctx, tx, now, Sistema, "plano.pausado", "", map[string]string{"ate": ateS, "motivo": motivo}); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	c.notify.Broadcast()
	return nil
}

// PlanoPausadoAte devolve até quando o plano está pausado (zero sem pausa).
func (c *Core) PlanoPausadoAte(ctx context.Context) (time.Time, error) {
	ate, _, err := c.PlanoPausado(ctx)
	return ate, err
}

// PlanoPausado devolve até quando o plano está pausado e por quê.
func (c *Core) PlanoPausado(ctx context.Context) (time.Time, string, error) {
	var ate sql.NullString
	var motivo string
	if err := c.db.QueryRowContext(ctx, `SELECT plano_pausado_ate, motivo FROM executor_estado WHERE id = 1`).Scan(&ate, &motivo); err != nil {
		return time.Time{}, "", err
	}
	if !ate.Valid {
		return time.Time{}, "", nil
	}
	return parseTime(ate.String), motivo, nil
}

// RetomarPlano tira a pausa do plano (evento plano.retomado); sem pausa, não faz nada.
func (c *Core) RetomarPlano(ctx context.Context) error {
	now := c.stamp()
	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, `UPDATE executor_estado SET plano_pausado_ate = NULL, motivo = '' WHERE id = 1 AND plano_pausado_ate IS NOT NULL`)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil
	}
	if err := event(ctx, tx, now, Sistema, "plano.retomado", "", map[string]string{}); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	c.notify.Broadcast()
	return nil
}

// Despertar pede um turno da vaga executada para em, com o motivo.
func (c *Core) Despertar(ctx context.Context, vaga string, em time.Time, motivo string) error {
	if strings.TrimSpace(motivo) == "" {
		return invalid("motivo é obrigatório")
	}
	now := c.stamp()
	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := c.vagaExecTx(ctx, tx, vaga); err != nil {
		return fmt.Errorf("a vaga %s não é executada pela Central: %w", vaga, err)
	}
	emS := em.UTC().Format(TimeLayout)
	if _, err := tx.ExecContext(ctx, `INSERT INTO despertares (vaga, quando, motivo) VALUES (?, ?, ?)`, vaga, emS, motivo); err != nil {
		return err
	}
	if err := event(ctx, tx, now, vaga, "vaga.despertar", vaga, map[string]string{"em": emS, "motivo": motivo}); err != nil {
		return err
	}
	return tx.Commit()
}

// DespertaresVencidos devolve, por vaga, o motivo do despertar vencido mais
// antigo ainda não feito. Não marca nada: o IniciarTurno da vaga os consome,
// para um despertar que vence durante um turno não se perder.
func (c *Core) DespertaresVencidos(ctx context.Context, agora time.Time) (map[string]string, error) {
	rows, err := c.db.QueryContext(ctx, `SELECT vaga, motivo FROM despertares WHERE feito = 0 AND quando <= ? ORDER BY quando DESC, id DESC`,
		agora.UTC().Format(TimeLayout))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var vaga, motivo string
		if err := rows.Scan(&vaga, &motivo); err != nil {
			return nil, err
		}
		out[vaga] = motivo
	}
	return out, rows.Err()
}

// CustoHoje soma o custo estimado dos turnos de hoje (dia local) de todas as vagas.
func (c *Core) CustoHoje(ctx context.Context) (float64, error) {
	var total sql.NullFloat64
	err := c.db.QueryRowContext(ctx, `SELECT sum(custo_dia) FROM vagas_exec WHERE custo_dia_em = ?`, diaLocal(c.now())).Scan(&total)
	return total.Float64, err
}

// NaoLidasDesde conta as mensagens não lidas da vaga criadas a partir de desde
// (zero conta todas). O escalonador só acorda a vaga por mensagem nova desde o
// último turno: uma mensagem que o turno deixou sem ler não a acorda de novo.
func (c *Core) NaoLidasDesde(ctx context.Context, vaga string, desde time.Time) (int, error) {
	a, err := scanAgent(c.db.QueryRowContext(ctx, agentSelect+` WHERE vaga = ?`, vaga))
	if err != nil {
		return 0, err
	}
	var n int
	err = c.db.QueryRowContext(ctx, `SELECT count(*) FROM mensagens`+forVaga+` AND criada_em >= ?`,
		a.Cursor, a.Vaga, a.Papel, a.Vaga, desde.UTC().Format(TimeLayout)).Scan(&n)
	return n, err
}
