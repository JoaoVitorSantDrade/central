package ext

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

var eco string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "central-ext-")
	if err != nil {
		panic(err)
	}
	eco = filepath.Join(dir, "eco")
	if runtime.GOOS == "windows" {
		eco += ".exe"
	}
	if out, err := exec.Command("go", "build", "-o", eco, "../../examples/ext/eco").CombinedOutput(); err != nil {
		panic(string(out))
	}
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

func rodarEco(t *testing.T, args map[string]any, timeout time.Duration) (Resposta, error) {
	t.Helper()
	pasta := t.TempDir()
	return Rodar(context.Background(), Execucao{Comando: `"` + eco + `"`, Pasta: pasta, Timeout: timeout,
		Ambiente: []string{"CENTRAL_PROJETO=p"}}, Pedido{Acao: "listar", Args: args,
		Projeto: Projeto{ID: "p", Nome: "P", Raiz: pasta}, Agente: &Agente{Vaga: "A1", Papel: "EXECUTOR"}})
}

func TestPedidoGolden(t *testing.T) {
	p := Pedido{Versao: Versao, Acao: "importar", Args: map[string]any{"arquivos": []string{"artifacts/entrada/a.mp4"}},
		Projeto: Projeto{ID: "lyriccut", Nome: "LyricCut", Raiz: "C:/x"}, Agente: &Agente{Vaga: "PESQ1", Papel: "PESQUISADOR"}}
	b, _ := json.MarshalIndent(p, "", "  ")
	got := string(b) + "\n"
	golden := filepath.Join("testdata", "pedido.golden.json")
	if os.Getenv("ATUALIZAR_GOLDEN") != "" {
		os.WriteFile(golden, []byte(got), 0o644)
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatal(err)
	}
	if got != strings.ReplaceAll(string(want), "\r\n", "\n") {
		t.Fatalf("o pedido ext/v1 mudou (isso quebra extensões):\n%s\nquer:\n%s", got, want)
	}
	h, _ := json.Marshal(Pedido{Acao: "x", Projeto: Projeto{ID: "p"}})
	if !strings.Contains(string(h), `"agente":null`) {
		t.Fatalf("sem agente deve ir null: %s", h)
	}
}

func TestRespostaEco(t *testing.T) {
	r, err := rodarEco(t, map[string]any{"x": "1", "evento": "exemplo-novo", "ref": "digimon", "linhas": "sim"}, 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	var d struct {
		Acao, Versao, Pasta, Ambiente string
		PythonUTF8                    string `json:"python_utf8"`
		Args                          map[string]any
		Agente                        map[string]any
	}
	if err := json.Unmarshal(r.Dados, &d); err != nil {
		t.Fatal(err)
	}
	if !r.OK || d.Acao != "listar" || d.Versao != Versao || d.Args["x"] != "1" || d.Agente["vaga"] != "A1" || d.Ambiente != Versao || d.PythonUTF8 != "1" {
		t.Fatalf("resposta = %+v, dados = %+v", r, d)
	}
	if len(r.Eventos) != 1 || r.Eventos[0].Tipo != "exemplo-novo" || r.Eventos[0].Ref != "digimon" {
		t.Fatalf("eventos = %+v", r.Eventos)
	}
	if r.TemFormulario() {
		t.Fatal("sem formulário")
	}
	r, err = rodarEco(t, map[string]any{"pedir": "sim"}, 30*time.Second)
	if err != nil || !r.TemFormulario() {
		t.Fatalf("formulário: %+v %v", r, err)
	}
	r, err = rodarEco(t, map[string]any{"falhar": "sem vídeo"}, 30*time.Second)
	if err != nil || r.OK || r.Erro == nil || r.Erro.Mensagem != "sem vídeo" {
		t.Fatalf("ok=false volta sem erro: %+v %v", r, err)
	}
}

func TestPrazoMataArvore(t *testing.T) {
	inicio := time.Now()
	_, err := rodarEco(t, map[string]any{"dormir": "30s"}, time.Second)
	if !errors.Is(err, ErrPrazo) {
		t.Fatalf("err = %v", err)
	}
	if d := time.Since(inicio); d > 10*time.Second {
		t.Fatalf("o prazo de 1 s levou %v", d)
	}
}

func TestSaidaGrande(t *testing.T) {
	_, err := rodarEco(t, map[string]any{"inundar": "1100000"}, 30*time.Second)
	if !errors.Is(err, ErrSaidaGrande) {
		t.Fatalf("err = %v", err)
	}
}

func TestRespostaInvalida(t *testing.T) {
	_, err := rodarEco(t, map[string]any{"quebrar": "faltou o ffmpeg"}, 30*time.Second)
	if !errors.Is(err, ErrResposta) || !strings.Contains(err.Error(), "faltou o ffmpeg") || !strings.Contains(err.Error(), "código 3") {
		t.Fatalf("err = %v", err)
	}
	_, err = Rodar(context.Background(), Execucao{Comando: "echo nada", Pasta: t.TempDir(), Timeout: 10 * time.Second}, Pedido{Acao: "x"})
	if !errors.Is(err, ErrResposta) {
		t.Fatalf("echo: err = %v", err)
	}
}

func TestPrograma(t *testing.T) {
	casos := map[string]string{
		`"C:\Program Files\py\python.exe" -I x.py`:                 `C:\Program Files\py\python.exe`,
		"resources/runtime/python.exe -I .central/ext/exemplos.py": "resources/runtime/python.exe",
		"  eco  ": "eco",
	}
	for in, quer := range casos {
		if got := Programa(in); got != quer {
			t.Errorf("Programa(%q) = %q, quer %q", in, got, quer)
		}
	}
	raiz := t.TempDir()
	os.MkdirAll(filepath.Join(raiz, "bin"), 0o755)
	os.WriteFile(filepath.Join(raiz, "bin", "x.exe"), nil, 0o755)
	if _, err := Localizar("bin/x.exe --y", raiz); err != nil {
		t.Fatalf("relativo à raiz: %v", err)
	}
	if _, err := Localizar("bin/nada.exe", raiz); err == nil {
		t.Fatal("inexistente devia falhar")
	}
	if _, err := Localizar(`"`+eco+`"`, raiz); err != nil {
		t.Fatalf("absoluto: %v", err)
	}
}
