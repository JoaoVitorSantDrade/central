package cli_test

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/JoaoVitorSantDrade/central/internal/config"
)

// withMeterFile acrescenta ao central.toml da pasta o recurso gpu com um
// medidor que só ecoa o conteúdo de leitura (caminho absoluto), lido a cada 1 s.
func withMeterFile(t *testing.T, dir, leitura string) {
	t.Helper()
	comando := fmt.Sprintf("cat '%s'", leitura)
	if runtime.GOOS == "windows" {
		comando = fmt.Sprintf("type \"%s\"", leitura)
	}
	f, err := os.OpenFile(filepath.Join(dir, config.FileName), os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := fmt.Fprintf(f, "\n[recursos.gpu]\ndimensoes = { vram_mb = 8192, uso_pct = 100 }\n"+
		"reservado = { vram_mb = 500, uso_pct = 5 }\nprioridades = [\"alta\"]\n"+
		"medidor = { comando = %q, mapa = [\"uso_pct\", \"vram_mb\"], intervalo = \"1s\" }\n", comando); err != nil {
		t.Fatal(err)
	}
}

// mensagemDaCentral devolve a primeira mensagem da caixa vinda de CENTRAL com o tipo dado.
func mensagemDaCentral(m map[string]any, tipo string) map[string]any {
	for _, x := range m["mensagens"].([]any) {
		if msg := x.(map[string]any); msg["de"] == "CENTRAL" && msg["tipo"] == tipo {
			return msg
		}
	}
	return nil
}

// TestCenarioM1a percorre o M1a de ponta a ponta com o binário e o daemon
// reais: travas, recurso com medidor, rodar, esperar acordado pela concessão,
// fatos, prazo de resposta vencido pelo Tick do daemon, agentes e doctor.
func TestCenarioM1a(t *testing.T) {
	e := newEnv(t)
	a, b := e.project("a"), e.project("b")
	leitura := filepath.Join(t.TempDir(), "leitura.txt")
	if err := os.WriteFile(leitura, []byte("10, 2000\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	withMeterFile(t, a, leitura)
	withMeterFile(t, b, leitura)

	// 1. As duas vagas entram.
	e.ok(a, "entrar", "--vaga", "FIXER1", "--papel", "FIXER")
	e.ok(b, "entrar", "--vaga", "FIXER2", "--papel", "FIXER")

	// 2. Trava: a segunda vaga recebe conflito (2) com o dono.
	e.ok(a, "trava", "package.json", "--motivo", "atualizando dependências")
	if code, m := e.run(b, "", "trava", "package.json", "--motivo", "eu também"); code != 2 || !strings.Contains(fmt.Sprint(m), "FIXER1") {
		t.Fatalf("trava de FIXER2: %d %v", code, m)
	}

	// 3. FIXER1 roda com a GPU; FIXER2 pede exclusiva e só ganha depois do fim do rodar.
	inicio := time.Now()
	rodar := e.start(a, "rodar", "--recurso", "gpu", "--tipo", "t", "--rotulo", "gate", "--", helper, "sleep", "2")
	deadline := time.Now().Add(10 * time.Second)
	for {
		v := e.ok(b, "recurso", "ver", "gpu")["recurso"].(map[string]any)
		if len(v["ativas"].([]any)) == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("o rodar não ganhou a concessão: %v", v)
		}
		time.Sleep(100 * time.Millisecond)
	}
	pedir := e.start(b, "recurso", "pedir", "gpu", "--exclusivo", "sim", "--motivo", "gate", "--esperar", "30s")
	m := pedir.wait(t, 40*time.Second)
	if estadoConcessao(t, m) != "ativa" {
		t.Fatalf("pedido de FIXER2: %v", m)
	}
	if d := pedir.end.Sub(inicio); d < 1500*time.Millisecond {
		t.Fatalf("FIXER2 ganhou a GPU %v depois do início do rodar (antes do fim do sleep 2)", d)
	}
	if x := execucao(t, rodar.wait(t, 10*time.Second)); x["codigo"] != float64(0) || x["concessao"] == nil {
		t.Fatalf("execução do rodar: %v", x)
	}

	// 4. A concessão chegou como mensagem da CENTRAL: esperar acorda com motivo recurso.
	w := e.ok(b, "esperar", "--ate", "5s")
	if w["motivo"] != "recurso" || w["primeira"].(map[string]any)["tipo"] != "recurso.concedida" {
		t.Fatalf("esperar de FIXER2: %v", w)
	}
	e.ok(b, "caixa") // lê a concessão
	e.ok(b, "recurso", "soltar", "gpu")

	// 5. Fatos.
	e.ok(a, "fato", "set", "e2e.base", `{"commit":"abc123"}`, "--motivo", "gate verde")
	f := e.ok(b, "fato", "get", "e2e.base")["fato"].(map[string]any)
	if v, _ := f["valor"].(map[string]any); v["commit"] != "abc123" || f["vaga"] != "FIXER1" {
		t.Fatalf("fato = %v", f)
	}

	// 6. Resposta com prazo: o Tick do daemon (não uma chamada direta) avisa o destino.
	enviada := e.ok(a, "enviar", "FIXER2", "pergunta", "--responder", "1s", "pode revisar?")
	seq := enviada["mensagem"].(map[string]any)["seq"]
	if p := e.ok(a, "pendentes"); len(p["esperam"].([]any)) != 1 {
		t.Fatalf("pendentes de FIXER1 = %v", p)
	}
	if p := e.ok(b, "pendentes"); len(p["devo"].([]any)) != 1 {
		t.Fatalf("pendentes de FIXER2 = %v", p)
	}
	time.Sleep(3 * time.Second)
	var aviso map[string]any
	// 3 s bastam (prazo 1 s + Tick de 2 s); a folga é para a máquina carregada.
	deadline = time.Now().Add(5 * time.Second)
	for aviso == nil {
		aviso = mensagemDaCentral(e.ok(b, "caixa"), "resposta.prazo")
		if aviso == nil {
			if time.Now().After(deadline) {
				t.Fatal("FIXER2 não recebeu resposta.prazo")
			}
			time.Sleep(200 * time.Millisecond)
		}
	}
	if aviso["ref"] != fmt.Sprint(seq) || aviso["para"] != "FIXER2" {
		t.Fatalf("aviso = %v", aviso)
	}

	// 7. As duas vagas seguem ativas.
	ags := e.ok(a, "agentes")
	for _, vaga := range []string{"FIXER1", "FIXER2"} {
		if ag := agenteNaLista(t, ags, vaga); ag["estado"] != "ativo" {
			t.Fatalf("%s = %v", vaga, ag)
		}
	}

	// 8. Doctor ok, com o medidor lendo o arquivo reescrito.
	if err := os.WriteFile(leitura, []byte("20, 3000\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	code, d := e.run(a, "", "doctor")
	if code != 0 || d["ok"] != true {
		t.Fatalf("doctor: %d %v", code, d)
	}
	if det, _ := verificacoes(t, d)["medidor:gpu"]["detalhe"].(string); !strings.Contains(det, "3000") {
		t.Fatalf("detalhe do medidor = %q", det)
	}
}
