package core

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"
)

// Sprint são os horários de um expediente (spec §6.4). O núcleo só guarda;
// quem calcula e decide é o processo.
type Sprint struct {
	ID               string               `json:"id"`
	Abertura         time.Time            `json:"abertura"`
	Checkin          time.Time            `json:"checkin"`
	Escopo           time.Time            `json:"escopo"`
	Congelamento     time.Time            `json:"congelamento"`
	Corte            time.Time            `json:"corte"`
	Retro            time.Time            `json:"retro"`
	Fim              time.Time            `json:"fim"`
	EncerradaEm      *time.Time           `json:"encerrada_em,omitempty"`
	AntecipadaEm     *time.Time           `json:"antecipada_em,omitempty"`
	Original         map[string]time.Time `json:"original,omitempty"`
	AnteciparDesde   *time.Time           `json:"antecipar_desde,omitempty"`
	AnteciparAvisado bool                 `json:"antecipar_avisado"`
	// Avisos são o que a abertura não conseguiu fazer junto (não é gravado).
	Avisos []string `json:"avisos,omitempty"`
}

// Horario devolve o horário da cerimônia pelo nome.
func (s Sprint) Horario(nome string) (time.Time, bool) {
	switch nome {
	case "abertura":
		return s.Abertura, true
	case "checkin":
		return s.Checkin, true
	case "escopo":
		return s.Escopo, true
	case "congelamento":
		return s.Congelamento, true
	case "corte":
		return s.Corte, true
	case "retro":
		return s.Retro, true
	case "fim":
		return s.Fim, true
	}
	return time.Time{}, false
}

// CeremonyNames são as cerimônias na ordem do expediente.
var CeremonyNames = []string{"abertura", "checkin", "escopo", "congelamento", "corte", "retro", "fim"}

// Ceremony é uma cerimônia vencida e ainda não disparada.
type Ceremony struct {
	Sprint string    `json:"sprint"`
	Nome   string    `json:"nome"`
	Quando time.Time `json:"quando"`
}

// SystemMsg é uma mensagem da CENTRAL gravada junto com uma mudança.
type SystemMsg struct{ Para, Tipo, Ref, Corpo string }

const sprintCols = `id, abertura, checkin, escopo, congelamento, corte, retro, fim, encerrada_em, antecipada_em,
	original, antecipar_desde, antecipar_avisado`

func fmtTime(t time.Time) string { return t.UTC().Format(TimeLayout) }

func fmtTimePtr(t *time.Time) any {
	if t == nil {
		return nil
	}
	return fmtTime(*t)
}

func scanSprint(row scanner) (Sprint, error) {
	var s Sprint
	var ab, ck, es, co, cr, re, fi, original string
	var enc, ant, desde sql.NullString
	err := row.Scan(&s.ID, &ab, &ck, &es, &co, &cr, &re, &fi, &enc, &ant, &original, &desde, &s.AnteciparAvisado)
	if errors.Is(err, sql.ErrNoRows) {
		return Sprint{}, ErrNotFound
	}
	if err != nil {
		return Sprint{}, err
	}
	s.Abertura, s.Checkin, s.Escopo, s.Congelamento = parseTime(ab), parseTime(ck), parseTime(es), parseTime(co)
	s.Corte, s.Retro, s.Fim = parseTime(cr), parseTime(re), parseTime(fi)
	ptr := func(n sql.NullString) *time.Time {
		if !n.Valid {
			return nil
		}
		t := parseTime(n.String)
		return &t
	}
	s.EncerradaEm, s.AntecipadaEm, s.AnteciparDesde = ptr(enc), ptr(ant), ptr(desde)
	if err := json.Unmarshal([]byte(original), &s.Original); err != nil {
		return Sprint{}, fmt.Errorf("sprint %s: original: %w", s.ID, err)
	}
	return s, nil
}

