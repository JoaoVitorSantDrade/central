package core

import (
	"context"
	"errors"
	"testing"
	"time"
)

func sprintAt(base time.Time) Sprint {
	return Sprint{ID: "S-1", Abertura: base, Checkin: base.Add(time.Hour), Escopo: base.Add(2 * time.Hour),
		Congelamento: base.Add(2*time.Hour + 15*time.Minute), Corte: base.Add(2*time.Hour + 45*time.Minute),
		Retro: base.Add(2*time.Hour + 55*time.Minute), Fim: base.Add(3*time.Hour + 15*time.Minute)}
}

func TestCreateSprintUmaAbertaPorVez(t *testing.T) {
	c, now := newCoreClock(t)
	enter(t, c, "G", "GERENTE", "/g")
	if err := c.CreateSprint(ctx, "G", sprintAt(*now)); err != nil {
		t.Fatal(err)
	}
	s2 := sprintAt(*now)
	s2.ID = "S-2"
	if err := c.CreateSprint(ctx, "G", s2); !errors.Is(err, ErrConflict) {
		t.Fatalf("segunda sprint aberta: %v", err)
	}
	cur, err := c.CurrentSprint(ctx)
	if err != nil || cur == nil || cur.ID != "S-1" || !cur.Fim.Equal(now.Add(3*time.Hour+15*time.Minute)) {
		t.Fatalf("corrente = %+v, %v", cur, err)
	}
	if n := count(t, c, `SELECT count(*) FROM eventos WHERE tipo = 'sprint.aberta' AND ref = 'S-1'`); n != 1 {
		t.Fatalf("eventos = %d", n)
	}
}

func TestCurrentSprintSemNenhuma(t *testing.T) {
	c, _ := newCoreClock(t)
	if s, err := c.CurrentSprint(ctx); err != nil || s != nil {
		t.Fatalf("s = %+v, %v", s, err)
	}
}

func TestDueEMarkCeremony(t *testing.T) {
	c, now := newCoreClock(t)
	enter(t, c, "G", "GERENTE", "/g")
	enter(t, c, "F", "FIXER", "/f")
	s := sprintAt(*now)
	if err := c.CreateSprint(ctx, "G", s); err != nil {
		t.Fatal(err)
	}
	*now = now.Add(70 * time.Minute) // abertura e checkin vencidos
	due, err := c.DueCeremonies(ctx, *now)
	if err != nil || len(due) != 2 || due[0].Nome != "abertura" || due[1].Nome != "checkin" {
		t.Fatalf("due = %+v, %v", due, err)
	}
	ok, err := c.MarkCeremony(ctx, "S-1", "abertura", due[0].Quando, []string{"TODOS"}, "sprint aberta")
	if err != nil || !ok {
		t.Fatalf("mark = %v, %v", ok, err)
	}
	// idempotente: a segunda marcação não avisa de novo
	ok, err = c.MarkCeremony(ctx, "S-1", "abertura", due[0].Quando, []string{"TODOS"}, "sprint aberta")
	if err != nil || ok {
		t.Fatalf("segunda marca = %v, %v", ok, err)
	}
	if ms := systemMessages(inbox(t, c, "F"), "cerimonia.abertura"); len(ms) != 1 || ms[0].Ref != "S-1" {
		t.Fatalf("mensagens = %+v", ms)
	}
	due, _ = c.DueCeremonies(ctx, *now)
	if len(due) != 1 || due[0].Nome != "checkin" {
		t.Fatalf("due depois = %+v", due)
	}
}

func TestMarkFimEncerraSprint(t *testing.T) {
	c, now := newCoreClock(t)
	enter(t, c, "G", "GERENTE", "/g")
	s := sprintAt(*now)
	if err := c.CreateSprint(ctx, "G", s); err != nil {
		t.Fatal(err)
	}
	if _, err := c.MarkCeremony(ctx, "S-1", "fim", s.Fim, []string{"TODOS"}, "fim"); err != nil {
		t.Fatal(err)
	}
	if cur, _ := c.CurrentSprint(ctx); cur != nil {
		t.Fatalf("sprint segue aberta depois do fim: %+v", cur)
	}
}

func TestUpdateSprintComMensagem(t *testing.T) {
	c, now := newCoreClock(t)
	enter(t, c, "G", "GERENTE", "/g")
	s := sprintAt(*now)
	if err := c.CreateSprint(ctx, "G", s); err != nil {
		t.Fatal(err)
	}
	desde := *now
	s.AnteciparDesde = &desde
	s.AnteciparAvisado = true
	if err := c.UpdateSprint(ctx, Sistema, s, "encerramento_condicao", SystemMsg{Para: "GERENTE", Tipo: "sprint.encerrar_antes", Ref: "S-1", Corpo: "x"}); err != nil {
		t.Fatal(err)
	}
	cur, _ := c.CurrentSprint(ctx)
	if cur.AnteciparDesde == nil || !cur.AnteciparDesde.Equal(desde) || !cur.AnteciparAvisado {
		t.Fatalf("corrente = %+v", cur)
	}
	if ms := systemMessages(inbox(t, c, "G"), "sprint.encerrar_antes"); len(ms) != 1 {
		t.Fatalf("mensagens = %+v", ms)
	}
	if ss, err := c.Sprints(ctx); err != nil || len(ss) != 1 {
		t.Fatalf("sprints = %+v, %v", ss, err)
	}
}

func TestTickHookRoda(t *testing.T) {
	c, _ := newCoreClock(t)
	n := 0
	c.AddTickHook(func(context.Context) error { n++; return nil })
	tick(t, c)
	tick(t, c)
	if n != 2 {
		t.Fatalf("gancho rodou %d vezes", n)
	}
}
