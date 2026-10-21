package service

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/JoaoVitorSantDrade/central/internal/core"
	"github.com/JoaoVitorSantDrade/central/internal/executor"
)

const cfgExecucao = `
[projeto]
id = "p"
nome = "P"

[execucao]
claude = "claude-falso"
max_simultaneos = 2
custo_dia_usd = %CUSTO%

[execucao.cargos]
GERENTE  = { max = 1, modelo = "sonnet", esforco = "medium" }
EXECUTOR = { max = 3, modelo = "opus", esforco = "high", prazo_turno = "3h" }
`

// relogioExec é o relógio do serviço nos testes do executor.
type relogioExec struct {
	mu sync.Mutex
	t  time.Time
}

func (r *relogioExec) now() time.Time { r.mu.Lock(); defer r.mu.Unlock(); return r.t }
func (r *relogioExec) avancar(d time.Duration) {
	r.mu.Lock()
	r.t = r.t.Add(d)
	r.mu.Unlock()
}

// rodarFalso substitui o executor.Rodar: grava cada turno, pode segurar uma
// vaga até ser solta e devolve o resultado que o teste escolher.
type rodarFalso struct {
	mu        sync.Mutex
	turnos    []executor.Turno
	segurar   map[string]chan struct{}
	resultado func(t executor.Turno) executor.Resultado
}

func (f *rodarFalso) rodar(ctx context.Context, t executor.Turno, onStart func(int)) (executor.Resultado, error) {
	f.mu.Lock()
	f.turnos = append(f.turnos, t)
	ch := f.segurar[t.Worktree]
	res := f.resultado
	f.mu.Unlock()
	if onStart != nil {
		onStart(os.Getpid())
	}
	if ch != nil {
		<-ch
	}
	if res != nil {
		return res(t), nil
	}
	r := executor.Resultado{Sessao: t.Sessao, Pasta: t.Pasta, CustoUSD: 0.01, Texto: "ok"}
	if t.Primeiro {
		r.Pasta = filepath.Join(t.Pasta, ".claude", "worktrees", t.Worktree)
	}
	return r, nil
}

func (f *rodarFalso) vagas() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var vs []string
	for _, t := range f.turnos {
		vs = append(vs, t.Worktree)
	}
	return vs
}

func (f *rodarFalso) ultimo(t *testing.T) executor.Turno {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.turnos) == 0 {
		t.Fatal("nenhum turno rodou")
	}
	return f.turnos[len(f.turnos)-1]
}

type ambienteExec struct {
	s    *Service
	p    *project
	rel  *relogioExec
	raiz string
	f    *rodarFalso
	t    *testing.T
}

func novoAmbienteExec(t *testing.T, custo string) *ambienteExec {
	t.Helper()
	rel := &relogioExec{t: time.Date(2026, 10, 21, 15, 0, 0, 0, time.UTC)}
	s, err := New(t.TempDir(), rel.now)
	if err != nil {
		t.Fatal(err)
	}
	f := &rodarFalso{segurar: map[string]chan struct{}{}}
	antigo := rodarTurno
	rodarTurno = f.rodar
	t.Cleanup(func() {
		f.soltarTodos()
		s.esperarTurnos()
		rodarTurno = antigo
		s.Close()
	})
	raiz := t.TempDir()
	if custo == "" {
		custo = "0"
	}
	cfg := strings.Replace(cfgExecucao, "%CUSTO%", custo, 1)
	if _, err := s.Enter(ctx, EnterInput{ProjetoID: "p", Nome: "P", Raiz: raiz, Pasta: filepath.Join(raiz, "humano"), Vaga: "G1", Papel: "GERENTE", Config: cfg}); err != nil {
		t.Fatal(err)
	}
	p, err := s.project(ctx, "p")
	if err != nil {
		t.Fatal(err)
	}
	return &ambienteExec{s: s, p: p, rel: rel, raiz: raiz, f: f, t: t}
}

