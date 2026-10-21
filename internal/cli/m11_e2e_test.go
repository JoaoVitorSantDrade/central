package cli_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

// binVersao compila a CLI com a versão dada (uma vez por versão no teste).
func binVersao(t *testing.T, versao string) string {
	t.Helper()
	b := filepath.Join(t.TempDir(), "central-"+versao)
	if runtime.GOOS == "windows" {
		b += ".exe"
	}
	build := exec.Command("go", "build", "-ldflags", "-X github.com/JoaoVitorSantDrade/central/internal/version.Versao="+versao, "-o", b, "../../cmd/central")
	build.Env = append(os.Environ(), "CGO_ENABLED=0")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build %s: %v\n%s", versao, err, out)
	}
	return b
}

// rodarCom roda um binário específico no ambiente do teste.
func (e *env) rodarCom(bin, dir string, args ...string) map[string]any {
	e.t.Helper()
	c := exec.Command(bin, args...)
	c.Dir = dir
	c.Env = e.environ()
	out, err := c.Output()
	if err != nil {
		e.t.Fatalf("%s %v: %v\n%s", filepath.Base(bin), args, err, out)
	}
	var m map[string]any
	json.Unmarshal(out, &m)
	return m
}

func versaoDoDaemon(t *testing.T, addr string) string {
	t.Helper()
	resp, err := http.Get("http://" + addr + "/v1/saude")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var m struct{ Versao string }
	json.NewDecoder(resp.Body).Decode(&m)
	return m.Versao
}

func TestCLINovaTrocaDaemonAntigo(t *testing.T) {
	e := newEnv(t)
	a := e.project("a")
	velho, novo := binVersao(t, "1.0.0"), binVersao(t, "9.9.9")
	e.rodarCom(velho, a, "entrar", "--vaga", "A1", "--papel", "EXECUTOR")
	if v := versaoDoDaemon(t, e.addr); v != "1.0.0" {
		t.Fatalf("daemon = %s", v)
	}
	e.rodarCom(novo, a, "caixa")
	if v := versaoDoDaemon(t, e.addr); v != "9.9.9" {
		t.Fatalf("a CLI nova não trocou o daemon antigo: %s", v)
	}
	// Os dados continuam: a vaga ainda está lá.
	m := e.rodarCom(novo, a, "agentes")
	if as, _ := m["agentes"].([]any); len(as) != 1 {
		t.Fatalf("agentes = %v", m)
	}
}

func TestCLIAntigaNaoDerrubaDaemonNovo(t *testing.T) {
	e := newEnv(t)
	a := e.project("a")
	velho, novo := binVersao(t, "1.0.0"), binVersao(t, "9.9.9")
	e.rodarCom(novo, a, "entrar", "--vaga", "A1", "--papel", "EXECUTOR")
	e.rodarCom(velho, a, "caixa")
	if v := versaoDoDaemon(t, e.addr); v != "9.9.9" {
		t.Fatalf("a CLI antiga derrubou o daemon novo: %s", v)
	}
}

func TestParar(t *testing.T) {
	e := newEnv(t)
	a := e.project("a")
	e.ok(a, "entrar", "--vaga", "A1", "--papel", "EXECUTOR")
	m := e.ok(a, "parar")
	if m["parado"] != true {
		t.Fatalf("parar = %v", m)
	}
	if _, err := http.Get("http://" + e.addr + "/v1/saude"); err == nil {
		t.Fatal("o daemon ainda responde")
	}
	m = e.ok(a, "parar")
	if m["parado"] != false {
		t.Fatalf("parar sem daemon = %v", m)
	}
	// O próximo comando sobe de novo.
	start := time.Now()
	e.ok(a, "caixa")
	fmt.Fprintf(os.Stderr, "religou em %v\n", time.Since(start))
}
