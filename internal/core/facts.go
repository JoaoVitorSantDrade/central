package core

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"time"
)

// Fact é um fato do projeto: um valor JSON sob uma chave, com quem o gravou e
// por quê. Gravar de novo não apaga o anterior; o último vence e o resto é histórico.
type Fact struct {
	Chave  string          `json:"chave"`
	Valor  json.RawMessage `json:"valor"`
	Vaga   string          `json:"vaga"`
	Motivo string          `json:"motivo"`
	Quando time.Time       `json:"quando"`
}

var chaveFato = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,127}$`)

const factSelect = `SELECT chave, valor, vaga, motivo, quando FROM fatos`

func scanFact(row scanner) (Fact, error) {
	var f Fact
	var valor, quando string
	err := row.Scan(&f.Chave, &valor, &f.Vaga, &f.Motivo, &quando)
	if errors.Is(err, sql.ErrNoRows) {
		return Fact{}, ErrNotFound
	}
	if err != nil {
		return Fact{}, err
	}
	f.Valor = json.RawMessage(valor)
	f.Quando = parseTime(quando)
	return f, nil
}

// SetFact grava um novo valor para a chave (evento fato.gravado). A chave segue
// ^[a-z0-9][a-z0-9._-]{0,127}$, o valor é JSON válido e o motivo é obrigatório.
func (c *Core) SetFact(ctx context.Context, vaga, chave string, valor json.RawMessage, motivo string) (Fact, error) {
	compact, err := validFact(chave, valor, motivo)
	if err != nil {
		return Fact{}, err
	}
	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return Fact{}, err
	}
	defer tx.Rollback()
	if _, err := scanAgent(tx.QueryRowContext(ctx, agentSelect+` WHERE vaga = ?`, vaga)); err != nil {
		return Fact{}, fmt.Errorf("vaga %q: %w", vaga, err)
	}
	if err := insertFact(ctx, tx, c.stamp(), vaga, chave, compact, motivo); err != nil {
		return Fact{}, err
	}
	f, err := scanFact(tx.QueryRowContext(ctx, factSelect+` WHERE chave = ? ORDER BY id DESC LIMIT 1`, chave))
	if err != nil {
		return Fact{}, err
	}
	return f, tx.Commit()
}

// GetFact devolve o valor vigente da chave; sem fato, ErrNotFound.
func (c *Core) GetFact(ctx context.Context, chave string) (Fact, error) {
	f, err := scanFact(c.db.QueryRowContext(ctx, factSelect+` WHERE chave = ? ORDER BY id DESC LIMIT 1`, chave))
	if errors.Is(err, ErrNotFound) {
		return Fact{}, fmt.Errorf("fato %q: %w", chave, ErrNotFound)
	}
	return f, err
}

func (c *Core) queryFacts(ctx context.Context, query string, args ...any) ([]Fact, error) {
	rows, err := c.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	fs := []Fact{}
	for rows.Next() {
		f, err := scanFact(rows)
		if err != nil {
			return nil, err
		}
		fs = append(fs, f)
	}
	return fs, rows.Err()
}

// FactHistory lista todos os valores já gravados na chave, do mais novo ao mais velho.
func (c *Core) FactHistory(ctx context.Context, chave string) ([]Fact, error) {
	return c.queryFacts(ctx, factSelect+` WHERE chave = ? ORDER BY id DESC`, chave)
}

// Facts lista o fato vigente de cada chave, em ordem de chave.
func (c *Core) Facts(ctx context.Context) ([]Fact, error) {
	return c.queryFacts(ctx, factSelect+` WHERE id IN (SELECT MAX(id) FROM fatos GROUP BY chave) ORDER BY chave`)
}

// validFact confere a chave, o valor (JSON) e o motivo e devolve o valor compacto.
func validFact(chave string, valor json.RawMessage, motivo string) (string, error) {
	if !chaveFato.MatchString(chave) {
		return "", invalid("chave de fato %q (use até 128 letras minúsculas, dígitos, ., _ ou -)", chave)
	}
	if motivo == "" {
		return "", invalid("o motivo do fato é obrigatório")
	}
	var compact bytes.Buffer
	if !json.Valid(valor) || json.Compact(&compact, valor) != nil {
		return "", invalid("o valor do fato %q não é JSON válido", chave)
	}
	return compact.String(), nil
}

// insertFact grava uma versão do fato e o evento fato.gravado dentro de tx.
func insertFact(ctx context.Context, tx *sql.Tx, stamp, quem, chave, valor, motivo string) error {
	if _, err := tx.ExecContext(ctx, `INSERT INTO fatos (chave, valor, vaga, motivo, quando) VALUES (?, ?, ?, ?, ?)`,
		chave, valor, quem, motivo, stamp); err != nil {
		return err
	}
	return event(ctx, tx, stamp, quem, "fato.gravado", chave, map[string]string{"motivo": motivo})
}
