package core

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"time"
)

// Tipos das mensagens da CENTRAL geradas pelo Tick (Decisão 2 do plano).
const (
	TipoRespostaPrazo = "resposta.prazo"
	TipoAgenteSumiu   = "agente.sumiu"
)

// PapelGerente recebe os avisos de sumiço quando alguma vaga viva o ocupa; sem ela, vão a TODOS.
const PapelGerente = "GERENTE"

// Tick faz o trabalho periódico do projeto numa transação: avisa, uma vez,
// cada prazo de resposta vencido (ao destino original) e cada vaga que sumiu
// (uma vez por sumiço); avisa e libera as concessões vencidas e roda a
// admissão de cada recurso; e, uma vez por hora, apaga as amostras com mais
// de 7 dias. Acorda quem espera se gravou alguma mensagem.
func (c *Core) Tick(ctx context.Context) error {
	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	n1, err := c.tickPrazos(ctx, tx)
	if err != nil {
		return err
	}
	n2, err := c.tickSumidos(ctx, tx)
	if err != nil {
		return err
	}
	n3, err := c.tickRecursos(ctx, tx)
	if err != nil {
		return err
	}
	limpou, err := c.pruneSamples(ctx, tx, c.now().UTC())
	if err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	if !limpou.IsZero() {
		c.mu.Lock()
		c.limpouAmostras = limpou
		c.mu.Unlock()
	}
	if n1+n2+n3 > 0 {
		c.notify.Broadcast()
	}
	c.mu.Lock()
	hooks := slices.Clone(c.hooks)
	c.mu.Unlock()
	var errs []error
	for _, h := range hooks {
		errs = append(errs, h(ctx))
	}
	return errors.Join(errs...)
}

// TickHook é trabalho periódico de uma camada acima do núcleo (o processo),
// chamado no fim de cada Tick, depois das regras do núcleo e fora da transação.
type TickHook func(ctx context.Context) error

// AddTickHook acrescenta um gancho ao Tick.
func (c *Core) AddTickHook(h TickHook) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.hooks = append(c.hooks, h)
}

func (c *Core) tickPrazos(ctx context.Context, tx *sql.Tx) (int, error) {
	vencidas, err := queryMessages(ctx, tx, msgSelect+` WHERE prazo_avisado = 0 AND responder_ate <= ? AND`+pendente+` ORDER BY seq`, c.stamp())
	if err != nil {
		return 0, err
	}
	for _, m := range vencidas {
		if _, err := tx.ExecContext(ctx, `UPDATE mensagens SET prazo_avisado = 1 WHERE seq = ?`, m.Seq); err != nil {
			return 0, err
		}
		corpo := fmt.Sprintf("prazo de resposta vencido: mensagem %d de %s (%s); responda com central responder %d", m.Seq, m.De, m.Tipo, m.Seq)
		if err := c.sendSystem(ctx, tx, m.Para, TipoRespostaPrazo, strconv.FormatInt(m.Seq, 10), corpo); err != nil {
			return 0, err
		}
	}
	return len(vencidas), nil
}

func (c *Core) tickSumidos(ctx context.Context, tx *sql.Tx) (int, error) {
	rows, err := tx.QueryContext(ctx, agentSelect+` WHERE sumido_avisado = 0 AND estado <> '`+EstadoEncerrado+`' ORDER BY vaga`)
	if err != nil {
		return 0, err
	}
	now, s := c.now(), c.currentSettings()
	var sumidos []Agent
	for rows.Next() {
		a, err := scanAgent(rows)
		if err != nil {
			rows.Close()
			return 0, err
		}
		if c.sumido(a, now, s) {
			sumidos = append(sumidos, a)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	stamp := c.stamp()
	for _, a := range sumidos {
		vivo, err := c.gerenteVivo(ctx, tx, a.Vaga, now, s)
		if err != nil {
			return 0, err
		}
		para := Todos
		if vivo {
			para = PapelGerente
		}
		if _, err := tx.ExecContext(ctx, `UPDATE agentes SET sumido_avisado = 1 WHERE vaga = ?`, a.Vaga); err != nil {
			return 0, err
		}
		bat := a.Batimento.UTC().Format(TimeLayout)
		if err := event(ctx, tx, stamp, a.Vaga, "agente.sumiu", a.Vaga, map[string]string{"batimento": bat, "avisado": para}); err != nil {
			return 0, err
		}
		corpo := fmt.Sprintf("%s (%s) sumiu: sem batimento desde %s", a.Vaga, a.Papel, bat)
		if err := c.sendSystem(ctx, tx, para, TipoAgenteSumiu, a.Vaga, corpo); err != nil {
			return 0, err
		}
	}
	return len(sumidos), nil
}

// gerenteVivo diz se alguma vaga GERENTE além de exceto está viva (nem
// encerrada nem sumida), para o aviso de sumiço ir a quem o lê.
func (c *Core) gerenteVivo(ctx context.Context, tx *sql.Tx, exceto string, now time.Time, s Settings) (bool, error) {
	rows, err := tx.QueryContext(ctx, agentSelect+` WHERE papel = ? AND vaga <> ? AND estado <> '`+EstadoEncerrado+`'`, PapelGerente, exceto)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	for rows.Next() {
		g, err := scanAgent(rows)
		if err != nil {
			return false, err
		}
		if !c.sumido(g, now, s) {
			return true, nil
		}
	}
	return false, rows.Err()
}
