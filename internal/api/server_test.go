package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/JoaoVitorSantDrade/central/internal/service"
)

func newServer(t *testing.T) *httptest.Server {
	t.Helper()
	return newServerIn(t, t.TempDir())
}

func newServerIn(t *testing.T, dataDir string) *httptest.Server {
	t.Helper()
	s, err := service.New(dataDir, nil)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(NewHandler(s))
	t.Cleanup(func() { srv.Close(); s.Close() })
	return srv
}

func do(t *testing.T, method, url string, body any) (int, map[string]any, string) {
	t.Helper()
	var r io.Reader
	switch b := body.(type) {
	case nil:
	case string:
		r = strings.NewReader(b)
	default:
		buf, _ := json.Marshal(b)
		r = bytes.NewReader(buf)
	}
	req, _ := http.NewRequest(method, url, r)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("resposta não é JSON: %s", raw)
	}
	return resp.StatusCode, m, string(raw)
}

func entrar(t *testing.T, base, pasta, vaga string) {
	t.Helper()
	code, _, raw := do(t, "POST", base+"/v1/projetos/p/agentes", map[string]string{
		"nome": "P", "raiz": "/p", "pasta": pasta, "vaga": vaga, "papel": "X"})
	if code != 200 {
		t.Fatalf("entrar %d: %s", code, raw)
	}
}

func enviar(t *testing.T, base, pasta, para, corpo string) {
	t.Helper()
	code, _, raw := do(t, "POST", base+"/v1/projetos/p/mensagens", map[string]string{
		"pasta": pasta, "para": para, "tipo": "nota", "corpo": corpo})
	if code != 200 {
		t.Fatalf("enviar %d: %s", code, raw)
	}
}

func TestSaude(t *testing.T) {
	srv := newServer(t)
	code, m, _ := do(t, "GET", srv.URL+"/v1/saude", nil)
	if code != 200 || m["ok"] != true || m["api"] != "v1" {
		t.Fatalf("%d %v", code, m)
	}
}

func TestSaudeInformaDados(t *testing.T) {
	dir := t.TempDir()
	srv := newServerIn(t, dir)
	code, m, raw := do(t, "GET", srv.URL+"/v1/saude", nil)
	if code != 200 || m["dados"] != dir {
		t.Fatalf("%d %s, quer dados = %q", code, raw, dir)
	}
	if pid, _ := m["pid"].(float64); int(pid) != os.Getpid() {
		t.Fatalf("pid = %v, quer %d", m["pid"], os.Getpid())
	}
}

func TestFluxoEntrarEnviarCaixa(t *testing.T) {
	srv := newServer(t)
	entrar(t, srv.URL, "/a", "A")
	entrar(t, srv.URL, "/b", "B")
	enviar(t, srv.URL, "/a", "B", "olá")
	code, m, raw := do(t, "POST", srv.URL+"/v1/projetos/p/caixa", map[string]string{"pasta": "/b"})
	if code != 200 {
		t.Fatalf("%d %s", code, raw)
	}
	ms := m["mensagens"].([]any)
	if len(ms) != 1 || ms[0].(map[string]any)["corpo"] != "olá" {
		t.Fatalf("caixa = %s", raw)
	}
}

func TestEsperarLongPoll(t *testing.T) {
	srv := newServer(t)
	entrar(t, srv.URL, "/a", "A")
	entrar(t, srv.URL, "/b", "B")
	// O envio roda em outra goroutine: nada de t.Fatalf lá; o erro volta por canal.
	sentc := make(chan time.Time, 1)
	errc := make(chan error, 1)
	go func() {
		time.Sleep(200 * time.Millisecond)
		sentc <- time.Now()
		buf, _ := json.Marshal(map[string]string{"pasta": "/a", "para": "B", "tipo": "nota", "corpo": "acorda"})
		resp, err := http.Post(srv.URL+"/v1/projetos/p/mensagens", "application/json", bytes.NewReader(buf))
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode != 200 {
				err = fmt.Errorf("enviar: status %d", resp.StatusCode)
			}
		}
		errc <- err
	}()
	code, m, raw := do(t, "GET", srv.URL+"/v1/projetos/p/esperar?pasta=/b&ate=5s", nil)
	if err := <-errc; err != nil {
		t.Fatal(err)
	}
	if code != 200 || m["motivo"] != "mensagem" {
		t.Fatalf("%d %s", code, raw)
	}
	if d := time.Since(<-sentc); d > time.Second {
		t.Fatalf("acordou %v depois do envio", d)
	}
}

