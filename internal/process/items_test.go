package process

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/JoaoVitorSantDrade/central/internal/core"
)

// timeEnv é o time padrão: P (planejador), F1/F2 (executores), R (revisor),
// I (integrador), V (validador), G (gerente).
func timeEnv(t *testing.T, roles Roles) *testEnv {
	env := newEnv(t, roles, defaultConfig())
	for _, v := range []struct{ vaga, papel string }{{"G", roles.Name(Gerente)}, {"P", roles.Name(Planejador)},
		{"F1", roles.Name(Executor)}, {"F2", roles.Name(Executor)}, {"R", roles.Name(Revisor)},
		{"I", roles.Name(Integrador)}, {"V", roles.Name(Validador)}} {
		env.enter(v.vaga, v.papel)
	}
	return env
}

func (env *testEnv) criar(in ItemInput) core.Item {
	env.t.Helper()
	it, err := env.e.Criar(ctx, "P", in)
	if err != nil {
		env.t.Fatal(err)
	}
	return it
}

func (env *testEnv) acao(vaga, acao, id string, a Args) core.Item {
	env.t.Helper()
	it, err := env.e.Acao(ctx, vaga, acao, id, a)
	if err != nil {
		env.t.Fatalf("%s %s %s: %v", vaga, acao, id, err)
	}
	return it
}

// pronta cria uma tarefa e a deixa pronta.
func (env *testEnv) pronta(id string, arquivos ...string) core.Item {
	env.t.Helper()
	env.criar(ItemInput{ID: id, Titulo: "tarefa " + id, Arquivos: arquivos})
	return env.acao("P", AcaoPronto, id, Args{})
}

func TestCriarEProntoValidaObrigatorios(t *testing.T) {
	env := timeEnv(t, NewRoles(nil, nil, nil))
	env.e.cfg.Obrigatorios = []string{"titulo", "criterio_de_pronto"}
	it := env.criar(ItemInput{Titulo: "sem critério"})
	if it.ID != "T-001" || it.Estado != Proposta {
		t.Fatalf("criado = %+v", it)
	}
	if _, err := env.e.Acao(ctx, "P", AcaoPronto, it.ID, Args{}); !errors.Is(err, core.ErrInvalid) {
		t.Fatalf("pronto sem criterio_de_pronto: %v", err)
	}
	env.criar(ItemInput{ID: "T-010", Titulo: "ok", Campos: map[string]any{"criterio_de_pronto": "x"}})
	if it := env.acao("P", AcaoPronto, "T-010", Args{}); it.Estado != Pronta {
		t.Fatalf("pronto = %+v", it)
	}
	if _, err := env.e.Criar(ctx, "P", ItemInput{ID: "T-010", Titulo: "dup"}); !errors.Is(err, core.ErrConflict) {
		t.Fatalf("id repetido: %v", err)
	}
}

