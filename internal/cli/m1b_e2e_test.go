package cli_test

import (
	"path/filepath"

	"fmt"
	"github.com/JoaoVitorSantDrade/central/internal/adapters/lockdir"
	"testing"
	"time"
)

// expedienteDeAgora devolve um expediente que começou há 1 min e dura 3 h, no fuso local.
func expedienteDeAgora() string {
	agora := time.Now()
	return fmt.Sprintf("%s-%s", agora.Add(-time.Minute).Format("15:04"), agora.Add(3*time.Hour).Format("15:04"))
}

func TestSprintCLI(t *testing.T) {
	e := newEnv(t)
	a, b := e.project("a"), e.project("b")
	e.ok(a, "entrar", "--vaga", "G", "--papel", "GERENTE")
	e.ok(b, "entrar", "--vaga", "F1", "--papel", "EXECUTOR")
	if code, _ := e.run(b, "", "sprint", "abrir", "--expediente", expedienteDeAgora()); code != 4 {
		t.Fatalf("executor abriu sprint: saída %d", code)
	}
	m := e.ok(a, "sprint", "abrir", "--expediente", expedienteDeAgora())
	id := m["sprint"].(map[string]any)["id"].(string)
	if v := e.ok(b, "sprint", "ver"); v["sprint"].(map[string]any)["id"] != id {
		t.Fatalf("sprint ver = %v", v)
	}
	// o ticker do daemon dispara a abertura e acorda o executor
	w := e.ok(b, "esperar", "--ate", "15s")
	if w["motivo"] != "cerimonia" {
		t.Fatalf("esperar = %v", w)
	}
	if code, _ := e.run(a, "", "sprint", "abrir", "--expediente", expedienteDeAgora()); code != 2 {
		t.Fatalf("segunda sprint aberta: saída %d", code)
	}
	e.ok(a, "sprint", "encerrar")
	if v := e.ok(b, "sprint", "ver"); v["sprint"] != nil {
		t.Fatalf("sprint segue aberta: %v", v)
	}
}

func TestItemCLI(t *testing.T) {
	e := newEnv(t)
	vagas := map[string]string{"P": "PLANEJADOR", "F1": "EXECUTOR", "R": "REVISOR", "I": "INTEGRADOR"}
	dirs := map[string]string{}
	for v, papel := range vagas {
		dirs[v] = e.project(v)
		e.ok(dirs[v], "entrar", "--vaga", v, "--papel", papel)
	}
	m := e.ok(dirs["P"], "item", "criar", "--titulo", "corrigir travas", "--area", "core", "--prioridade", "P1",
		"--arquivos", "internal/core/locks.go", "--criterio", "teste passa")
	id := m["item"].(map[string]any)["id"].(string)
	e.ok(dirs["P"], "item", "pronto", id)
	if code, _ := e.run(dirs["R"], "", "reservar", id); code != 4 {
		t.Fatalf("revisor reservou: %d", code)
	}
	e.ok(dirs["F1"], "reservar", id)
	e.ok(dirs["F1"], "concluir", id, "--ponta", "abc123")
	if w := e.ok(dirs["R"], "esperar", "--ate", "5s"); w["motivo"] != "item" {
		t.Fatalf("revisor esperar = %v", w)
	}
	e.ok(dirs["R"], "revisar", id)
	if code, _ := e.run(dirs["R"], "", "aprovar", id, "--ponta", "outra"); code != 2 {
		t.Fatalf("aprovou ponta errada: %d", code)
	}
	e.ok(dirs["R"], "aprovar", id, "--ponta", "abc123")
	e.ok(dirs["I"], "integrar", id, "--commit", "deadbeef")
	v := e.ok(dirs["P"], "item", "ver", id)
	if it := v["item"].(map[string]any); it["estado"] != "integrada" || it["commit"] != "deadbeef" {
		t.Fatalf("item = %v", it)
	}
	l := e.ok(dirs["P"], "item", "listar", "--estado", "integrada")
	if n := len(l["itens"].([]any)); n != 1 {
		t.Fatalf("listar = %v", l)
	}
	e.ok(dirs["P"], "item", "criar", "--titulo", "outra", "--id", "T-900", "--area", "a", "--prioridade", "P2", "--criterio", "x")
	e.ok(dirs["P"], "item", "pronto", "T-900")
	e.ok(dirs["F1"], "reservar", "T-900")
	e.ok(dirs["F1"], "bloquear", "T-900", "não reproduz na develop")
}

