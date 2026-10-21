package core

import (
	"context"
	"fmt"
	"regexp"
)

var prefixoID = regexp.MustCompile(`^[A-Z]{1,5}$`)

// NextID entrega o próximo id do prefixo (T-001, T-002...). O contador é
// atômico: ultimo = max(ultimo, minimo-1) + 1, então minimo permite pular para
// além de ids já usados fora da Central. Grava o evento id.gerado com quem = vaga.
func (c *Core) NextID(ctx context.Context, vaga, prefixo string, minimo int) (string, error) {
	if !prefixoID.MatchString(prefixo) {
		return "", invalid("prefixo de id %q (use de 1 a 5 letras maiúsculas)", prefixo)
	}
	if minimo < 1 {
		minimo = 1
	}
	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	var n int
	if err := tx.QueryRowContext(ctx, `INSERT INTO contadores (prefixo, ultimo) VALUES (?, ?)
		ON CONFLICT (prefixo) DO UPDATE SET ultimo = MAX(ultimo, ?) + 1 RETURNING ultimo`,
		prefixo, minimo, minimo-1).Scan(&n); err != nil {
		return "", err
	}
	id := fmt.Sprintf("%s-%03d", prefixo, n)
	if err := event(ctx, tx, c.stamp(), vaga, "id.gerado", id, map[string]string{}); err != nil {
		return "", err
	}
	return id, tx.Commit()
}
