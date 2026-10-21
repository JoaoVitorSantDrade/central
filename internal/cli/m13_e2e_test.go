package cli_test

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/JoaoVitorSantDrade/central/internal/config"
)

var (
	claudeFalsoOnce sync.Once
	claudeFalsoBin  string
	claudeFalsoErr  error
)

// claudeFalso compila o examples/claude-falso uma vez (nenhum teste usa o claude real).
func claudeFalso(t *testing.T) string {
	t.Helper()
	claudeFalsoOnce.Do(func() {
		claudeFalsoBin = filepath.Join(filepath.Dir(bin), "claude-falso")
		if runtime.GOOS == "windows" {
			claudeFalsoBin += ".exe"
		}
		build := exec.Command("go", "build", "-o", claudeFalsoBin, "../../examples/claude-falso")
		build.Env = append(os.Environ(), "CGO_ENABLED=0")
		if out, err := build.CombinedOutput(); err != nil {
			claudeFalsoErr = fmt.Errorf("build claude-falso: %v\n%s", err, out)
		}
	})
	if claudeFalsoErr != nil {
		t.Fatal(claudeFalsoErr)
	}
	return claudeFalsoBin
}

// withExecucao acrescenta o [execucao] com o claude dado ao central.toml do projeto.
func withExecucao(t *testing.T, dir, claude string) {
	t.Helper()
	f, err := os.OpenFile(filepath.Join(dir, config.FileName), os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := fmt.Fprintf(f, "\n[execucao]\nclaude = '%s'\n\n[execucao.cargos]\nGERENTE = { max = 1, modelo = \"sonnet\", esforco = \"medium\" }\nEXECUTOR = { max = 1, modelo = \"opus\", esforco = \"high\" }\n", claude); err != nil {
		t.Fatal(err)
	}
}

// esperarArquivo espera o caminho existir (o daemon roda o turno em segundo plano).
func esperarArquivo(t *testing.T, caminho string) {
	t.Helper()
	for fim := time.Now().Add(15 * time.Second); time.Now().Before(fim); time.Sleep(50 * time.Millisecond) {
		if _, err := os.Stat(caminho); err == nil {
			return
		}
	}
	t.Fatalf("%s não apareceu", caminho)
}

func TestVagaCLI(t *testing.T) {
	e := newEnv(t)
	a := e.project("a")
	withExecucao(t, a, claudeFalso(t))
	e.ok(a, "entrar", "--vaga", "G1", "--papel", "GERENTE")
	m := e.ok(a, "vaga", "abrir", "EXEC1", "--papel", "EXECUTOR", "--nota", "comece pelo T-1")
	if v := m["vaga"].(map[string]any); v["estado"] != "aberta" || v["modelo"] != "opus" || v["nota"] != "comece pelo T-1" {
		t.Fatalf("abrir = %v", m)
	}
	if code, m := e.run(a, "", "vaga", "abrir", "EXEC2", "--papel", "EXECUTOR"); code != 2 || !strings.Contains(fmt.Sprint(m), "teto") {
		t.Fatalf("passando do teto: %d %v", code, m)
	}
	m = e.ok(a, "vagas")
	if vs := m["vagas"].([]any); len(vs) != 1 || vs[0].(map[string]any)["vaga"] != "EXEC1" || m["execucao"] != true {
		t.Fatalf("vagas = %v", m)
	}
	// o daemon roda o primeiro turno com o claude falso: o worktree aparece
	esperarArquivo(t, filepath.Join(a, ".claude", "worktrees", "EXEC1"))
	if v := e.ok(a, "vaga", "fechar", "EXEC1")["vaga"].(map[string]any); v["estado"] != "fechada" {
		t.Fatalf("fechar = %v", v)
	}
	// uma pasta do projeto que não entrou é o humano
	h := filepath.Join(t.TempDir(), "humano")
	os.MkdirAll(h, 0o755)
	raw, _ := os.ReadFile(filepath.Join(a, config.FileName))
	os.WriteFile(filepath.Join(h, config.FileName), raw, 0o644)
	if v := e.ok(h, "vaga", "abrir", "EXEC1", "--papel", "EXECUTOR")["vaga"].(map[string]any); v["estado"] != "aberta" {
		t.Fatalf("humano reabrindo = %v", v)
	}
	// um EXECUTOR não abre vagas
	x := filepath.Join(t.TempDir(), "x")
	os.MkdirAll(x, 0o755)
	os.WriteFile(filepath.Join(x, config.FileName), raw, 0o644)
	e.ok(x, "entrar", "--vaga", "F9", "--papel", "EXECUTOR")
	if code, m := e.run(x, "", "vaga", "fechar", "EXEC1"); code == 0 || !strings.Contains(fmt.Sprint(m), "GERENTE") {
		t.Fatalf("EXECUTOR fechando: %d %v", code, m)
	}
	if code, _ := e.run(a, "", "vaga", "abrir"); code != 4 {
		t.Fatalf("uso: %d", code)
	}
}

func TestDespertarCLI(t *testing.T) {
	e := newEnv(t)
	a := e.project("a")
	withExecucao(t, a, claudeFalso(t))
	e.ok(a, "entrar", "--vaga", "G1", "--papel", "GERENTE")
	e.ok(a, "vaga", "abrir", "EXEC1", "--papel", "EXECUTOR")
	w := filepath.Join(a, ".claude", "worktrees", "EXEC1")
	esperarArquivo(t, w)
	raw, _ := os.ReadFile(filepath.Join(a, config.FileName))
	os.WriteFile(filepath.Join(w, config.FileName), raw, 0o644)
	e.ok(w, "entrar", "--vaga", "EXEC1", "--papel", "EXECUTOR")
	m := e.ok(w, "despertar", "--em", "20m", "ver", "o", "CI")
	if m["motivo"] != "ver o CI" {
		t.Fatalf("despertar = %v", m)
	}
	if code, _ := e.run(w, "", "despertar", "--em", "10s", "x"); code == 0 {
		t.Fatal("--em de 10s passou")
	}
	if code, _ := e.run(w, "", "despertar", "x"); code != 4 {
		t.Fatalf("sem --em: %d", code)
	}
	// a vaga G1 não é executada
	if code, _ := e.run(a, "", "despertar", "--em", "20m", "x"); code == 0 {
		t.Fatal("vaga não executada pediu despertar")
	}
}

func TestMCPVagas(t *testing.T) {
	e := newEnv(t)
	a := e.project("a")
	withExecucao(t, a, claudeFalso(t))
	e.ok(a, "entrar", "--vaga", "G1", "--papel", "GERENTE")
	cs := e.mcpSessao(a)
	out, isErr := chamar(t, cs, "vaga_abrir", map[string]any{"vaga": "EXEC1", "papel": "EXECUTOR", "nota": "oi"})
	if isErr || !strings.Contains(out, `"aberta"`) {
		t.Fatalf("vaga_abrir = %s", out)
	}
	if out, isErr = chamar(t, cs, "vagas", nil); isErr || !strings.Contains(out, "EXEC1") {
		t.Fatalf("vagas = %s", out)
	}
	if out, isErr = chamar(t, cs, "vaga_fechar", map[string]any{"vaga": "EXEC1"}); isErr || !strings.Contains(out, `"fechada"`) {
		t.Fatalf("vaga_fechar = %s", out)
	}
	if out, isErr = chamar(t, cs, "despertar", map[string]any{"em": "20m", "motivo": "x"}); !isErr {
		t.Fatalf("despertar de vaga não executada = %s", out)
	}
}