func TestErros(t *testing.T) {
	srv := newServer(t)
	entrar(t, srv.URL, "/a", "A")
	cases := []struct {
		nome, method, path string
		body               any
		status             int
		codigo             string
	}{
		{"pasta desconhecida", "POST", "/v1/projetos/p/caixa", map[string]string{"pasta": "/zz"}, 404, "nao_encontrado"},
		{"projeto desconhecido", "POST", "/v1/projetos/zz/caixa", map[string]string{"pasta": "/a"}, 404, "nao_encontrado"},
		{"corpo vazio", "POST", "/v1/projetos/p/mensagens", map[string]string{"pasta": "/a", "para": "B", "tipo": "nota"}, 422, "invalido"},
		{"json malformado", "POST", "/v1/projetos/p/mensagens", "{x", 422, "invalido"},
		{"ate grande", "GET", "/v1/projetos/p/esperar?pasta=/a&ate=2h", nil, 422, "invalido"},
		{"ate ruim", "GET", "/v1/projetos/p/esperar?pasta=/a&ate=xx", nil, 422, "invalido"},
	}
	for _, c := range cases {
		code, m, raw := do(t, c.method, srv.URL+c.path, c.body)
		if code != c.status {
			t.Errorf("%s: status %d, quer %d (%s)", c.nome, code, c.status, raw)
			continue
		}
		e, _ := m["erro"].(map[string]any)
		if m["ok"] != false || e == nil || e["codigo"] != c.codigo || e["mensagem"] == "" {
			t.Errorf("%s: corpo %s", c.nome, raw)
		}
	}
}

func TestCaixaVaziaEhLista(t *testing.T) {
	srv := newServer(t)
	entrar(t, srv.URL, "/a", "A")
	_, _, raw := do(t, "POST", srv.URL+"/v1/projetos/p/caixa", map[string]string{"pasta": "/a"})
	if !strings.Contains(raw, `"mensagens":[]`) {
		t.Fatalf("raw = %s", raw)
	}
}

func TestResponderEPendentes(t *testing.T) {
	srv := newServer(t)
	entrar(t, srv.URL, "/a", "A")
	entrar(t, srv.URL, "/b", "B")
	code, m, raw := do(t, "POST", srv.URL+"/v1/projetos/p/mensagens", map[string]string{
		"pasta": "/a", "para": "B", "tipo": "pergunta", "corpo": "ok?", "responder": "30m"})
	if code != 200 {
		t.Fatalf("enviar %d: %s", code, raw)
	}
	msg := m["mensagem"].(map[string]any)
	if msg["responder_ate"] == nil {
		t.Fatalf("sem responder_ate: %s", raw)
	}
	seq := msg["seq"].(float64)
	code, m, raw = do(t, "GET", srv.URL+"/v1/projetos/p/pendentes?pasta=/b", nil)
	if code != 200 || len(m["devo"].([]any)) != 1 || len(m["esperam"].([]any)) != 0 {
		t.Fatalf("pendentes %d: %s", code, raw)
	}
	code, m, raw = do(t, "POST", srv.URL+"/v1/projetos/p/responder", map[string]any{"pasta": "/b", "seq": seq, "corpo": "sim"})
	if code != 200 || m["mensagem"].(map[string]any)["responde_a"] != seq {
		t.Fatalf("responder %d: %s", code, raw)
	}
	code, m, raw = do(t, "GET", srv.URL+"/v1/projetos/p/pendentes?pasta=/a", nil)
	if code != 200 || len(m["esperam"].([]any)) != 0 {
		t.Fatalf("pendentes depois %d: %s", code, raw)
	}
	if code, _, raw := do(t, "POST", srv.URL+"/v1/projetos/p/responder", map[string]any{"pasta": "/a", "seq": seq, "corpo": "eu"}); code != 422 {
		t.Fatalf("remetente respondeu %d: %s", code, raw)
	}
	if code, _, raw := do(t, "POST", srv.URL+"/v1/projetos/p/mensagens", map[string]string{
		"pasta": "/a", "para": "B", "tipo": "nota", "corpo": "x", "responder": "-1m"}); code != 422 {
		t.Fatalf("prazo negativo %d: %s", code, raw)
	}
}
