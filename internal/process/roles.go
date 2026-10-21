// Package process implementa o processo opinativo "sprint" da spec §6 sobre o
// núcleo: papéis, ciclo de vida dos itens, escolha de tarefa, sprint e métricas.
// Não usa SQL: tudo passa pelo core (spec §4, regra 2).
package process

import (
	"slices"

	"github.com/JoaoVitorSantDrade/central/internal/core"
)

// Papéis canônicos (spec §6.1). HUMANO só veta e responde.
const (
	Gerente     = "GERENTE"
	Planejador  = "PLANEJADOR"
	Pesquisador = "PESQUISADOR"
	Executor    = "EXECUTOR"
	Revisor     = "REVISOR"
	Validador   = "VALIDADOR"
	Integrador  = "INTEGRADOR"
	Humano      = core.Humano
)

// Canonicos lista os papéis canônicos na ordem da spec.
var Canonicos = []string{Gerente, Planejador, Pesquisador, Executor, Revisor, Validador, Integrador, Humano}

// Obrigatorios são os papéis que não podem ser desligados.
var Obrigatorios = []string{Gerente, Planejador, Executor}

// Roles traduz entre os papéis canônicos e os nomes que o projeto usa.
type Roles struct {
	nome       map[string]string // canônico → nome no projeto
	canonico   map[string]string // nome no projeto → canônico
	desligados map[string]bool
	senior     map[string]bool
}

// NewRoles monta os papéis a partir do [papeis] do central.toml (já validado).
func NewRoles(nomes map[string]string, desligados, senior []string) Roles {
	r := Roles{nome: map[string]string{}, canonico: map[string]string{}, desligados: map[string]bool{}, senior: map[string]bool{}}
	for _, c := range Canonicos {
		n := c
		if v, ok := nomes[c]; ok && v != "" {
			n = v
		}
		r.nome[c] = n
		r.canonico[n] = c
	}
	for _, d := range desligados {
		r.desligados[d] = true
	}
	for _, s := range senior {
		r.senior[s] = true
	}
	return r
}

// Canonical devolve o papel canônico de um papel do projeto.
func (r Roles) Canonical(papel string) (string, bool) {
	c, ok := r.canonico[papel]
	return c, ok
}

// Name devolve o nome no projeto de um papel canônico.
func (r Roles) Name(canonico string) string {
	if n, ok := r.nome[canonico]; ok {
		return n
	}
	return canonico
}

// Enabled diz se o papel canônico está ligado no projeto.
func (r Roles) Enabled(canonico string) bool { return !r.desligados[canonico] }

// IsSenior diz se a vaga é sênior (pega primeiro as tarefas bloqueadas).
func (r Roles) IsSenior(vaga string) bool { return r.senior[vaga] }

// HasSenior diz se o projeto declara alguma vaga sênior.
func (r Roles) HasSenior() bool { return len(r.senior) > 0 }

// IsCanonical diz se o nome é um papel canônico.
func IsCanonical(p string) bool { return slices.Contains(Canonicos, p) }

// Cerimonias são os horários da sprint, na ordem (spec §6.4).
var Cerimonias = []string{"abertura", "checkin", "escopo", "congelamento", "corte", "retro", "fim"}

// EstadosDeFonte são os estados que uma pasta do adaptador de arquivos pode representar.
var EstadosDeFonte = []string{"proposta", "pronta", "integrada"}
