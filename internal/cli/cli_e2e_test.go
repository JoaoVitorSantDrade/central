package cli_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/JoaoVitorSantDrade/central/internal/config"
	"github.com/JoaoVitorSantDrade/central/internal/daemon"
)

var bin, helper, eco string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "central-bin-")
	if err != nil {
		panic(err)
	}
	bin = filepath.Join(dir, "central")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	build := exec.Command("go", "build", "-o", bin, "../../cmd/central")
	build.Env = append(os.Environ(), "CGO_ENABLED=0")
	if out, err := build.CombinedOutput(); err != nil {
		panic(fmt.Sprintf("build: %v\n%s", err, out))
	}
	helper = filepath.Join(dir, "helper")
	if runtime.GOOS == "windows" {
		helper += ".exe"
	}
	build = exec.Command("go", "build", "-o", helper, "./testdata/helper")
	build.Env = append(os.Environ(), "CGO_ENABLED=0")
	if out, err := build.CombinedOutput(); err != nil {
		panic(fmt.Sprintf("build helper: %v\n%s", err, out))
	}
	eco = filepath.Join(dir, "eco")
	if runtime.GOOS == "windows" {
		eco += ".exe"
	}
	build = exec.Command("go", "build", "-o", eco, "../../examples/ext/eco")
	build.Env = append(os.Environ(), "CGO_ENABLED=0")
	if out, err := build.CombinedOutput(); err != nil {
		panic(fmt.Sprintf("build eco: %v\n%s", err, out))
	}
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

type env struct {
	t    *testing.T
	home string
	addr string
}

func newEnv(t *testing.T) *env {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	l.Close()
	e := &env{t: t, home: t.TempDir(), addr: addr}
	t.Cleanup(e.killDaemon)
	return e
}

func (e *env) environ() []string {
	return append(os.Environ(), "CENTRAL_HOME="+e.home, "CENTRAL_ADDR="+e.addr)
}

func (e *env) killDaemon() {
	raw, err := os.ReadFile(filepath.Join(e.home, daemon.InfoFile))
	if err != nil {
		return
	}
	var info daemon.Info
	if json.Unmarshal(raw, &info) != nil {
		return
	}
	if p, err := os.FindProcess(info.PID); err == nil {
		p.Kill()
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := http.Get("http://" + e.addr + "/v1/saude")
		if err != nil {
			break
		}
		resp.Body.Close()
		time.Sleep(50 * time.Millisecond)
	}
	// o processo morto não apaga daemon.json; o próximo daemon o regrava
	os.Remove(filepath.Join(e.home, daemon.InfoFile))
}

// project cria uma pasta com o central.toml do projeto "piloto".
func (e *env) project(name string) string {
	e.t.Helper()
	d := filepath.Join(e.t.TempDir(), name)
	if err := os.MkdirAll(d, 0o755); err != nil {
		e.t.Fatal(err)
	}
	if _, err := config.InitProject(d, "piloto", "Piloto"); err != nil {
		e.t.Fatal(err)
	}
	return d
}

func (e *env) cmd(dir string, stdin string, args ...string) *exec.Cmd {
	c := exec.Command(bin, args...)
	c.Dir = dir
	c.Env = e.environ()
	if stdin != "" {
		c.Stdin = strings.NewReader(stdin)
	}
	return c
}

func parse(t *testing.T, raw []byte) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(raw), &m); err != nil {
		t.Fatalf("saída não é JSON: %q", raw)
	}
	return m
}

func (e *env) run(dir, stdin string, args ...string) (int, map[string]any) {
	e.t.Helper()
	c := e.cmd(dir, stdin, args...)
	var out bytes.Buffer
	c.Stdout = &out
	err := c.Run()
	code := 0
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
	} else if err != nil {
		e.t.Fatal(err)
	}
	return code, parse(e.t, out.Bytes())
}

func (e *env) ok(dir string, args ...string) map[string]any {
	e.t.Helper()
	code, m := e.run(dir, "", args...)
	if code != 0 {
		e.t.Fatalf("central %v: saída %d: %v", args, code, m)
	}
	return m
}

func corpos(m map[string]any) []string {
	var out []string
	for _, x := range m["mensagens"].([]any) {
		out = append(out, x.(map[string]any)["corpo"].(string))
	}
	return out
}

type bg struct {
	done chan struct{}
	out  bytes.Buffer
	code int
	end  time.Time
}

