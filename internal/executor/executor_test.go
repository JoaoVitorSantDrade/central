package executor

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"
)

var falso string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "central-executor-")
	if err != nil {
		panic(err)
	}
	falso = filepath.Join(dir, "claude")
	if runtime.GOOS == "windows" {
		falso += ".exe"
	}
	if out, err := exec.Command("go", "build", "-o", falso, "../../examples/claude-falso").CombinedOutput(); err != nil {
		panic(string(out))
	}
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

const sessaoTeste = "0f8fad5b-d9cb-469f-a165-70867728950e"

func turnoTeste(t *testing.T, env ...string) (Turno, string) {
	t.Helper()
	dir := t.TempDir()
	argsArq := filepath.Join(dir, "args.txt")
	return Turno{
		Claude: falso, Pasta: dir, Sessao: sessaoTeste, Worktree: "EXEC1", Primeiro: true,
		Modelo: "opus", Esforco: "high", Texto: "Você é a vaga EXEC1 <duração> \"x\" & y",
		Log: filepath.Join(dir, "turnos", "EXEC1", "1.jsonl"), Prazo: time.Minute,
		Ambiente: append([]string{"CLAUDE_FALSO_ARGS=" + argsArq}, env...),
	}, argsArq
}

func TestArgsPrimeiroESeguintes(t *testing.T) {
	tr := Turno{Sessao: "S", Worktree: "EXEC1", Modelo: "opus", Esforco: "high", Primeiro: true, Texto: "oi"}
	want := []string{"-p", "--session-id", "S", "-w", "EXEC1", "--model", "opus", "--effort", "high",
		"--permission-mode", "auto", "--permission-prompts", "none", "--output-format", "stream-json", "--verbose"}
	if got := Args(tr); !slices.Equal(got, want) {
		t.Fatalf("primeiro:\n got %q\nwant %q", got, want)
	}
	tr.Primeiro = false
	want = []string{"-p", "--resume", "S", "--model", "opus", "--effort", "high",
		"--permission-mode", "auto", "--permission-prompts", "none", "--output-format", "stream-json", "--verbose"}
	if got := Args(tr); !slices.Equal(got, want) {
		t.Fatalf("seguinte:\n got %q\nwant %q", got, want)
	}
}

func TestRodarLeStream(t *testing.T) {
	tr, argsArq := turnoTeste(t)
	var pid int
	r, err := Rodar(context.Background(), tr, func(p int) { pid = p })
	if err != nil {
		t.Fatal(err)
	}
	if pid == 0 {
		t.Fatal("onStart não recebeu o pid")
	}
	if r.Falha != FalhaOK || r.Sessao != sessaoTeste || r.CustoUSD != 0.01 || r.TokensEntrada != 10 ||
		r.TokensSaida != 5 || r.TurnosModelo != 1 || r.Texto != "ok" || r.Codigo != 0 {
		t.Fatalf("resultado = %+v", r)
	}
	if want := filepath.Join(tr.Pasta, ".claude", "worktrees", "EXEC1"); r.Pasta != want {
		t.Fatalf("pasta = %q, want %q", r.Pasta, want)
	}
	log, err := os.ReadFile(tr.Log)
	if err != nil || !strings.Contains(string(log), `"type":"result"`) {
		t.Fatalf("log = %q, %v", log, err)
	}
	got, _ := os.ReadFile(argsArq)
	partes := strings.SplitN(string(got), "--stdin--\n", 2)
	if len(partes) != 2 || strings.TrimSpace(partes[1]) != tr.Texto {
		t.Fatalf("o texto não chegou intacto pelo stdin: %q", got)
	}
	if !slices.Equal(strings.Fields(partes[0]), Args(tr)) {
		t.Fatalf("args = %q", partes[0])
	}
}

func TestRodarNegacao(t *testing.T) {
	tr, _ := turnoTeste(t, "CLAUDE_FALSO_NEGAR=rm -rf /")
	r, err := Rodar(context.Background(), tr, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Negacoes) != 1 || !strings.Contains(r.Negacoes[0], "rm -rf /") || !strings.HasPrefix(r.Negacoes[0], "Bash") {
		t.Fatalf("negacoes = %q", r.Negacoes)
	}
}

