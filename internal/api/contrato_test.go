package api

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// golden compara got com testdata/<nome>; ATUALIZAR_GOLDEN=1 regrava.
func golden(t *testing.T, nome, got string) {
	t.Helper()
	p := filepath.Join("testdata", nome)
	if os.Getenv("ATUALIZAR_GOLDEN") != "" {
		os.MkdirAll("testdata", 0o755)
		if err := os.WriteFile(p, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("%v (rode com ATUALIZAR_GOLDEN=1 para criar)", err)
	}
	if w := strings.ReplaceAll(string(want), "\r\n", "\n"); got != w {
		t.Fatalf("o contrato /v1 mudou (%s). Quebrar /v1 exige /v2 (spec §10); se a mudança é aditiva, regrave com ATUALIZAR_GOLDEN=1.\n--- agora:\n%s\n--- golden:\n%s", nome, got, w)
	}
}

func TestRotasV1Golden(t *testing.T) {
	rs := Rotas()
	sort.Strings(rs)
	golden(t, "rotas-v1.golden.txt", strings.Join(rs, "\n")+"\n")
}

// formato descreve um valor JSON pelas chaves e tipos, sem os valores.
func formato(v any) any {
	switch x := v.(type) {
	case map[string]any:
		out := map[string]any{}
		for k, e := range x {
			out[k] = formato(e)
		}
		return out
	case []any:
		if len(x) == 0 {
			return []any{}
		}
		return []any{formato(x[0])}
	case string:
		return "texto"
	case float64:
		return "numero"
	case bool:
		return "booleano"
	case nil:
		return "nulo"
	}
	return fmt.Sprintf("%T", v)
}

func TestFormatoDasRespostasV1Golden(t *testing.T) {
	srv := newServer(t)
	resp := map[string]any{}
	pegar := func(nome, metodo, caminho string, corpo any) {
		t.Helper()
		_, m, _ := do(t, metodo, srv.URL+caminho, corpo)
		resp[nome] = formato(m)
	}
	pegar("saude", "GET", "/v1/saude", nil)
	pegar("entrar", "POST", "/v1/projetos/p/agentes", map[string]string{"nome": "P", "raiz": "/p", "pasta": "/a", "vaga": "A", "papel": "PLANEJADOR"})
	do(t, "POST", srv.URL+"/v1/projetos/p/agentes", map[string]string{"nome": "P", "raiz": "/p", "pasta": "/b", "vaga": "B", "papel": "EXECUTOR"})
	pegar("enviar", "POST", "/v1/projetos/p/mensagens", map[string]string{"pasta": "/a", "para": "B", "tipo": "nota", "corpo": "oi", "responder": "30m"})
	pegar("esperar", "GET", "/v1/projetos/p/esperar?pasta=/b&ate=1s", nil)
	pegar("caixa", "POST", "/v1/projetos/p/caixa", map[string]string{"pasta": "/b"})
	pegar("erro", "GET", "/v1/projetos/nada/agentes", nil)
	pegar("item_criar", "POST", "/v1/projetos/p/itens", map[string]any{"pasta": "/a", "titulo": "x", "area": "a", "prioridade": "P1", "criterio": "c"})
	pegar("proxima", "GET", "/v1/projetos/p/proxima?pasta=/b", nil)
	pegar("brief", "GET", "/v1/projetos/p/brief?pasta=/b", nil)
	pegar("projetos", "GET", "/v1/projetos", nil)
	b, _ := json.MarshalIndent(resp, "", "  ")
	golden(t, "respostas-v1.golden.json", string(b)+"\n")
}
