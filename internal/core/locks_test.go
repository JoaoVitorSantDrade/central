package core

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func lock(t *testing.T, c *Core, vaga, nome string) Lock {
	t.Helper()
	l, err := c.Lock(ctx, vaga, nome, "motivo de "+vaga)
	if err != nil {
		t.Fatal(err)
	}
	return l
}

func locks(t *testing.T, c *Core) []Lock {
	t.Helper()
	ls, err := c.Locks(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return ls
}

func TestTravaExclusiva(t *testing.T) {
	c, _ := newCoreClock(t)
	enter(t, c, "A", "FIXER", "/a")
	enter(t, c, "B", "FIXER", "/b")
	l := lock(t, c, "A", "src/main.go")
	if l.Nome != "src/main.go" || l.Vaga != "A" || l.Motivo != "motivo de A" || l.Inicio.IsZero() {
		t.Fatalf("trava = %+v", l)
	}
	_, err := c.Lock(ctx, "B", "src/main.go", "eu também")
	if !errors.Is(err, ErrConflict) || !strings.Contains(err.Error(), "A") {
		t.Fatalf("Lock de B = %v", err)
	}
	ls := locks(t, c)
	if len(ls) != 1 || ls[0].Vaga != "A" || ls[0].DonoEstado != EstadoAtivo {
		t.Fatalf("travas = %+v", ls)
	}
}

func TestTravaIdempotente(t *testing.T) {
	c, now := newCoreClock(t)
	enter(t, c, "A", "FIXER", "/a")
	l1 := lock(t, c, "A", "x")
	*now = now.Add(time.Minute)
	l2 := lock(t, c, "A", "x")
	if !l1.Inicio.Equal(l2.Inicio) || l2.Vaga != "A" {
		t.Fatalf("l1 = %+v, l2 = %+v", l1, l2)
	}
	if ls := locks(t, c); len(ls) != 1 {
		t.Fatalf("travas = %+v", ls)
	}
}

func TestSoltarSoDono(t *testing.T) {
	c, _ := newCoreClock(t)
	enter(t, c, "A", "FIXER", "/a")
	enter(t, c, "B", "FIXER", "/b")
	lock(t, c, "A", "x")
	if err := c.Unlock(ctx, "B", "x"); !errors.Is(err, ErrConflict) {
		t.Fatalf("Unlock de B = %v", err)
	}
	if err := c.Unlock(ctx, "A", "nada"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Unlock inexistente = %v", err)
	}
	if err := c.Unlock(ctx, "A", "x"); err != nil {
		t.Fatal(err)
	}
	if ls := locks(t, c); len(ls) != 0 {
		t.Fatalf("travas = %+v", ls)
	}
	// Livre de novo: B toma.
	lock(t, c, "B", "x")
}

func TestTravaCaiComDonoSumido(t *testing.T) {
	c, now := newCoreClock(t)
	c.SetSettings(Settings{SumidoApos: 30 * time.Minute})
	enter(t, c, "A", "FIXER", "/a")
	enter(t, c, "B", "FIXER", "/b")
	lock(t, c, "A", "x")
	*now = now.Add(31 * time.Minute)
	if ls := locks(t, c); len(ls) != 1 || ls[0].DonoEstado != EstadoSumido {
		t.Fatalf("travas = %+v", ls)
	}
	l, err := c.Lock(ctx, "B", "x", "assumo")
	if err != nil {
		t.Fatal(err)
	}
	if l.Vaga != "B" || l.Motivo != "assumo" || !l.Inicio.Equal(*now) {
		t.Fatalf("trava = %+v", l)
	}
	var dados string
	if err := c.db.QueryRow(`SELECT dados FROM eventos WHERE tipo = 'trava.tomada' AND quem = 'B'`).Scan(&dados); err != nil {
		t.Fatal(err)
	}
	var d map[string]string
	if err := json.Unmarshal([]byte(dados), &d); err != nil {
		t.Fatal(err)
	}
	if d["de"] != "A" {
		t.Fatalf("evento = %s", dados)
	}
}

func TestLeaveSoltaTravas(t *testing.T) {
	c, _ := newCoreClock(t)
	enter(t, c, "A", "FIXER", "/a")
	enter(t, c, "B", "FIXER", "/b")
	lock(t, c, "A", "x")
	lock(t, c, "A", "y")
	lock(t, c, "B", "z")
	if _, err := c.Leave(ctx, "A"); err != nil {
		t.Fatal(err)
	}
	ls := locks(t, c)
	if len(ls) != 1 || ls[0].Nome != "z" {
		t.Fatalf("travas = %+v", ls)
	}
	// A trava solta no Leave é livre: B toma sem "de".
	lock(t, c, "B", "x")
	var dados string
	if err := c.db.QueryRow(`SELECT dados FROM eventos WHERE tipo = 'trava.tomada' AND quem = 'B' AND ref = 'x'`).Scan(&dados); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(dados, `"de"`) {
		t.Fatalf("evento = %s", dados)
	}
}

func TestNomeDeTravaInvalido(t *testing.T) {
	c, _ := newCoreClock(t)
	enter(t, c, "A", "FIXER", "/a")
	for _, nome := range []string{"", "com espaço", "a:b", strings.Repeat("x", 129), "ç"} {
		if _, err := c.Lock(ctx, "A", nome, "m"); !errors.Is(err, ErrInvalid) {
			t.Fatalf("Lock(%q) = %v", nome, err)
		}
	}
	if _, err := c.Lock(ctx, "A", strings.Repeat("x", 128), "m"); err != nil {
		t.Fatalf("128 caracteres: %v", err)
	}
	if _, err := c.Lock(ctx, "A", "a.B_1/c-d", "m"); err != nil {
		t.Fatalf("nome válido: %v", err)
	}
	if _, err := c.Lock(ctx, "NINGUEM", "q", "m"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("vaga inexistente: %v", err)
	}
}

func TestTickSumidoSemGerenteVivoVaiATodos(t *testing.T) {
	c, now := newCoreClock(t)
	enter(t, c, "GERENTE", "GERENTE", "/g")
	enter(t, c, "FIXER1", "FIXER", "/f")
	enter(t, c, "OUTRO", "FIXER", "/o")
	if _, err := c.Leave(ctx, "GERENTE"); err != nil {
		t.Fatal(err)
	}
	*now = now.Add(20 * time.Minute)
	inbox(t, c, "OUTRO")
	*now = now.Add(11 * time.Minute)
	if err := c.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	got := systemMessages(inbox(t, c, "OUTRO"), "agente.sumiu")
	if len(got) != 1 || got[0].Ref != "FIXER1" || got[0].Para != Todos {
		t.Fatalf("avisos = %+v", got)
	}
}

func TestVagaEncerradaNaoTomaTrava(t *testing.T) {
	c, _ := newCoreClock(t)
	enter(t, c, "A", "FIXER", "/a")
	if _, err := c.Leave(ctx, "A"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Lock(ctx, "A", "x", "teste"); !errors.Is(err, ErrConflict) {
		t.Fatalf("vaga encerrada tomou trava: %v", err)
	}
}
