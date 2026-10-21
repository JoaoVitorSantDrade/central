package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func freeAddr(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().String()
}

func waitHealthy(t *testing.T, addr string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := http.Get("http://" + addr + "/v1/saude")
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == 200 {
				return
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("daemon não respondeu em 2 s")
}

func TestRunServeSaude(t *testing.T) {
	dir, addr := t.TempDir(), freeAddr(t)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- Run(ctx, dir, addr) }()
	waitHealthy(t, addr)

	raw, err := os.ReadFile(filepath.Join(dir, InfoFile))
	if err != nil {
		t.Fatal(err)
	}
	var info Info
	if err := json.Unmarshal(raw, &info); err != nil {
		t.Fatal(err)
	}
	if info.PID != os.Getpid() || info.Addr != addr {
		t.Fatalf("info = %+v", info)
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run = %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Run não terminou")
	}
	if _, err := os.Stat(filepath.Join(dir, InfoFile)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("%s não foi apagado: %v", InfoFile, err)
	}
}

func TestSegundaInstanciaRecusada(t *testing.T) {
	dir := t.TempDir()
	addr := freeAddr(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- Run(ctx, dir, addr) }()
	waitHealthy(t, addr)
	if err := Run(context.Background(), dir, freeAddr(t)); !errors.Is(err, ErrAlreadyRunning) {
		t.Fatalf("err = %v", err)
	}
	cancel()
	<-done
}

func TestRecusaEnderecoNaoLoopback(t *testing.T) {
	for _, a := range []string{"0.0.0.0:0", ":0", "192.168.0.1:7420"} {
		if err := Run(context.Background(), t.TempDir(), a); !errors.Is(err, ErrNotLoopback) {
			t.Errorf("%s: err = %v", a, err)
		}
	}
}

func TestShutdownDevolve503ParaEsperar(t *testing.T) {
	dir, addr := t.TempDir(), freeAddr(t)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- Run(ctx, dir, addr) }()
	waitHealthy(t, addr)
	base := "http://" + addr + "/v1/projetos/p"
	resp, err := http.Post(base+"/agentes", "application/json",
		strings.NewReader(`{"nome":"P","raiz":"/p","pasta":"/b","vaga":"B","papel":"X"}`))
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("entrar: %v %v", resp, err)
	}
	resp.Body.Close()
	type result struct {
		status int
		body   string
	}
	got := make(chan result, 1)
	go func() {
		r, err := http.Get(base + "/esperar?pasta=/b&ate=30s")
		if err != nil {
			got <- result{-1, err.Error()}
			return
		}
		defer r.Body.Close()
		b, _ := io.ReadAll(r.Body)
		got <- result{r.StatusCode, string(b)}
	}()
	time.Sleep(300 * time.Millisecond)
	cancel()
	select {
	case r := <-got:
		if r.status != http.StatusServiceUnavailable || !strings.Contains(r.body, `"daemon_reiniciando"`) {
			t.Fatalf("esperar durante shutdown = %d %q", r.status, r.body)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("esperar não terminou com o shutdown")
	}
	<-done
}

func TestGiraLogGrande(t *testing.T) {
	dir, addr := t.TempDir(), freeAddr(t)
	logs := filepath.Join(dir, "logs")
	if err := os.MkdirAll(logs, 0o755); err != nil {
		t.Fatal(err)
	}
	base := filepath.Join(dir, LogFile)
	grande := make([]byte, LogMaxBytes+1)
	if err := os.WriteFile(base, grande, 0o644); err != nil {
		t.Fatal(err)
	}
	for i, txt := range []string{"um", "dois", "tres"} {
		if err := os.WriteFile(fmt.Sprintf("%s.%d", base, i+1), []byte(txt), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- Run(ctx, dir, addr) }()
	waitHealthy(t, addr)
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(base)
	if err != nil || st.Size() >= LogMaxBytes {
		t.Fatalf("daemon.log não foi girado: %v %v", st, err)
	}
	if st, err := os.Stat(base + ".1"); err != nil || st.Size() != LogMaxBytes+1 {
		t.Fatalf("daemon.log.1 = %v %v", st, err)
	}
	for i, want := range map[int]string{2: "um", 3: "dois"} {
		if b, err := os.ReadFile(fmt.Sprintf("%s.%d", base, i)); err != nil || string(b) != want {
			t.Fatalf("daemon.log.%d = %q %v, quer %q", i, b, err, want)
		}
	}
	if _, err := os.Stat(base + ".4"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("daemon.log.4 existe: %v", err)
	}
}

func TestLogPequenoNaoGira(t *testing.T) {
	dir := t.TempDir()
	base := filepath.Join(dir, "daemon.log")
	if err := os.WriteFile(base, []byte("pouco"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := rotateLog(base); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(base); string(b) != "pouco" {
		t.Fatalf("daemon.log = %q", b)
	}
	if _, err := os.Stat(base + ".1"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("daemon.log.1 existe: %v", err)
	}
}

// Uma falha ao girar não impede o daemon: ele continua no log atual e anota.
func TestGiraLogFalhaNaoImpede(t *testing.T) {
	old := renameFile
	renameFile = func(string, string) error { return errors.New("arquivo em uso") }
	defer func() { renameFile = old }()
	dir, addr := t.TempDir(), freeAddr(t)
	if err := os.MkdirAll(filepath.Join(dir, "logs"), 0o755); err != nil {
		t.Fatal(err)
	}
	base := filepath.Join(dir, LogFile)
	if err := os.WriteFile(base, make([]byte, LogMaxBytes+1), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- Run(ctx, dir, addr) }()
	waitHealthy(t, addr)
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(base)
	if err != nil {
		t.Fatal(err)
	}
	if len(b) <= LogMaxBytes+1 {
		t.Fatalf("daemon.log tem %d bytes: foi girado ou não recebeu o log novo", len(b))
	}
	if novo := string(b[LogMaxBytes+1:]); !strings.Contains(novo, "girar o log falhou") || !strings.Contains(novo, "arquivo em uso") {
		t.Fatalf("daemon.log não anotou a falha: %q", novo)
	}
}

// O daemon faz uma cópia ao subir, sem esperar a primeira hora.
func TestBackupAoSubir(t *testing.T) {
	dir, addr := t.TempDir(), freeAddr(t)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- Run(ctx, dir, addr) }()
	waitHealthy(t, addr)
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if files, _ := filepath.Glob(filepath.Join(dir, "backups", "registro-*.db")); len(files) != 1 {
		t.Fatalf("backups ao subir = %v, quer 1", files)
	}
}

func TestEncerrarPelaAPI(t *testing.T) {
	dir, addr := t.TempDir(), freeAddr(t)
	done := make(chan error, 1)
	go func() { done <- Run(context.Background(), dir, addr) }()
	waitHealthy(t, addr)
	resp, err := http.Post("http://"+addr+"/v1/daemon/encerrar", "application/json", strings.NewReader("{}"))
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || !strings.Contains(string(b), `"ok":true`) {
		t.Fatalf("encerrar = %d %s", resp.StatusCode, b)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run = %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("o daemon não encerrou")
	}
	if _, err := os.Stat(filepath.Join(dir, InfoFile)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("daemon.json ficou: %v", err)
	}
}
