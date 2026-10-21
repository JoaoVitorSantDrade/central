package cli_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JoaoVitorSantDrade/central/internal/config"
)

// withEco acrescenta o eco como extensão "eco" ao central.toml do projeto.
func withEco(t *testing.T, dir, comando string) {
	t.Helper()
	f, err := os.OpenFile(filepath.Join(dir, config.FileName), os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := fmt.Fprintf(f, "\n[[extensoes]]\nnome = \"eco\"\ncomando = '%s'\ncli = [\"listar\", \"importar\"]\n\n[acordar]\nexemplo-novo = [\"PESQUISADOR\"]\n", comando); err != nil {
		t.Fatal(err)
	}
}

func TestCLIExtensao(t *testing.T) {
	e := newEnv(t)
	a, b := e.project("a"), e.project("b")
	withEco(t, a, `"`+eco+`"`)
	withEco(t, b, `"`+eco+`"`)
	e.ok(a, "entrar", "--vaga", "EXEC1", "--papel", "EXECUTOR")
	e.ok(b, "entrar", "--vaga", "PESQ1", "--papel", "PESQUISADOR")
	m := e.ok(a, "eco", "importar", "--evento", "exemplo-novo", "--ref=digimon", "um", "dois")
	d := m["dados"].(map[string]any)
	args := d["args"].(map[string]any)
	if d["acao"] != "importar" || args["ref"] != "digimon" || fmt.Sprint(args["_"]) != "[um dois]" {
		t.Fatalf("eco = %v", m)
	}
	w := e.ok(b, "esperar", "--ate", "5s")
	if w["motivo"] != "ext" {
		t.Fatalf("PESQ1 = %v", w)
	}
	code, m := e.run(a, "texto do stdin", "eco", "listar", "-")
	if code != 0 || m["dados"].(map[string]any)["args"].(map[string]any)["stdin"] != "texto do stdin" {
		t.Fatalf("stdin: %d %v", code, m)
	}
	if code, m = e.run(a, "", "eco", "apagar"); code != 4 || !strings.Contains(fmt.Sprint(m), "listar, importar") {
		t.Fatalf("ação desconhecida: %d %v", code, m)
	}
	if code, m = e.run(a, "", "eco", "listar", "--falhar", "sem vídeo"); code != 1 || !strings.Contains(fmt.Sprint(m), "sem vídeo") {
		t.Fatalf("falha: %d %v", code, m)
	}
	if code, _ = e.run(a, "", "naoexiste", "x"); code != 4 {
		t.Fatalf("comando desconhecido: %d", code)
	}
}

func TestMCPExtensao(t *testing.T) {
	e := newEnv(t)
	a := e.project("a")
	withEco(t, a, `"`+eco+`"`)
	e.ok(a, "entrar", "--vaga", "EXEC1", "--papel", "EXECUTOR")
	cs := e.mcpSessao(a)
	txt, isErr := chamar(t, cs, "extensao", map[string]any{"nome": "eco", "acao": "listar", "args": map[string]any{"x": 1}})
	if isErr || !strings.Contains(txt, `"acao":"listar"`) {
		t.Fatalf("extensao = %s (erro %v)", txt, isErr)
	}
	if txt, isErr = chamar(t, cs, "extensao", map[string]any{"nome": "eco", "acao": "apagar"}); !isErr {
		t.Fatalf("ação fora do cli devia falhar: %s", txt)
	}
}

func TestDoctorExtensao(t *testing.T) {
	e := newEnv(t)
	a := e.project("a")
	withEco(t, a, `"`+eco+`"`)
	e.ok(a, "entrar", "--vaga", "A1", "--papel", "DEV")
	if code, m := e.run(a, "", "doctor"); code != 0 || verificacoes(t, m)["extensao:eco"]["ok"] != true {
		t.Fatalf("doctor = %d %v", code, m)
	}
	b := e.project("b")
	withEco(t, b, "programa-que-nao-existe-xyz --x")
	code, m := e.run(b, "", "doctor")
	v := verificacoes(t, m)["extensao:eco"]
	if code != 1 || v["ok"] != false || !strings.Contains(fmt.Sprint(v["detalhe"]), "programa-que-nao-existe-xyz") {
		t.Fatalf("doctor quebrado = %d %v", code, v)
	}
}
