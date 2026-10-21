package core

import (
	"encoding/json"
	"errors"
	"testing"
	"time"
)

func TestFatoUltimoVence(t *testing.T) {
	c, now := newCoreClock(t)
	enter(t, c, "A", "FIXER", "/a")
	if _, err := c.SetFact(ctx, "A", "e2e.base", json.RawMessage(`{"commit":"abc"}`), "primeiro"); err != nil {
		t.Fatal(err)
	}
	*now = now.Add(time.Minute)
	f, err := c.SetFact(ctx, "A", "e2e.base", json.RawMessage(`{"commit":"def"}`), "segundo")
	if err != nil {
		t.Fatal(err)
	}
	if f.Vaga != "A" || f.Motivo != "segundo" || f.Quando.IsZero() {
		t.Fatalf("fato = %+v", f)
	}
	g, err := c.GetFact(ctx, "e2e.base")
	if err != nil || string(g.Valor) != `{"commit":"def"}` {
		t.Fatalf("GetFact = %+v, %v", g, err)
	}
	if _, err := c.SetFact(ctx, "A", "outro", json.RawMessage(`1`), "m"); err != nil {
		t.Fatal(err)
	}
	fs, err := c.Facts(ctx)
	if err != nil || len(fs) != 2 || fs[0].Chave != "e2e.base" || string(fs[0].Valor) != `{"commit":"def"}` || fs[1].Chave != "outro" {
		t.Fatalf("Facts = %+v, %v", fs, err)
	}
	if _, err := c.GetFact(ctx, "nada"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetFact inexistente = %v", err)
	}
	var n int
	if err := c.db.QueryRow(`SELECT COUNT(*) FROM eventos WHERE tipo = 'fato.gravado' AND ref = 'e2e.base'`).Scan(&n); err != nil || n != 2 {
		t.Fatalf("eventos fato.gravado = %d, %v", n, err)
	}
}

func TestHistoricoDoFato(t *testing.T) {
	c, _ := newCoreClock(t)
	enter(t, c, "A", "FIXER", "/a")
	for i, v := range []string{`1`, `"dois"`, `[3]`} {
		if _, err := c.SetFact(ctx, "A", "k", json.RawMessage(v), "motivo"+string(rune('a'+i))); err != nil {
			t.Fatal(err)
		}
	}
	h, err := c.FactHistory(ctx, "k")
	if err != nil || len(h) != 3 {
		t.Fatalf("histórico = %+v, %v", h, err)
	}
	if string(h[0].Valor) != `[3]` || string(h[1].Valor) != `"dois"` || string(h[2].Valor) != `1` {
		t.Fatalf("ordem = %+v", h)
	}
	if h, err := c.FactHistory(ctx, "nada"); err != nil || len(h) != 0 {
		t.Fatalf("histórico vazio = %+v, %v", h, err)
	}
}

func TestFatoValidacao(t *testing.T) {
	c, _ := newCoreClock(t)
	enter(t, c, "A", "FIXER", "/a")
	for name, tc := range map[string]struct {
		chave, valor, motivo string
	}{
		"chave maiúscula":  {"Base", `1`, "m"},
		"chave vazia":      {"", `1`, "m"},
		"chave com espaço": {"a b", `1`, "m"},
		"json inválido":    {"k", `{nao`, "m"},
		"valor vazio":      {"k", ``, "m"},
		"motivo vazio":     {"k", `1`, ""},
	} {
		if _, err := c.SetFact(ctx, "A", tc.chave, json.RawMessage(tc.valor), tc.motivo); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
	if _, err := c.SetFact(ctx, "Z", "k", json.RawMessage(`1`), "m"); !errors.Is(err, ErrNotFound) {
		t.Errorf("vaga inexistente: err = %v", err)
	}
}
