package web

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func api() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "api "+r.URL.Path) })
}

func get(t *testing.T, h http.Handler, method, url string, hdr map[string]string) (int, string, string) {
	t.Helper()
	req := httptest.NewRequest(method, url, nil)
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec.Code, rec.Header().Get("Content-Type"), rec.Body.String()
}

func TestPainelServidoEmbutido(t *testing.T) {
	h := Handler(api())
	code, ct, body := get(t, h, "GET", "http://127.0.0.1:7420/", nil)
	if code != 200 || !strings.HasPrefix(ct, "text/html") || !strings.Contains(body, `id="app"`) {
		t.Fatalf("/ = %d %q %.80s", code, ct, body)
	}
	code, ct, _ = get(t, h, "GET", "http://127.0.0.1:7420/static/app.js", nil)
	if code != 200 || !strings.Contains(ct, "javascript") {
		t.Fatalf("app.js = %d %q", code, ct)
	}
	code, _, _ = get(t, h, "GET", "http://127.0.0.1:7420/static/nada.js", nil)
	if code != 404 {
		t.Fatalf("desconhecido = %d", code)
	}
	code, _, body = get(t, h, "GET", "http://127.0.0.1:7420/v1/saude", nil)
	if code != 200 || body != "api /v1/saude" {
		t.Fatalf("/v1 = %d %q", code, body)
	}
}

func TestPainelRecusaHostEOrigemDeFora(t *testing.T) {
	h := Handler(api())
	if code, _, _ := get(t, h, "GET", "http://malicioso.example:7420/v1/projetos", nil); code != 403 {
		t.Fatalf("host de fora = %d", code)
	}
	if code, _, _ := get(t, h, "GET", "http://localhost:7420/", nil); code != 200 {
		t.Fatalf("localhost = %d", code)
	}
	if code, _, _ := get(t, h, "POST", "http://127.0.0.1:7420/v1/projetos/p/mensagens", map[string]string{"Origin": "https://malicioso.example"}); code != 403 {
		t.Fatalf("origem de fora = %d", code)
	}
	if code, _, _ := get(t, h, "POST", "http://127.0.0.1:7420/v1/projetos/p/mensagens", map[string]string{"Origin": "http://127.0.0.1:7420"}); code != 200 {
		t.Fatalf("mesma origem = %d", code)
	}
	if code, _, _ := get(t, h, "POST", "http://127.0.0.1:7420/v1/projetos/p/mensagens", nil); code != 200 {
		t.Fatalf("CLI sem Origin = %d", code)
	}
}
