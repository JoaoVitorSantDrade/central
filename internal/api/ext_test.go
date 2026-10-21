package api

import (
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
)

var (
	ecoOnce sync.Once
	ecoPath string
)

func ecoBin(t *testing.T) string {
	t.Helper()
	ecoOnce.Do(func() {
		dir, _ := os.MkdirTemp("", "central-eco-")
		ecoPath = filepath.Join(dir, "eco")
		if runtime.GOOS == "windows" {
			ecoPath += ".exe"
		}
		if out, err := exec.Command("go", "build", "-o", ecoPath, "../../examples/ext/eco").CombinedOutput(); err != nil {
			ecoPath = "erro: " + string(out)
		}
	})
	if strings.HasPrefix(ecoPath, "erro: ") {
		t.Fatal(ecoPath)
	}
	return ecoPath
}

func TestRotasDeExtensao(t *testing.T) {
	srv := newServer(t)
	raiz := t.TempDir()
	cfg := "[projeto]\nid = \"p\"\n\n[[extensoes]]\nnome = \"eco\"\ncomando = '\"" + ecoBin(t) + "\"'\ncli = [\"listar\"]\n"
	code, _, raw := do(t, "POST", srv.URL+"/v1/projetos/p/agentes", map[string]string{
		"nome": "P", "raiz": raiz, "pasta": "/a", "vaga": "A", "papel": "X", "config": cfg})
	if code != 200 {
		t.Fatalf("entrar %d: %s", code, raw)
	}
	code, m, raw := do(t, "GET", srv.URL+"/v1/projetos/p/extensoes", nil)
	if code != 200 || len(m["extensoes"].([]any)) != 1 {
		t.Fatalf("%d %s", code, raw)
	}
	code, m, raw = do(t, "POST", srv.URL+"/v1/projetos/p/extensoes/eco/listar", map[string]any{"pasta": "/a", "args": map[string]any{"x": "1"}})
	if code != 200 || m["dados"].(map[string]any)["acao"] != "listar" {
		t.Fatalf("chamar %d %s", code, raw)
	}
	code, m, raw = do(t, "POST", srv.URL+"/v1/projetos/p/extensoes/eco/listar", map[string]any{"args": map[string]any{"falhar": "sem vídeo"}})
	if code != 502 || m["erro"].(map[string]any)["codigo"] != "extensao" || !strings.Contains(raw, "sem vídeo") {
		t.Fatalf("falha = %d %s", code, raw)
	}
	if code, _, _ = do(t, "POST", srv.URL+"/v1/projetos/p/extensoes/eco/apagar", map[string]any{}); code != 422 {
		t.Fatalf("ação fora da lista = %d", code)
	}
}

func TestRotasDaEntrada(t *testing.T) {
	srv := newServer(t)
	raiz := t.TempDir()
	cfg := "[projeto]\nid = \"p\"\n\n[[extensoes]]\nnome = \"eco\"\ncomando = '\"" + ecoBin(t) + "\"'\nentrada = { pasta = \"in\", ao_chegar = \"importar\" }\n"
	if code, _, raw := do(t, "POST", srv.URL+"/v1/projetos/p/agentes", map[string]string{
		"nome": "P", "raiz": raiz, "pasta": "/a", "vaga": "A", "papel": "X", "config": cfg}); code != 200 {
		t.Fatalf("entrar %d: %s", code, raw)
	}
	code, _, raw := do(t, "PUT", srv.URL+"/v1/projetos/p/extensoes/eco/entrada/clip%20novo.mp4", "conteúdo do vídeo")
	if code != 200 {
		t.Fatalf("upload %d: %s", code, raw)
	}
	if b, err := os.ReadFile(filepath.Join(raiz, "in", "clip novo.mp4")); err != nil || string(b) != "conteúdo do vídeo" {
		t.Fatalf("arquivo = %q %v", b, err)
	}
	if code, _, _ = do(t, "PUT", srv.URL+"/v1/projetos/p/extensoes/eco/entrada/clip%20novo.mp4", "x"); code != 409 {
		t.Fatalf("repetido = %d", code)
	}
	if code, _, _ = do(t, "PUT", srv.URL+"/v1/projetos/p/extensoes/eco/entrada/..%5Cx.mp4", "x"); code != 422 {
		t.Fatalf("nome com barra = %d", code)
	}
	code, m, raw := do(t, "GET", srv.URL+"/v1/projetos/p/extensoes/eco/pendentes", nil)
	if code != 200 || m["entrada"].(map[string]any)["pasta"] != "in" {
		t.Fatalf("pendentes %d: %s", code, raw)
	}
	if code, _, _ = do(t, "POST", srv.URL+"/v1/projetos/p/extensoes/eco/pendentes/42", map[string]any{"campos": map[string]any{}}); code != 404 {
		t.Fatalf("pendência inexistente = %d", code)
	}
}

func TestArquivoDentroDaRaiz(t *testing.T) {
	srv := newServer(t)
	raiz := t.TempDir()
	os.MkdirAll(filepath.Join(raiz, "thumbs"), 0o755)
	os.WriteFile(filepath.Join(raiz, "thumbs", "a.png"), []byte("\x89PNG\r\n\x1a\nfalso"), 0o644)
	if code, _, raw := do(t, "POST", srv.URL+"/v1/projetos/p/agentes", map[string]string{
		"nome": "P", "raiz": raiz, "pasta": "/a", "vaga": "A", "papel": "X"}); code != 200 {
		t.Fatalf("entrar %d: %s", code, raw)
	}
	resp, err := http.Get(srv.URL + "/v1/projetos/p/arquivo?caminho=thumbs/a.png")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 || resp.Header.Get("Content-Type") != "image/png" || resp.Header.Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("png = %d %v", resp.StatusCode, resp.Header)
	}
}

func TestArquivoForaDaRaiz(t *testing.T) {
	srv := newServer(t)
	base := t.TempDir()
	raiz := filepath.Join(base, "proj")
	os.MkdirAll(raiz, 0o755)
	os.WriteFile(filepath.Join(base, "segredo.png"), []byte("x"), 0o644)
	os.WriteFile(filepath.Join(raiz, "nota.txt"), []byte("x"), 0o644)
	os.WriteFile(filepath.Join(raiz, "x.svg"), []byte("<svg/>"), 0o644)
	if code, _, raw := do(t, "POST", srv.URL+"/v1/projetos/p/agentes", map[string]string{
		"nome": "P", "raiz": raiz, "pasta": "/a", "vaga": "A", "papel": "X"}); code != 200 {
		t.Fatalf("entrar %d: %s", code, raw)
	}
	for _, c := range []string{"../segredo.png", "..%2Fsegredo.png", filepath.Join(base, "segredo.png"), "nota.txt", "x.svg", "", "nada.png"} {
		resp, err := http.Get(srv.URL + "/v1/projetos/p/arquivo?caminho=" + c)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode == 200 {
			t.Errorf("%q foi servido", c)
		}
	}
}