func TestTransicoes(t *testing.T) {
	type passo struct {
		vaga, acao string
		args       Args
		quer       string // estado depois, ou "" quando deve falhar
		erro       error
	}
	casos := []struct {
		nome   string
		passos []passo
	}{
		{"fluxo feliz", []passo{
			{"F1", AcaoReservar, Args{}, EmAndamento, nil},
			{"F1", AcaoConcluir, Args{Ponta: "abc"}, Concluida, nil},
			{"R", AcaoRevisar, Args{}, EmRevisao, nil},
			{"R", AcaoAprovar, Args{Ponta: "abc"}, Aprovada, nil},
			{"I", AcaoIntegrar, Args{Commit: "m1"}, Integrada, nil},
			{"V", AcaoValidar, Args{Resultado: "ligada"}, Validada, nil},
		}},
		{"papéis errados", []passo{
			{"R", AcaoReservar, Args{}, "", core.ErrInvalid},
			{"F1", AcaoReservar, Args{}, EmAndamento, nil},
			{"F2", AcaoConcluir, Args{Ponta: "x"}, "", core.ErrInvalid},
			{"F1", AcaoConcluir, Args{Ponta: "abc"}, Concluida, nil},
			{"F1", AcaoRevisar, Args{}, "", core.ErrInvalid},
			{"I", AcaoIntegrar, Args{Commit: "m"}, "", core.ErrConflict},
		}},
		{"estado errado", []passo{
			{"F1", AcaoConcluir, Args{Ponta: "x"}, "", core.ErrConflict},
			{"R", AcaoAprovar, Args{Ponta: "x"}, "", core.ErrConflict},
		}},
		{"campos obrigatórios da ação", []passo{
			{"F1", AcaoReservar, Args{}, EmAndamento, nil},
			{"F1", AcaoConcluir, Args{}, "", core.ErrInvalid},
			{"F1", AcaoBloquear, Args{}, "", core.ErrInvalid},
			{"F1", AcaoConcluir, Args{Ponta: "abc"}, Concluida, nil},
			{"R", AcaoRevisar, Args{}, EmRevisao, nil},
			{"R", AcaoRejeitar, Args{}, "", core.ErrInvalid},
			{"R", AcaoRejeitar, Args{Motivo: "sem teste"}, Rejeitada, nil},
			{"F2", AcaoReservar, Args{}, "", core.ErrInvalid},
			{"F1", AcaoReservar, Args{}, EmAndamento, nil},
		}},
		{"validar resultado", []passo{
			{"F1", AcaoReservar, Args{}, EmAndamento, nil},
			{"F1", AcaoConcluir, Args{Ponta: "a"}, Concluida, nil},
			{"R", AcaoRevisar, Args{}, EmRevisao, nil},
			{"R", AcaoAprovar, Args{Ponta: "a"}, Aprovada, nil},
			{"I", AcaoIntegrar, Args{}, "", core.ErrInvalid},
			{"I", AcaoIntegrar, Args{Commit: "m"}, Integrada, nil},
			{"V", AcaoValidar, Args{Resultado: "talvez"}, "", core.ErrInvalid},
			{"V", AcaoValidar, Args{Resultado: "sem_caminho"}, Validada, nil},
		}},
	}
	for _, c := range casos {
		t.Run(c.nome, func(t *testing.T) {
			env := timeEnv(t, NewRoles(nil, nil, nil))
			env.pronta("T-001")
			for i, p := range c.passos {
				it, err := env.e.Acao(ctx, p.vaga, p.acao, "T-001", p.args)
				if p.erro != nil {
					if !errors.Is(err, p.erro) {
						t.Fatalf("passo %d (%s %s): err = %v, quer %v", i, p.vaga, p.acao, err, p.erro)
					}
					continue
				}
				if err != nil || it.Estado != p.quer {
					t.Fatalf("passo %d (%s %s): estado %q, err %v; quer %q", i, p.vaga, p.acao, it.Estado, err, p.quer)
				}
			}
		})
	}
}

func TestAcordaQuemDeve(t *testing.T) {
	env := timeEnv(t, NewRoles(nil, nil, nil))
	env.pronta("T-001")
	env.acao("F1", AcaoReservar, "T-001", Args{})
	env.acao("F1", AcaoConcluir, "T-001", Args{Ponta: "a"})
	if ms := ofType(env.inbox("R"), "item.concluida"); len(ms) != 1 || ms[0].Ref != "T-001" {
		t.Fatalf("REVISOR recebeu %+v", ms)
	}
	env.acao("R", AcaoRevisar, "T-001", Args{})
	env.acao("R", AcaoAprovar, "T-001", Args{Ponta: "a"})
	if ms := ofType(env.inbox("I"), "item.aprovada"); len(ms) != 1 {
		t.Fatalf("INTEGRADOR recebeu %+v", ms)
	}
	env.acao("I", AcaoIntegrar, "T-001", Args{Commit: "m"})
	if ms := ofType(env.inbox("P"), "item.integrada"); len(ms) != 1 {
		t.Fatalf("PLANEJADOR (aceite) recebeu %+v", ms)
	}
	if ms := ofType(env.inbox("V"), "item.integrada"); len(ms) != 0 {
		t.Fatalf("VALIDADOR acordado sem visivel_ao_usuario: %+v", ms)
	}
	env.criar(ItemInput{ID: "T-002", Titulo: "visível", Visivel: true})
	env.acao("P", AcaoPronto, "T-002", Args{})
	env.acao("F1", AcaoReservar, "T-002", Args{})
	env.acao("F1", AcaoConcluir, "T-002", Args{Ponta: "b"})
	env.acao("R", AcaoRevisar, "T-002", Args{})
	env.acao("R", AcaoRejeitar, "T-002", Args{Motivo: "falta teste"})
	if ms := ofType(env.inbox("F1"), "item.rejeitada"); len(ms) != 1 {
		t.Fatalf("dono recebeu %+v", ms)
	}
	env.acao("F1", AcaoReservar, "T-002", Args{})
	env.acao("F1", AcaoConcluir, "T-002", Args{Ponta: "c"})
	env.acao("R", AcaoRevisar, "T-002", Args{})
	env.acao("R", AcaoAprovar, "T-002", Args{Ponta: "c"})
	env.acao("I", AcaoIntegrar, "T-002", Args{Commit: "m2"})
	if ms := ofType(env.inbox("V"), "item.integrada"); len(ms) != 1 {
		t.Fatalf("VALIDADOR recebeu %+v", ms)
	}
	env.acao("V", AcaoValidar, "T-002", Args{Resultado: "quebrada"})
	if ms := ofType(env.inbox("P"), "item.validada"); len(ms) != 1 {
		t.Fatalf("PLANEJADOR não soube da validação quebrada: %+v", ms)
	}
}