func (e *env) start(dir string, args ...string) *bg {
	e.t.Helper()
	b := &bg{done: make(chan struct{})}
	c := e.cmd(dir, "", args...)
	c.Stdout = &b.out
	if err := c.Start(); err != nil {
		e.t.Fatal(err)
	}
	go func() {
		err := c.Wait()
		b.end = time.Now()
		if ee, ok := err.(*exec.ExitError); ok {
			b.code = ee.ExitCode()
		}
		close(b.done)
	}()
	return b
}

func (b *bg) wait(t *testing.T, max time.Duration) map[string]any {
	t.Helper()
	select {
	case <-b.done:
	case <-time.After(max):
		t.Fatalf("processo não terminou em %v", max)
	}
	if b.code != 0 {
		t.Fatalf("saída %d: %s", b.code, b.out.String())
	}
	return parse(t, b.out.Bytes())
}

func TestAutostartEFluxo(t *testing.T) {
	e := newEnv(t)
	a, b := e.project("a"), e.project("b")
	e.ok(a, "entrar", "--vaga", "FIXER1", "--papel", "FIXER")
	e.ok(b, "entrar", "--vaga", "REVISOR1", "--papel", "REVISOR")
	m := e.ok(a, "enviar", "REVISOR1", "concluida", "--ref", "T-1", "T-1 pronta")
	if m["mensagem"].(map[string]any)["ref"] != "T-1" {
		t.Fatalf("enviar = %v", m)
	}
	got := corpos(e.ok(b, "caixa"))
	if len(got) != 1 || got[0] != "T-1 pronta" {
		t.Fatalf("caixa = %v", got)
	}
	// subpasta do projeto age como o mesmo agente
	sub := filepath.Join(b, "x", "y")
	os.MkdirAll(sub, 0o755)
	if got := corpos(e.ok(sub, "caixa")); len(got) != 0 {
		t.Fatalf("caixa da subpasta = %v", got)
	}
}

func TestEsperarAcordaEmMenosDeUmSegundo(t *testing.T) {
	e := newEnv(t)
	a, b := e.project("a"), e.project("b")
	e.ok(a, "entrar", "--vaga", "A1", "--papel", "EMISSOR")
	e.ok(b, "entrar", "--vaga", "B1", "--papel", "OUVINTE")
	w := e.start(b, "esperar", "--ate", "30s")
	time.Sleep(500 * time.Millisecond)
	sent := time.Now()
	e.ok(a, "enviar", "B1", "ping", "acorda")
	m := w.wait(t, 10*time.Second)
	if m["motivo"] != "mensagem" {
		t.Fatalf("esperar = %v", m)
	}
	// O limite real é 1 s; 2 s dá folga para a máquina carregada.
	if d := w.end.Sub(sent); d > 2*time.Second {
		t.Fatalf("acordou %v depois do envio", d)
	}
}

func TestEsperarTempo(t *testing.T) {
	e := newEnv(t)
	b := e.project("b")
	e.ok(b, "entrar", "--vaga", "B1", "--papel", "OUVINTE")
	if m := e.ok(b, "esperar", "--ate", "300ms"); m["motivo"] != "tempo" {
		t.Fatalf("esperar = %v", m)
	}
}

func TestEsperarSobreviveReinicioDoDaemon(t *testing.T) {
	e := newEnv(t)
	a, b := e.project("a"), e.project("b")
	e.ok(a, "entrar", "--vaga", "A1", "--papel", "EMISSOR")
	e.ok(b, "entrar", "--vaga", "B1", "--papel", "OUVINTE")
	w := e.start(b, "esperar", "--ate", "30s")
	time.Sleep(500 * time.Millisecond)
	e.killDaemon()
	time.Sleep(300 * time.Millisecond)
	e.ok(a, "enviar", "B1", "ping", "depois do reinício")
	if m := w.wait(t, 15*time.Second); m["motivo"] != "mensagem" {
		t.Fatalf("esperar = %v", m)
	}
}

func TestEnviarCorpoUTF8EStdin(t *testing.T) {
	e := newEnv(t)
	a, b := e.project("a"), e.project("b")
	e.ok(a, "entrar", "--vaga", "A1", "--papel", "X")
	e.ok(b, "entrar", "--vaga", "B1", "--papel", "Y")
	arg := `Revisão concluída: "ok" ✓`
	multi := "linha 1\nlinha \"2\" com acento: ação\nlinha 3"
	e.ok(a, "enviar", "B1", "nota", arg)
	code, m := e.run(a, multi+"\n", "enviar", "B1", "nota", "-")
	if code != 0 {
		t.Fatalf("stdin: %d %v", code, m)
	}
	got := corpos(e.ok(b, "caixa"))
	if len(got) != 2 || got[0] != arg || got[1] != multi {
		t.Fatalf("caixa = %q", got)
	}
}