func (a *ambienteExec) abrir(vaga, papel string) core.VagaExec {
	a.t.Helper()
	modelo, esforco := "opus", "high"
	if papel == "GERENTE" {
		modelo, esforco = "sonnet", "medium"
	}
	v, err := a.p.core.AbrirVagaExec(ctx, core.Humano, core.VagaExec{Vaga: vaga, Papel: papel, Nota: "comece pelo T-1", Modelo: modelo, Esforco: esforco})
	if err != nil {
		a.t.Fatal(err)
	}
	return v
}

// passo roda um passo do escalonador e espera os turnos que não estão seguros.
func (a *ambienteExec) passo() {
	a.t.Helper()
	a.s.passoExecutor(ctx, a.rel.now())
}

func (a *ambienteExec) passoEEspera() {
	a.t.Helper()
	a.passo()
	a.s.esperarTurnos()
}

func (a *ambienteExec) vaga(vaga string) core.VagaExec {
	a.t.Helper()
	v, err := a.p.core.VagaExec(ctx, vaga)
	if err != nil {
		a.t.Fatal(err)
	}
	return v
}

// entrar simula o central entrar da vaga na pasta do worktree.
func (a *ambienteExec) entrar(vaga, papel string) {
	a.t.Helper()
	if _, err := a.p.core.Enter(ctx, vaga, papel, a.vaga(vaga).Pasta); err != nil {
		a.t.Fatal(err)
	}
}

// segurar faz os próximos turnos da vaga esperarem até soltar.
func (a *ambienteExec) segurar(vaga string) {
	a.f.mu.Lock()
	a.f.segurar[vaga] = make(chan struct{})
	a.f.mu.Unlock()
}

// soltarTodos solta os turnos seguros e os próximos não esperam mais.
func (f *rodarFalso) soltarTodos() {
	f.mu.Lock()
	defer f.mu.Unlock()
	for v, ch := range f.segurar {
		close(ch)
		delete(f.segurar, v)
	}
}

// esperarComecar espera n turnos terem começado (eles rodam numa goroutine).
func (a *ambienteExec) esperarComecar(n int) {
	a.t.Helper()
	for fim := time.Now().Add(5 * time.Second); time.Now().Before(fim); time.Sleep(5 * time.Millisecond) {
		if len(a.f.vagas()) >= n {
			return
		}
	}
	a.t.Fatalf("esperava %d turnos, rodaram %v", n, a.f.vagas())
}

func TestPrimeiroTurnoNaRaizComWorktree(t *testing.T) {
	a := novoAmbienteExec(t, "")
	v := a.abrir("EXEC1", "EXECUTOR")
	a.passoEEspera()
	tr := a.f.ultimo(t)
	if !tr.Primeiro || tr.Pasta != a.raiz || tr.Worktree != "EXEC1" || tr.Sessao != v.Sessao || tr.Modelo != "opus" ||
		tr.Esforco != "high" || tr.Claude != "claude-falso" || tr.Prazo != 3*time.Hour {
		t.Fatalf("turno = %+v", tr)
	}
	for _, s := range []string{"Você é a vaga EXEC1 (papel EXECUTOR) do projeto p.", "central entrar --vaga EXEC1 --papel EXECUTOR",
		"central brief --md", "Nota do Gerente: comece pelo T-1", "termine o turno", "central despertar --em"} {
		if !strings.Contains(tr.Texto, s) {
			t.Fatalf("texto sem %q:\n%s", s, tr.Texto)
		}
	}
	if want := filepath.Join(a.s.DataDir(), "projetos", "p", "turnos", "EXEC1", "1.jsonl"); tr.Log != want {
		t.Fatalf("log = %q, want %q", tr.Log, want)
	}
	if !slices.Contains(tr.Ambiente, "CENTRAL_HOME="+a.s.DataDir()) {
		t.Fatalf("ambiente = %q", tr.Ambiente)
	}
	v = a.vaga("EXEC1")
	if v.NTurnos != 1 || v.TurnoDesde != nil || v.Pasta != filepath.Join(a.raiz, ".claude", "worktrees", "EXEC1") {
		t.Fatalf("depois do turno = %+v", v)
	}
	a.passoEEspera()
	if n := len(a.f.vagas()); n != 1 {
		t.Fatalf("sem motivo, nenhum turno novo: %d turnos", n)
	}
}

