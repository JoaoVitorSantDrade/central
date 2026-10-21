package core

import "context"

// TipoResposta é o tipo das mensagens enviadas por Reply.
const TipoResposta = "resposta"

// pendente filtra (em msgSelect) as mensagens com prazo de resposta que ainda
// não receberam nenhuma resposta.
const pendente = ` responder_ate IS NOT NULL
	AND NOT EXISTS (SELECT 1 FROM mensagens r WHERE r.responde_a = mensagens.seq)`

// Pendencias são as respostas em aberto de uma vaga: as que ela deve e as que espera.
type Pendencias struct {
	Devo    []Message `json:"devo"`
	Esperam []Message `json:"esperam"`
}

// Reply responde à mensagem seq: vai para o remetente original, com tipo
// resposta, o mesmo ref e RespondeA = seq. Só um destinatário dela responde.
func (c *Core) Reply(ctx context.Context, deVaga string, seq int64, corpo string) (Message, error) {
	orig, err := messageBySeq(ctx, c.db, seq)
	if err != nil {
		return Message{}, err
	}
	// Send confere de novo, na transação, que deVaga pode responder.
	return c.Send(ctx, SendInput{De: deVaga, Para: orig.De, Tipo: TipoResposta, Ref: orig.Ref, Corpo: corpo, RespondeA: seq})
}

// canReply diz se deVaga recebeu orig: endereçada a ela, ao papel dela ou a
// TODOS, sem ser ela a remetente. Mensagens da CENTRAL não têm resposta.
func canReply(ctx context.Context, q querier, deVaga string, orig Message) error {
	if orig.De == Sistema {
		return invalid("mensagens da %s não recebem resposta", Sistema)
	}
	if orig.De == deVaga {
		return invalid("%s não pode responder à própria mensagem %d", deVaga, orig.Seq)
	}
	if orig.Para == deVaga {
		return nil
	}
	rows, err := q.QueryContext(ctx, `SELECT papel FROM agentes WHERE vaga = ?`, deVaga)
	if err != nil {
		return err
	}
	defer rows.Close()
	var papel string
	if rows.Next() {
		if err := rows.Scan(&papel); err != nil {
			return err
		}
		if orig.Para == Todos || orig.Para == papel {
			return nil
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	return invalid("%s não recebeu a mensagem %d (para %s)", deVaga, orig.Seq, orig.Para)
}

// Pending devolve as pendências de resposta da vaga, em ordem de seq: Devo são
// as endereçadas a ela (direto, pelo papel ou a TODOS); Esperam são as que ela enviou.
func (c *Core) Pending(ctx context.Context, vaga string) (Pendencias, error) {
	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return Pendencias{}, err
	}
	defer tx.Rollback()
	a, err := scanAgent(tx.QueryRowContext(ctx, agentSelect+` WHERE vaga = ?`, vaga))
	if err != nil {
		return Pendencias{}, err
	}
	var p Pendencias
	if p.Devo, err = queryMessages(ctx, tx, msgSelect+` WHERE para IN (?, ?, '`+Todos+`') AND de <> ? AND`+pendente+` ORDER BY seq`,
		a.Vaga, a.Papel, a.Vaga); err != nil {
		return Pendencias{}, err
	}
	if p.Esperam, err = queryMessages(ctx, tx, msgSelect+` WHERE de = ? AND`+pendente+` ORDER BY seq`, a.Vaga); err != nil {
		return Pendencias{}, err
	}
	return p, tx.Commit()
}
