package lessons

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseLicoesNoFormatoDoTime(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("testdata", "LICOES.md"))
	if err != nil {
		t.Fatal(err)
	}
	ls, avisos := Parse(b)
	if len(ls) != 28 || len(avisos) != 0 {
		t.Fatalf("%d lições, avisos %v", len(ls), avisos)
	}
	por := map[string]Lesson{}
	for _, l := range ls {
		por[l.ID] = l
	}
	if l := por["L-004"]; l.Estado != "no_manual" || len(l.Aplica) != 2 || l.Aplica[0] != "INTEGRADOR" {
		t.Fatalf("L-004 = %+v", l)
	}
	if l := por["L-005"]; len(l.Aplica) != 1 || l.Aplica[0] != "TODOS" {
		t.Fatalf("L-005 = %+v", l)
	}
	if l := por["L-001"]; l.Estado != "ativa" || l.Texto == "" || l.Pratica == "" || l.Origem == "" {
		t.Fatalf("L-001 = %+v", l)
	}
}

func TestParseIgnoraLixo(t *testing.T) {
	md := "# título\n\n| ID | Origem | Lição | Regra prática | Quem aplica | Estado |\n|---|---|---|---|---|---|\n" +
		"| L-001 | o | l | r | FIXER | ativa |\ntexto solto\n| L-002 | só três | colunas |\n| L-003 | o | l | r | todos (REVISOR, em especial) | aposentada (motivo) |\n"
	ls, avisos := Parse([]byte(md))
	if len(ls) != 2 || len(avisos) != 1 {
		t.Fatalf("lições %+v avisos %v", ls, avisos)
	}
	if ls[1].Aplica[0] != "TODOS" || ls[1].Estado != "aposentada" {
		t.Fatalf("L-003 = %+v", ls[1])
	}
}