func TestTurnoPorMensagem(t *testing.T) {
	a := novoAmbienteExec(t, "")
	a.abrir("EXEC1", "EXECUTOR")
	a.passoEEspera()
	a.entrar("EXEC1", "EXECUTOR")
	if _, err := a.p.core.Send(ctx, core.SendInput{De: "G1", Para: "EXEC1", Tipo: "nota", Corpo: "pegue T-2"}); err != nil {
		t.Fatal(err)
	}
	a.passoEEspera()
	tr := a.f.ultimo(t)
	if tr.Primeiro || tr.Pasta != a.vaga("EXEC1").Pasta {
		t.Fatalf("turno seguinte = %+v", tr)
	}
	if want := "Você acordou: mensagem de G1 (1 mensagens). Comece por central caixa."; tr.Texto != want {
		t.Fatalf("texto = %q", tr.Texto)
	}
}

func TestTurnoPorDespertar(t *testing.T) {
	a := novoAmbienteExec(t, "")
	a.abrir("EXEC1", "EXECUTOR")
	a.passoEEspera()
	a.entrar("EXEC1", "EXECUTOR")
	if err := a.p.core.Despertar(ctx, "EXEC1", a.rel.now().Add(20*time.Minute), "ver o CI"); err != nil {
		t.Fatal(err)
	}
	a.passoEEspera()
	if n := len(a.f.vagas()); n != 1 {
		t.Fatalf("despertar antes da hora: %d turnos", n)
	}
	a.rel.avancar(21 * time.Minute)
	a.passoEEspera()
	if tr := a.f.ultimo(t); len(a.f.vagas()) != 2 || !strings.Contains(tr.Texto, "Você acordou: despertar: ver o CI") {
		t.Fatalf("turno = %+v", tr)
	}
	a.passoEEspera()
	if n := len(a.f.vagas()); n != 2 {
		t.Fatalf("despertar consumido rodou de novo: %d", n)
	}
}

func TestMensagemDuranteTurnoEsperaOProximo(t *testing.T) {
	a := novoAmbienteExec(t, "")
	a.abrir("EXEC1", "EXECUTOR")
	a.passoEEspera()
	a.entrar("EXEC1", "EXECUTOR")
	a.segurar("EXEC1")
	a.p.core.Send(ctx, core.SendInput{De: "G1", Para: "EXEC1", Tipo: "nota", Corpo: "1"})
	a.passo()
	a.esperarComecar(2)
	a.p.core.Send(ctx, core.SendInput{De: "G1", Para: "EXEC1", Tipo: "nota", Corpo: "2"})
	a.passo()
	a.passo()
	if n := len(a.f.vagas()); n != 2 {
		t.Fatalf("turno sobreposto: %d turnos", n)
	}
	a.f.soltarTodos()
	a.s.esperarTurnos()
	a.passoEEspera()
	if n := len(a.f.vagas()); n != 3 {
		t.Fatalf("o próximo turno não começou: %d turnos", n)
	}
	if tr := a.f.ultimo(t); !strings.Contains(tr.Texto, "(2 mensagens)") {
		t.Fatalf("texto = %q", tr.Texto)
	}
}

