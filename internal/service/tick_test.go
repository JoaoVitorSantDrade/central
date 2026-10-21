package service

import (
	"context"
	"testing"
	"time"

	"github.com/JoaoVitorSantDrade/central/internal/core"
)

func coreOf(t *testing.T, s *Service, proj string) *core.Core {
	t.Helper()
	p, err := s.project(ctx, proj)
	if err != nil {
		t.Fatal(err)
	}
	return p.core
}

func TestMotivoDerivado(t *testing.T) {
	s, _ := newService(t)
	mustEnter(t, s, "p", "/a", "A1")
	mustEnter(t, s, "p", "/b", "B1")
	c := coreOf(t, s, "p")
	sis, err := c.Send(ctx, core.SendInput{De: core.Sistema, Para: "B1", Tipo: "recurso.concedida", Ref: "gpu", Corpo: "sua vez"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Send(ctx, "p", "/a", core.SendInput{Para: "B1", Tipo: "nota", Corpo: "oi"}); err != nil {
		t.Fatal(err)
	}
	r, err := s.Wait(ctx, "p", "/b", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if r.Motivo != "recurso" || r.Novas != 2 || r.Primeira == nil || r.Primeira.Seq != sis.Seq {
		t.Fatalf("r = %+v", r)
	}
	if _, err := s.Inbox(ctx, "p", "/b"); err != nil {
		t.Fatal(err)
	}
	m, err := s.Send(ctx, "p", "/a", core.SendInput{Para: "B1", Tipo: "recurso.pedido", Corpo: "de um agente"})
	if err != nil {
		t.Fatal(err)
	}
	r, err = s.Wait(ctx, "p", "/b", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if r.Motivo != "mensagem" || r.Novas != 1 || r.Primeira == nil || r.Primeira.Seq != m.Seq {
		t.Fatalf("r = %+v", r)
	}
}

func TestTickerChamaTick(t *testing.T) {
	s, clk, _ := newServiceClock(t)
	mustEnter(t, s, "p", "/a", "A1")
	mustEnter(t, s, "p", "/b", "B1")
	if _, err := s.Send(ctx, "p", "/a", core.SendInput{Para: "B1", Tipo: "pergunta", Corpo: "e aí?", ResponderEm: time.Minute}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Inbox(ctx, "p", "/b"); err != nil {
		t.Fatal(err)
	}
	clk.advance(2 * time.Minute)
	tctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() { s.RunTicker(tctx, 10*time.Millisecond); close(done) }()
	defer func() { cancel(); <-done }()
	r, err := s.Wait(ctx, "p", "/b", 500*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if r.Motivo != "resposta" || r.Primeira == nil || r.Primeira.Tipo != "resposta.prazo" {
		t.Fatalf("r = %+v", r)
	}
}

func TestOpenAllAbreRegistrados(t *testing.T) {
	dir := t.TempDir()
	s, err := New(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	mustEnter(t, s, "p1", "/a", "A1")
	mustEnter(t, s, "p2", "/b", "B1")
	s.Close()
	s2, err := New(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	if err := s2.OpenAll(ctx); err != nil {
		t.Fatal(err)
	}
	if len(s2.projects) != 2 {
		t.Fatalf("abertos = %d", len(s2.projects))
	}
}

func TestWaitRenovaBatimentoEnquantoEspera(t *testing.T) {
	s, clk, _ := newServiceClock(t)
	s.waitTouchEvery = 10 * time.Millisecond
	mustEnter(t, s, "p", "/b", "B1")
	wctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() { s.Wait(wctx, "p", "/b", 5*time.Second); close(done) }()
	defer func() { cancel(); <-done }()
	batimentoChega := func(quer time.Time) {
		t.Helper()
		deadline := time.Now().Add(time.Second)
		for !agentOf(t, s, "p", "B1").Batimento.Equal(quer) {
			if time.Now().After(deadline) {
				t.Fatalf("batimento = %v, quer %v", agentOf(t, s, "p", "B1").Batimento, quer)
			}
			select {
			case <-done:
				t.Fatal("Wait terminou antes da hora")
			case <-time.After(5 * time.Millisecond):
			}
		}
	}
	// Primeiro o Wait está de pé (o toque do início ou da renovação já leu
	// este relógio); depois, só a renovação periódica alcança o seguinte.
	batimentoChega(clk.advance(10 * time.Minute))
	batimentoChega(clk.advance(10 * time.Minute))
}
