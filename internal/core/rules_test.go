package core

import (
	"errors"
	"testing"
)

func TestRegraCriarEFiltrar(t *testing.T) {
	c, _ := newCoreClock(t)
	for _, r := range []Rule{
		{ID: "L-001", Texto: "a", Pratica: "faça a", Aplica: []string{"EXECUTOR", "REVISOR"}, Estado: "ativa"},
		{ID: "L-002", Texto: "b", Pratica: "faça b", Aplica: []string{"TODOS"}, Estado: "ativa"},
		{ID: "L-003", Texto: "c", Pratica: "faça c", Aplica: []string{"GERENTE"}, Estado: "no_codigo", Comando: "central proxima"},
	} {
		if _, _, err := c.SaveRule(ctx, "G", r); err != nil {
			t.Fatal(err)
		}
	}
	rs, err := c.Rules(ctx, RuleFilter{Papel: "EXECUTOR"})
	if err != nil || len(rs) != 2 {
		t.Fatalf("regras do executor = %+v, %v", rs, err)
	}
	if rs, _ := c.Rules(ctx, RuleFilter{Estado: "ativa"}); len(rs) != 2 {
		t.Fatalf("ativas = %+v", rs)
	}
	_, mudou, err := c.SaveRule(ctx, "G", Rule{ID: "L-001", Texto: "a", Pratica: "faça a", Aplica: []string{"EXECUTOR", "REVISOR"}, Estado: "ativa"})
	if err != nil || mudou {
		t.Fatalf("regra igual regravada: %v, %v", mudou, err)
	}
	if n := count(t, c, `SELECT count(*) FROM eventos WHERE tipo = 'regra.gravada'`); n != 3 {
		t.Fatalf("eventos = %d", n)
	}
}

func TestRegraEstadoValido(t *testing.T) {
	c, _ := newCoreClock(t)
	if _, _, err := c.SaveRule(ctx, "G", Rule{ID: "L-001", Texto: "a", Pratica: "b", Aplica: []string{"TODOS"}, Estado: "talvez"}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("estado inválido: %v", err)
	}
	if _, _, err := c.SaveRule(ctx, "G", Rule{ID: "L-001", Texto: "a", Pratica: "b", Aplica: []string{"TODOS"}, Estado: "ativa"}); err != nil {
		t.Fatal(err)
	}
	r, err := c.SetRuleState(ctx, "G", "L-001", "no_codigo", "central proxima")
	if err != nil || r.Estado != "no_codigo" || r.Comando != "central proxima" {
		t.Fatalf("r = %+v, %v", r, err)
	}
	if _, err := c.SetRuleState(ctx, "G", "L-999", "ativa", ""); !errors.Is(err, ErrNotFound) {
		t.Fatalf("regra inexistente: %v", err)
	}
	if _, _, err := c.SaveRule(ctx, "G", Rule{ID: "x", Texto: "a", Pratica: "b", Estado: "ativa"}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("regra sem aplica: %v", err)
	}
}