func TestFilaRespeitaMaxSimultaneosEGerentePrimeiro(t *testing.T) {
	a := novoAmbienteExec(t, "")
	for _, v := range []string{"EXEC1", "EXEC2", "GERENTE1"} {
		a.segurar(v)
	}
	a.abrir("EXEC1", "EXECUTOR")
	a.rel.avancar(time.Second)
	a.abrir("EXEC2", "EXECUTOR")
	a.rel.avancar(time.Second)
	a.abrir("GERENTE1", "GERENTE")
	a.passo()
	a.esperarComecar(2)
	a.passo()
	time.Sleep(50 * time.Millisecond)
	got := a.f.vagas()
	slices.Sort(got)
	if !slices.Equal(got, []string{"EXEC1", "GERENTE1"}) {
		t.Fatalf("fila = %v", got)
	}
	es, _ := a.p.core.Events(ctx, core.EventFilter{Tipos: []string{"turno.iniciado"}})
	if len(es) != 2 || es[0].Ref != "GERENTE1" || es[1].Ref != "EXEC1" {
		t.Fatalf("ordem dos turnos = %+v", es)
	}
	a.f.soltarTodos()
	a.s.esperarTurnos()
	a.passoEEspera()
	if got := a.f.vagas(); len(got) != 3 || got[2] != "EXEC2" {
		t.Fatalf("depois = %v", got)
	}
}

func TestTresFalhasSuspendem(t *testing.T) {
	a := novoAmbienteExec(t, "")
	a.abrir("EXEC1", "EXECUTOR")
	a.f.resultado = func(executor.Turno) executor.Resultado {
		return executor.Resultado{Falha: executor.FalhaErro, Codigo: 1, Stderr: "quebrou"}
	}
	for i, espera := range []time.Duration{time.Minute, 5 * time.Minute} {
		ini := a.rel.now()
		a.passoEEspera()
		v := a.vaga("EXEC1")
		if v.Falhas != i+1 || v.ProximoEm == nil || !v.ProximoEm.Equal(ini.Add(espera)) || v.Estado != core.VagaAberta {
			t.Fatalf("falha %d: %+v", i+1, v)
		}
		a.rel.avancar(espera - time.Second)
		a.passoEEspera()
		if n := len(a.f.vagas()); n != i+1 {
			t.Fatalf("nova tentativa antes da hora: %d", n)
		}
		a.rel.avancar(time.Second)
	}
	a.passoEEspera()
	v := a.vaga("EXEC1")
	if v.Falhas != 3 || v.Estado != core.VagaSuspensa {
		t.Fatalf("3ª falha: %+v", v)
	}
	if tr := a.f.ultimo(t); !tr.Primeiro || !strings.HasPrefix(tr.Texto, "O turno anterior terminou por erro; confira onde parou. ") {
		t.Fatalf("texto da nova tentativa = %q", tr.Texto)
	}
	ms, _ := a.p.core.Inbox(ctx, "G1")
	if len(ms) != 1 || ms[0].Tipo != core.TipoVagaSuspensa {
		t.Fatalf("caixa do gerente = %+v", ms)
	}
	a.rel.avancar(time.Hour)
	a.passoEEspera()
	if n := len(a.f.vagas()); n != 3 {
		t.Fatalf("vaga suspensa rodou: %d", n)
	}
}

func TestLimitePlanoPausaTodas(t *testing.T) {
	a := novoAmbienteExec(t, "")
	reset := a.rel.now().Add(2 * time.Hour).Truncate(time.Second)
	a.f.resultado = func(t executor.Turno) executor.Resultado {
		if t.Worktree == "EXEC1" {
			return executor.Resultado{Falha: executor.FalhaLimitePlano, ResetEm: &reset}
		}
		return executor.Resultado{Pasta: "x"}
	}
	a.abrir("EXEC1", "EXECUTOR")
	a.passoEEspera()
	a.abrir("EXEC2", "EXECUTOR")
	if ate, _ := a.p.core.PlanoPausadoAte(ctx); !ate.Equal(reset) {
		t.Fatalf("pausado até %v, want %v", ate, reset)
	}
	if v := a.vaga("EXEC1"); v.Falhas != 0 {
		t.Fatalf("limite do plano contou falha: %+v", v)
	}
	a.rel.avancar(time.Hour)
	a.passoEEspera()
	if n := len(a.f.vagas()); n != 1 {
		t.Fatalf("turno com o plano pausado: %v", a.f.vagas())
	}
	a.rel.avancar(time.Hour)
	a.f.resultado = nil
	a.passoEEspera()
	if got := a.f.vagas(); len(got) != 3 {
		t.Fatalf("depois do reset = %v", got)
	}
	if ate, _ := a.p.core.PlanoPausadoAte(ctx); !ate.IsZero() {
		t.Fatalf("ainda pausado até %v", ate)
	}
	// sem hora de reset: 30 min
	a.f.resultado = func(executor.Turno) executor.Resultado { return executor.Resultado{Falha: executor.FalhaLimitePlano} }
	a.p.core.Despertar(ctx, "EXEC1", a.rel.now(), "x")
	ini := a.rel.now()
	a.passoEEspera()
	if ate, _ := a.p.core.PlanoPausadoAte(ctx); !ate.Equal(ini.Add(30 * time.Minute)) {
		t.Fatalf("pausa sem reset até %v", ate)
	}
}