func TestForaDeProjetoSai3(t *testing.T) {
	e := newEnv(t)
	dir := t.TempDir()
	if p, err := config.FindProject(dir); err == nil {
		t.Skipf("há um central.toml num ancestral da pasta temporária (%s); o teste não se aplica", p.Root)
	}
	code, m := e.run(dir, "", "caixa")
	if code != 3 || m["erro"].(map[string]any)["codigo"] != "sem_projeto" {
		t.Fatalf("%d %v", code, m)
	}
}

func TestSemEntrarSai3(t *testing.T) {
	e := newEnv(t)
	a := e.project("a")
	e.ok(e.project("outro"), "entrar", "--vaga", "Z", "--papel", "Z") // registra o projeto
	code, m := e.run(a, "", "caixa")
	erro := m["erro"].(map[string]any)
	if code != 3 || erro["codigo"] != "nao_encontrado" {
		t.Fatalf("%d %v", code, m)
	}
	if !strings.Contains(erro["mensagem"].(string), "central entrar") {
		t.Fatalf("mensagem sem a dica: %v", erro["mensagem"])
	}
}

func TestUsoInvalidoSai4(t *testing.T) {
	e := newEnv(t)
	a := e.project("a")
	code, m := e.run(a, "", "entrar", "--vaga", "A1")
	if code != 4 || m["ok"] != false {
		t.Fatalf("%d %v", code, m)
	}
	code, _ = e.run(a, "", "inexistente")
	if code != 4 {
		t.Fatalf("comando desconhecido: %d", code)
	}
}

func TestInitEVersao(t *testing.T) {
	e := newEnv(t)
	d := t.TempDir()
	m := e.ok(d, "init", "--id", "novo", "--nome", "Novo")
	if m["projeto"].(map[string]any)["id"] != "novo" {
		t.Fatalf("init = %v", m)
	}
	if code, _ := e.run(d, "", "init", "--id", "novo"); code != 2 {
		t.Fatalf("init repetido: %d", code)
	}
	if m := e.ok(d, "versao"); m["api"] != "v1" {
		t.Fatalf("versao = %v", m)
	}
}

func TestEsperarDesisteSemDaemon(t *testing.T) {
	// Porta ocupada por algo que não é a Central: o autostart nunca consegue.
	srv := httptest.NewServer(http.NotFoundHandler())
	defer srv.Close()
	e := newEnv(t)
	e.addr = strings.TrimPrefix(srv.URL, "http://")
	a := e.project("a")
	start := time.Now()
	code, m := e.run(a, "", "esperar", "--ate", "2m")
	if code != 5 || m["erro"].(map[string]any)["codigo"] != "daemon_indisponivel" {
		t.Fatalf("%d %v", code, m)
	}
	if d := time.Since(start); d > 40*time.Second {
		t.Fatalf("levou %v para desistir", d)
	}
}

func TestEntrarComConfigInvalidaSai4(t *testing.T) {
	e := newEnv(t)
	a := e.project("a")
	e.ok(a, "entrar", "--vaga", "A1", "--papel", "X")
	ruim := "[projeto]\nid = \"piloto\"\n\n[recursos.gpu]\ndimensoes = { vram_mb = 8192 }\nreservado = { ram_mb = 1 }\nprioridades = [\"a\"]\n"
	if err := os.WriteFile(filepath.Join(a, config.FileName), []byte(ruim), 0o644); err != nil {
		t.Fatal(err)
	}
	code, m := e.run(a, "", "entrar", "--vaga", "A1", "--papel", "X")
	if code != 4 {
		t.Fatalf("%d %v", code, m)
	}
	if msg := m["erro"].(map[string]any)["mensagem"].(string); !strings.Contains(msg, "recursos.gpu.reservado") {
		t.Fatalf("mensagem = %q", msg)
	}
	quebrado := "[projeto]\nid = \"piloto\"\nnome = \n"
	if err := os.WriteFile(filepath.Join(a, config.FileName), []byte(quebrado), 0o644); err != nil {
		t.Fatal(err)
	}
	code, m = e.run(a, "", "entrar", "--vaga", "A1", "--papel", "X")
	if msg, _ := m["erro"].(map[string]any)["mensagem"].(string); code != 4 || !strings.Contains(msg, "line 3") {
		t.Fatalf("%d %v", code, m)
	}
}

