package core

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"time"
)

// Execution é uma execução longa de `central rodar` (spec §5.4). Fim e Codigo
// ficam nil enquanto ela roda; Concessao é a concessão de recurso que ela usa
// (0: nenhuma).
type Execution struct {
	ID        int64      `json:"id"`
	Vaga      string     `json:"vaga"`
	Rotulo    string     `json:"rotulo"`
	Comando   string     `json:"comando"`
	FinalLog  string     `json:"final_log"`
	Inicio    time.Time  `json:"inicio"`
	Batimento time.Time  `json:"batimento"`
	Fim       *time.Time `json:"fim,omitempty"`
	Codigo    *int       `json:"codigo,omitempty"`
	Concessao int64      `json:"concessao,omitempty"`
}

const execSelect = `SELECT id, vaga, rotulo, comando, inicio, batimento, fim, codigo, COALESCE(final_log, ''), COALESCE(concessao, 0) FROM execucoes`

func executionByID(ctx context.Context, q querier, id int64) (Execution, error) {
	rows, err := q.QueryContext(ctx, execSelect+` WHERE id = ?`, id)
	if err != nil {
		return Execution{}, err
	}
	defer rows.Close()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return Execution{}, err
		}
		return Execution{}, fmt.Errorf("execução %d: %w", id, ErrNotFound)
	}
	var e Execution
	var inicio, bat string
	var fim sql.NullString
	var codigo sql.NullInt64
	if err := rows.Scan(&e.ID, &e.Vaga, &e.Rotulo, &e.Comando, &inicio, &bat, &fim, &codigo, &e.FinalLog, &e.Concessao); err != nil {
		return Execution{}, err
	}
	e.Inicio, e.Batimento, e.Fim = parseTime(inicio), parseTime(bat), nullTime(fim)
	if codigo.Valid {
		c := int(codigo.Int64)
		e.Codigo = &c
	}
	return e, rows.Err()
}

// ExecutionByID devolve a execução id em qualquer estado.
func (c *Core) ExecutionByID(ctx context.Context, id int64) (Execution, error) {
	return executionByID(ctx, c.db, id)
}

// StartExecution grava o início de uma execução da vaga. concessao > 0 liga a
// execução a uma concessão da própria vaga (o fim resume as amostras do
// recurso dela no período).
func (c *Core) StartExecution(ctx context.Context, vaga, rotulo, comando string, concessao int64) (Execution, error) {
	if rotulo == "" || comando == "" {
		return Execution{}, invalid("rótulo e comando são obrigatórios")
	}
	if concessao < 0 {
		return Execution{}, invalid("concessão %d inválida", concessao)
	}
	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return Execution{}, err
	}
	defer tx.Rollback()
	if _, err := scanAgent(tx.QueryRowContext(ctx, agentSelect+` WHERE vaga = ?`, vaga)); err != nil {
		return Execution{}, fmt.Errorf("vaga %q: %w", vaga, err)
	}
	var concArg any
	if concessao > 0 {
		g, err := grantByID(ctx, tx, concessao)
		if errors.Is(err, ErrNotFound) || (err == nil && g.Vaga != vaga) {
			return Execution{}, invalid("a concessão %d não é de %s", concessao, vaga)
		}
		if err != nil {
			return Execution{}, err
		}
		concArg = concessao
	}
	stamp := c.stamp()
	res, err := tx.ExecContext(ctx, `INSERT INTO execucoes (vaga, rotulo, comando, inicio, batimento, concessao) VALUES (?, ?, ?, ?, ?, ?)`,
		vaga, rotulo, comando, stamp, stamp, concArg)
	if err != nil {
		return Execution{}, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return Execution{}, err
	}
	if err := event(ctx, tx, stamp, vaga, "execucao.inicio", strconv.FormatInt(id, 10), map[string]any{
		"rotulo": rotulo, "comando": comando, "concessao": concessao}); err != nil {
		return Execution{}, err
	}
	e, err := executionByID(ctx, tx, id)
	if err != nil {
		return Execution{}, err
	}
	return e, tx.Commit()
}

