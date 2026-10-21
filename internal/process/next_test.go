package process

import (
	"errors"
	"testing"
	"time"

	"github.com/JoaoVitorSantDrade/central/internal/core"
)

func (env *testEnv) prontaCom(in ItemInput) {
	env.t.Helper()
	if in.Titulo == "" {
		in.Titulo = "tarefa " + in.ID
	}
	env.criar(in)
	env.acao("P", AcaoPronto, in.ID, Args{})
}

func (env *testEnv) proxima(vaga string) Escolha {
	env.t.Helper()
	esc, err := env.e.Proxima(ctx, vaga)
	if err != nil {
		env.t.Fatal(err)
	}
	return esc
}

func idDe(esc Escolha) string {
	if esc.Item == nil {
		return ""
	}
	return esc.Item.ID
}

func TestProximaBugP0Primeiro(t *testing.T) {
	env := timeEnv(t, NewRoles(nil, nil, nil))
	env.prontaCom(ItemInput{ID: "T-001", Prioridade: "P0"})
	env.prontaCom(ItemInput{ID: "T-002", Prioridade: "P1", Categoria: "bug"})
	env.prontaCom(ItemInput{ID: "T-003", Prioridade: "P0", Categoria: "bug"})
	if got := idDe(env.proxima("F1")); got != "T-003" {
		t.Fatalf("proxima = %s, quer o bug P0", got)
	}
}

func TestProximaSprintAntesDeFolga(t *testing.T) {
	env := timeEnv(t, NewRoles(nil, nil, nil))
	s, err := env.e.AbrirSprint(ctx, "G", "22:00-06:00", "")
	if err != nil {
		t.Fatal(err)
	}
	env.prontaCom(ItemInput{ID: "T-001", Prioridade: "P0"})
	env.prontaCom(ItemInput{ID: "T-002", Prioridade: "P2", Folga: true})
	env.prontaCom(ItemInput{ID: "T-003", Prioridade: "P3", Sprint: s.ID})
	if got := idDe(env.proxima("F1")); got != "T-003" {
		t.Fatalf("proxima = %s, quer a da sprint", got)
	}
}

func TestProximaFolgaAntesDoResto(t *testing.T) {
	env := timeEnv(t, NewRoles(nil, nil, nil))
	env.prontaCom(ItemInput{ID: "T-001", Prioridade: "P0"})
	env.prontaCom(ItemInput{ID: "T-002", Prioridade: "P3", Folga: true})
	if got := idDe(env.proxima("F1")); got != "T-002" {
		t.Fatalf("proxima = %s, quer a de folga", got)
	}
}

func TestProximaPrioridadeEBloqueante(t *testing.T) {
	env := timeEnv(t, NewRoles(nil, nil, nil))
	env.prontaCom(ItemInput{ID: "T-001", Prioridade: "P2"})
	env.prontaCom(ItemInput{ID: "T-002", Prioridade: "P1"})
	env.prontaCom(ItemInput{ID: "T-003", Prioridade: "P1", Bloqueante: true})
	if got := idDe(env.proxima("F1")); got != "T-003" {
		t.Fatalf("proxima = %s", got)
	}
}

func TestProximaAreaPreferida(t *testing.T) {
	env := timeEnv(t, NewRoles(nil, nil, nil))
	if _, _, err := env.c.EnterWith(ctx, core.EnterArgs{Vaga: "F1", Papel: Executor, Pasta: "/F1", Area: "tipografia"}); err != nil {
		t.Fatal(err)
	}
	env.prontaCom(ItemInput{ID: "T-001", Prioridade: "P1", Area: "alinhamento"})
	env.prontaCom(ItemInput{ID: "T-002", Prioridade: "P1", Area: "tipografia"})
	if got := idDe(env.proxima("F1")); got != "T-002" {
		t.Fatalf("proxima = %s, quer a da área", got)
	}
	// a área é preferência: sem tarefa nela, pega a de outra área
	env.acao("F2", AcaoReservar, "T-002", Args{})
	if got := idDe(env.proxima("F1")); got != "T-001" {
		t.Fatalf("proxima = %s", got)
	}
}