func TestReservaConcorrente(t *testing.T) {
	env := timeEnv(t, NewRoles(nil, nil, nil))
	env.pronta("T-001")
	var wg sync.WaitGroup
	var mu sync.Mutex
	venceu := 0
	for i := 0; i < 20; i++ {
		vaga := []string{"F1", "F2"}[i%2]
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := env.e.Acao(ctx, vaga, AcaoReservar, "T-001", Args{}); err == nil {
				mu.Lock()
				venceu++
				mu.Unlock()
			} else if !errors.Is(err, core.ErrConflict) {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if venceu != 1 {
		t.Fatalf("%d reservas venceram", venceu)
	}
}

func TestConflitoDeArquivos(t *testing.T) {
	env := timeEnv(t, NewRoles(nil, nil, nil))
	env.pronta("T-001", "backend/")
	env.pronta("T-002", "backend/x.py")
	env.pronta("T-003", "src/app.tsx")
	env.acao("F1", AcaoReservar, "T-001", Args{})
	if _, err := env.e.Acao(ctx, "F2", AcaoReservar, "T-002", Args{}); !errors.Is(err, core.ErrConflict) {
		t.Fatalf("conflito backend/ × backend/x.py: %v", err)
	}
	env.acao("F2", AcaoReservar, "T-003", Args{})
}

func TestAprovarExigePontaAtual(t *testing.T) {
	env := timeEnv(t, NewRoles(nil, nil, nil))
	env.pronta("T-001")
	env.acao("F1", AcaoReservar, "T-001", Args{})
	env.acao("F1", AcaoConcluir, "T-001", Args{Ponta: "abc"})
	env.acao("R", AcaoRevisar, "T-001", Args{})
	if _, err := env.e.Acao(ctx, "R", AcaoAprovar, "T-001", Args{Ponta: "velha"}); !errors.Is(err, core.ErrConflict) {
		t.Fatalf("aprovou ponta diferente: %v", err)
	}
}

func TestBloquearVoltaAPronta(t *testing.T) {
	env := timeEnv(t, NewRoles(nil, nil, nil))
	env.pronta("T-001")
	env.acao("F1", AcaoReservar, "T-001", Args{})
	it := env.acao("F1", AcaoBloquear, "T-001", Args{Nota: "não reproduz"})
	if it.Estado != Pronta || it.Bloqueios != 1 || it.Dono != "" {
		t.Fatalf("bloqueado = %+v", it)
	}
	_, notas, _ := env.c.Item(ctx, "T-001")
	if len(notas) != 1 || notas[0].Tipo != "bloqueio" {
		t.Fatalf("notas = %+v", notas)
	}
	if ms := ofType(env.inbox("P"), "item.bloqueada"); len(ms) != 1 {
		t.Fatalf("PLANEJADOR recebeu %+v", ms)
	}
	// com uma nota, outro executor pode tentar (não há vaga sênior); com duas, ninguém
	env.acao("F2", AcaoReservar, "T-001", Args{})
	env.acao("F2", AcaoBloquear, "T-001", Args{Nota: "também não"})
	if _, err := env.e.Acao(ctx, "F1", AcaoReservar, "T-001", Args{}); !errors.Is(err, core.ErrConflict) {
		t.Fatalf("reservou com duas notas de bloqueio: %v", err)
	}
}

func TestBloqueioSoSenior(t *testing.T) {
	env := timeEnv(t, NewRoles(nil, nil, []string{"FS"}))
	env.enter("FS", Executor)
	env.pronta("T-001")
	env.acao("F1", AcaoReservar, "T-001", Args{})
	env.acao("F1", AcaoBloquear, "T-001", Args{Nota: "x"})
	if _, err := env.e.Acao(ctx, "F2", AcaoReservar, "T-001", Args{}); !errors.Is(err, core.ErrConflict) {
		t.Fatalf("executor comum pegou bloqueada com vaga sênior: %v", err)
	}
	env.acao("FS", AcaoReservar, "T-001", Args{})
}

func TestReabrirAnulaRevisao(t *testing.T) {
	env := timeEnv(t, NewRoles(nil, nil, nil))
	env.pronta("T-001")
	env.acao("F1", AcaoReservar, "T-001", Args{})
	env.acao("F1", AcaoConcluir, "T-001", Args{Ponta: "a"})
	env.acao("R", AcaoRevisar, "T-001", Args{})
	env.inbox("R")
	it := env.acao("F1", AcaoReabrir, "T-001", Args{})
	if it.Estado != EmAndamento || it.PontaRevisada != "" {
		t.Fatalf("reaberto = %+v", it)
	}
	if ms := ofType(env.inbox("R"), "item.em_andamento"); len(ms) != 1 {
		t.Fatalf("REVISOR não soube da revisão anulada: %+v", ms)
	}
}

func TestFluxoSemRevisor(t *testing.T) {
	env := timeEnv(t, NewRoles(nil, []string{Revisor, Validador}, nil))
	env.pronta("T-001")
	env.acao("F1", AcaoReservar, "T-001", Args{})
	env.acao("F1", AcaoConcluir, "T-001", Args{Ponta: "a"})
	if ms := ofType(env.inbox("I"), "item.concluida"); len(ms) != 1 {
		t.Fatalf("sem REVISOR, o INTEGRADOR não acordou: %+v", ms)
	}
	env.acao("I", AcaoAprovar, "T-001", Args{Ponta: "a"})
	if it := env.acao("I", AcaoIntegrar, "T-001", Args{Commit: "m"}); it.Estado != Integrada {
		t.Fatalf("integrado = %+v", it)
	}
}

func TestCongeladaNaoReserva(t *testing.T) {
	env := timeEnv(t, NewRoles(nil, nil, nil))
	env.pronta("T-001")
	if _, err := env.e.AbrirSprint(ctx, "G", "22:00-23:30", ""); err != nil {
		t.Fatal(err)
	}
	env.advance(31 * time.Minute) // 22:31 ≥ congelamento (22:30)
	if _, err := env.e.Acao(ctx, "F1", AcaoReservar, "T-001", Args{}); !errors.Is(err, core.ErrConflict) {
		t.Fatalf("reservou depois do congelamento: %v", err)
	}
}

func TestMissaoPorMarco(t *testing.T) {
	env := timeEnv(t, NewRoles(nil, nil, nil))
	env.criar(ItemInput{ID: "M-001", Tipo: TipoMissao, Titulo: "missão"})
	env.acao("P", AcaoPronto, "M-001", Args{})
	env.acao("F1", AcaoReservar, "M-001", Args{})
	env.acao("F1", AcaoConcluir, "M-001", Args{Ponta: "a"})
	env.acao("R", AcaoRevisar, "M-001", Args{})
	env.acao("R", AcaoAprovar, "M-001", Args{Ponta: "a"})
	it := env.acao("I", AcaoIntegrar, "M-001", Args{Commit: "m1"})
	if it.Estado != EmAndamento || it.Marco != 1 || it.Dono != "F1" {
		t.Fatalf("depois do marco 1 = %+v", it)
	}
	env.acao("F1", AcaoConcluir, "M-001", Args{Ponta: "b"})
	env.acao("R", AcaoRevisar, "M-001", Args{})
	env.acao("R", AcaoAprovar, "M-001", Args{Ponta: "b"})
	if it := env.acao("I", AcaoIntegrar, "M-001", Args{Commit: "m2", Final: true}); it.Estado != Integrada || it.Marco != 2 {
		t.Fatalf("marco final = %+v", it)
	}
}
