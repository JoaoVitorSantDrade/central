package core

import (
	"context"
	"testing"
)

func TestExtEventsAcordaEGrava(t *testing.T) {
	c := newCore(t)
	ctx := context.Background()
	enter(t, c, "PESQ1", "PESQUISADOR", "/p")
	enter(t, c, "EXEC1", "EXECUTOR", "/e")
	err := c.ExtEvents(ctx, Humano, "exemplos", "importar", []ExtEvent{
		{Tipo: "exemplo-novo", Ref: "digimon", Dados: map[string]any{"resumo": "conjunto digimon"}, Para: []string{"PESQUISADOR"}, Corpo: "conjunto digimon"},
	}, nil, map[string]any{"ok": true, "ms": 12})
	if err != nil {
		t.Fatal(err)
	}
	ms, err := c.Inbox(ctx, "PESQ1")
	if err != nil {
		t.Fatal(err)
	}
	if len(ms) != 1 || ms[0].De != Sistema || ms[0].Tipo != "ext.exemplo-novo" || ms[0].Ref != "digimon" || ms[0].Corpo != "conjunto digimon" {
		t.Fatalf("caixa = %+v", ms)
	}
	if n, _ := c.Unread(ctx, "EXEC1"); n != 0 {
		t.Fatalf("EXEC1 não devia acordar (%d)", n)
	}
	evs, err := c.Events(ctx, EventFilter{Prefixos: []string{"ext."}})
	if err != nil {
		t.Fatal(err)
	}
	if len(evs) != 2 || evs[0].Tipo != "ext.exemplo-novo" || evs[0].Quem != Humano || evs[0].Dados["extensao"] != "exemplos" ||
		evs[1].Tipo != "ext.chamada" || evs[1].Dados["acao"] != "importar" {
		t.Fatalf("eventos = %+v", evs)
	}
	if err := c.ExtEvents(ctx, "PESQ1", "e", "a", []ExtEvent{{Tipo: "Ruim Tipo"}}, nil, nil); err == nil {
		t.Fatal("tipo inválido deveria falhar")
	}
}

func TestExtEventsGravaFatos(t *testing.T) {
	c := newCore(t)
	ctx := context.Background()
	fatos := []ExtFact{{Chave: "e2e.abc1234-1", Valor: []byte(`{"gates":{"x":1}}`), Motivo: "e2e nível 1"}}
	if err := c.ExtEvents(ctx, Sistema, "e2e", "registrar", nil, fatos, map[string]any{"ok": true}); err != nil {
		t.Fatal(err)
	}
	f, err := c.GetFact(ctx, "e2e.abc1234-1")
	if err != nil || f.Vaga != Sistema || string(f.Valor) != `{"gates":{"x":1}}` {
		t.Fatalf("fato = %+v %v", f, err)
	}
	ruim := []ExtFact{{Chave: "Chave Ruim", Valor: []byte(`1`), Motivo: "x"}}
	if err := c.ExtEvents(ctx, Sistema, "e2e", "registrar", nil, ruim, nil); err == nil {
		t.Fatal("chave inválida devia falhar")
	}
	if err := c.ExtEvents(ctx, Sistema, "e2e", "registrar", nil, []ExtFact{{Chave: "a", Valor: []byte(`{`), Motivo: "x"}}, nil); err == nil {
		t.Fatal("valor inválido devia falhar")
	}
}
