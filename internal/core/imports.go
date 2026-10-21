package core

import (
	"context"
	"errors"
	"time"
)

// ImportedMessage é uma mensagem trazida de outro sistema, com o horário e a
// autoria originais. Chave identifica a origem (reimportar não duplica);
// RespondeChave liga a resposta à mensagem original pela chave dela.
type ImportedMessage struct {
	De, Para, Tipo, Ref, Corpo string
	CriadaEm                   time.Time
	Chave, RespondeChave       string
}

// ImportMessages grava as mensagens que ainda não existem (pela chave) e liga
// as respostas. Devolve quantas foram gravadas. Não acorda ninguém: é histórico.
func (c *Core) ImportMessages(ctx context.Context, quem string, ms []ImportedMessage) (int, error) {
	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	novas := 0
	for _, m := range ms {
		if m.Chave == "" || m.De == "" || m.Para == "" || m.Tipo == "" {
			return 0, invalid("mensagem importada precisa de chave, de, para e tipo")
		}
		var n int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM mensagens WHERE chave = ?`, m.Chave).Scan(&n); err != nil {
			return 0, err
		}
		if n > 0 {
			continue
		}
		corpo := m.Corpo
		if corpo == "" {
			corpo = "(vazia)"
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO mensagens (de, para, tipo, ref, corpo, criada_em, chave) VALUES (?, ?, ?, ?, ?, ?, ?)`,
			m.De, m.Para, m.Tipo, m.Ref, corpo, fmtTime(m.CriadaEm), m.Chave); err != nil {
			return 0, err
		}
		novas++
	}
	for _, m := range ms {
		if m.RespondeChave == "" {
			continue
		}
		if _, err := tx.ExecContext(ctx, `UPDATE mensagens SET responde_a = (SELECT seq FROM mensagens WHERE chave = ? LIMIT 1)
			WHERE chave = ? AND responde_a IS NULL`, m.RespondeChave, m.Chave); err != nil {
			return 0, err
		}
	}
	if novas > 0 {
		if err := event(ctx, tx, c.stamp(), quem, "importado.mensagens", "", map[string]any{"n": novas}); err != nil {
			return 0, err
		}
	}
	return novas, tx.Commit()
}

// ImportAgent cria a vaga se ela ainda não existe (uma vaga viva nunca é
// sobrescrita). Uma pasta já usada por outra vaga vira "(importado) <vaga>".
func (c *Core) ImportAgent(ctx context.Context, quem, vaga, papel, pasta, estado string, batimento time.Time) (bool, error) {
	if err := validEnter(vaga, papel, "x"); err != nil {
		return false, err
	}
	switch estado {
	case EstadoAtivo, EstadoOcioso, EstadoTrabalhando, EstadoEncerrado:
	default:
		estado = EstadoAtivo
	}
	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	if _, err := scanAgent(tx.QueryRowContext(ctx, agentSelect+` WHERE vaga = ?`, vaga)); err == nil {
		return false, nil
	} else if !errors.Is(err, ErrNotFound) {
		return false, err
	}
	if pasta == "" {
		pasta = "(importado) " + vaga
	} else if _, err := scanAgent(tx.QueryRowContext(ctx, agentSelect+` WHERE pasta = ?`, pasta)); err == nil {
		pasta = "(importado) " + vaga
	} else if !errors.Is(err, ErrNotFound) {
		return false, err
	}
	b := fmtTime(batimento)
	if _, err := tx.ExecContext(ctx, `INSERT INTO agentes (vaga, papel, pasta, entrou_em, batimento, estado) VALUES (?, ?, ?, ?, ?, ?)`,
		vaga, papel, pasta, b, b, estado); err != nil {
		return false, err
	}
	if err := event(ctx, tx, c.stamp(), quem, "importado.agente", vaga, map[string]any{"papel": papel, "pasta": pasta}); err != nil {
		return false, err
	}
	return true, tx.Commit()
}

// MarkAllRead põe o cursor de todas as vagas na última mensagem (o histórico
// importado não acorda ninguém).
func (c *Core) MarkAllRead(ctx context.Context) error {
	_, err := c.db.ExecContext(ctx, `UPDATE agentes SET cursor = (SELECT COALESCE(max(seq), 0) FROM mensagens)`)
	return err
}

// EnsureCounter garante que o próximo id do prefixo seja maior que ultimo.
func (c *Core) EnsureCounter(ctx context.Context, prefixo string, ultimo int) error {
	if !prefixoID.MatchString(prefixo) {
		return invalid("prefixo de id %q", prefixo)
	}
	_, err := c.db.ExecContext(ctx, `INSERT INTO contadores (prefixo, ultimo) VALUES (?, ?)
		ON CONFLICT (prefixo) DO UPDATE SET ultimo = max(ultimo, excluded.ultimo)`, prefixo, ultimo)
	return err
}

// MessagesAll lista todas as mensagens em ordem (para exportar).
func (c *Core) MessagesAll(ctx context.Context) ([]Message, error) {
	return queryMessages(ctx, c.db, msgSelect+` ORDER BY seq`)
}