func TestProximaCLI(t *testing.T) {
	e := newEnv(t)
	p, f := e.project("p"), e.project("f")
	e.ok(p, "entrar", "--vaga", "P", "--papel", "PLANEJADOR")
	e.ok(f, "entrar", "--vaga", "F1", "--papel", "EXECUTOR")
	if m := e.ok(f, "proxima"); m["item"] != nil || m["motivo"] == "" {
		t.Fatalf("fila vazia: %v", m)
	}
	e.ok(p, "item", "criar", "--id", "T-001", "--titulo", "a", "--area", "x", "--prioridade", "P1", "--criterio", "c")
	e.ok(p, "item", "pronto", "T-001")
	m := e.ok(f, "proxima")
	if it, _ := m["item"].(map[string]any); it == nil || it["id"] != "T-001" || m["sugestao"] != "central reservar T-001" {
		t.Fatalf("proxima = %v", m)
	}
}

func TestDecisaoCLI(t *testing.T) {
	e := newEnv(t)
	p, g := e.project("p"), e.project("g")
	e.ok(p, "entrar", "--vaga", "P", "--papel", "PLANEJADOR")
	e.ok(g, "entrar", "--vaga", "G", "--papel", "GERENTE")
	m := e.ok(p, "decisao", "abrir", "--titulo", "ligar X", "contexto e riscos")
	id := m["decisao"].(map[string]any)["id"].(string)
	if w := e.ok(g, "esperar", "--ate", "5s"); w["motivo"] != "decisao" {
		t.Fatalf("gerente esperar = %v", w)
	}
	if code, _ := e.run(g, "", "decisao", "chave", id); code != 4 {
		t.Fatalf("chave sem acordo/objeção: %d", code)
	}
	if d := e.ok(g, "decisao", "chave", id, "--acordo", "sim")["decisao"].(map[string]any); d["estado"] != "aprovada" {
		t.Fatalf("decisao = %v", d)
	}
	if d := e.ok(p, "decisao", "veto", id, "muda o rumo")["decisao"].(map[string]any); d["estado"] != "vetada" {
		t.Fatalf("veto = %v", d)
	}
	if l := e.ok(p, "decisao", "listar"); len(l["itens"].([]any)) != 1 {
		t.Fatalf("listar = %v", l)
	}
}

func TestSaudeMetricasCLI(t *testing.T) {
	e := newEnv(t)
	g := e.project("g")
	e.ok(g, "entrar", "--vaga", "G", "--papel", "GERENTE")
	if s := e.ok(g, "saude")["saude"].(map[string]any); len(s["alertas"].([]any)) != 0 {
		t.Fatalf("saude = %v", s)
	}
	if m := e.ok(g, "metricas"); m["metricas"] == nil || m["resumo"] == "" {
		t.Fatalf("metricas = %v", m)
	}
	if o := e.ok(g, "orfas"); len(o["itens"].([]any)) != 0 {
		t.Fatalf("orfas = %v", o)
	}
	e.ok(g, "orfas", "--liberar", "sim")
}

func TestImportarCLI(t *testing.T) {
	e := newEnv(t)
	g := e.project("g")
	e.ok(g, "entrar", "--vaga", "G", "--papel", "GERENTE")
	dir := filepath.Join(t.TempDir(), "main", ".git", "lyriccut-agents")
	snap := lockdir.Snapshot{
		Mensagens: []lockdir.Mensagem{{ID: "20261020T205624-FIXER2-REVISOR1", De: "FIXER2", Para: "REVISOR1", Assunto: "alerta", Corpo: "oi"}},
		Pontos:    []lockdir.Ponto{{ID: "wt", Vaga: "FIXER2", Papel: "EXECUTOR", Estado: "ocioso", Atualizado: "2026-10-20T20:56"}},
		Reservas:  []lockdir.Reserva{{Tarefa: "T-114", Agente: "wt", Estado: "em_andamento"}},
	}
	if err := lockdir.Export(dir, snap); err != nil {
		t.Fatal(err)
	}
	r := e.ok(g, "importar-locks", dir)["resumo"].(map[string]any)
	if r["mensagens"].(float64) != 1 || r["itens"].(float64) != 1 || r["agentes"].(float64) != 1 {
		t.Fatalf("resumo = %v", r)
	}
	out := filepath.Join(t.TempDir(), "volta")
	if n := e.ok(g, "exportar-locks", out)["resumo"].(map[string]any); n["reservas"].(float64) != 1 {
		t.Fatalf("exportou %v", n)
	}
	if code, _ := e.run(g, "", "importar-locks", filepath.Join(t.TempDir(), "nao-existe")); code != 4 {
		t.Fatalf("pasta inexistente: %d", code)
	}
}
