package service

import (
	"strings"
	"testing"
)

func TestBriefDeVagaExecutada(t *testing.T) {
	a := novoAmbienteExec(t, "")
	a.abrir("EXEC1", "EXECUTOR")
	a.passoEEspera()
	a.entrar("EXEC1", "EXECUTOR")
	pasta := a.vaga("EXEC1").Pasta
	b, err := a.s.Brief(ctx, "p", pasta, false, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(b.Texto, "termine o turno") || strings.Contains(b.Texto, "central esperar") {
		t.Fatalf("brief da vaga executada:\n%s", b.Texto)
	}
	// a vaga comum continua com o esperar
	if b, _ = a.s.Brief(ctx, "p", a.pastaG1(), false, 0); !strings.Contains(b.Texto, "central esperar") {
		t.Fatalf("brief da vaga comum:\n%s", b.Texto)
	}
}
