package core

import (
	"errors"
	"testing"
	"time"
)

func saveItem(t *testing.T, c *Core, quem string, it Item, evento string) {
	t.Helper()
	if err := c.WithItems(ctx, quem, func(tx ItemTx) error { return tx.Save(it, evento, nil) }); err != nil {
		t.Fatal(err)
	}
}

func TestWithItemsSalvaEventoEMensagem(t *testing.T) {
	c, _ := newCoreClock(t)
	enter(t, c, "P1", "PLANEJADOR", "/p")
	enter(t, c, "R1", "REVISOR", "/r")
	ch := c.Changed()
	err := c.WithItems(ctx, "P1", func(tx ItemTx) error {
		it := Item{ID: "T-001", Tipo: "tarefa", Titulo: "corrigir x", Estado: "pronta", Prioridade: "P1",
			Arquivos: []string{"backend/x.py"}, Campos: map[string]any{"criterio_de_pronto": "teste passa"}}
		if err := tx.Save(it, "criado", map[string]any{"origem": "teste"}); err != nil {
			return err
		}
		return tx.Notify("REVISOR", "item.pronta", "T-001", "T-001 pronta")
	})
	if err != nil {
		t.Fatal(err)
	}
	it, notas, err := c.Item(ctx, "T-001")
	if err != nil || it.Titulo != "corrigir x" || len(it.Arquivos) != 1 || it.Campos["criterio_de_pronto"] != "teste passa" || len(notas) != 0 {
		t.Fatalf("item = %+v, %v, %v", it, notas, err)
	}
	if n := count(t, c, `SELECT count(*) FROM eventos WHERE tipo = 'item.criado' AND ref = 'T-001' AND quem = 'P1'`); n != 1 {
		t.Fatalf("eventos = %d", n)
	}
	if ms := systemMessages(inbox(t, c, "R1"), "item.pronta"); len(ms) != 1 || ms[0].Ref != "T-001" {
		t.Fatalf("mensagens = %+v", ms)
	}
	select {
	case <-ch:
	default:
		t.Fatal("Notify não acordou ninguém")
	}
}

func TestWithItemsRollback(t *testing.T) {
	c, _ := newCoreClock(t)
	enter(t, c, "P1", "PLANEJADOR", "/p")
	ch := c.Changed()
	boom := errors.New("boom")
	err := c.WithItems(ctx, "P1", func(tx ItemTx) error {
		if err := tx.Save(Item{ID: "T-001", Tipo: "tarefa", Estado: "pronta"}, "criado", nil); err != nil {
			return err
		}
		if err := tx.Notify("TODOS", "item.pronta", "T-001", "x"); err != nil {
			return err
		}
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v", err)
	}
	if _, _, err := c.Item(ctx, "T-001"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("item gravado apesar do erro: %v", err)
	}
	select {
	case <-ch:
		t.Fatal("acordou alguém numa transação desfeita")
	default:
	}
}

func TestEstadoDesdeMudaSoComEstado(t *testing.T) {
	c, now := newCoreClock(t)
	enter(t, c, "P1", "PLANEJADOR", "/p")
	saveItem(t, c, "P1", Item{ID: "T-001", Tipo: "tarefa", Estado: "pronta", Titulo: "a"}, "criado")
	t0 := *now
	*now = now.Add(5 * time.Minute)
	it, _, _ := c.Item(ctx, "T-001")
	it.Titulo = "b"
	saveItem(t, c, "P1", it, "atualizado")
	it, _, _ = c.Item(ctx, "T-001")
	if !it.EstadoDesde.Equal(t0) || !it.AtualizadoEm.Equal(*now) {
		t.Fatalf("estado_desde = %v, atualizado = %v", it.EstadoDesde, it.AtualizadoEm)
	}
	*now = now.Add(5 * time.Minute)
	it.Estado = "em_andamento"
	saveItem(t, c, "P1", it, "reservado")
	it, _, _ = c.Item(ctx, "T-001")
	if !it.EstadoDesde.Equal(*now) {
		t.Fatalf("estado_desde não mudou com o estado: %v", it.EstadoDesde)
	}
}

func TestItemsFiltro(t *testing.T) {
	c, _ := newCoreClock(t)
	enter(t, c, "P1", "PLANEJADOR", "/p")
	saveItem(t, c, "P1", Item{ID: "T-001", Tipo: "tarefa", Estado: "pronta", Sprint: "S-1"}, "criado")
	saveItem(t, c, "P1", Item{ID: "T-002", Tipo: "tarefa", Estado: "em_andamento", Dono: "F1"}, "criado")
	saveItem(t, c, "P1", Item{ID: "D-001", Tipo: "decisao", Estado: "chave1"}, "criado")
	casos := []struct {
		f    ItemFilter
		quer int
	}{
		{ItemFilter{}, 3},
		{ItemFilter{Tipos: []string{"tarefa"}}, 2},
		{ItemFilter{Estados: []string{"pronta", "em_andamento"}}, 2},
		{ItemFilter{Dono: "F1"}, 1},
		{ItemFilter{Sprint: "S-1"}, 1},
		{ItemFilter{IDs: []string{"T-001", "D-001"}}, 2},
	}
	for _, cs := range casos {
		its, err := c.Items(ctx, cs.f)
		if err != nil || len(its) != cs.quer {
			t.Fatalf("filtro %+v → %d itens (%v), quer %d", cs.f, len(its), err, cs.quer)
		}
	}
}

func TestNotasDoItem(t *testing.T) {
	c, _ := newCoreClock(t)
	enter(t, c, "F1", "FIXER", "/f")
	err := c.WithItems(ctx, "F1", func(tx ItemTx) error {
		if err := tx.Save(Item{ID: "T-001", Tipo: "tarefa", Estado: "pronta"}, "criado", nil); err != nil {
			return err
		}
		return tx.Note("T-001", "bloqueio", "não reproduz")
	})
	if err != nil {
		t.Fatal(err)
	}
	_, notas, err := c.Item(ctx, "T-001")
	if err != nil || len(notas) != 1 || notas[0].Tipo != "bloqueio" || notas[0].Vaga != "F1" {
		t.Fatalf("notas = %+v, %v", notas, err)
	}
}

func TestEntrarComArea(t *testing.T) {
	c, _ := newCoreClock(t)
	a, _, err := c.EnterWith(ctx, EnterArgs{Vaga: "F1", Papel: "FIXER", Pasta: "/f", Area: "tipografia"})
	if err != nil || a.Area != "tipografia" {
		t.Fatalf("a = %+v, %v", a, err)
	}
	a, _, err = c.EnterWith(ctx, EnterArgs{Vaga: "F1", Papel: "FIXER", Pasta: "/f"})
	if err != nil || a.Area != "" {
		t.Fatalf("reentrar sem área manteve %q (%v)", a.Area, err)
	}
}
