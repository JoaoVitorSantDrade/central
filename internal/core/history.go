package core

import "context"

// Messages devolve o histórico do projeto (sem cursor): as mensagens com seq
// maior que depois, em ordem; com limite > 0, só as limite mais novas.
func (c *Core) Messages(ctx context.Context, depois int64, limite int) ([]Message, error) {
	if limite <= 0 {
		return queryMessages(ctx, c.db, msgSelect+` WHERE seq > ? ORDER BY seq`, depois)
	}
	return queryMessages(ctx, c.db, `SELECT * FROM (`+msgSelect+` WHERE seq > ? ORDER BY seq DESC LIMIT ?) ORDER BY seq`, depois, limite)
}

// OpenReplies lista as pendências de resposta em aberto do projeto, em ordem de seq.
func (c *Core) OpenReplies(ctx context.Context) ([]Message, error) {
	return queryMessages(ctx, c.db, msgSelect+` WHERE`+pendente+` ORDER BY seq`)
}

// LastSeq devolve o seq do último evento do projeto (0 sem eventos).
func (c *Core) LastSeq(ctx context.Context) (int64, error) {
	var seq int64
	err := c.db.QueryRowContext(ctx, `SELECT COALESCE(max(seq), 0) FROM eventos`).Scan(&seq)
	return seq, err
}
