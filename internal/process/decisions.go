package process

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/JoaoVitorSantDrade/central/internal/core"
)

// Decisões de duas chaves (spec §6.2 e Decisão 4 do plano): o PLANEJADOR abre
// (chave 1), o GERENTE dá a chave 2 ou objeta; depois de uma objeção há mais
// uma rodada, e um segundo desacordo vai ao humano. O humano pode vetar a
// qualquer momento e responder as que foram escaladas.

// AbrirDecisao (PLANEJADOR) cria a decisão D-XXX em chave1 e acorda o GERENTE.
func (e *Engine) AbrirDecisao(ctx context.Context, vaga, titulo, corpo string) (core.Item, error) {
	if strings.TrimSpace(titulo) == "" || strings.TrimSpace(corpo) == "" {
		return core.Item{}, fmt.Errorf("%w: a decisão precisa de título e corpo", core.ErrInvalid)
	}
	if _, _, err := e.vagaPapel(ctx, vaga, "abrir decisão", Planejador); err != nil {
		return core.Item{}, err
	}
	id, err := e.c.NextID(ctx, vaga, "D", 1)
	if err != nil {
		return core.Item{}, err
	}
	err = e.c.WithItems(ctx, vaga, func(tx core.ItemTx) error {
		d := core.Item{ID: id, Tipo: TipoDecisao, Titulo: titulo, Estado: Chave1,
			Campos: map[string]any{"corpo": corpo, "chave1": vaga, "rodada": 1}}
		if err := tx.Save(d, "decisao_aberta", nil); err != nil {
			return err
		}
		return tx.Notify(e.roles.Name(Gerente), "decisao."+Chave1, id,
			fmt.Sprintf("%s pede a sua chave: %s (central decisao chave %s --acordo sim | --objecao \"motivo\")", id, titulo, id))
	})
	return e.decisao(ctx, id, err)
}

func (e *Engine) decisao(ctx context.Context, id string, err error) (core.Item, error) {
	if err != nil {
		return core.Item{}, err
	}
	it, _, err := e.c.Item(ctx, id)
	return it, err
}

// mudarDecisao carrega a decisão e aplica f numa transação.
func (e *Engine) mudarDecisao(ctx context.Context, quem, id string, f func(tx core.ItemTx, d *core.Item) error) (core.Item, error) {
	err := e.c.WithItems(ctx, quem, func(tx core.ItemTx) error {
		d, err := tx.Get(id)
		if err != nil {
			return err
		}
		if d.Tipo != TipoDecisao {
			return fmt.Errorf("%w: %s não é uma decisão", core.ErrInvalid, id)
		}
		if d.Campos == nil {
			d.Campos = map[string]any{}
		}
		return f(tx, &d)
	})
	return e.decisao(ctx, id, err)
}

func estadoDecisao(d *core.Item, acao string, estados ...string) error {
	if slices.Contains(estados, d.Estado) {
		return nil
	}
	return fmt.Errorf("%w: %s está em %s; %s exige %s", core.ErrConflict, d.ID, d.Estado, acao, strings.Join(estados, " ou "))
}

func rodada(d *core.Item) int {
	switch v := d.Campos["rodada"].(type) {
	case float64:
		return int(v)
	case int:
		return v
	}
	return 1
}

// Chave (GERENTE) dá a chave 2 (acordo) ou objeta com motivo. A segunda
// objeção leva a decisão ao humano.
func (e *Engine) Chave(ctx context.Context, vaga, id string, acordo bool, motivo string) (core.Item, error) {
	if !acordo && strings.TrimSpace(motivo) == "" {
		return core.Item{}, fmt.Errorf("%w: a objeção precisa de motivo e de uma alternativa", core.ErrInvalid)
	}
	return e.mudarDecisao(ctx, vaga, id, func(tx core.ItemTx, d *core.Item) error {
		if err := estadoDecisao(d, "a chave 2", Chave1); err != nil {
			return err
		}
		if _, _, err := e.exigePapel(tx, vaga, "dar a chave 2", Gerente); err != nil {
			return err
		}
		planejador := e.roles.Name(Planejador)
		if acordo {
			d.Estado = Aprovada
			d.Campos["chave2"] = vaga
			if err := tx.Save(*d, "decisao_aprovada", nil); err != nil {
				return err
			}
			return tx.Notify(planejador, "decisao."+Aprovada, d.ID, fmt.Sprintf("%s aprovada por %s (duas chaves)", d.ID, vaga))
		}
		if err := tx.Note(d.ID, "objecao", motivo); err != nil {
			return err
		}
		if rodada(d) >= 2 {
			d.Estado = AHumano
			if err := tx.Save(*d, "decisao_ao_humano", map[string]any{"motivo": motivo}); err != nil {
				return err
			}
			corpo := fmt.Sprintf("%s: o desacordo persistiu depois de duas rodadas e vai à caixa do humano; vale a opção mais conservadora até ele responder", d.ID)
			if err := tx.Notify(planejador, "decisao."+AHumano, d.ID, corpo); err != nil {
				return err
			}
			return tx.Notify(e.roles.Name(Gerente), "decisao."+AHumano, d.ID, corpo)
		}
		d.Estado = Objecao
		if err := tx.Save(*d, "decisao_objecao", map[string]any{"motivo": motivo}); err != nil {
			return err
		}
		return tx.Notify(planejador, "decisao."+Objecao, d.ID,
			fmt.Sprintf("%s recebeu objeção de %s: %s (revise com central decisao revisar %s ou desista)", d.ID, vaga, motivo, d.ID))
	})
}

