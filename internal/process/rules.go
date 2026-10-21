package process

import (
	"context"
	"fmt"
	"strings"

	"github.com/JoaoVitorSantDrade/central/internal/core"
)

// canonicosDe traduz os nomes de "quem aplica" (do projeto ou canônicos) para
// os canônicos; TODOS fica TODOS. Nomes desconhecidos voltam em desconhecidos.
func (e *Engine) canonicosDe(nomes []string) (out, desconhecidos []string) {
	vistos := map[string]bool{}
	for _, n := range nomes {
		n = strings.ToUpper(strings.TrimSpace(n))
		c := ""
		switch {
		case n == core.Todos:
			c = core.Todos
		default:
			if can, ok := e.roles.Canonical(n); ok {
				c = can
			} else if IsCanonical(n) {
				c = n
			}
		}
		if c == "" {
			desconhecidos = append(desconhecidos, n)
			continue
		}
		if !vistos[c] {
			vistos[c] = true
			out = append(out, c)
		}
	}
	return out, desconhecidos
}

// SalvarRegra (GERENTE ou PLANEJADOR) grava uma regra; os nomes de quem aplica
// são traduzidos para os papéis canônicos. mudou=false se nada mudou.
func (e *Engine) SalvarRegra(ctx context.Context, vaga string, r core.Rule) (core.Rule, bool, error) {
	if _, _, err := e.vagaPapel(ctx, vaga, "gravar regras", Gerente, Planejador); err != nil {
		return core.Rule{}, false, err
	}
	aplica, desconhecidos := e.canonicosDe(r.Aplica)
	if len(desconhecidos) > 0 {
		return core.Rule{}, false, fmt.Errorf("%w: regra %s: papéis desconhecidos %v", core.ErrInvalid, r.ID, desconhecidos)
	}
	r.Aplica = aplica
	if r.Estado == "" {
		r.Estado = "ativa"
	}
	return e.c.SaveRule(ctx, vaga, r)
}

// EstadoRegra (GERENTE ou PLANEJADOR) muda o estado de uma regra.
func (e *Engine) EstadoRegra(ctx context.Context, vaga, id, estado, comando string) (core.Rule, error) {
	if _, _, err := e.vagaPapel(ctx, vaga, "mudar regras", Gerente, Planejador); err != nil {
		return core.Rule{}, err
	}
	return e.c.SetRuleState(ctx, vaga, id, estado, comando)
}

// RegrasDoPapel devolve as regras ativas que se aplicam ao papel canônico.
func (e *Engine) RegrasDoPapel(ctx context.Context, canonico string) ([]core.Rule, error) {
	return e.c.Rules(ctx, core.RuleFilter{Papel: canonico, Estado: "ativa"})
}
