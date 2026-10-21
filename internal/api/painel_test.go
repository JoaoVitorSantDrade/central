package api

import (
	"bufio"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"
)

const configPainel = `[projeto]
id = "p"

[recursos.gpu]
dimensoes = { vram_mb = 8192 }
prioridades = ["executor"]
`

func entrarComConfig(t *testing.T, base, pasta, vaga, cfg string) {
	t.Helper()
	code, _, raw := do(t, "POST", base+"/v1/projetos/p/agentes", map[string]string{
		"nome": "P", "raiz": "/p", "pasta": pasta, "vaga": vaga, "papel": "X", "config": cfg})
	if code != 200 {
		t.Fatalf("entrar %d: %s", code, raw)
	}
}

func TestListaProjetosComResumo(t *testing.T) {
	srv := newServer(t)
	entrar(t, srv.URL, "/a", "A")
	entrar(t, srv.URL, "/b", "B")
	code, m, raw := do(t, "GET", srv.URL+"/v1/projetos", nil)
	if code != 200 {
		t.Fatalf("%d %s", code, raw)
	}
	ps := m["projetos"].([]any)
	if len(ps) != 1 {
		t.Fatalf("projetos = %s", raw)
	}
	p := ps[0].(map[string]any)
	if p["id"] != "p" || p["nome"] != "P" || p["raiz"] != "/p" || p["ultimo_acesso"] == "" {
		t.Fatalf("resumo = %s", raw)
	}
	vagas := p["vagas"].(map[string]any)
	if vagas["ativo"] != float64(2) {
		t.Fatalf("vagas por estado = %v", vagas)
	}
	if _, ok := p["fila_qa"]; !ok {
		t.Fatalf("sem fila_qa: %s", raw)
	}
	if _, ok := p["alertas"]; !ok {
		t.Fatalf("sem alertas: %s", raw)
	}
	if p["sprint"] != nil {
		t.Fatalf("sprint sem abrir = %v", p["sprint"])
	}
}

func TestHistoricoDeMensagens(t *testing.T) {
	srv := newServer(t)
	entrar(t, srv.URL, "/a", "A")
	entrar(t, srv.URL, "/b", "B")
	enviar(t, srv.URL, "/a", "B", "um")
	enviar(t, srv.URL, "/a", "B", "dois")
	code, _, raw := do(t, "POST", srv.URL+"/v1/projetos/p/mensagens", map[string]string{
		"pasta": "/b", "para": "A", "tipo": "pergunta", "corpo": "tres?", "responder": "30m"})
	if code != 200 {
		t.Fatalf("%d %s", code, raw)
	}
	code, m, raw := do(t, "GET", srv.URL+"/v1/projetos/p/mensagens?limite=2", nil)
	if code != 200 {
		t.Fatalf("%d %s", code, raw)
	}
	ms := m["mensagens"].([]any)
	if len(ms) != 2 || ms[0].(map[string]any)["corpo"] != "dois" || ms[1].(map[string]any)["corpo"] != "tres?" {
		t.Fatalf("mensagens = %s", raw)
	}
	pend := m["pendencias"].([]any)
	if len(pend) != 1 || pend[0].(map[string]any)["corpo"] != "tres?" {
		t.Fatalf("pendencias = %s", raw)
	}
	seq := ms[0].(map[string]any)["seq"].(float64)
	_, m, raw = do(t, "GET", srv.URL+"/v1/projetos/p/mensagens?depois="+itoa(int64(seq)), nil)
	if ms := m["mensagens"].([]any); len(ms) != 1 {
		t.Fatalf("depois = %s", raw)
	}
	code, _, _ = do(t, "GET", srv.URL+"/v1/projetos/p/mensagens?limite=x", nil)
	if code != 422 {
		t.Fatalf("limite inválido = %d", code)
	}
}

func TestEventosFiltrados(t *testing.T) {
	srv := newServer(t)
	entrar(t, srv.URL, "/a", "A")
	entrar(t, srv.URL, "/b", "B")
	enviar(t, srv.URL, "/a", "B", "um")
	code, m, raw := do(t, "GET", srv.URL+"/v1/projetos/p/eventos?prefixos=mensagem.", nil)
	if code != 200 {
		t.Fatalf("%d %s", code, raw)
	}
	evs := m["eventos"].([]any)
	if len(evs) != 1 || evs[0].(map[string]any)["tipo"] != "mensagem.enviada" {
		t.Fatalf("eventos = %s", raw)
	}
	_, m, raw = do(t, "GET", srv.URL+"/v1/projetos/p/eventos?limite=1", nil)
	if evs := m["eventos"].([]any); len(evs) != 1 {
		t.Fatalf("limite = %s", raw)
	}
	_, m, _ = do(t, "GET", srv.URL+"/v1/projetos/p/eventos", nil)
	todos := m["eventos"].([]any)
	ultimo := todos[len(todos)-1].(map[string]any)["seq"].(float64)
	_, m, raw = do(t, "GET", srv.URL+"/v1/projetos/p/eventos?depois="+itoa(int64(ultimo)), nil)
	if evs := m["eventos"].([]any); len(evs) != 0 {
		t.Fatalf("depois do último = %s", raw)
	}
	if m["ultimo"] != ultimo {
		t.Fatalf("ultimo = %v, quer %v", m["ultimo"], ultimo)
	}
	code, _, _ = do(t, "GET", srv.URL+"/v1/projetos/p/eventos?de=ontem", nil)
	if code != 422 {
		t.Fatalf("de inválido = %d", code)
	}
}