func TestRodarLimitePlano(t *testing.T) {
	tr, _ := turnoTeste(t, "CLAUDE_FALSO_LIMITE=1760000000")
	r, err := Rodar(context.Background(), tr, nil)
	if err != nil {
		t.Fatal(err)
	}
	if r.Falha != FalhaLimitePlano || r.ResetEm == nil || r.ResetEm.Unix() != 1760000000 {
		t.Fatalf("resultado = %+v", r)
	}
	tr, _ = turnoTeste(t, "CLAUDE_FALSO_LIMITE=")
	if r, _ = Rodar(context.Background(), tr, nil); r.Falha != FalhaLimitePlano || r.ResetEm != nil {
		t.Fatalf("sem epoch: %+v", r)
	}
}

func TestRodarPrazoMataArvore(t *testing.T) {
	tr, _ := turnoTeste(t, "CLAUDE_FALSO_DORMIR=30s")
	tr.Prazo = time.Second
	ini := time.Now()
	r, err := Rodar(context.Background(), tr, nil)
	if err != nil {
		t.Fatal(err)
	}
	if r.Falha != FalhaPrazo {
		t.Fatalf("falha = %q", r.Falha)
	}
	if d := time.Since(ini); d > 10*time.Second {
		t.Fatalf("demorou %v", d)
	}
}

func TestRodarErroESemClaude(t *testing.T) {
	tr, _ := turnoTeste(t, "CLAUDE_FALSO_SAIR=3")
	r, err := Rodar(context.Background(), tr, nil)
	if err != nil {
		t.Fatal(err)
	}
	if r.Falha != FalhaErro || r.Codigo != 3 || !strings.Contains(r.Stderr, "falha simulada") {
		t.Fatalf("resultado = %+v", r)
	}
	tr, _ = turnoTeste(t)
	tr.Claude = "claude-que-nao-existe-xyz"
	if r, err = Rodar(context.Background(), tr, nil); err != nil || r.Falha != FalhaSemClaude {
		t.Fatalf("sem claude: %+v, %v", r, err)
	}
	tr.Claude = filepath.Join(tr.Pasta, "nada", "claude.exe")
	if r, err = Rodar(context.Background(), tr, nil); err != nil || r.Falha != FalhaSemClaude {
		t.Fatalf("caminho inexistente: %+v, %v", r, err)
	}
}

func TestLerLogDeTurnoOrfao(t *testing.T) {
	tr, _ := turnoTeste(t)
	if _, err := Rodar(context.Background(), tr, nil); err != nil {
		t.Fatal(err)
	}
	r, ok := LerLog(tr.Log)
	if !ok || r.Sessao != sessaoTeste || r.CustoUSD != 0.01 {
		t.Fatalf("LerLog = %+v, %v", r, ok)
	}
	if _, ok := LerLog(filepath.Join(tr.Pasta, "nao-existe.jsonl")); ok {
		t.Fatal("log inexistente não tem resultado")
	}
}

func TestCortarLogPreservaResult(t *testing.T) {
	arq := filepath.Join(t.TempDir(), "1.jsonl")
	result := []byte(`{"type":"result","result":"ok","total_cost_usd":0.5}`)
	grande := strings.Repeat(`{"type":"assistant","x":"`+strings.Repeat("a", 1000)+`"}`+"\n", (MaxLog/1000)+100)
	if err := os.WriteFile(arq, append([]byte(grande), append(result, '\n')...), 0o644); err != nil {
		t.Fatal(err)
	}
	r, linha, ok := lerLog(arq)
	if !ok || r.CustoUSD != 0.5 {
		t.Fatalf("lerLog = %+v %v", r, ok)
	}
	if err := cortarLog(arq, linha); err != nil {
		t.Fatal(err)
	}
	st, _ := os.Stat(arq)
	if st.Size() > MaxLog {
		t.Fatalf("log com %d bytes", st.Size())
	}
	if r, ok := LerLog(arq); !ok || r.CustoUSD != 0.5 {
		t.Fatalf("depois do corte: %+v %v", r, ok)
	}
}