func agenteNaLista(t *testing.T, m map[string]any, vaga string) map[string]any {
	t.Helper()
	for _, x := range m["agentes"].([]any) {
		if a := x.(map[string]any); a["vaga"] == vaga {
			return a
		}
	}
	t.Fatalf("vaga %s fora de %v", vaga, m)
	return nil
}

func TestAgentesStatusSair(t *testing.T) {
	e := newEnv(t)
	a, b := e.project("a"), e.project("b")
	e.ok(a, "entrar", "--vaga", "A1", "--papel", "FIXER")
	e.ok(b, "entrar", "--vaga", "B1", "--papel", "REVISOR")
	m := e.ok(a, "status", "--estado", "trabalhando", "rodando gate")
	if ag := m["agente"].(map[string]any); ag["estado"] != "trabalhando" || ag["status"] != "rodando gate" {
		t.Fatalf("status = %v", m)
	}
	ag := agenteNaLista(t, e.ok(b, "agentes"), "A1")
	if ag["estado"] != "trabalhando" || ag["status"] != "rodando gate" {
		t.Fatalf("agentes = %v", ag)
	}
	e.ok(a, "sair")
	if ag := agenteNaLista(t, e.ok(b, "agentes"), "A1"); ag["estado"] != "encerrado" {
		t.Fatalf("depois de sair = %v", ag)
	}
	if code, m := e.run(a, "", "status", "--estado", "dormindo", "x"); code != 4 {
		t.Fatalf("estado inválido: %d %v", code, m)
	}
	if code, m := e.run(a, "", "status"); code != 4 {
		t.Fatalf("status sem texto: %d %v", code, m)
	}
}

func TestResponderEPendentes(t *testing.T) {
	e := newEnv(t)
	a, b := e.project("a"), e.project("b")
	e.ok(a, "entrar", "--vaga", "A1", "--papel", "FIXER")
	e.ok(b, "entrar", "--vaga", "B1", "--papel", "REVISOR")
	m := e.ok(a, "enviar", "B1", "pergunta", "--responder", "30m", "pode revisar?")
	seq := fmt.Sprint(m["mensagem"].(map[string]any)["seq"])
	if p := e.ok(b, "pendentes"); len(p["devo"].([]any)) != 1 {
		t.Fatalf("pendentes de B1 = %v", p)
	}
	if code, m := e.run(b, "sim\n", "responder", seq, "-"); code != 0 {
		t.Fatalf("responder: %d %v", code, m)
	}
	if p := e.ok(a, "pendentes"); len(p["esperam"].([]any)) != 0 {
		t.Fatalf("pendentes de A1 = %v", p)
	}
	if c := corpos(e.ok(a, "caixa")); len(c) != 1 || c[0] != "sim" {
		t.Fatalf("caixa de A1 = %v", c)
	}
	if code, m := e.run(b, "", "responder", "abc", "x"); code != 4 {
		t.Fatalf("seq inválido: %d %v", code, m)
	}
}

func TestTravaCLI(t *testing.T) {
	e := newEnv(t)
	a, b := e.project("a"), e.project("b")
	e.ok(a, "entrar", "--vaga", "A1", "--papel", "FIXER")
	e.ok(b, "entrar", "--vaga", "B1", "--papel", "FIXER")
	m := e.ok(a, "trava", "src/main.go", "--motivo", "refatorando")
	if tr := m["trava"].(map[string]any); tr["vaga"] != "A1" || tr["nome"] != "src/main.go" || tr["motivo"] != "refatorando" {
		t.Fatalf("trava = %v", m)
	}
	code, m := e.run(b, "", "trava", "src/main.go", "--motivo", "eu também")
	if code != 2 || !strings.Contains(fmt.Sprint(m), "A1") {
		t.Fatalf("segunda vaga: %d %v", code, m)
	}
	if code, m := e.run(b, "", "soltar", "src/main.go"); code != 2 {
		t.Fatalf("soltar de quem não é dono: %d %v", code, m)
	}
	ls := e.ok(b, "travas")["travas"].([]any)
	if len(ls) != 1 || ls[0].(map[string]any)["dono_estado"] != "ativo" {
		t.Fatalf("travas = %v", ls)
	}
	e.ok(a, "soltar", "src/main.go")
	if code, m := e.run(a, "", "soltar", "src/main.go"); code != 3 || strings.Contains(fmt.Sprint(m), "rode central entrar") {
		t.Fatalf("soltar de novo: %d %v", code, m)
	}
	e.ok(b, "trava", "src/main.go", "--motivo", "agora eu")
	if code, m := e.run(a, "", "trava", "x y", "--motivo", "m"); code != 4 {
		t.Fatalf("nome inválido: %d %v", code, m)
	}
	if code, m := e.run(a, "", "trava", "x"); code != 4 {
		t.Fatalf("sem motivo: %d %v", code, m)
	}
}

