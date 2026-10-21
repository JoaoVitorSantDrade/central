package cli_test

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestPainelSoURL(t *testing.T) {
	e := newEnv(t)
	m := e.ok(t.TempDir(), "painel", "--so-url", "sim")
	url, _ := m["url"].(string)
	if url != "http://"+e.addr+"/" {
		t.Fatalf("url = %q, quer http://%s/", url, e.addr)
	}
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("o painel deveria estar no ar: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 || !strings.Contains(string(body), `id="app"`) {
		t.Fatalf("painel = %d %.80s", resp.StatusCode, body)
	}
}