// BeatExecution renova o batimento da execução id e o da vaga dela. Uma
// execução encerrada dá ErrConflict.
func (c *Core) BeatExecution(ctx context.Context, id int64) error {
	e, err := executionByID(ctx, c.db, id)
	if err != nil {
		return err
	}
	if e.Fim != nil {
		return fmt.Errorf("%w: a execução %d já terminou", ErrConflict, id)
	}
	if _, err := c.db.ExecContext(ctx, `UPDATE execucoes SET batimento = ? WHERE id = ? AND fim IS NULL`, c.stamp(), id); err != nil {
		return err
	}
	return c.Touch(ctx, e.Vaga)
}

// FinishExecution encerra a execução id com o código de saída e o final do
// log (evento execucao.fim com duracao_ms). Com concessão, o evento traz o
// resumo das amostras do recurso no período: quantas, o pico e a média por
// dimensão, da leitura menos o reservado (nunca negativa), como o pico de uma
// concessão (Decisão 6). Já encerrada: ErrConflict.
func (c *Core) FinishExecution(ctx context.Context, id int64, codigo int, finalLog string) (Execution, error) {
	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return Execution{}, err
	}
	defer tx.Rollback()
	e, err := executionByID(ctx, tx, id)
	if err != nil {
		return Execution{}, err
	}
	if e.Fim != nil {
		return Execution{}, fmt.Errorf("%w: a execução %d já terminou", ErrConflict, id)
	}
	now := c.now().UTC()
	stamp := now.Format(TimeLayout)
	if _, err := tx.ExecContext(ctx, `UPDATE execucoes SET fim = ?, batimento = ?, codigo = ?, final_log = ? WHERE id = ?`,
		stamp, stamp, codigo, finalLog, id); err != nil {
		return Execution{}, err
	}
	dados := map[string]any{"rotulo": e.Rotulo, "codigo": codigo, "duracao_ms": now.Sub(e.Inicio).Milliseconds()}
	if e.Concessao > 0 {
		g, err := grantByID(ctx, tx, e.Concessao)
		if err != nil {
			return Execution{}, err
		}
		ss, err := querySamples(ctx, tx, sampleSelect+` WHERE recurso = ? AND quando >= ? AND quando <= ? ORDER BY quando`,
			g.Recurso, e.Inicio.UTC().Format(TimeLayout), stamp)
		if err != nil {
			return Execution{}, err
		}
		dados["concessao"] = e.Concessao
		dados["recurso"] = g.Recurso
		dados["amostras"] = summarize(ss, c.currentSettings().Resources[g.Recurso].Reservado)
	}
	if err := event(ctx, tx, stamp, e.Vaga, "execucao.fim", strconv.FormatInt(id, 10), dados); err != nil {
		return Execution{}, err
	}
	e, err = executionByID(ctx, tx, id)
	if err != nil {
		return Execution{}, err
	}
	return e, tx.Commit()
}

// samplesSummary é o resumo das leituras de um período.
type samplesSummary struct {
	N     int  `json:"n"`
	Pico  Dims `json:"pico"`
	Media Dims `json:"media"`
}

// summarize resume as leituras menos o reservado (nunca negativas): o pico e
// a média de cada dimensão (a média sobre as leituras que trazem a dimensão).
func summarize(ss []Sample, reservado Dims) samplesSummary {
	r := samplesSummary{N: len(ss), Pico: Dims{}, Media: Dims{}}
	soma, n := Dims{}, map[string]int{}
	for _, s := range ss {
		for d, v := range s.Valores {
			v = max(v-reservado[d], 0)
			if n[d] == 0 || v > r.Pico[d] {
				r.Pico[d] = v
			}
			soma[d] += v
			n[d]++
		}
	}
	for d, k := range n {
		r.Media[d] = soma[d] / float64(k)
	}
	return r
}
