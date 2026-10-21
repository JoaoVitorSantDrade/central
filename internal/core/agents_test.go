package core

import (
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/JoaoVitorSantDrade/central/internal/store"
)

// newCoreClock cria um Core cujo relógio o teste avança mexendo em *now.
func newCoreClock(t *testing.T) (*Core, *time.Time) {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "p.db"), ProjectMigrations, store.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	now := time.Date(2026, 10, 20, 22, 0, 0, 0, time.UTC)
	return New(db, func() time.Time { return now }), &now
}

func agentByVaga(t *testing.T, c *Core, vaga string) Agent {
	t.Helper()
	as, err := c.Agents(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range as {
		if a.Vaga == vaga {
			return a
		}
	}
	t.Fatalf("vaga %s não listada: %+v", vaga, as)
	return Agent{}
}

func TestTouchRenovaBatimento(t *testing.T) {
	c, now := newCoreClock(t)
	enter(t, c, "A1", "PAPEL", "/a")
	*now = now.Add(10 * time.Minute)
	if err := c.Touch(ctx, "A1"); err != nil {
		t.Fatal(err)
	}
	if a := agentByVaga(t, c, "A1"); !a.Batimento.Equal(*now) {
		t.Fatalf("batimento = %v, quer %v", a.Batimento, *now)
	}
	if err := c.Touch(ctx, "NINGUEM"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Touch de vaga inexistente = %v", err)
	}
}

func TestSumidoDerivado(t *testing.T) {
	c, now := newCoreClock(t)
	c.SetSettings(Settings{SumidoApos: 30 * time.Minute})
	enter(t, c, "A1", "PAPEL", "/a")
	if a := agentByVaga(t, c, "A1"); a.Estado != "ativo" {
		t.Fatalf("estado ao entrar = %q", a.Estado)
	}
	*now = now.Add(29 * time.Minute)
	if a := agentByVaga(t, c, "A1"); a.Estado != "ativo" {
		t.Fatalf("estado com 29 min = %q", a.Estado)
	}
	*now = now.Add(2 * time.Minute)
	if a := agentByVaga(t, c, "A1"); a.Estado != "sumido" {
		t.Fatalf("estado com 31 min = %q", a.Estado)
	}
	if err := c.Touch(ctx, "A1"); err != nil {
		t.Fatal(err)
	}
	if a := agentByVaga(t, c, "A1"); a.Estado != "ativo" {
		t.Fatalf("estado depois do Touch = %q", a.Estado)
	}
	// O estado gravado volta, não um "ativo" fixo.
	if _, err := c.SetStatus(ctx, "A1", "trabalhando", "gate"); err != nil {
		t.Fatal(err)
	}
	*now = now.Add(31 * time.Minute)
	if a := agentByVaga(t, c, "A1"); a.Estado != "sumido" {
		t.Fatalf("trabalhando sem batimento = %q", a.Estado)
	}
	c.Touch(ctx, "A1")
	if a := agentByVaga(t, c, "A1"); a.Estado != "trabalhando" {
		t.Fatalf("depois do Touch = %q", a.Estado)
	}
}

func TestSetStatusValida(t *testing.T) {
	c, now := newCoreClock(t)
	enter(t, c, "A1", "PAPEL", "/a")
	if _, err := c.SetStatus(ctx, "A1", "dormindo", "x"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("estado dormindo = %v", err)
	}
	for _, e := range []string{"sumido", "encerrado"} {
		if _, err := c.SetStatus(ctx, "A1", e, "x"); !errors.Is(err, ErrInvalid) {
			t.Fatalf("estado %s = %v", e, err)
		}
	}
	*now = now.Add(time.Minute)
	a, err := c.SetStatus(ctx, "A1", "trabalhando", "rodando gate")
	if err != nil {
		t.Fatal(err)
	}
	if a.Estado != "trabalhando" || a.Status != "rodando gate" || !a.Batimento.Equal(*now) {
		t.Fatalf("SetStatus = %+v", a)
	}
	a, err = c.SetStatus(ctx, "A1", "", "outra coisa")
	if err != nil {
		t.Fatal(err)
	}
	if a.Estado != "trabalhando" || a.Status != "outra coisa" {
		t.Fatalf("estado vazio deveria manter: %+v", a)
	}
	if _, err := c.SetStatus(ctx, "NINGUEM", "", "x"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("SetStatus de vaga inexistente = %v", err)
	}
	var n int
	if err := c.db.QueryRow(`SELECT count(*) FROM eventos WHERE tipo = 'agente.status' AND quem = 'A1'`).Scan(&n); err != nil || n != 2 {
		t.Fatalf("eventos agente.status = %d, %v", n, err)
	}
}

func TestLeaveEncerra(t *testing.T) {
	c, now := newCoreClock(t)
	c.SetSettings(Settings{SumidoApos: 30 * time.Minute})
	enter(t, c, "A1", "PAPEL", "/a")
	a, err := c.Leave(ctx, "A1")
	if err != nil {
		t.Fatal(err)
	}
	if a.Estado != "encerrado" {
		t.Fatalf("Leave = %+v", a)
	}
	*now = now.Add(5 * time.Hour)
	if a := agentByVaga(t, c, "A1"); a.Estado != "encerrado" {
		t.Fatalf("encerrado virou %q", a.Estado)
	}
	var n int
	if err := c.db.QueryRow(`SELECT count(*) FROM eventos WHERE tipo = 'agente.saiu' AND quem = 'A1'`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("eventos agente.saiu = %d, %v", n, err)
	}
	if _, err := c.Leave(ctx, "NINGUEM"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Leave de vaga inexistente = %v", err)
	}
	// Entrar de novo reativa a vaga.
	enter(t, c, "A1", "PAPEL", "/a")
	if a := agentByVaga(t, c, "A1"); a.Estado != "ativo" {
		t.Fatalf("depois de entrar de novo = %q", a.Estado)
	}
}

func TestEnterRecusaReservados(t *testing.T) {
	c, _ := newCoreClock(t)
	for _, v := range []string{Todos, Sistema} {
		if _, err := c.Enter(ctx, v, "PAPEL", "/a"); !errors.Is(err, ErrInvalid) {
			t.Fatalf("Enter %s = %v", v, err)
		}
	}
}

func TestAgentsOrdenadoPorVaga(t *testing.T) {
	c, _ := newCoreClock(t)
	enter(t, c, "B1", "PAPEL", "/b")
	enter(t, c, "A1", "PAPEL", "/a")
	as, err := c.Agents(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(as) != 2 || as[0].Vaga != "A1" || as[1].Vaga != "B1" {
		t.Fatalf("Agents = %+v", as)
	}
}
