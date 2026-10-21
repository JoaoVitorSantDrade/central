package process

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/JoaoVitorSantDrade/central/internal/core"
)

// Config são os parâmetros do processo vindos do central.toml.
type Config struct {
	// Obrigatorios são os campos que um item precisa ter para ficar pronto.
	Obrigatorios []string
	Sprint       SprintCfg
	// LimiteParado é quanto um item pode ficar num estado antes de virar alerta.
	LimiteParado map[string]time.Duration
	// OrfasApos é quanto o dono de um item reservado pode sumir antes de o item virar órfão.
	OrfasApos time.Duration
}

// SprintCfg são os deslocamentos antes do fim e quem cada cerimônia acorda.
type SprintCfg struct {
	Escopo, Congelamento, Corte, Retro time.Duration
	// Acordar sobrescreve, por cerimônia, os papéis canônicos acordados (ou TODOS).
	Acordar map[string][]string
}

// Engine aplica as regras do processo sobre o núcleo de um projeto.
type Engine struct {
	c     *core.Core
	roles Roles
	cfg   Config
	loc   *time.Location

	// invalidosVistos evita repetir o evento de um arquivo inválido igual (caminho+hash).
	invalidosVistos sync.Map
}

// NewEngine cria o processo de um projeto.
func NewEngine(c *core.Core, roles Roles, cfg Config) *Engine {
	return &Engine{c: c, roles: roles, cfg: cfg, loc: time.Local}
}

// Roles devolve os papéis do projeto.
func (e *Engine) Roles() Roles { return e.roles }

// papelDe devolve o papel canônico da vaga dentro da transação.
func (e *Engine) papelDe(tx core.ItemTx, vaga string) (core.Agent, string, error) {
	a, err := tx.Agent(vaga)
	if err != nil {
		return core.Agent{}, "", err
	}
	c, ok := e.roles.Canonical(a.Papel)
	if !ok {
		return a, "", fmt.Errorf("%w: a vaga %s tem o papel %q, que este projeto não conhece", core.ErrInvalid, vaga, a.Papel)
	}
	return a, c, nil
}

// exigePapel recusa com ErrInvalid quando a vaga não tem um dos papéis.
func (e *Engine) exigePapel(tx core.ItemTx, vaga, acao string, papeis ...string) (core.Agent, string, error) {
	a, c, err := e.papelDe(tx, vaga)
	if err != nil {
		return a, c, err
	}
	for _, p := range papeis {
		if c == p {
			return a, c, nil
		}
	}
	nomes := make([]string, len(papeis))
	for i, p := range papeis {
		nomes[i] = e.roles.Name(p)
	}
	return a, c, fmt.Errorf("%w: o papel %s não pode %s (só %v)", core.ErrInvalid, a.Papel, acao, nomes)
}

// vagaPapel lê a vaga fora de uma transação de itens.
func (e *Engine) vagaPapel(ctx context.Context, vaga, acao string, papeis ...string) (core.Agent, string, error) {
	var a core.Agent
	var c string
	err := e.c.WithItems(ctx, vaga, func(tx core.ItemTx) error {
		var err error
		a, c, err = e.exigePapel(tx, vaga, acao, papeis...)
		return err
	})
	return a, c, err
}
