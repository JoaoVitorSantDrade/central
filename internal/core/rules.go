package core

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"time"
)

// EstadosRegra são os estados de uma regra (lição); só as ativas entram no brief.
var EstadosRegra = []string{"ativa", "no_manual", "no_codigo", "aposentada"}

// Rule é uma lição do time com a regra prática e quem a aplica (spec §7).
type Rule struct {
	ID           string    `json:"id"`
	Origem       string    `json:"origem"`
	Texto        string    `json:"texto"`
	Pratica      string    `json:"pratica"`
	Aplica       []string  `json:"aplica"`
	Estado       string    `json:"estado"`
	Comando      string    `json:"comando,omitempty"`
	CriadaEm     time.Time `json:"criada_em"`
	AtualizadaEm time.Time `json:"atualizada_em"`
}

// RuleFilter escolhe regras: Papel casa com TODOS ou com aplica; Estado é exato.
type RuleFilter struct{ Papel, Estado string }

const ruleCols = `id, origem, texto, pratica, aplica, estado, comando, criada_em, atualizada_em`

func scanRule(row scanner) (Rule, error) {
	var r Rule
	var aplica, criada, atual string
	err := row.Scan(&r.ID, &r.Origem, &r.Texto, &r.Pratica, &aplica, &r.Estado, &r.Comando, &criada, &atual)
	if errors.Is(err, sql.ErrNoRows) {
		return Rule{}, ErrNotFound
	}
	if err != nil {
		return Rule{}, err
	}
	if err := json.Unmarshal([]byte(aplica), &r.Aplica); err != nil {
		return Rule{}, err
	}
	r.CriadaEm, r.AtualizadaEm = parseTime(criada), parseTime(atual)
	return r, nil
}

func validRule(r Rule) error {
	if r.ID == "" || strings.TrimSpace(r.Pratica) == "" {
		return invalid("regra precisa de id e de regra prática")
	}
	if len(r.Aplica) == 0 {
		return invalid("regra %s precisa dizer a quem se aplica (papéis ou TODOS)", r.ID)
	}
	if !slices.Contains(EstadosRegra, r.Estado) {
		return invalid("estado de regra %q (use %v)", r.Estado, EstadosRegra)
	}
	return nil
}

// SaveRule insere ou atualiza a regra (evento regra.gravada); mudou=false se nada mudou.
func (c *Core) SaveRule(ctx context.Context, quem string, r Rule) (Rule, bool, error) {
	if err := validRule(r); err != nil {
		return Rule{}, false, err
	}
	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return Rule{}, false, err
	}
	defer tx.Rollback()
	antes, err := scanRule(tx.QueryRowContext(ctx, `SELECT `+ruleCols+` FROM regras WHERE id = ?`, r.ID))
	novo := errors.Is(err, ErrNotFound)
	if err != nil && !novo {
		return Rule{}, false, err
	}
	if !novo && antes.Origem == r.Origem && antes.Texto == r.Texto && antes.Pratica == r.Pratica &&
		reflect.DeepEqual(antes.Aplica, r.Aplica) && antes.Estado == r.Estado && antes.Comando == r.Comando {
		return antes, false, nil
	}
	aplica, _ := json.Marshal(r.Aplica)
	stamp := c.stamp()
	criada := stamp
	if !novo {
		criada = fmtTime(antes.CriadaEm)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO regras (`+ruleCols+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (id) DO UPDATE SET origem = excluded.origem, texto = excluded.texto, pratica = excluded.pratica,
		aplica = excluded.aplica, estado = excluded.estado, comando = excluded.comando, atualizada_em = excluded.atualizada_em`,
		r.ID, r.Origem, r.Texto, r.Pratica, string(aplica), r.Estado, r.Comando, criada, stamp); err != nil {
		return Rule{}, false, err
	}
	if err := event(ctx, tx, stamp, quem, "regra.gravada", r.ID, map[string]any{"estado": r.Estado, "novo": novo}); err != nil {
		return Rule{}, false, err
	}
	out, err := scanRule(tx.QueryRowContext(ctx, `SELECT `+ruleCols+` FROM regras WHERE id = ?`, r.ID))
	if err != nil {
		return Rule{}, false, err
	}
	return out, true, tx.Commit()
}

// SetRuleState muda o estado (e o comando que aplica a regra, quando no_codigo).
func (c *Core) SetRuleState(ctx context.Context, quem, id, estado, comando string) (Rule, error) {
	r, err := scanRule(c.db.QueryRowContext(ctx, `SELECT `+ruleCols+` FROM regras WHERE id = ?`, id))
	if err != nil {
		return Rule{}, fmt.Errorf("regra %s: %w", id, err)
	}
	r.Estado, r.Comando = estado, comando
	out, _, err := c.SaveRule(ctx, quem, r)
	return out, err
}

// Rules lista as regras pelo filtro, por id.
func (c *Core) Rules(ctx context.Context, f RuleFilter) ([]Rule, error) {
	q := `SELECT ` + ruleCols + ` FROM regras`
	var args []any
	if f.Estado != "" {
		q += ` WHERE estado = ?`
		args = append(args, f.Estado)
	}
	rows, err := c.db.QueryContext(ctx, q+` ORDER BY id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	rs := []Rule{}
	for rows.Next() {
		r, err := scanRule(rows)
		if err != nil {
			return nil, err
		}
		if f.Papel != "" && !slices.Contains(r.Aplica, Todos) && !slices.Contains(r.Aplica, f.Papel) {
			continue
		}
		rs = append(rs, r)
	}
	return rs, rows.Err()
}
