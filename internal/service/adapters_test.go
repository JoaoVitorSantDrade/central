package service

import (
	"github.com/JoaoVitorSantDrade/central/internal/core"
	"os"
	"path/filepath"
	"testing"
)

func TestSyncAdaptersSoComHeadNovo(t *testing.T) {
	s, _ := newService(t)
	raiz := t.TempDir()
	todo := filepath.Join(raiz, "backlog", "todo")
	if err := os.MkdirAll(todo, 0o755); err != nil {
		t.Fatal(err)
	}
	escrever := func(nome, txt string) {
		if err := os.WriteFile(filepath.Join(todo, nome), []byte(txt), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	escrever("T-001.md", "---\nid: T-001\ntitulo: a\n---\n")
	cfg := "[projeto]\nid = \"p\"\n\n[itens.arquivos]\npastas = { pronta = \"backlog/todo\" }\n"
	if _, err := s.Enter(ctx, EnterInput{ProjetoID: "p", Nome: "P", Raiz: raiz, Pasta: "/a", Vaga: "P1", Papel: "PLANEJADOR", Config: cfg}); err != nil {
		t.Fatal(err)
	}
	r, err := s.SyncAdapters(ctx)
	if err != nil || r["p"].Criados != 1 {
		t.Fatalf("primeira passada = %+v, %v", r, err)
	}
	if r, _ := s.SyncAdapters(ctx); len(r) != 0 {
		t.Fatalf("head igual sincronizou de novo: %+v", r)
	}
	escrever("T-002.md", "---\nid: T-002\ntitulo: b\n---\n")
	if r, _ := s.SyncAdapters(ctx); r["p"].Criados != 1 {
		t.Fatalf("head novo = %+v", r)
	}
	its, err := s.Itens(ctx, "p", "", coreFilterTodos())
	if err != nil || len(its) != 2 {
		t.Fatalf("itens = %+v, %v", its, err)
	}
}

func coreFilterTodos() core.ItemFilter { return core.ItemFilter{} }