func (s Sprint) valid() error {
	if s.ID == "" {
		return invalid("sprint sem id")
	}
	ordem := []time.Time{s.Abertura, s.Congelamento, s.Corte, s.Retro, s.Fim}
	for i := 1; i < len(ordem); i++ {
		if ordem[i].Before(ordem[i-1]) {
			return invalid("horários da sprint %s fora de ordem", s.ID)
		}
	}
	if s.Checkin.Before(s.Abertura) || s.Escopo.After(s.Congelamento) {
		return invalid("horários da sprint %s fora de ordem", s.ID)
	}
	return nil
}

func currentSprint(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}) (*Sprint, error) {
	s, err := scanSprint(q.QueryRowContext(ctx, `SELECT `+sprintCols+` FROM sprints WHERE encerrada_em IS NULL ORDER BY criada_em DESC LIMIT 1`))
	if errors.Is(err, ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &s, nil
}

// CreateSprint grava uma sprint nova (evento sprint.aberta). Recusa com
// ErrConflict se já há uma aberta ou se o id existe.
func (c *Core) CreateSprint(ctx context.Context, quem string, s Sprint) error {
	if err := s.valid(); err != nil {
		return err
	}
	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if cur, err := currentSprint(ctx, tx); err != nil {
		return err
	} else if cur != nil {
		return fmt.Errorf("%w: a sprint %s ainda está aberta (encerre-a antes)", ErrConflict, cur.ID)
	}
	var n int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM sprints WHERE id = ?`, s.ID).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return fmt.Errorf("%w: a sprint %s já existe", ErrConflict, s.ID)
	}
	original, err := json.Marshal(s.Original)
	if err != nil {
		return err
	}
	stamp := c.stamp()
	if _, err := tx.ExecContext(ctx, `INSERT INTO sprints (id, abertura, checkin, escopo, congelamento, corte, retro, fim,
		original, criada_em) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, s.ID, fmtTime(s.Abertura), fmtTime(s.Checkin), fmtTime(s.Escopo),
		fmtTime(s.Congelamento), fmtTime(s.Corte), fmtTime(s.Retro), fmtTime(s.Fim), string(original), stamp); err != nil {
		return err
	}
	if err := event(ctx, tx, stamp, quem, "sprint.aberta", s.ID, map[string]any{"fim": fmtTime(s.Fim)}); err != nil {
		return err
	}
	return tx.Commit()
}

// CurrentSprint devolve a sprint aberta mais recente, ou nil.
func (c *Core) CurrentSprint(ctx context.Context) (*Sprint, error) { return currentSprint(ctx, c.db) }

