package core

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
	"time"
)

// Event é um evento imutável do projeto (spec §4, regra 4).
type Event struct {
	Seq    int64          `json:"seq"`
	Quando time.Time      `json:"quando"`
	Quem   string         `json:"quem"`
	Tipo   string         `json:"tipo"`
	Ref    string         `json:"ref"`
	Dados  map[string]any `json:"dados"`
}

// EventFilter escolhe eventos; campos vazios não filtram.
type EventFilter struct {
	// Tipos são tipos exatos; Prefixos, prefixos de tipo (ex.: "item.").
	Tipos, Prefixos []string
	Ref             string
	De, Ate         time.Time
	// DepoisDe devolve só eventos com seq maior (para quem acompanha ao vivo).
	DepoisDe int64
	// Limite é o máximo de eventos (0 = sem limite).
	Limite int
}

// Events lista eventos pelo filtro, em ordem de seq.
func (c *Core) Events(ctx context.Context, f EventFilter) ([]Event, error) {
	var where []string
	var args []any
	var ors []string
	for _, t := range f.Tipos {
		ors = append(ors, "tipo = ?")
		args = append(args, t)
	}
	for _, p := range f.Prefixos {
		ors = append(ors, "tipo LIKE ? ESCAPE '\\'")
		args = append(args, strings.NewReplacer(`\`, `\\`, "%", `\%`, "_", `\_`).Replace(p)+"%")
	}
	if len(ors) > 0 {
		where = append(where, "("+strings.Join(ors, " OR ")+")")
	}
	if f.Ref != "" {
		where = append(where, "ref = ?")
		args = append(args, f.Ref)
	}
	if !f.De.IsZero() {
		where = append(where, "quando >= ?")
		args = append(args, fmtTime(f.De))
	}
	if !f.Ate.IsZero() {
		where = append(where, "quando <= ?")
		args = append(args, fmtTime(f.Ate))
	}
	if f.DepoisDe > 0 {
		where = append(where, "seq > ?")
		args = append(args, f.DepoisDe)
	}
	q := `SELECT seq, quando, quem, tipo, ref, dados FROM eventos`
	if len(where) > 0 {
		q += " WHERE " + strings.Join(where, " AND ")
	}
	q += " ORDER BY seq"
	if f.Limite > 0 {
		q += " LIMIT ?"
		args = append(args, f.Limite)
	}
	rows, err := c.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	evs := []Event{}
	for rows.Next() {
		var e Event
		var quando, dados string
		if err := rows.Scan(&e.Seq, &quando, &e.Quem, &e.Tipo, &e.Ref, &dados); err != nil {
			return nil, err
		}
		e.Quando = parseTime(quando)
		if err := json.Unmarshal([]byte(dados), &e.Dados); err != nil || e.Dados == nil {
			e.Dados = map[string]any{}
		}
		evs = append(evs, e)
	}
	return evs, rows.Err()
}

// OverdueReplies lista as mensagens com prazo de resposta vencido e ainda sem resposta.
func (c *Core) OverdueReplies(ctx context.Context) ([]Message, error) {
	return queryMessages(ctx, c.db, msgSelect+` WHERE responder_ate <= ? AND`+pendente+` ORDER BY seq`, c.stamp())
}

// ResourceNames lista os recursos configurados, em ordem.
func (c *Core) ResourceNames() []string {
	s := c.currentSettings()
	nomes := make([]string, 0, len(s.Resources))
	for n := range s.Resources {
		nomes = append(nomes, n)
	}
	sort.Strings(nomes)
	return nomes
}

// GrantsBetween lista as concessões do recurso que estiveram ativas em algum
// momento entre de e ate (as ainda ativas contam até agora).
func (c *Core) GrantsBetween(ctx context.Context, recurso string, de, ate time.Time) ([]Grant, error) {
	return queryGrants(ctx, c.db, grantSelect+` WHERE recurso = ? AND inicio IS NOT NULL AND inicio <= ?
		AND (fim IS NULL OR fim >= ?) ORDER BY inicio`, recurso, fmtTime(ate), fmtTime(de))
}

// UnreadMessages devolve até limite mensagens não lidas da vaga e o total, sem
// avançar o cursor (o brief resume a caixa sem consumi-la).
func (c *Core) UnreadMessages(ctx context.Context, vaga string, limite int) ([]Message, int, error) {
	a, err := scanAgent(c.db.QueryRowContext(ctx, agentSelect+` WHERE vaga = ?`, vaga))
	if err != nil {
		return nil, 0, err
	}
	var n int
	if err := c.db.QueryRowContext(ctx, `SELECT count(*) FROM mensagens`+forVaga, a.Cursor, a.Vaga, a.Papel, a.Vaga).Scan(&n); err != nil {
		return nil, 0, err
	}
	ms, err := queryMessages(ctx, c.db, msgSelect+forVaga+` ORDER BY seq LIMIT ?`, a.Cursor, a.Vaga, a.Papel, a.Vaga, limite)
	return ms, n, err
}

// RecordEvent grava um evento avulso (que não acompanha uma mudança de estado), como brief.gerado.
func (c *Core) RecordEvent(ctx context.Context, quem, tipo, ref string, dados map[string]any) error {
	if dados == nil {
		dados = map[string]any{}
	}
	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := event(ctx, tx, c.stamp(), quem, tipo, ref, dados); err != nil {
		return err
	}
	return tx.Commit()
}
