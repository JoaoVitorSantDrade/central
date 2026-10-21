package cli_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// mcpSessao sobe "central mcp" na pasta e conecta um cliente MCP.
func (e *env) mcpSessao(dir string) *mcp.ClientSession {
	e.t.Helper()
	cmd := e.cmd(dir, "", "mcp")
	cmd.Stdin = nil
	client := mcp.NewClient(&mcp.Implementation{Name: "teste", Version: "0"}, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cs, err := client.Connect(ctx, &mcp.CommandTransport{Command: cmd}, nil)
	if err != nil {
		e.t.Fatal(err)
	}
	e.t.Cleanup(func() { cs.Close() })
	return cs
}

func chamar(t *testing.T, cs *mcp.ClientSession, nome string, args map[string]any) (string, bool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	r, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: nome, Arguments: args})
	if err != nil {
		t.Fatalf("%s: %v", nome, err)
	}
	var b strings.Builder
	for _, c := range r.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			b.WriteString(tc.Text)
		}
	}
	return b.String(), r.IsError
}

func TestMCPListaFerramentas(t *testing.T) {
	e := newEnv(t)
	cs := e.mcpSessao(e.project("a"))
	r, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	nomes := map[string]bool{}
	for _, tl := range r.Tools {
		nomes[tl.Name] = true
	}
	for _, n := range []string{"brief", "caixa", "enviar", "esperar", "proxima", "concluir", "recurso_pedir", "saude"} {
		if !nomes[n] {
			t.Errorf("falta a ferramenta %s", n)
		}
	}
}

func TestMCPEnviarECaixa(t *testing.T) {
	e := newEnv(t)
	a, b := e.project("a"), e.project("b")
	e.ok(a, "entrar", "--vaga", "A1", "--papel", "EXECUTOR")
	cs := e.mcpSessao(b)
	if txt, isErr := chamar(t, cs, "entrar", map[string]any{"vaga": "B1", "papel": "REVISOR"}); isErr {
		t.Fatalf("entrar: %s", txt)
	}
	e.ok(a, "enviar", "B1", "nota", "olá pelo CLI")
	if txt, isErr := chamar(t, cs, "caixa", nil); isErr || !strings.Contains(txt, "olá pelo CLI") {
		t.Fatalf("caixa = %s (erro %v)", txt, isErr)
	}
	if txt, isErr := chamar(t, cs, "enviar", map[string]any{"para": "A1", "tipo": "nota", "corpo": "olá pelo MCP", "ref": "T-1"}); isErr {
		t.Fatalf("enviar: %s", txt)
	}
	m := e.ok(a, "caixa")
	if got := corpos(m); len(got) != 1 || got[0] != "olá pelo MCP" {
		t.Fatalf("caixa do A1 = %v", got)
	}
	if txt, isErr := chamar(t, cs, "brief", map[string]any{"curto": true}); isErr || !strings.Contains(txt, "## 1. Quem você é") {
		t.Fatalf("brief = %s", txt)
	}
}

func TestMCPErroDeUso(t *testing.T) {
	e := newEnv(t)
	b := e.project("b")
	cs := e.mcpSessao(b)
	chamar(t, cs, "entrar", map[string]any{"vaga": "B1", "papel": "EXECUTOR"})
	if txt, isErr := chamar(t, cs, "reservar", nil); !isErr || !strings.Contains(txt, "precisa de id") {
		t.Fatalf("reservar sem id = %s (erro %v)", txt, isErr)
	}
	if txt, isErr := chamar(t, cs, "brief", map[string]any{"curto": "talvez"}); !isErr || !strings.Contains(txt, "boolean") {
		t.Fatalf("tipo errado = %s (erro %v)", txt, isErr)
	}
	if txt, isErr := chamar(t, cs, "esperar", map[string]any{"ate": "1h"}); !isErr || !strings.Contains(txt, "5m") {
		t.Fatalf("esperar longo = %s (erro %v)", txt, isErr)
	}
	// o servidor segue de pé depois dos erros
	if _, isErr := chamar(t, cs, "pendentes", nil); isErr {
		t.Fatal("servidor quebrou depois de erros de uso")
	}
}

func TestMCPEncerraComStdin(t *testing.T) {
	e := newEnv(t)
	b := e.project("b")
	cs := e.mcpSessao(b)
	chamar(t, cs, "entrar", map[string]any{"vaga": "B1", "papel": "EXECUTOR"})
	inicio := time.Now()
	if err := cs.Close(); err != nil {
		t.Fatalf("fechar: %v", err)
	}
	if d := time.Since(inicio); d > 4*time.Second {
		t.Fatalf("o servidor levou %v para sair depois de fechar o stdin", d)
	}
}
