package core

import (
	"context"
	"encoding/json"
	"regexp"
)

// tipoExt é o formato do tipo de um evento de extensão (sem o prefixo ext.).
var tipoExt = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]*$`)

// ExtEvent é um evento emitido por uma extensão e quem ele acorda.
type ExtEvent struct {
	Tipo  string
	Ref   string
	Dados map[string]any
	// Para são os destinos (papéis, vagas ou TODOS) da mensagem de acordar.
	Para  []string
	Corpo string
}

// ExtFact é um fato do projeto gravado pela resposta de uma extensão.
type ExtFact struct {
	Chave  string
	Valor  json.RawMessage
	Motivo string
}

// ExtEvents grava, numa transação, os eventos ext.<tipo> de uma chamada da
// extensão (com dados.extensao), as mensagens de CENTRAL que acordam os
// destinos de cada um, os fatos da resposta (quem = quem chamou) e o evento
// ext.chamada (chamada nil não grava).
func (c *Core) ExtEvents(ctx context.Context, quem, extensao, acao string, evs []ExtEvent, fatos []ExtFact, chamada map[string]any) error {
	for _, ev := range evs {
		if !tipoExt.MatchString(ev.Tipo) {
			return invalid("evento de extensão com tipo %q (use letras minúsculas, dígitos, . _ e -)", ev.Tipo)
		}
	}
	compactos := make([]string, len(fatos))
	for i, f := range fatos {
		v, err := validFact(f.Chave, f.Valor, f.Motivo)
		if err != nil {
			return err
		}
		compactos[i] = v
	}
	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	stamp := c.stamp()
	for _, ev := range evs {
		dados := map[string]any{}
		for k, v := range ev.Dados {
			dados[k] = v
		}
		dados["extensao"] = extensao
		if err := event(ctx, tx, stamp, quem, "ext."+ev.Tipo, ev.Ref, dados); err != nil {
			return err
		}
		for _, para := range ev.Para {
			if err := c.sendSystem(ctx, tx, para, "ext."+ev.Tipo, ev.Ref, ev.Corpo); err != nil {
				return err
			}
		}
	}
	for i, f := range fatos {
		if err := insertFact(ctx, tx, stamp, quem, f.Chave, compactos[i], f.Motivo); err != nil {
			return err
		}
	}
	if chamada != nil {
		dados := map[string]any{"extensao": extensao, "acao": acao}
		for k, v := range chamada {
			dados[k] = v
		}
		if err := event(ctx, tx, stamp, quem, "ext.chamada", extensao, dados); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	c.notify.Broadcast()
	return nil
}
