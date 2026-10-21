package cli_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JoaoVitorSantDrade/central/internal/config"
)

// comConfig acrescenta texto ao central.toml da pasta do projeto.
func comConfig(t *testing.T, dir, texto string) {
	t.Helper()
	f, err := os.OpenFile(filepath.Join(dir, config.FileName), os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteString("\n" + texto + "\n"); err != nil {
		t.Fatal(err)
	}
}

func TestRegraCLI(t *testing.T) {
	e := newEnv(t)
	g := e.project("g")
	comConfig(t, g, "[papeis]\nnomes = { EXECUTOR = \"FIXER\" }")
	e.ok(g, "entrar", "--vaga", "G", "--papel", "GERENTE")
	arq, err := filepath.Abs(filepath.Join("..", "adapters", "lessons", "testdata", "LICOES.md"))
	if err != nil {
		t.Fatal(err)
	}
	r := e.ok(g, "regra", "importar", arq)["resumo"].(map[string]any)
	if r["lidas"].(float64) != 28 || r["gravadas"].(float64) != 28 || len(r["avisos"].([]any)) != 0 {
		t.Fatalf("importação = %v", r)
	}
	if r := e.ok(g, "regra", "importar", arq)["resumo"].(map[string]any); r["gravadas"].(float64) != 0 {
		t.Fatalf("reimportação gravou %v", r)
	}
	// o papel do projeto (FIXER) acha as regras do canônico (EXECUTOR) e as de TODOS
	l := e.ok(g, "regra", "listar", "--papel", "FIXER", "--estado", "ativa")["regras"].([]any)
	if len(l) == 0 {
		t.Fatal("nenhuma regra ativa para o FIXER")
	}
	e.ok(g, "regra", "criar", "--id", "L-900", "--aplica", "FIXER,REVISOR", "--pratica", "rode o teste antes")
	if v := e.ok(g, "regra", "estado", "L-900", "no_codigo", "--comando", "central concluir")["regra"].(map[string]any); v["estado"] != "no_codigo" {
		t.Fatalf("estado = %v", v)
	}
	if code, _ := e.run(g, "", "regra", "criar", "--id", "L-901", "--aplica", "COZINHEIRO", "--pratica", "x"); code != 4 {
		t.Fatalf("papel desconhecido aceito: %d", code)
	}
}

func TestBriefCLI(t *testing.T) {
	e := newEnv(t)
	f := e.project("f")
	comConfig(t, f, "[papeis]\nnomes = { EXECUTOR = \"FIXER\" }\n\n[[leitura]]\ncaminho = \"docs/ARQ.md\"\nresumo = \"arquitetura\"\nquando = \"antes de mexer no núcleo\"")
	os.MkdirAll(filepath.Join(f, ".central"), 0o755)
	os.WriteFile(filepath.Join(f, ".central", "nucleo.md"), []byte("Nunca apague dados do usuário."), 0o644)
	e.ok(f, "entrar", "--vaga", "F1", "--papel", "FIXER")
	c := e.cmd(f, "", "brief", "--md")
	out, err := c.Output()
	if err != nil {
		t.Fatal(err)
	}
	md := string(out)
	for _, trecho := range []string{"## 1. Quem você é", "F1", "## 4. Seu papel", "central proxima", "Nunca apague dados", "docs/ARQ.md"} {
		if !strings.Contains(md, trecho) {
			t.Fatalf("brief sem %q:\n%s", trecho, md)
		}
	}
	j := e.ok(f, "brief", "--curto")["brief"].(map[string]any)
	if n := len(j["blocos"].([]any)); n != 3 || j["tokens"].(float64) <= 0 {
		t.Fatalf("curto = %v", j)
	}
}

func TestCenarioM2(t *testing.T) {
	e := newEnv(t)
	g, f := e.project("g"), e.project("f")
	cfg := "[papeis]\nnomes = { EXECUTOR = \"FIXER\" }\n\n[brief]\nfatos = { EXECUTOR = [\"e2e.base*\"] }\n\n" +
		"[[leitura]]\ncaminho = \"docs/PLANO.md\"\nresumo = \"o roteiro\"\nquando = \"antes de planejar\"\npapeis = [\"PLANEJADOR\"]\n\n" +
		"[[leitura]]\ncaminho = \"docs/ARQ.md\"\nresumo = \"arquitetura\"\nquando = \"antes de mexer no núcleo\"\n"
	for _, d := range []string{g, f} {
		comConfig(t, d, cfg)
		os.MkdirAll(filepath.Join(d, ".central", "papeis"), 0o755)
		os.WriteFile(filepath.Join(d, ".central", "nucleo.md"), []byte("Invariante: nada inventado é apresentado como medido."), 0o644)
	}
	os.WriteFile(filepath.Join(f, ".central", "papeis", "FIXER.md"), []byte("Cartão do FIXER deste projeto: rode o e2e parcial."), 0o644)
	e.ok(g, "entrar", "--vaga", "G", "--papel", "GERENTE")
	e.ok(f, "entrar", "--vaga", "F1", "--papel", "FIXER")
	e.ok(g, "regra", "criar", "--id", "L-001", "--aplica", "FIXER", "--pratica", "o teste novo falha na develop")
	e.ok(g, "regra", "criar", "--id", "L-002", "--aplica", "FIXER", "--pratica", "regra que já virou código")
	e.ok(g, "regra", "estado", "L-002", "no_codigo", "--comando", "central concluir")
	e.ok(g, "fato", "set", "e2e.base", `{"commit":"abc123"}`, "--motivo", "linha de base")
	e.ok(g, "fato", "set", "outro.fato", "x", "--motivo", "não é do FIXER")

	out, err := e.cmd(f, "", "brief", "--md").Output()
	if err != nil {
		t.Fatal(err)
	}
	md := string(out)
	for _, quer := range []string{"Cartão do FIXER deste projeto", "nada inventado", "L-001", "abc123", "docs/ARQ.md"} {
		if !strings.Contains(md, quer) {
			t.Fatalf("brief sem %q:\n%s", quer, md)
		}
	}
	for _, nao := range []string{"L-002", "outro.fato", "docs/PLANO.md"} {
		if strings.Contains(md, nao) {
			t.Fatalf("brief com %q:\n%s", nao, md)
		}
	}
	if j := e.ok(f, "brief", "--curto")["brief"].(map[string]any); len(j["blocos"].([]any)) != 3 {
		t.Fatalf("curto = %v", j)
	}
	cs := e.mcpSessao(f)
	if txt, isErr := chamar(t, cs, "brief", nil); isErr || txt != md {
		t.Fatalf("brief pelo MCP difere do da CLI (erro %v):\n%s", isErr, txt)
	}
}