// RevisarDecisao (PLANEJADOR) reescreve a decisão depois de uma objeção e a
// devolve à chave 1 para a última rodada.
func (e *Engine) RevisarDecisao(ctx context.Context, vaga, id, corpo string) (core.Item, error) {
	if strings.TrimSpace(corpo) == "" {
		return core.Item{}, fmt.Errorf("%w: a revisão precisa do corpo novo", core.ErrInvalid)
	}
	return e.mudarDecisao(ctx, vaga, id, func(tx core.ItemTx, d *core.Item) error {
		if err := estadoDecisao(d, "revisar", Objecao); err != nil {
			return err
		}
		if _, _, err := e.exigePapel(tx, vaga, "revisar decisão", Planejador); err != nil {
			return err
		}
		d.Estado = Chave1
		d.Campos["corpo"] = corpo
		d.Campos["rodada"] = rodada(d) + 1
		if err := tx.Save(*d, "decisao_revisada", nil); err != nil {
			return err
		}
		return tx.Notify(e.roles.Name(Gerente), "decisao."+Chave1, d.ID, fmt.Sprintf("%s revisada: última rodada da chave 2", d.ID))
	})
}

// EscalarDecisao (GERENTE ou PLANEJADOR) manda a decisão ao humano.
func (e *Engine) EscalarDecisao(ctx context.Context, vaga, id string) (core.Item, error) {
	return e.mudarDecisao(ctx, vaga, id, func(tx core.ItemTx, d *core.Item) error {
		if err := estadoDecisao(d, "escalar", Rascunho, Chave1, Objecao); err != nil {
			return err
		}
		if _, _, err := e.exigePapel(tx, vaga, "escalar decisão", Gerente, Planejador); err != nil {
			return err
		}
		d.Estado = AHumano
		return tx.Save(*d, "decisao_ao_humano", nil)
	})
}

// Veto (HUMANO) vale em qualquer estado não final: a decisão vira "não"
// dali em diante, e PLANEJADOR e GERENTE são avisados.
func (e *Engine) Veto(ctx context.Context, id, motivo string) (core.Item, error) {
	if strings.TrimSpace(motivo) == "" {
		return core.Item{}, fmt.Errorf("%w: o veto precisa de motivo", core.ErrInvalid)
	}
	return e.mudarDecisao(ctx, Humano, id, func(tx core.ItemTx, d *core.Item) error {
		if d.Estado == Vetada {
			return fmt.Errorf("%w: %s já foi vetada", core.ErrConflict, d.ID)
		}
		if err := tx.Note(d.ID, "veto", motivo); err != nil {
			return err
		}
		d.Estado = Vetada
		if err := tx.Save(*d, "decisao_vetada", map[string]any{"motivo": motivo}); err != nil {
			return err
		}
		corpo := fmt.Sprintf("o humano vetou %s: %s (a reversão vira tarefa P1)", d.ID, motivo)
		if err := tx.Notify(e.roles.Name(Planejador), "decisao."+Vetada, d.ID, corpo); err != nil {
			return err
		}
		return tx.Notify(e.roles.Name(Gerente), "decisao."+Vetada, d.ID, corpo)
	})
}

// ResponderDecisao (HUMANO) aprova ou recusa uma decisão escalada.
func (e *Engine) ResponderDecisao(ctx context.Context, id string, aprovada bool, texto string) (core.Item, error) {
	return e.mudarDecisao(ctx, Humano, id, func(tx core.ItemTx, d *core.Item) error {
		if err := estadoDecisao(d, "a resposta do humano", AHumano); err != nil {
			return err
		}
		if texto != "" {
			if err := tx.Note(d.ID, "resposta_humano", texto); err != nil {
				return err
			}
		}
		d.Estado = Vetada
		if aprovada {
			d.Estado = Aprovada
		}
		if err := tx.Save(*d, "decisao_respondida", map[string]any{"aprovada": aprovada}); err != nil {
			return err
		}
		return tx.Notify(e.roles.Name(Planejador), "decisao."+d.Estado, d.ID, fmt.Sprintf("o humano respondeu %s: %s", d.ID, d.Estado))
	})
}