func TestAmostrasEConcessoes(t *testing.T) {
	srv := newServer(t)
	entrarComConfig(t, srv.URL, "/a", "A", configPainel)
	code, _, raw := do(t, "POST", srv.URL+"/v1/projetos/p/recursos/gpu/pedidos", map[string]any{
		"pasta": "/a", "declarado": map[string]float64{"vram_mb": 1000}})
	if code != 200 {
		t.Fatalf("pedir %d: %s", code, raw)
	}
	code, m, raw := do(t, "GET", srv.URL+"/v1/projetos/p/recursos", nil)
	if code != 200 {
		t.Fatalf("%d %s", code, raw)
	}
	rs := m["recursos"].(map[string]any)
	gpu, ok := rs["gpu"].(map[string]any)
	if !ok || len(gpu["ativas"].([]any)) != 1 {
		t.Fatalf("recursos = %s", raw)
	}
	code, m, raw = do(t, "GET", srv.URL+"/v1/projetos/p/recursos/gpu/amostras", nil)
	if code != 200 {
		t.Fatalf("%d %s", code, raw)
	}
	if _, ok := m["amostras"].([]any); !ok {
		t.Fatalf("amostras = %s", raw)
	}
	if cs := m["concessoes"].([]any); len(cs) != 1 {
		t.Fatalf("concessoes = %s", raw)
	}
	code, _, _ = do(t, "GET", srv.URL+"/v1/projetos/p/recursos/nada/amostras", nil)
	if code != 404 {
		t.Fatalf("recurso desconhecido = %d", code)
	}
}

func TestStreamMandaEventosNovos(t *testing.T) {
	srv := newServer(t)
	entrar(t, srv.URL, "/a", "A")
	entrar(t, srv.URL, "/b", "B")
	_, m, _ := do(t, "GET", srv.URL+"/v1/projetos/p/eventos", nil)
	evs := m["eventos"].([]any)
	ultimo := int64(evs[len(evs)-1].(map[string]any)["seq"].(float64))

	req, _ := http.NewRequest("GET", srv.URL+"/v1/projetos/p/stream?depois="+itoa(ultimo), nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Fatalf("content-type = %q", ct)
	}
	linhas := make(chan string, 64)
	go func() {
		sc := bufio.NewScanner(resp.Body)
		for sc.Scan() {
			linhas <- sc.Text()
		}
		close(linhas)
	}()
	inicio := time.Now()
	enviar(t, srv.URL, "/a", "B", "ao vivo")
	var id, dados string
	prazo := time.After(2 * time.Second)
	for dados == "" {
		select {
		case l, ok := <-linhas:
			if !ok {
				t.Fatal("stream fechou")
			}
			if strings.HasPrefix(l, "id: ") {
				id = strings.TrimPrefix(l, "id: ")
			}
			if strings.HasPrefix(l, "data: ") {
				dados = strings.TrimPrefix(l, "data: ")
			}
		case <-prazo:
			t.Fatal("evento não chegou em 2 s")
		}
	}
	if id != itoa(ultimo+1) || !strings.Contains(dados, `"mensagem.enviada"`) {
		t.Fatalf("id = %q, data = %s", id, dados)
	}
	t.Logf("chegou em %v", time.Since(inicio))
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }

func TestHumanoEnviaEAcorda(t *testing.T) {
	srv := newServer(t)
	entrar(t, srv.URL, "/a", "A")
	acordou := make(chan map[string]any, 1)
	go func() {
		_, m, _ := do(t, "GET", srv.URL+"/v1/projetos/p/esperar?pasta=/a&ate=10s", nil)
		acordou <- m
	}()
	time.Sleep(100 * time.Millisecond)
	code, m, raw := do(t, "POST", srv.URL+"/v1/projetos/p/humano/mensagens", map[string]string{
		"para": "A", "tipo": "nota", "corpo": "pare o T-1", "ref": "T-1"})
	if code != 200 {
		t.Fatalf("%d %s", code, raw)
	}
	msg := m["mensagem"].(map[string]any)
	if msg["de"] != "HUMANO" || msg["ref"] != "T-1" {
		t.Fatalf("mensagem = %s", raw)
	}
	select {
	case m := <-acordou:
		if m["motivo"] != "mensagem" {
			t.Fatalf("esperar = %v", m)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("A não acordou")
	}
	code, _, _ = do(t, "POST", srv.URL+"/v1/projetos/p/humano/mensagens", map[string]string{"para": "A", "tipo": "nota"})
	if code != 422 {
		t.Fatalf("sem corpo = %d", code)
	}
}

func TestHumanoReservado(t *testing.T) {
	srv := newServer(t)
	code, _, raw := do(t, "POST", srv.URL+"/v1/projetos/p/agentes", map[string]string{
		"nome": "P", "raiz": "/p", "pasta": "/h", "vaga": "HUMANO", "papel": "X"})
	if code != 422 || !strings.Contains(raw, "HUMANO") {
		t.Fatalf("entrar como HUMANO = %d %s", code, raw)
	}
}