// withGPU acrescenta ao central.toml da pasta o recurso gpu do exemplo da spec, sem medidor.
func withGPU(t *testing.T, dir string) {
	t.Helper()
	f, err := os.OpenFile(filepath.Join(dir, config.FileName), os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteString("\n[recursos.gpu]\ndimensoes = { vram_mb = 8192, uso_pct = 100 }\n" +
		"reservado = { vram_mb = 2200, uso_pct = 15 }\nprioridades = [\"versao\", \"executor\"]\n"); err != nil {
		t.Fatal(err)
	}
}

func estadoConcessao(t *testing.T, m map[string]any) string {
	t.Helper()
	g, ok := m["concessao"].(map[string]any)
	if !ok {
		t.Fatalf("sem concessao: %v", m)
	}
	return g["estado"].(string)
}

func TestRecursoCLI(t *testing.T) {
	e := newEnv(t)
	a, b := e.project("a"), e.project("b")
	withGPU(t, a)
	withGPU(t, b)
	e.ok(a, "entrar", "--vaga", "FIXER1", "--papel", "FIXER")
	e.ok(b, "entrar", "--vaga", "FIXER2", "--papel", "FIXER")
	if m := e.ok(a, "recurso", "pedir", "gpu", "--exclusivo", "sim", "--motivo", "gate"); estadoConcessao(t, m) != "ativa" {
		t.Fatalf("FIXER1: %v", m)
	}
	code, m := e.run(b, "", "recurso", "pedir", "gpu", "--dims", "vram_mb=1000,uso_pct=20", "--chave", "k1", "--esperar", "3s")
	if erro, _ := m["erro"].(map[string]any); code != 2 || erro["codigo"] != "ainda_na_fila" {
		t.Fatalf("FIXER2 com --esperar 3s: %d %v", code, m)
	}
	v := e.ok(b, "recurso", "ver", "gpu")["recurso"].(map[string]any)
	if len(v["ativas"].([]any)) != 1 || len(v["fila"].([]any)) != 1 {
		t.Fatalf("ver = %v", v)
	}
	// O pedido continua na fila: outro pedido é conflito; a mesma chave volta a esperar por ele.
	if code, m := e.run(b, "", "recurso", "pedir", "gpu", "--dims", "vram_mb=1000"); code != 2 {
		t.Fatalf("segundo pedido: %d %v", code, m)
	}
	espera := e.start(b, "recurso", "pedir", "gpu", "--dims", "vram_mb=1000,uso_pct=20", "--chave", "k1", "--esperar", "10s")
	time.Sleep(500 * time.Millisecond)
	e.ok(a, "recurso", "soltar", "gpu")
	m = espera.wait(t, 10*time.Second)
	if espera.code != 0 || estadoConcessao(t, m) != "ativa" {
		t.Fatalf("depois de soltar: %d %v", espera.code, m)
	}
	if spec := v["spec"].(map[string]any); spec["prazo_max_ms"] != float64(30*60*1000) || spec["nome"] != "gpu" {
		t.Fatalf("spec = %v", spec)
	}
	// A mesma chave depois de soltar devolve o pedido encerrado: saída 2 com encerrada.
	e.ok(b, "recurso", "soltar", "gpu")
	code, m = e.run(b, "", "recurso", "pedir", "gpu", "--dims", "vram_mb=1000,uso_pct=20", "--chave", "k1")
	if erro, _ := m["erro"].(map[string]any); code != 2 || erro["codigo"] != "encerrada" {
		t.Fatalf("chave de pedido encerrado: %d %v", code, m)
	}
	if code, m := e.run(a, "", "recurso", "pedir", "gpu", "--min", "45"); code != 4 || !strings.Contains(fmt.Sprint(m), "divida") {
		t.Fatalf("--min acima do teto: %d %v", code, m)
	}
	if code, m := e.run(a, "", "recurso", "pedir", "gpu", "--dims", "vram_mb"); code != 4 {
		t.Fatalf("--dims inválido: %d %v", code, m)
	}
}

// execucao devolve a execução da saída de rodar.
func execucao(t *testing.T, m map[string]any) map[string]any {
	t.Helper()
	x, ok := m["execucao"].(map[string]any)
	if !ok {
		t.Fatalf("sem execucao: %v", m)
	}
	return x
}

func TestRodarPropagaCodigo(t *testing.T) {
	e := newEnv(t)
	a := e.project("a")
	e.ok(a, "entrar", "--vaga", "FIXER1", "--papel", "FIXER")
	code, m := e.run(a, "", "rodar", "--rotulo", "t", "--", helper, "exit", "3")
	if code != 3 || m["codigo"] != float64(3) {
		t.Fatalf("rodar exit 3: %d %v", code, m)
	}
	x := execucao(t, m)
	if x["codigo"] != float64(3) || x["fim"] == nil || x["rotulo"] != "t" || !strings.Contains(x["final_log"].(string), "saindo com 3") {
		t.Fatalf("execução = %v", x)
	}
	if code, m := e.run(a, "", "rodar", "--rotulo", "t", "--", helper, "exit", "0"); code != 0 || m["ok"] != true {
		t.Fatalf("rodar exit 0: %d %v", code, m)
	}
	if code, m := e.run(a, "", "rodar", "--", helper, "exit", "0"); code != 4 {
		t.Fatalf("sem --rotulo: %d %v", code, m)
	}
	if code, m := e.run(a, "", "rodar", "--rotulo", "t"); code != 4 {
		t.Fatalf("sem comando: %d %v", code, m)
	}
}

func TestRodarComandoInexistente(t *testing.T) {
	e := newEnv(t)
	a := e.project("a")
	e.ok(a, "entrar", "--vaga", "FIXER1", "--papel", "FIXER")
	code, m := e.run(a, "", "rodar", "--rotulo", "t", "--", filepath.Join(a, "nao-existe"))
	if code != 1 || m["codigo"] != float64(-1) {
		t.Fatalf("comando inexistente: %d %v", code, m)
	}
	if x := execucao(t, m); x["codigo"] != float64(-1) || x["fim"] == nil {
		t.Fatalf("execução = %v", x)
	}
}

func TestRodarSoltaRecursoNoFim(t *testing.T) {
	e := newEnv(t)
	a := e.project("a")
	withGPU(t, a)
	e.ok(a, "entrar", "--vaga", "FIXER1", "--papel", "FIXER")
	m := e.ok(a, "rodar", "--recurso", "gpu", "--tipo", "t", "--rotulo", "gate", "--", helper, "sleep", "1")
	if x := execucao(t, m); x["concessao"] == nil || x["codigo"] != float64(0) {
		t.Fatalf("execução = %v", x)
	}
	v := e.ok(a, "recurso", "ver", "gpu")["recurso"].(map[string]any)
	if len(v["ativas"].([]any)) != 0 || len(v["fila"].([]any)) != 0 {
		t.Fatalf("depois de rodar: %v", v)
	}
	// Se a concessão do rodar é solta no meio e a vaga pede de novo, o fim do
	// rodar solta só a dele: o pedido novo continua ativo.
	espera := e.start(a, "rodar", "--recurso", "gpu", "--tipo", "t", "--rotulo", "gate", "--", helper, "sleep", "3")
	deadline := time.Now().Add(5 * time.Second)
	for {
		v := e.ok(a, "recurso", "ver", "gpu")["recurso"].(map[string]any)
		if len(v["ativas"].([]any)) == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("o rodar não ganhou a concessão: %v", v)
		}
		time.Sleep(100 * time.Millisecond)
	}
	e.ok(a, "recurso", "soltar", "gpu")
	novo := e.ok(a, "recurso", "pedir", "gpu", "--exclusivo", "sim")
	if estadoConcessao(t, novo) != "ativa" {
		t.Fatalf("pedido novo: %v", novo)
	}
	espera.wait(t, 10*time.Second)
	v = e.ok(a, "recurso", "ver", "gpu")["recurso"].(map[string]any)
	ativas := v["ativas"].([]any)
	if len(ativas) != 1 || ativas[0].(map[string]any)["id"] != novo["concessao"].(map[string]any)["id"] {
		t.Fatalf("depois do fim do rodar: %v", v)
	}
	e.ok(a, "recurso", "soltar", "gpu")
	// Recurso fora do central.toml: recusa sem rodar nada.
	if code, m := e.run(a, "", "rodar", "--recurso", "tpu", "--rotulo", "gate", "--", helper, "exit", "0"); code != 4 || m["execucao"] != nil {
		t.Fatalf("recurso desconhecido: %d %v", code, m)
	}
}

