package core

import (
	"context"
	"database/sql"
	"errors"
)

// CurrentConfig devolve o texto e o hash da configuração vigente do projeto
// (a última versão gravada). Sem nenhuma versão, devolve textos vazios.
func (c *Core) CurrentConfig(ctx context.Context) (texto, hash string, err error) {
	err = c.db.QueryRowContext(ctx, `SELECT texto, hash FROM config ORDER BY versao DESC LIMIT 1`).Scan(&texto, &hash)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", nil
	}
	return texto, hash, err
}

// EnterArgs são os dados de uma entrada: a vaga, a pasta, a área preferida
// (opcional) e a configuração do projeto (texto vazio não mexe nela).
type EnterArgs struct {
	Vaga, Papel, Pasta, Area string
	Config, Hash             string
}

// EnterWithConfig faz o Enter e, na mesma transação, grava uma nova versão da
// configuração quando o hash difere do vigente (evento config.atualizada).
// Texto vazio não mexe na configuração. mudou diz se gravou uma versão.
func (c *Core) EnterWithConfig(ctx context.Context, vaga, papel, pasta, texto, hash string) (a Agent, mudou bool, err error) {
	return c.EnterWith(ctx, EnterArgs{Vaga: vaga, Papel: papel, Pasta: pasta, Config: texto, Hash: hash})
}

// EnterWith é o Enter completo: vaga, área e configuração numa transação.
func (c *Core) EnterWith(ctx context.Context, in EnterArgs) (a Agent, mudou bool, err error) {
	vaga, papel, pasta, texto, hash := in.Vaga, in.Papel, in.Pasta, in.Config, in.Hash
	if err := validEnter(vaga, papel, pasta); err != nil {
		return Agent{}, false, err
	}
	if texto != "" && hash == "" {
		return Agent{}, false, invalid("configuração sem hash")
	}
	now := c.stamp()
	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return Agent{}, false, err
	}
	defer tx.Rollback()
	if a, err = enterTx(ctx, tx, now, vaga, papel, pasta, in.Area); err != nil {
		return Agent{}, false, err
	}
	if texto != "" {
		var atual string
		err := tx.QueryRowContext(ctx, `SELECT hash FROM config ORDER BY versao DESC LIMIT 1`).Scan(&atual)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return Agent{}, false, err
		}
		if atual != hash {
			res, err := tx.ExecContext(ctx, `INSERT INTO config (texto, hash, vaga, quando) VALUES (?, ?, ?, ?)`, texto, hash, vaga, now)
			if err != nil {
				return Agent{}, false, err
			}
			versao, err := res.LastInsertId()
			if err != nil {
				return Agent{}, false, err
			}
			if err := event(ctx, tx, now, vaga, "config.atualizada", "", map[string]any{"versao": versao, "hash": hash}); err != nil {
				return Agent{}, false, err
			}
			mudou = true
		}
	}
	return a, mudou, tx.Commit()
}