// Sprints lista todas as sprints, da mais nova para a mais velha.
func (c *Core) Sprints(ctx context.Context) ([]Sprint, error) {
	rows, err := c.db.QueryContext(ctx, `SELECT `+sprintCols+` FROM sprints ORDER BY criada_em DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ss := []Sprint{}
	for rows.Next() {
		s, err := scanSprint(rows)
		if err != nil {
			return nil, err
		}
		ss = append(ss, s)
	}
	return ss, rows.Err()
}

// SprintByID devolve uma sprint pelo id.
func (c *Core) SprintByID(ctx context.Context, id string) (Sprint, error) {
	s, err := scanSprint(c.db.QueryRowContext(ctx, `SELECT `+sprintCols+` FROM sprints WHERE id = ?`, id))
	if err != nil {
		return Sprint{}, fmt.Errorf("sprint %s: %w", id, err)
	}
	return s, nil
}

// UpdateSprint regrava os horários e o estado da sprint (evento sprint.<evento>)
// e envia as mensagens dadas, tudo numa transação.
func (c *Core) UpdateSprint(ctx context.Context, quem string, s Sprint, evento string, msgs ...SystemMsg) error {
	if err := s.valid(); err != nil {
		return err
	}
	original, err := json.Marshal(s.Original)
	if err != nil {
		return err
	}
	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, `UPDATE sprints SET abertura = ?, checkin = ?, escopo = ?, congelamento = ?, corte = ?,
		retro = ?, fim = ?, encerrada_em = ?, antecipada_em = ?, original = ?, antecipar_desde = ?, antecipar_avisado = ?
		WHERE id = ?`, fmtTime(s.Abertura), fmtTime(s.Checkin), fmtTime(s.Escopo), fmtTime(s.Congelamento), fmtTime(s.Corte),
		fmtTime(s.Retro), fmtTime(s.Fim), fmtTimePtr(s.EncerradaEm), fmtTimePtr(s.AntecipadaEm), string(original),
		fmtTimePtr(s.AnteciparDesde), s.AnteciparAvisado, s.ID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("sprint %s: %w", s.ID, ErrNotFound)
	}
	stamp := c.stamp()
	if err := event(ctx, tx, stamp, quem, "sprint."+evento, s.ID, map[string]any{"fim": fmtTime(s.Fim)}); err != nil {
		return err
	}
	for _, m := range msgs {
		if err := c.sendSystem(ctx, tx, m.Para, m.Tipo, m.Ref, m.Corpo); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	if len(msgs) > 0 {
		c.notify.Broadcast()
	}
	return nil
}

// DueCeremonies devolve as cerimônias da sprint aberta com horário até now e
// ainda não disparadas, na ordem do expediente.
func (c *Core) DueCeremonies(ctx context.Context, now time.Time) ([]Ceremony, error) {
	s, err := c.CurrentSprint(ctx)
	if err != nil || s == nil {
		return nil, err
	}
	rows, err := c.db.QueryContext(ctx, `SELECT nome FROM cerimonias WHERE sprint = ?`, s.ID)
	if err != nil {
		return nil, err
	}
	feitas := map[string]bool{}
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			rows.Close()
			return nil, err
		}
		feitas[n] = true
	}
	rows.Close()
	due := []Ceremony{}
	for _, nome := range CeremonyNames {
		q, _ := s.Horario(nome)
		if !feitas[nome] && !q.After(now) {
			due = append(due, Ceremony{Sprint: s.ID, Nome: nome, Quando: q})
		}
	}
	sort.SliceStable(due, func(i, j int) bool { return due[i].Quando.Before(due[j].Quando) })
	return due, nil
}

// MarkCeremony registra o disparo da cerimônia (evento cerimonia.<nome>) e
// avisa cada destinatário com cerimonia.<nome> (ref = sprint), numa transação.
// Idempotente: se já foi disparada, devolve false e não avisa. O fim encerra a sprint.
func (c *Core) MarkCeremony(ctx context.Context, sprint, nome string, quando time.Time, para []string, corpo string) (bool, error) {
	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	stamp := c.stamp()
	res, err := tx.ExecContext(ctx, `INSERT INTO cerimonias (sprint, nome, quando, disparada_em) VALUES (?, ?, ?, ?)
		ON CONFLICT (sprint, nome) DO NOTHING`, sprint, nome, fmtTime(quando), stamp)
	if err != nil {
		return false, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return false, nil
	}
	if err := event(ctx, tx, stamp, Sistema, "cerimonia."+nome, sprint, map[string]any{"quando": fmtTime(quando), "para": para}); err != nil {
		return false, err
	}
	for _, p := range para {
		if err := c.sendSystem(ctx, tx, p, "cerimonia."+nome, sprint, corpo); err != nil {
			return false, err
		}
	}
	if nome == "fim" {
		if _, err := tx.ExecContext(ctx, `UPDATE sprints SET encerrada_em = ? WHERE id = ? AND encerrada_em IS NULL`, stamp, sprint); err != nil {
			return false, err
		}
		if err := event(ctx, tx, stamp, Sistema, "sprint.encerrada", sprint, map[string]any{"por": "fim"}); err != nil {
			return false, err
		}
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	if len(para) > 0 {
		c.notify.Broadcast()
	}
	return true, nil
}
