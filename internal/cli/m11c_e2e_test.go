package cli_test

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// releaseFalsa serve a API de releases do GitHub com um binário e o SHA256SUMS.
func releaseFalsa(t *testing.T, versao, binario string, somaErrada bool) *httptest.Server {
	t.Helper()
	conteudo, err := os.ReadFile(binario)
	if err != nil {
		t.Fatal(err)
	}
	nome := fmt.Sprintf("central-%s-%s", runtime.GOOS, runtime.GOARCH)
	if runtime.GOOS == "windows" {
		nome += ".exe"
	}
	h := sha256.Sum256(conteudo)
	soma := hex.EncodeToString(h[:])
	if somaErrada {
		soma = strings.Repeat("0", 64)
	}
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/JoaoVitorSantDrade/central/releases/latest":
			json.NewEncoder(w).Encode(map[string]any{"tag_name": "v" + versao, "assets": []map[string]string{
				{"name": nome, "browser_download_url": srv.URL + "/dl/" + nome},
				{"name": "SHA256SUMS", "browser_download_url": srv.URL + "/dl/SHA256SUMS"},
			}})
		case "/dl/" + nome:
			w.Write(conteudo)
		case "/dl/SHA256SUMS":
			io.WriteString(w, soma+"  "+nome+"\n")
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// copiaDaCLI copia o binário de teste para uma pasta própria (o atualizar se substitui).
func copiaDaCLI(t *testing.T) string {
	t.Helper()
	dst := filepath.Join(t.TempDir(), filepath.Base(bin))
	b, err := os.ReadFile(bin)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dst, b, 0o755); err != nil {
		t.Fatal(err)
	}
	return dst
}

func versaoDe(t *testing.T, binario string) string {
	t.Helper()
	out, err := exec.Command(binario, "versao").Output()
	if err != nil {
		t.Fatal(err)
	}
	var m struct{ Versao string }
	json.Unmarshal(out, &m)
	return m.Versao
}

func TestAtualizarDeUmaReleaseFalsa(t *testing.T) {
	e := newEnv(t)
	srv := releaseFalsa(t, "9.9.9", binVersao(t, "9.9.9"), false)
	t.Setenv("CENTRAL_RELEASES", srv.URL)
	cli := copiaDaCLI(t)
	m := e.rodarCom(cli, t.TempDir(), "atualizar")
	if m["atualizado"] != true || m["para"] != "9.9.9" {
		t.Fatalf("atualizar = %v", m)
	}
	if v := versaoDe(t, cli); v != "9.9.9" {
		t.Fatalf("o binário continuou %s", v)
	}
	// Já na última: nada muda (o .antigo da vez anterior é limpo).
	m = e.rodarCom(cli, t.TempDir(), "atualizar")
	if m["atualizado"] != false {
		t.Fatalf("segunda vez = %v", m)
	}
	if _, err := os.Stat(cli + ".antigo"); !os.IsNotExist(err) {
		t.Fatalf("o .antigo ficou: %v", err)
	}
}

func TestAtualizarRecusaSomaErrada(t *testing.T) {
	e := newEnv(t)
	srv := releaseFalsa(t, "9.9.9", binVersao(t, "9.9.9"), true)
	t.Setenv("CENTRAL_RELEASES", srv.URL)
	cli := copiaDaCLI(t)
	c := exec.Command(cli, "atualizar")
	c.Env = e.environ()
	out, err := c.Output()
	if err == nil || !strings.Contains(string(out), "SHA256") {
		t.Fatalf("soma errada devia falhar: %s %v", out, err)
	}
	if v := versaoDe(t, cli); v == "9.9.9" {
		t.Fatal("trocou o binário mesmo com a soma errada")
	}
}