func TestRodarGuardaFinalDoLog(t *testing.T) {
	e := newEnv(t)
	a := e.project("a")
	e.ok(a, "entrar", "--vaga", "FIXER1", "--papel", "FIXER")
	c := e.cmd(a, "", "rodar", "--rotulo", "t", "--", helper, "lines", "80")
	var out, errOut bytes.Buffer
	c.Stdout, c.Stderr = &out, &errOut
	if err := c.Run(); err != nil {
		t.Fatalf("rodar: %v\n%s", err, errOut.String())
	}
	if n := strings.Count(out.String(), "\n"); n != 1 {
		t.Fatalf("stdout com %d linhas: %q", n, out.String())
	}
	var linhas []string
	for i := 31; i <= 80; i++ {
		linhas = append(linhas, fmt.Sprintf("linha %d", i))
	}
	if got := execucao(t, parse(t, out.Bytes()))["final_log"]; got != strings.Join(linhas, "\n") {
		t.Fatalf("final_log = %q", got)
	}
	if !strings.Contains(errOut.String(), "linha 1\n") || !strings.Contains(errOut.String(), "linha 80") {
		t.Fatalf("stderr sem a saída do filho: %q", errOut.String())
	}
}

func TestFatoCLI(t *testing.T) {
	e := newEnv(t)
	a := e.project("a")
	e.ok(a, "entrar", "--vaga", "A1", "--papel", "FIXER")
	e.ok(a, "fato", "set", "e2e.base", `{"commit":"abc"}`, "--motivo", "x")
	f := e.ok(a, "fato", "get", "e2e.base")["fato"].(map[string]any)
	if v, _ := f["valor"].(map[string]any); v["commit"] != "abc" || f["vaga"] != "A1" || f["motivo"] != "x" {
		t.Fatalf("fato = %v", f)
	}
	e.ok(a, "fato", "set", "nota", "texto", "--motivo", "y")
	if f := e.ok(a, "fato", "get", "nota")["fato"].(map[string]any); f["valor"] != "texto" {
		t.Fatalf("nota = %v", f)
	}
	e.ok(a, "fato", "set", "nota", "novo", "--motivo", "z")
	if h := e.ok(a, "fato", "historico", "nota")["fatos"].([]any); len(h) != 2 || h[0].(map[string]any)["valor"] != "novo" {
		t.Fatalf("histórico = %v", h)
	}
	if fs := e.ok(a, "fatos")["fatos"].([]any); len(fs) != 2 {
		t.Fatalf("fatos = %v", fs)
	}
	if code, m := e.run(a, "", "fato", "get", "nada"); code != 3 || strings.Contains(fmt.Sprint(m), "rode central entrar") {
		t.Fatalf("fato inexistente: %d %v", code, m)
	}
	if code, m := e.run(a, "", "fato", "set", "Ruim", "1", "--motivo", "m"); code != 4 {
		t.Fatalf("chave inválida: %d %v", code, m)
	}
	if code, m := e.run(a, "", "fato", "set", "k", "1"); code != 4 {
		t.Fatalf("sem motivo: %d %v", code, m)
	}
	if m := e.ok(a, "id", "T", "--minimo", "115"); m["id"] != "T-115" {
		t.Fatalf("id = %v", m)
	}
	if m := e.ok(a, "id", "T"); m["id"] != "T-116" {
		t.Fatalf("id = %v", m)
	}
	if code, m := e.run(a, "", "id", "t"); code != 4 {
		t.Fatalf("prefixo inválido: %d %v", code, m)
	}
}

