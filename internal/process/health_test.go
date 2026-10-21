package process

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/JoaoVitorSantDrade/central/internal/core"
)

// vivos renova o batimento de todas as vagas, menos as dadas.
func (env *testEnv) vivos(menos ...string) {
	env.t.Helper()
	fora := map[string]bool{}
	for _, v := range menos {
		fora[v] = true
	}
	for _, v := range []string{"G", "P", "F1", "F2", "R", "I", "V"} {
		if !fora[v] {
			if err := env.c.Touch(ctx, v); err != nil {
				env.t.Fatal(err)
			}
		}
	}
}

func TestSaudeMostraParadosESumidos(t *testing.T) {
	env := timeEnv(t, NewRoles(nil, nil, nil))
	env.pronta("T-001")
	env.acao("F1", AcaoReservar, "T-001", Args{})
	env.acao("F1", AcaoConcluir, "T-001", Args{Ponta: "a"})
	env.advance(35 * time.Minute) // passa do limite de concluida (30 min) e do sumido (30 min)
	env.vivos("F2")
	s, err := env.e.Saude(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Parados) != 1 || s.Parados[0].Item != "T-001" || s.FilaQA != 1 {
		t.Fatalf("parados = %+v fila %d", s.Parados, s.FilaQA)
	}
	if len(s.Sumidos) != 1 || s.Sumidos[0].Vaga != "F2" {
		t.Fatalf("sumidos = %+v", s.Sumidos)
	}
	if len(s.Alertas) < 2 {
		t.Fatalf("alertas = %v", s.Alertas)
	}
}

func TestSaudeLimpa(t *testing.T) {
	env := timeEnv(t, NewRoles(nil, nil, nil))
	s, err := env.e.Saude(ctx)
	if err != nil || len(s.Alertas) != 0 {
		t.Fatalf("saúde = %+v, %v", s, err)
	}
}

func TestOrfasDepoisDeDozeHoras(t *testing.T) {
	env := timeEnv(t, NewRoles(nil, nil, nil))
	env.pronta("T-001")
	env.pronta("T-002")
	env.acao("F1", AcaoReservar, "T-001", Args{})
	env.acao("F2", AcaoReservar, "T-002", Args{})
	env.advance(11 * time.Hour)
	env.vivos("F1")
	if os, _ := env.e.Orfas(ctx); len(os) != 0 {
		t.Fatalf("órfãs com 11 h: %+v", os)
	}
	env.advance(2 * time.Hour)
	env.vivos("F1")
	os, err := env.e.Orfas(ctx)
	if err != nil || len(os) != 1 || os[0].ID != "T-001" {
		t.Fatalf("órfãs = %+v, %v", os, err)
	}
	if _, err := env.e.LiberarOrfas(ctx, "F2"); !errors.Is(err, core.ErrInvalid) {
		t.Fatalf("executor liberou órfãs: %v", err)
	}
	liberadas, err := env.e.LiberarOrfas(ctx, "I")
	if err != nil || len(liberadas) != 1 {
		t.Fatalf("liberadas = %+v, %v", liberadas, err)
	}
	it, notas, _ := env.c.Item(ctx, "T-001")
	if it.Estado != Pronta || it.Dono != "" || len(notas) != 1 || notas[0].Tipo != "orfa" {
		t.Fatalf("T-001 = %+v notas %+v", it, notas)
	}
}

func TestMetricasLatencias(t *testing.T) {
	env := timeEnv(t, NewRoles(nil, nil, nil))
	s, err := env.e.AbrirSprint(ctx, "G", "22:00-06:00", "")
	if err != nil {
		t.Fatal(err)
	}
	env.pronta("T-001")
	env.advance(10 * time.Minute)
	env.acao("F1", AcaoReservar, "T-001", Args{})
	env.advance(20 * time.Minute)
	env.acao("F1", AcaoConcluir, "T-001", Args{Ponta: "a"})
	env.advance(5 * time.Minute)
	env.acao("R", AcaoRevisar, "T-001", Args{})
	env.acao("R", AcaoRejeitar, "T-001", Args{Motivo: "falta teste"})
	env.acao("F1", AcaoReservar, "T-001", Args{})
	env.acao("F1", AcaoConcluir, "T-001", Args{Ponta: "b"})
	env.advance(3 * time.Minute)
	env.acao("R", AcaoRevisar, "T-001", Args{})
	env.acao("R", AcaoAprovar, "T-001", Args{Ponta: "b"})
	env.advance(12 * time.Minute)
	env.acao("I", AcaoIntegrar, "T-001", Args{Commit: "m"})
	env.advance(10 * time.Minute)
	m, err := env.e.Metricas(ctx, s.ID)
	if err != nil {
		t.Fatal(err)
	}
	if m.Integradas != 1 || m.Rejeicoes != 1 {
		t.Fatalf("contagens = %+v", m)
	}
	l := m.Latencias
	if l["pronta_em_andamento"].MedianaS != 600 || l["concluida_em_revisao"].N != 2 || l["concluida_integrada"].MedianaS != 900 {
		t.Fatalf("latências = %+v", l)
	}
	if m.TarefasPorExecutorHora <= 0 {
		t.Fatalf("tarefas/executor/hora = %v", m.TarefasPorExecutorHora)
	}
}

