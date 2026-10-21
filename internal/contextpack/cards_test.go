package contextpack

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var canonicos = []string{"GERENTE", "PLANEJADOR", "PESQUISADOR", "EXECUTOR", "REVISOR", "VALIDADOR", "INTEGRADOR"}

func TestCartaoPadraoParaTodoPapel(t *testing.T) {
	for _, p := range canonicos {
		txt, fonte := Card(t.TempDir(), p, p)
		if fonte != "embutido:"+p {
			t.Errorf("%s: fonte %q", p, fonte)
		}
		if len(txt) >= 4800 || !strings.Contains(txt, "central esperar") {
			t.Errorf("%s: %d bytes, cita esperar: %v", p, len(txt), strings.Contains(txt, "central esperar"))
		}
	}
}

func escrever(t *testing.T, dir, nome, txt string) {
	t.Helper()
	p := filepath.Join(dir, ".central", "papeis", nome+".md")
	os.MkdirAll(filepath.Dir(p), 0o755)
	if err := os.WriteFile(p, []byte(txt), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestCartaoPeloNomeDoProjeto(t *testing.T) {
	d := t.TempDir()
	escrever(t, d, "FIXER", "cartão do FIXER")
	escrever(t, d, "EXECUTOR", "cartão do EXECUTOR")
	if txt, fonte := Card(d, "FIXER", "EXECUTOR"); !strings.HasPrefix(txt, "cartão do FIXER\n") || !strings.HasSuffix(fonte, "FIXER.md") {
		t.Fatalf("%q de %q", txt, fonte)
	}
}

func TestCartaoCanonicoSemONomeDoProjeto(t *testing.T) {
	d := t.TempDir()
	escrever(t, d, "EXECUTOR", "cartão do EXECUTOR")
	if txt, _ := Card(d, "FIXER", "EXECUTOR"); !strings.HasPrefix(txt, "cartão do EXECUTOR\n") {
		t.Fatalf("%q", txt)
	}
	if txt, fonte := Card(d, "X", "HUMANO"); fonte != "embutido:generico" || !strings.Contains(txt, "central esperar") {
		t.Fatalf("genérico = %q %q", txt, fonte)
	}
}

func TestNucleoAusente(t *testing.T) {
	d := t.TempDir()
	if _, ok := Nucleo(d); ok {
		t.Fatal("núcleo inexistente achado")
	}
	os.MkdirAll(filepath.Join(d, ".central"), 0o755)
	os.WriteFile(filepath.Join(d, ".central", "nucleo.md"), []byte("  invariantes  \n"), 0o644)
	if n, ok := Nucleo(d); !ok || n != "invariantes" {
		t.Fatalf("núcleo = %q %v", n, ok)
	}
}

func TestCartaoDoProjetoGanhaOFimDeCiclo(t *testing.T) {
	pasta := t.TempDir()
	os.MkdirAll(filepath.Join(pasta, ".central", "papeis"), 0o755)
	os.WriteFile(filepath.Join(pasta, ".central", "papeis", "FIXER.md"), []byte("Você corrige bugs do LyricCut.\n"), 0o644)
	txt, _ := Card(pasta, "FIXER", "EXECUTOR")
	if !strings.HasPrefix(txt, "Você corrige bugs do LyricCut.") || !strings.HasSuffix(txt, FimDeCiclo) {
		t.Fatalf("cartão do projeto sem o fim de ciclo:\n%s", txt)
	}
	os.WriteFile(filepath.Join(pasta, ".central", "papeis", "FIXER.md"), []byte("Sem trabalho: `central esperar --ate 10m`.\n"), 0o644)
	if txt, _ := Card(pasta, "FIXER", "EXECUTOR"); strings.Contains(txt, FimDeCiclo) {
		t.Fatalf("o cartão que já diz como esperar não recebe o padrão:\n%s", txt)
	}
}
