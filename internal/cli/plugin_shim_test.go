package cli_test

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
)

func TestShimDoPluginInstalaNaPrimeiraVez(t *testing.T) {
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("sem sh no PATH")
	}
	e := newEnv(t)
	conteudo, _ := os.ReadFile(bin)
	nome := fmt.Sprintf("central-%s-%s", runtime.GOOS, runtime.GOARCH)
	if runtime.GOOS == "windows" {
		nome += ".exe"
	}
	h := sha256.Sum256(conteudo)
	var downloads atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/" + nome:
			downloads.Add(1)
			w.Write(conteudo)
		case "/SHA256SUMS":
			fmt.Fprintf(w, "%s  %s\n", hex.EncodeToString(h[:]), nome)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	destino := filepath.Join(t.TempDir(), "Programs", "central")
	shim, _ := filepath.Abs(filepath.Join(pluginDir, "bin", "central"))
	rodar := func() string {
		c := exec.Command(sh, shim, "versao")
		c.Env = append(e.environ(), "CENTRAL_INSTALAR_EM="+destino, "CENTRAL_TESTE_PATH="+filepath.Join(t.TempDir(), "path.txt"),
			"CENTRAL_RELEASE_BASE="+srv.URL)
		var errb strings.Builder
		c.Stderr = &errb
		out, err := c.Output()
		if err != nil {
			t.Fatalf("shim: %v\nstderr: %s", err, errb.String())
		}
		return strings.TrimSpace(string(out))
	}
	out := rodar()
	if !strings.HasPrefix(out, `{"api":"v1"`) || strings.Count(out, "\n") != 0 {
		t.Fatalf("o stdout do shim tem que ser só a saída da Central: %q", out)
	}
	alvo := filepath.Join(destino, "central")
	if runtime.GOOS == "windows" {
		alvo += ".exe"
	}
	if _, err := os.Stat(alvo); err != nil {
		t.Fatalf("o shim não instalou em %s: %v", alvo, err)
	}
	rodar()
	if n := downloads.Load(); n != 1 {
		t.Fatalf("baixou %d vezes; a segunda chamada devia usar o instalado", n)
	}
}
