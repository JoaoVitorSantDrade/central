package cli

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/JoaoVitorSantDrade/central/internal/config"
)

func TestCall503EVazioSaoRede(t *testing.T) {
	for _, h := range []http.HandlerFunc{
		func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusServiceUnavailable)
			w.Write([]byte(`{"ok":false,"erro":{"codigo":"daemon_reiniciando","mensagem":"x"}}`))
		},
		func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) },
	} {
		srv := httptest.NewServer(h)
		c := &client{base: srv.URL, http: srv.Client()}
		_, err := c.call("GET", "/x", nil)
		srv.Close()
		if !errors.Is(err, errNetwork) {
			t.Errorf("err = %v, quer errNetwork", err)
		}
	}
}

func TestDaemonCommandUsaPastaDeDados(t *testing.T) {
	cmd := daemonCommand(`C:\bin\central.exe`, `C:\dados`, true)
	if cmd.Dir != `C:\dados` {
		t.Fatalf("Dir = %q", cmd.Dir)
	}
	if last := cmd.Env[len(cmd.Env)-1]; last != `CENTRAL_HOME=C:\dados` {
		t.Fatalf("Env termina com %q", last)
	}
	if len(cmd.Args) != 2 || cmd.Args[1] != "daemon" {
		t.Fatalf("Args = %v", cmd.Args)
	}
}

func TestReadBody(t *testing.T) {
	got, err := readBody(strings.NewReader("\ufeffação\r\nlinha 2\r\n"))
	if err != nil || got != "ação\r\nlinha 2" {
		t.Fatalf("readBody = %q, %v", got, err)
	}
	_, err = readBody(strings.NewReader("a\xffb"))
	var ce *cliError
	if !errors.As(err, &ce) || ce.exit != ExitRecusado {
		t.Fatalf("UTF-8 inválido: %v", err)
	}
}

func TestEsperarRestoMinimo(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("servidor chamado: %s %s", r.Method, r.URL)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	c := &client{base: srv.URL, http: srv.Client()}
	m, err := wait(c, "/v1/projetos/p", "/a", 500*time.Microsecond)
	if err != nil {
		t.Fatal(err)
	}
	if m["motivo"] != "tempo" {
		t.Fatalf("wait = %v", m)
	}
}

func TestEnsureDaemonRecusaOutraPastaDeDados(t *testing.T) {
	minha, outra := t.TempDir(), t.TempDir()
	t.Setenv("CENTRAL_HOME", minha)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/saude" {
			t.Errorf("chamada inesperada: %s", r.URL)
		}
		json.NewEncoder(w).Encode(map[string]any{"ok": true, "api": "v1", "dados": outra, "pid": 1})
	}))
	defer srv.Close()
	c := &client{base: srv.URL, http: srv.Client()}
	err := c.ensureDaemon()
	var ce *cliError
	if !errors.As(err, &ce) || ce.exit != ExitIndisponivel || ce.codigo != "daemon_indisponivel" {
		t.Fatalf("err = %v", err)
	}
	if !strings.Contains(ce.mensagem, minha) || !strings.Contains(ce.mensagem, outra) {
		t.Fatalf("mensagem sem as duas pastas: %s", ce.mensagem)
	}
}

func TestEnsureDaemonAceitaMesmaPastaDeDados(t *testing.T) {
	minha := t.TempDir()
	t.Setenv("CENTRAL_HOME", minha)
	// O daemon informa a pasta com outra caixa (Windows) e uma barra no fim.
	informada := minha + string(filepath.Separator)
	if runtime.GOOS == "windows" {
		informada = strings.ToUpper(informada)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"ok": true, "api": "v1", "dados": informada, "pid": 1})
	}))
	defer srv.Close()
	c := &client{base: srv.URL, http: srv.Client()}
	if err := c.ensureDaemon(); err != nil {
		t.Fatal(err)
	}
}

func TestTailWriterGuardaAsUltimasLinhas(t *testing.T) {
	var all strings.Builder
	w := &tailWriter{w: &all, max: 3}
	for _, p := range []string{"um\r\ndo", "is\n", "três\nquatro\ncinco"} {
		if n, err := w.Write([]byte(p)); n != len(p) || err != nil {
			t.Fatalf("Write(%q) = %d, %v", p, n, err)
		}
	}
	if got := w.String(); got != "três\nquatro\ncinco" {
		t.Fatalf("String = %q", got)
	}
	if all.String() != "um\r\ndois\ntrês\nquatro\ncinco" {
		t.Fatalf("repassado = %q", all.String())
	}
	w.Write([]byte("\n"))
	if got := w.String(); got != "três\nquatro\ncinco" {
		t.Fatalf("depois da quebra final: %q", got)
	}
}