func TestMetricasUsoDeRecurso(t *testing.T) {
	env := timeEnv(t, NewRoles(nil, nil, nil))
	spec := core.ResourceSpec{Nome: "gpu", Dimensoes: core.Dims{"vram_mb": 8000}, Prioridades: []string{"a"}, PrazoMax: 2 * time.Hour}
	env.c.SetSettings(core.Settings{Resources: map[string]core.ResourceSpec{"gpu": spec}})
	s, err := env.e.AbrirSprint(ctx, "G", "22:00-06:00", "")
	if err != nil {
		t.Fatal(err)
	}
	env.advance(time.Hour)
	g, err := env.c.RequestGrant(ctx, core.GrantRequest{Recurso: "gpu", Vaga: "F1", Prioridade: "a", Exclusivo: true, Motivo: "e2e", Duracao: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	env.advance(time.Hour)
	if _, err := env.c.ReleaseGrantByID(ctx, "F1", g.ID); err != nil {
		t.Fatal(err)
	}
	env.advance(2 * time.Hour) // janela de 4 h, 1 h com a GPU concedida
	m, err := env.e.Metricas(ctx, s.ID)
	if err != nil {
		t.Fatal(err)
	}
	if u := m.UsoRecursos["gpu"]; u < 24.9 || u > 25.1 {
		t.Fatalf("uso da gpu = %v%%", u)
	}
}

func TestMetricasOciosidade(t *testing.T) {
	env := timeEnv(t, NewRoles(nil, nil, nil))
	s, err := env.e.AbrirSprint(ctx, "G", "22:00-06:00", "")
	if err != nil {
		t.Fatal(err)
	}
	env.c.SetStatus(ctx, "F1", core.EstadoOcioso, "")
	env.advance(20 * time.Minute)
	env.c.SetStatus(ctx, "F1", core.EstadoTrabalhando, "x")
	env.advance(10 * time.Minute)
	m, err := env.e.Metricas(ctx, s.ID)
	if err != nil {
		t.Fatal(err)
	}
	if m.OciosidadeMin["F1"] != 20 {
		t.Fatalf("ociosidade = %+v", m.OciosidadeMin)
	}
}

func TestMetricasIgnoramEventoSemMudancaDeEstado(t *testing.T) {
	env := timeEnv(t, NewRoles(nil, nil, nil))
	s, err := env.e.AbrirSprint(ctx, "G", "22:00-06:00", "")
	if err != nil {
		t.Fatal(err)
	}
	env.sync([]Found{found(Pronta, "T-001")})
	env.acao("F1", AcaoReservar, "T-001", Args{})
	env.acao("F1", AcaoConcluir, "T-001", Args{Ponta: "a"})
	env.acao("R", AcaoRevisar, "T-001", Args{})
	env.acao("R", AcaoAprovar, "T-001", Args{Ponta: "a"})
	env.advance(10 * time.Minute)
	env.acao("I", AcaoIntegrar, "T-001", Args{Commit: "m"})
	env.advance(time.Minute)
	f := found(Integrada, "T-001")
	f.Caminho = "backlog/done/T-001.md" // o arquivo foi para done: sincroniza sem mudar o estado
	env.sync([]Found{f})
	m, err := env.e.Metricas(ctx, s.ID)
	if err != nil {
		t.Fatal(err)
	}
	if m.Integradas != 1 || m.Latencias["concluida_integrada"].N != 1 || m.Latencias["concluida_integrada"].MedianaS != 600 {
		t.Fatalf("métricas = %+v", m)
	}
}

func TestVagaExecutadaNuncaSumidaNaSaude(t *testing.T) {
	env := timeEnv(t, NewRoles(nil, nil, nil))
	if _, err := env.c.AbrirVagaExec(ctx, "G", core.VagaExec{Vaga: "F1", Papel: "EXECUTOR", Modelo: "opus", Esforco: "high"}); err != nil {
		t.Fatal(err)
	}
	env.pronta("T-001")
	env.acao("F1", AcaoReservar, "T-001", Args{})
	env.advance(13 * time.Hour)
	env.vivos("F1")
	s, err := env.e.Saude(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range s.Sumidos {
		if a.Vaga == "F1" {
			t.Fatalf("vaga executada aparece sumida: %+v", s.Sumidos)
		}
	}
	if orfas, _ := env.e.Orfas(ctx); len(orfas) != 0 {
		t.Fatalf("órfãs de vaga executada aberta = %+v", orfas)
	}
	// suspensa: a saúde avisa e as tarefas dela viram órfãs
	env.c.IniciarTurno(ctx, "F1", "x", 0)
	if _, err := env.c.TerminarTurno(ctx, "F1", core.TurnoFim{Falha: "erro", Suspender: true}); err != nil {
		t.Fatal(err)
	}
	s, _ = env.e.Saude(ctx)
	achou := false
	for _, a := range s.Alertas {
		achou = achou || strings.Contains(a, "F1") && strings.Contains(a, "suspensa")
	}
	if !achou {
		t.Fatalf("alertas = %v", s.Alertas)
	}
	if orfas, _ := env.e.Orfas(ctx); len(orfas) != 1 {
		t.Fatalf("órfãs de vaga suspensa = %+v", orfas)
	}
}

func TestSaudeAvisaVagaSuspensaQueNuncaEntrou(t *testing.T) {
	env := timeEnv(t, NewRoles(nil, nil, nil))
	if _, err := env.c.AbrirVagaExec(ctx, "G", core.VagaExec{Vaga: "EXEC9", Papel: "EXECUTOR", Modelo: "opus", Esforco: "high"}); err != nil {
		t.Fatal(err)
	}
	env.c.IniciarTurno(ctx, "EXEC9", "x", 0)
	if _, err := env.c.TerminarTurno(ctx, "EXEC9", core.TurnoFim{Falha: "sem_claude", Suspender: true}); err != nil {
		t.Fatal(err)
	}
	s, err := env.e.Saude(ctx)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, a := range s.Alertas {
		if strings.Contains(a, "EXEC9") && strings.Contains(a, "suspensa") {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("alertas = %v", s.Alertas)
	}
}