// withMeter acrescenta ao central.toml da pasta o recurso gpu com o medidor comando.
func withMeter(t *testing.T, dir, comando string) {
	t.Helper()
	f, err := os.OpenFile(filepath.Join(dir, config.FileName), os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := fmt.Fprintf(f, "\n[recursos.gpu]\ndimensoes = { vram_mb = 8192, uso_pct = 100 }\n"+
		"prioridades = [\"alta\"]\nmedidor = { comando = %q, mapa = [\"uso_pct\", \"vram_mb\"] }\n", comando); err != nil {
		t.Fatal(err)
	}
}

// verificacoes indexa as verificações do doctor pelo nome.
func verificacoes(t *testing.T, m map[string]any) map[string]map[string]any {
	t.Helper()
	vs, ok := m["verificacoes"].([]any)
	if !ok {
		t.Fatalf("sem verificacoes: %v", m)
	}
	out := map[string]map[string]any{}
	for _, v := range vs {
		vm := v.(map[string]any)
		out[vm["nome"].(string)] = vm
	}
	return out
}

func TestDoctorTudoOk(t *testing.T) {
	e := newEnv(t)
	a := e.project("a")
	withMeter(t, a, "echo 10, 2000")
	e.ok(a, "entrar", "--vaga", "A1", "--papel", "DEV")
	code, m := e.run(a, "", "doctor")
	if code != 0 || m["ok"] != true {
		t.Fatalf("doctor: %d %v", code, m)
	}
	vs := verificacoes(t, m)
	for _, nome := range []string{"daemon", "dados", "projeto", "medidor:gpu", "porta"} {
		if v, ok := vs[nome]; !ok || v["ok"] != true {
			t.Errorf("%s = %v", nome, v)
		}
	}
	if d, _ := vs["medidor:gpu"]["detalhe"].(string); !strings.Contains(d, "2000") {
		t.Errorf("detalhe do medidor sem a leitura: %q", d)
	}
}

func TestDoctorMedidorQuebrado(t *testing.T) {
	e := newEnv(t)
	a := e.project("a")
	withMeter(t, a, "exit 1")
	e.ok(a, "entrar", "--vaga", "A1", "--papel", "DEV")
	code, m := e.run(a, "", "doctor")
	if code != 1 || m["ok"] != false {
		t.Fatalf("doctor: %d %v", code, m)
	}
	vs := verificacoes(t, m)
	if vs["medidor:gpu"]["ok"] != false {
		t.Fatalf("medidor:gpu = %v", vs["medidor:gpu"])
	}
	for _, nome := range []string{"daemon", "dados", "projeto", "porta"} {
		if vs[nome]["ok"] != true {
			t.Errorf("%s = %v", nome, vs[nome])
		}
	}
}

// Sem daemon, o doctor não sobe um: diz que ele não roda e confere a porta.
func TestDoctorSemDaemon(t *testing.T) {
	e := newEnv(t)
	a := e.project("a")
	code, m := e.run(a, "", "doctor")
	vs := verificacoes(t, m)
	if code != 1 || vs["daemon"]["ok"] != false || vs["porta"]["ok"] != true {
		t.Fatalf("doctor sem daemon: %d %v", code, m)
	}
	if _, err := os.Stat(filepath.Join(e.home, daemon.InfoFile)); !os.IsNotExist(err) {
		t.Fatalf("o doctor subiu o daemon: %v", err)
	}
	// Outro processo na porta: o daemon não responde e a porta está ocupada.
	l, err := net.Listen("tcp", e.addr)
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: http.NotFoundHandler()}
	go srv.Serve(l)
	defer srv.Close()
	code, m = e.run(a, "", "doctor")
	vs = verificacoes(t, m)
	if code != 1 || vs["daemon"]["ok"] != false || vs["porta"]["ok"] != false {
		t.Fatalf("doctor com a porta ocupada: %d %v", code, m)
	}
}