// TestHelperDorme não é um teste: é o filho que TestRunChildInterrompido roda.
func TestHelperDorme(t *testing.T) {
	if os.Getenv("CENTRAL_HELPER_DORME") != "1" {
		t.Skip("só como filho de TestRunChildInterrompido")
	}
	time.Sleep(30 * time.Second)
}

func TestRunChildInterrompido(t *testing.T) {
	t.Setenv("CENTRAL_HELPER_DORME", "1")
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(300*time.Millisecond, cancel)
	start := time.Now()
	var out strings.Builder
	code, err := runChild(ctx, []string{os.Args[0], "-test.run=^TestHelperDorme$"}, &tailWriter{w: &out, max: 5}, func() {})
	if err != nil {
		t.Fatal(err)
	}
	if d := time.Since(start); d > 15*time.Second {
		t.Fatalf("o filho levou %v para morrer", d)
	}
	if code == 0 {
		t.Fatalf("código do filho interrompido = 0")
	}
	if _, err := runChild(context.Background(), []string{filepath.Join(t.TempDir(), "nao-existe")}, &tailWriter{w: &out, max: 5}, func() {}); err == nil {
		t.Fatal("comando inexistente sem erro")
	}
}

func TestDoctorDadosAindaNaoCriados(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "central") // pai existe e aceita escrita; a pasta ainda não
	if v := checkDados(dir, nil); !v.OK {
		t.Fatalf("máquina nova reprovada: %+v", v)
	}
	if _, err := os.Stat(dir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("doctor criou a pasta de dados: %v", err)
	}
}

func TestComandosReservadosCobremACLI(t *testing.T) {
	nomes := []string{"versao", "daemon", "init", "painel", "rodar", "doctor", "mcp", "fato", "recurso",
		"entrar", "enviar", "responder", "pendentes", "caixa", "esperar", "status", "sair", "agentes", "trava", "soltar", "travas", "fatos", "id"}
	for k := range comandosProcesso {
		nome, _, _ := strings.Cut(k, " ")
		nomes = append(nomes, nome)
	}
	for k := range gruposProcesso {
		nomes = append(nomes, k)
	}
	for _, n := range nomes {
		if !slices.Contains(config.ComandosReservados, n) {
			t.Errorf("o comando %q falta em config.ComandosReservados", n)
		}
	}
}

func TestCompararVersoes(t *testing.T) {
	casos := []struct {
		a, b string
		quer int
	}{{"1.0.0", "1.0.0", 0}, {"1.0.0", "1.1.0", -1}, {"1.10.0", "1.9.3", 1}, {"2.0.0", "1.99.99", 1}, {"0.1.0", "1.0.0", -1}, {"x", "1.0.0", -1}}
	for _, c := range casos {
		if got := compararVersoes(c.a, c.b); got != c.quer {
			t.Errorf("compararVersoes(%s, %s) = %d, quer %d", c.a, c.b, got, c.quer)
		}
	}
}

func TestNaoMataPidQueNaoConfere(t *testing.T) {
	t.Setenv("CENTRAL_HOME", t.TempDir())
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/saude" {
			json.NewEncoder(w).Encode(map[string]any{"ok": true, "versao": "0.0.1", "pid": os.Getpid()})
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()
	c := &client{base: srv.URL, http: srv.Client()}
	err := c.encerrarDaemon(saudeInfo{Versao: "0.0.1", PID: os.Getpid()})
	if err == nil || !strings.Contains(err.Error(), "não confere") {
		t.Fatalf("err = %v (sem daemon.json que confirme, nada é encerrado)", err)
	}
}

func TestPathAcrescentaUmaVez(t *testing.T) {
	p := pathCom(`C:\a;C:\b`, `C:\Prog\central`, true)
	if p != `C:\a;C:\b;C:\Prog\central` {
		t.Fatalf("pathCom = %q", p)
	}
	if again := pathCom(p, `c:\prog\CENTRAL\`, true); again != p {
		t.Fatalf("duplicou (Windows ignora caixa e barra final): %q", again)
	}
	if got := pathCom("", "/home/x/.local/bin", false); got != "/home/x/.local/bin" {
		t.Fatalf("vazio = %q", got)
	}
	if got := pathCom("/usr/bin:/HOME/x", "/home/x", false); got != "/usr/bin:/HOME/x:/home/x" {
		t.Fatalf("unix diferencia caixa: %q", got)
	}
}

func TestPathRemove(t *testing.T) {
	if got := pathSem(`C:\a;C:\Prog\central\;C:\b;c:\prog\central`, `C:\Prog\central`, true); got != `C:\a;C:\b` {
		t.Fatalf("pathSem = %q", got)
	}
	if got := pathSem(`C:\a;;C:\b`, `C:\x`, true); got != `C:\a;;C:\b` {
		t.Fatalf("sem a pasta, nada muda: %q", got)
	}
}