func TestTetoDeCustoPausa(t *testing.T) {
	a := novoAmbienteExec(t, "0.05")
	a.f.resultado = func(t executor.Turno) executor.Resultado { return executor.Resultado{Pasta: "x", CustoUSD: 0.06} }
	a.abrir("EXEC1", "EXECUTOR")
	a.passoEEspera()
	ate, err := a.p.core.PlanoPausadoAte(ctx)
	l := a.rel.now().Local()
	meiaNoite := time.Date(l.Year(), l.Month(), l.Day()+1, 0, 0, 0, 0, time.Local)
	if err != nil || !ate.Equal(meiaNoite) {
		t.Fatalf("pausado até %v, want %v", ate, meiaNoite)
	}
	a.abrir("EXEC2", "EXECUTOR")
	a.passoEEspera()
	if n := len(a.f.vagas()); n != 1 {
		t.Fatalf("turno com o teto passado: %v", a.f.vagas())
	}
}

// pidMorto devolve o pid de um processo que já terminou.
func pidMorto(t *testing.T) int {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^$")
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
	return cmd.Process.Pid
}

func TestRecuperaTurnoVivoEMorto(t *testing.T) {
	a := novoAmbienteExec(t, "")
	for _, v := range []string{"VIVO1", "MORTO1", "FEITO1"} {
		a.abrir(v, "EXECUTOR")
		// um daemon anterior começou o turno
		if _, err := a.p.core.IniciarTurno(ctx, v, "primeiro turno", 0); err != nil {
			t.Fatal(err)
		}
	}
	a.p.core.RegistrarPID(ctx, "VIVO1", os.Getpid())
	a.p.core.RegistrarPID(ctx, "MORTO1", pidMorto(t))
	a.p.core.RegistrarPID(ctx, "FEITO1", pidMorto(t))
	// o turno de FEITO1 terminou enquanto o daemon estava fora: o log tem o result
	log := a.s.logTurno("p", "FEITO1", 1)
	os.MkdirAll(filepath.Dir(log), 0o755)
	os.WriteFile(log, []byte(`{"type":"system","subtype":"init","session_id":"s","cwd":"/w/FEITO1"}`+"\n"+
		`{"type":"result","is_error":false,"result":"ok","total_cost_usd":0.2}`+"\n"), 0o644)
	a.passoEEspera()
	if n := len(a.f.vagas()); n != 0 {
		t.Fatalf("turno duplicado: %v", a.f.vagas())
	}
	if v := a.vaga("VIVO1"); v.TurnoDesde == nil || v.Falhas != 0 {
		t.Fatalf("turno vivo virou falha: %+v", v)
	}
	if v := a.vaga("MORTO1"); v.TurnoDesde != nil || v.Falhas != 1 || v.FalhaAnterior != "daemon_reiniciou" || v.ProximoEm == nil {
		t.Fatalf("turno morto: %+v", v)
	}
	if v := a.vaga("FEITO1"); v.TurnoDesde != nil || v.Falhas != 0 || v.Pasta != "/w/FEITO1" || v.CustoDia != 0.2 {
		t.Fatalf("turno terminado fora: %+v", v)
	}
}