// O medidor roda na raiz do projeto, no daemon e no doctor, mesmo com o
// comando chamado de uma subpasta: um caminho relativo se resolve a partir dela.
func TestMedidorRodaNaRaiz(t *testing.T) {
	e := newEnv(t)
	a := e.project("a")
	if err := os.WriteFile(filepath.Join(a, "leitura.txt"), []byte("10, 2000"), 0o644); err != nil {
		t.Fatal(err)
	}
	cat := "cat leitura.txt"
	if runtime.GOOS == "windows" {
		cat = "type leitura.txt"
	}
	withMeter(t, a, cat)
	sub := filepath.Join(a, "sub")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	e.ok(sub, "entrar", "--vaga", "A1", "--papel", "DEV")
	code, m := e.run(sub, "", "doctor")
	if v := verificacoes(t, m)["medidor:gpu"]; code != 0 || v["ok"] != true {
		t.Fatalf("doctor na subpasta: %d %v", code, m)
	}
	// O daemon (que sobe na pasta de dados) também lê o arquivo da raiz.
	deadline := time.Now().Add(10 * time.Second)
	for {
		v := e.ok(sub, "recurso", "ver", "gpu")["recurso"].(map[string]any)
		if u, ok := v["ultima"].(map[string]any); ok && strings.Contains(fmt.Sprint(u["valores"]), "2000") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("o daemon não leu o medidor: %v", v)
		}
		time.Sleep(200 * time.Millisecond)
	}
}