func TestProximaBloqueioSoSenior(t *testing.T) {
	env := timeEnv(t, NewRoles(nil, nil, []string{"FS"}))
	env.enter("FS", Executor)
	env.prontaCom(ItemInput{ID: "T-001", Prioridade: "P3"})
	env.prontaCom(ItemInput{ID: "T-002", Prioridade: "P1"})
	env.acao("F1", AcaoReservar, "T-001", Args{})
	env.acao("F1", AcaoBloquear, "T-001", Args{Nota: "x"})
	if esc := env.proxima("F2"); idDe(esc) != "T-002" || esc.Descartados["T-001"] == "" {
		t.Fatalf("executor comum: %+v", esc)
	}
	if got := idDe(env.proxima("FS")); got != "T-001" {
		t.Fatalf("sênior pega a bloqueada primeiro: %s", got)
	}
}

func TestProximaCongelada(t *testing.T) {
	env := timeEnv(t, NewRoles(nil, nil, nil))
	env.prontaCom(ItemInput{ID: "T-001", Prioridade: "P1"})
	if _, err := env.e.AbrirSprint(ctx, "G", "22:00-23:30", ""); err != nil {
		t.Fatal(err)
	}
	env.advance(45 * time.Minute)
	if esc := env.proxima("F1"); esc.Item != nil || esc.Motivo == "" {
		t.Fatalf("congelada: %+v", esc)
	}
}

func TestProximaConflitoDeArquivos(t *testing.T) {
	env := timeEnv(t, NewRoles(nil, nil, nil))
	env.prontaCom(ItemInput{ID: "T-001", Prioridade: "P0", Arquivos: []string{"a.go"}})
	env.prontaCom(ItemInput{ID: "T-002", Prioridade: "P1", Arquivos: []string{"a.go"}})
	env.prontaCom(ItemInput{ID: "T-003", Prioridade: "P2", Arquivos: []string{"b.go"}})
	env.acao("F2", AcaoReservar, "T-001", Args{})
	if got := idDe(env.proxima("F1")); got != "T-003" {
		t.Fatalf("proxima = %s, quer a sem conflito", got)
	}
}

func TestProximaEvitaRecursoSaturado(t *testing.T) {
	env := timeEnv(t, NewRoles(nil, nil, nil))
	spec := core.ResourceSpec{Nome: "gpu", Dimensoes: core.Dims{"vram_mb": 8000}, Prioridades: []string{"a"}, PrazoMax: time.Hour}
	env.c.SetSettings(core.Settings{Resources: map[string]core.ResourceSpec{"gpu": spec}})
	if _, err := env.c.RequestGrant(ctx, core.GrantRequest{Recurso: "gpu", Vaga: "F2", Prioridade: "a", Exclusivo: true, Motivo: "e2e"}); err != nil {
		t.Fatal(err)
	}
	env.prontaCom(ItemInput{ID: "T-001", Prioridade: "P0", Recurso: "gpu"})
	env.prontaCom(ItemInput{ID: "T-002", Prioridade: "P2"})
	if got := idDe(env.proxima("F1")); got != "T-002" {
		t.Fatalf("proxima = %s, quer a que não usa a GPU ocupada", got)
	}
}

func TestProximaFilaVaziaAvisaUmaVez(t *testing.T) {
	env := timeEnv(t, NewRoles(nil, nil, nil))
	for i := 0; i < 3; i++ {
		if esc := env.proxima("F1"); esc.Item != nil {
			t.Fatalf("achou %+v", esc)
		}
	}
	if ms := ofType(env.inbox("P"), TipoFilaVazia); len(ms) != 1 {
		t.Fatalf("avisos = %+v", ms)
	}
	env.advance(31 * time.Minute)
	env.proxima("F1")
	if ms := ofType(env.inbox("P"), TipoFilaVazia); len(ms) != 1 {
		t.Fatalf("depois de 30 min = %+v", ms)
	}
}

func TestProximaSoExecutor(t *testing.T) {
	env := timeEnv(t, NewRoles(nil, nil, nil))
	if _, err := env.e.Proxima(ctx, "R"); !errors.Is(err, core.ErrInvalid) {
		t.Fatalf("revisor pediu tarefa: %v", err)
	}
}
