package core

import (
	"errors"
	"testing"
)

func count(t *testing.T, c *Core, query string) int {
	t.Helper()
	var n int
	if err := c.db.QueryRow(query).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestEnterWithConfigGravaSoQuandoMuda(t *testing.T) {
	c := newCore(t)
	if texto, hash, err := c.CurrentConfig(ctx); err != nil || texto != "" || hash != "" {
		t.Fatalf("sem config: %q %q %v", texto, hash, err)
	}
	_, mudou, err := c.EnterWithConfig(ctx, "A1", "X", "/a", "texto A", "hA")
	if err != nil || !mudou {
		t.Fatalf("primeira: mudou = %v, %v", mudou, err)
	}
	_, mudou, err = c.EnterWithConfig(ctx, "B1", "X", "/b", "texto A", "hA")
	if err != nil || mudou {
		t.Fatalf("mesmo hash: mudou = %v, %v", mudou, err)
	}
	if _, mudou, err = c.EnterWithConfig(ctx, "B1", "X", "/b", "", ""); err != nil || mudou {
		t.Fatalf("sem texto: mudou = %v, %v", mudou, err)
	}
	if n := count(t, c, `SELECT count(*) FROM config`); n != 1 {
		t.Fatalf("linhas = %d", n)
	}
	if n := count(t, c, `SELECT count(*) FROM eventos WHERE tipo = 'config.atualizada' AND quem = 'A1'`); n != 1 {
		t.Fatalf("eventos = %d", n)
	}
	if _, mudou, err = c.EnterWithConfig(ctx, "B1", "X", "/b", "texto B", "hB"); err != nil || !mudou {
		t.Fatalf("texto novo: mudou = %v, %v", mudou, err)
	}
	texto, hash, err := c.CurrentConfig(ctx)
	if err != nil || texto != "texto B" || hash != "hB" {
		t.Fatalf("vigente = %q %q %v", texto, hash, err)
	}
}

// Se gravar a configuração falhar, a vaga também não entra: é uma transação só.
func TestEnterWithConfigAtomico(t *testing.T) {
	c := newCore(t)
	if _, err := c.db.Exec(`CREATE TRIGGER falha BEFORE INSERT ON config BEGIN SELECT RAISE(ABORT, 'falha de teste'); END`); err != nil {
		t.Fatal(err)
	}
	if _, _, err := c.EnterWithConfig(ctx, "A1", "X", "/a", "texto", "h"); err == nil {
		t.Fatal("gravou apesar da falha")
	}
	if _, err := c.AgentByPasta(ctx, "/a"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("a vaga entrou: %v", err)
	}
	if n := count(t, c, `SELECT count(*) FROM eventos`); n != 0 {
		t.Fatalf("eventos = %d", n)
	}
}
