package service

import (
	"sync"
	"testing"
	"time"

	"github.com/JoaoVitorSantDrade/central/internal/core"
)

// clock é um relógio que o teste avança; seguro entre goroutines.
type clock struct {
	mu sync.Mutex
	t  time.Time
}

func newClock() *clock { return &clock{t: time.Date(2026, 10, 20, 22, 0, 0, 0, time.UTC)} }

func (c *clock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *clock) advance(d time.Duration) time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
	return c.t
}

func newServiceClock(t *testing.T) (*Service, *clock, string) {
	t.Helper()
	dir := t.TempDir()
	clk := newClock()
	s, err := New(dir, clk.now)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s, clk, dir
}

func agentOf(t *testing.T, s *Service, proj, vaga string) core.Agent {
	t.Helper()
	as, err := s.Agents(ctx, proj, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range as {
		if a.Vaga == vaga {
			return a
		}
	}
	t.Fatalf("vaga %s não listada: %+v", vaga, as)
	return core.Agent{}
}

func TestSendEWaitRenovamBatimento(t *testing.T) {
	s, clk, _ := newServiceClock(t)
	mustEnter(t, s, "p", "/a", "A1")
	mustEnter(t, s, "p", "/b", "B1")

	quando := clk.advance(10 * time.Minute)
	if _, err := s.Send(ctx, "p", "/a", core.SendInput{Para: "B1", Tipo: "nota", Corpo: "oi"}); err != nil {
		t.Fatal(err)
	}
	if a := agentOf(t, s, "p", "A1"); !a.Batimento.Equal(quando) {
		t.Fatalf("Send: batimento = %v, quer %v", a.Batimento, quando)
	}

	// Wait renova no início...
	quando = clk.advance(10 * time.Minute)
	if _, err := s.Wait(ctx, "p", "/b", time.Millisecond); err != nil {
		t.Fatal(err)
	}
	if b := agentOf(t, s, "p", "B1"); !b.Batimento.Equal(quando) {
		t.Fatalf("Wait: batimento = %v, quer %v", b.Batimento, quando)
	}

	// ...e no retorno: o relógio avança enquanto B1 espera.
	s.Inbox(ctx, "p", "/b")
	done := make(chan error, 1)
	go func() {
		_, err := s.Wait(ctx, "p", "/b", 5*time.Second)
		done <- err
	}()
	time.Sleep(200 * time.Millisecond)
	quando = clk.advance(10 * time.Minute)
	if _, err := s.Send(ctx, "p", "/a", core.SendInput{Para: "B1", Tipo: "nota", Corpo: "acorda"}); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if b := agentOf(t, s, "p", "B1"); !b.Batimento.Equal(quando) {
		t.Fatalf("retorno do Wait: batimento = %v, quer %v", b.Batimento, quando)
	}
}

func TestStatusESair(t *testing.T) {
	s, _, _ := newServiceClock(t)
	mustEnter(t, s, "p", "/a", "A1")
	a, err := s.Status(ctx, "p", "/a", "trabalhando", "rodando gate")
	if err != nil {
		t.Fatal(err)
	}
	if a.Estado != "trabalhando" || a.Status != "rodando gate" {
		t.Fatalf("Status = %+v", a)
	}
	if a, err = s.Leave(ctx, "p", "/a"); err != nil || a.Estado != "encerrado" {
		t.Fatalf("Leave = %+v, %v", a, err)
	}
}

func TestSumidoAposDaConfig(t *testing.T) {
	s, clk, dir := newServiceClock(t)
	if _, err := enterWith(s, "/a", "A1", configA); err != nil { // sumido_apos = 45m
		t.Fatal(err)
	}
	clk.advance(40 * time.Minute)
	if a := agentOf(t, s, "p", "A1"); a.Estado != "ativo" {
		t.Fatalf("40 min com sumido_apos 45m = %q", a.Estado)
	}
	clk.advance(6 * time.Minute)
	if a := agentOf(t, s, "p", "A1"); a.Estado != "sumido" {
		t.Fatalf("46 min = %q", a.Estado)
	}
	// Reaberto do disco, o projeto aplica a configuração gravada.
	s.Close()
	s2, err := New(dir, clk.now)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	s2.Inbox(ctx, "p", "/a")
	clk.advance(40 * time.Minute)
	if a := agentOf(t, s2, "p", "A1"); a.Estado != "ativo" {
		t.Fatalf("reaberto, 40 min = %q", a.Estado)
	}
}

func TestAgentsComPastaRenova(t *testing.T) {
	s, clk, _ := newServiceClock(t)
	mustEnter(t, s, "p", "/a", "A1")
	quando := clk.advance(time.Hour)
	as, err := s.Agents(ctx, "p", "/a")
	if err != nil {
		t.Fatal(err)
	}
	if len(as) != 1 || as[0].Estado != "ativo" || !as[0].Batimento.Equal(quando) {
		t.Fatalf("Agents = %+v", as)
	}
	// Uma pasta que não entrou só lista.
	if _, err := s.Agents(ctx, "p", "/fora"); err != nil {
		t.Fatal(err)
	}
}