var (
	claudeFalsoOnce sync.Once
	claudeFalsoPath string
	claudeFalsoErr  string
)

func claudeFalso(t *testing.T) string {
	t.Helper()
	claudeFalsoOnce.Do(func() {
		dir, err := os.MkdirTemp("", "central-claude-falso-")
		if err != nil {
			claudeFalsoErr = err.Error()
			return
		}
		claudeFalsoPath = filepath.Join(dir, "claude")
		if runtime.GOOS == "windows" {
			claudeFalsoPath += ".exe"
		}
		if out, err := exec.Command("go", "build", "-o", claudeFalsoPath, "../../examples/claude-falso").CombinedOutput(); err != nil {
			claudeFalsoErr = string(out)
		}
	})
	if claudeFalsoErr != "" {
		t.Fatal(claudeFalsoErr)
	}
	return claudeFalsoPath
}

func TestExecutorComClaudeFalso(t *testing.T) {
	falso := claudeFalso(t)
	a := novoAmbienteExec(t, "")
	rodarTurno = executor.Rodar
	cfg := strings.Replace(strings.Replace(cfgExecucao, "%CUSTO%", "0", 1), `claude = "claude-falso"`, "claude = '"+falso+"'", 1)
	if _, err := a.s.Enter(ctx, EnterInput{ProjetoID: "p", Nome: "P", Raiz: a.raiz, Pasta: filepath.Join(a.raiz, "humano"), Vaga: "G1", Papel: "GERENTE", Config: cfg}); err != nil {
		t.Fatal(err)
	}
	args := filepath.Join(t.TempDir(), "args.txt")
	t.Setenv("CLAUDE_FALSO_ARGS", args)
	v := a.abrir("EXEC1", "EXECUTOR")
	a.passoEEspera()
	v = a.vaga("EXEC1")
	if want := filepath.Join(a.raiz, ".claude", "worktrees", "EXEC1"); v.Pasta != want || v.NTurnos != 1 || v.Falhas != 0 || v.CustoDia != 0.01 {
		t.Fatalf("primeiro turno: %+v", v)
	}
	a.entrar("EXEC1", "EXECUTOR")
	a.p.core.Send(ctx, core.SendInput{De: "G1", Para: "EXEC1", Tipo: "nota", Corpo: "oi"})
	a.passoEEspera()
	got, _ := os.ReadFile(args)
	if !strings.Contains(string(got), "--resume\n"+v.Sessao+"\n") || !strings.Contains(string(got), "Você acordou: mensagem de G1") {
		t.Fatalf("segundo turno recebeu:\n%s", got)
	}
	if v = a.vaga("EXEC1"); v.NTurnos != 2 || v.Falhas != 0 {
		t.Fatalf("segundo turno: %+v", v)
	}
}

func TestMensagemNaoLidaNaoReacordaSemFim(t *testing.T) {
	a := novoAmbienteExec(t, "")
	a.abrir("EXEC1", "EXECUTOR")
	a.passoEEspera()
	a.entrar("EXEC1", "EXECUTOR")
	a.p.core.Send(ctx, core.SendInput{De: "G1", Para: "EXEC1", Tipo: "nota", Corpo: "1"})
	a.rel.avancar(time.Second)
	a.passoEEspera()
	// o turno não leu a caixa: a mesma mensagem não acorda a vaga de novo
	a.rel.avancar(time.Second)
	a.passoEEspera()
	a.passoEEspera()
	if n := len(a.f.vagas()); n != 2 {
		t.Fatalf("a mesma mensagem acordou de novo: %d turnos", n)
	}
	a.rel.avancar(time.Second)
	a.p.core.Send(ctx, core.SendInput{De: "G1", Para: "EXEC1", Tipo: "nota", Corpo: "2"})
	a.passoEEspera()
	if tr := a.f.ultimo(t); len(a.f.vagas()) != 3 || !strings.Contains(tr.Texto, "(2 mensagens)") {
		t.Fatalf("mensagem nova: %d turnos, %q", len(a.f.vagas()), tr.Texto)
	}
}
