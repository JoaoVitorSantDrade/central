package executor

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestClaudeReal confere os argumentos contra o Claude Code de verdade: um
// primeiro turno (--session-id e -w num repositório git) e um seguinte
// (--resume), com --model haiku e o texto pelo stdin. Gasta o plano do
// usuário, então só roda com CENTRAL_TESTE_CLAUDE_REAL=1; fica fora do CI.
func TestClaudeReal(t *testing.T) {
	if os.Getenv("CENTRAL_TESTE_CLAUDE_REAL") != "1" {
		t.Skip("defina CENTRAL_TESTE_CLAUDE_REAL=1 para rodar contra o claude real")
	}
	claude, err := exec.LookPath("claude")
	if err != nil {
		t.Skip("claude não está no PATH")
	}
	repo := t.TempDir()
	for _, args := range [][]string{{"init", "-q"}, {"-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "--allow-empty", "-m", "x"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	tr := Turno{Claude: claude, Pasta: repo, Sessao: "6f9619ff-8b86-4d01-b42d-00cf4fc964ff", Worktree: "TESTE1", Primeiro: true,
		Modelo: "haiku", Esforco: "low", Texto: "responda ok", Log: filepath.Join(t.TempDir(), "1.jsonl"), Prazo: 3 * time.Minute}
	r, err := Rodar(context.Background(), tr, nil)
	if err != nil || r.Falha != FalhaOK || r.Sessao != tr.Sessao {
		log, _ := os.ReadFile(tr.Log)
		t.Fatalf("primeiro turno: %+v, %v\nlog:\n%s", r, err, log)
	}
	if !strings.Contains(filepath.ToSlash(r.Pasta), "TESTE1") {
		t.Fatalf("o init não informou a pasta do worktree: %q", r.Pasta)
	}
	t.Logf("primeiro: pasta %s, US$ %.4f, texto %q", r.Pasta, r.CustoUSD, r.Texto)
	tr.Primeiro, tr.Pasta, tr.Texto = false, r.Pasta, "responda de novo ok"
	tr.Log = filepath.Join(t.TempDir(), "2.jsonl")
	r2, err := Rodar(context.Background(), tr, nil)
	if err != nil || r2.Falha != FalhaOK || r2.Sessao != tr.Sessao {
		log, _ := os.ReadFile(tr.Log)
		t.Fatalf("turno seguinte: %+v, %v\nlog:\n%s", r2, err, log)
	}
	t.Logf("seguinte: US$ %.4f, texto %q", r2.CustoUSD, r2.Texto)
}
