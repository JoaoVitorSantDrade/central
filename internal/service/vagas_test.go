package service

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/JoaoVitorSantDrade/central/internal/core"
)

// comClaudeFalso troca o claude do [execucao] pelo binário falso (ou pelo comando dado).
func (a *ambienteExec) comClaude(t *testing.T, claude string) {
	t.Helper()
	cfg := strings.Replace(strings.Replace(cfgExecucao, "%CUSTO%", "0", 1), `claude = "claude-falso"`, "claude = '"+claude+"'", 1)
	if _, err := a.s.Enter(ctx, EnterInput{ProjetoID: "p", Nome: "P", Raiz: a.raiz, Pasta: a.pastaG1(), Vaga: "G1", Papel: "GERENTE", Config: cfg}); err != nil {
		t.Fatal(err)
	}
}

func (a *ambienteExec) pastaG1() string { return filepath.Join(a.raiz, "humano") }

// expedienteAgora é um expediente que contém a hora do relógio.
func (a *ambienteExec) expedienteAgora() string {
	l := a.rel.now().Local()
	return l.Add(-time.Hour).Format("15:04") + "-" + l.Add(8*time.Hour).Format("15:04")
}

func TestServicoAbrirFecharVagas(t *testing.T) {
	a := novoAmbienteExec(t, "")
	a.comClaude(t, claudeFalso(t))
	v, err := a.s.AbrirVaga(ctx, "p", a.pastaG1(), VagaAbrir{Vaga: "EXEC1", Papel: "EXECUTOR", Nota: "T-1"})
	if err != nil || v.Estado != core.VagaAberta || v.Modelo != "opus" {
		t.Fatalf("abrir = %+v, %v", v, err)
	}
	// sem pasta é o humano
	if _, err := a.s.AbrirVaga(ctx, "p", "", VagaAbrir{Vaga: "EXEC2", Papel: "EXECUTOR"}); err != nil {
		t.Fatalf("humano = %v", err)
	}
	info, err := a.s.Vagas(ctx, "p", "")
	if err != nil || len(info.Vagas) != 2 || info.PlanoPausadoAte != nil {
		t.Fatalf("vagas = %+v, %v", info, err)
	}
	if v, err = a.s.FecharVaga(ctx, "p", "", "EXEC2"); err != nil || v.Estado != core.VagaFechada {
		t.Fatalf("fechar = %+v, %v", v, err)
	}
}

func TestAbrirSemClaudeRecusaNoServico(t *testing.T) {
	a := novoAmbienteExec(t, "")
	a.comClaude(t, filepath.Join(t.TempDir(), "nao-existe", "claude"))
	_, err := a.s.AbrirVaga(ctx, "p", "", VagaAbrir{Vaga: "EXEC1", Papel: "EXECUTOR"})
	if !errors.Is(err, core.ErrInvalid) || !strings.Contains(err.Error(), "Claude Code") {
		t.Fatalf("sem claude = %v", err)
	}
	// sem login: auth status sai com erro
	a.comClaude(t, claudeFalso(t))
	t.Setenv("CLAUDE_FALSO_SAIR", "1")
	_, err = a.s.AbrirVaga(ctx, "p", "", VagaAbrir{Vaga: "EXEC1", Papel: "EXECUTOR"})
	if !errors.Is(err, core.ErrInvalid) || !strings.Contains(err.Error(), "auth status") {
		t.Fatalf("sem login = %v", err)
	}
	if vs, _ := a.p.core.VagasExec(ctx); len(vs) != 0 {
		t.Fatalf("vaga meio aberta: %+v", vs)
	}
}

func TestDespertarDaVaga(t *testing.T) {
	a := novoAmbienteExec(t, "")
	a.abrir("EXEC1", "EXECUTOR")
	a.passoEEspera()
	a.entrar("EXEC1", "EXECUTOR")
	pasta := a.vaga("EXEC1").Pasta
	if err := a.s.Despertar(ctx, "p", pasta, 30*time.Second, "x"); !errors.Is(err, core.ErrInvalid) {
		t.Fatalf("menos de 1 min = %v", err)
	}
	if err := a.s.Despertar(ctx, "p", pasta, 25*time.Hour, "x"); !errors.Is(err, core.ErrInvalid) {
		t.Fatalf("mais de 24 h = %v", err)
	}
	if err := a.s.Despertar(ctx, "p", pasta, 20*time.Minute, "ver o CI"); err != nil {
		t.Fatal(err)
	}
	a.rel.avancar(20 * time.Minute)
	if d, _ := a.p.core.DespertaresVencidos(ctx, a.rel.now()); d["EXEC1"] != "ver o CI" {
		t.Fatalf("despertares = %v", d)
	}
	if err := a.s.Despertar(ctx, "p", a.pastaG1(), 20*time.Minute, "x"); err == nil {
		t.Fatal("vaga que não é executada não pede despertar")
	}
}

func TestSprintAbrirAbreGerente1(t *testing.T) {
	a := novoAmbienteExec(t, "")
	a.comClaude(t, claudeFalso(t))
	sp, err := a.s.AbrirSprint(ctx, "p", a.pastaG1(), a.expedienteAgora(), "")
	if err != nil || len(sp.Avisos) != 0 {
		t.Fatalf("sprint = %+v, %v", sp, err)
	}
	g := a.vaga("GERENTE1")
	if g.Estado != core.VagaAberta || g.Sprint != sp.ID || g.Modelo != "sonnet" {
		t.Fatalf("GERENTE1 = %+v", g)
	}
	a.s.EncerrarSprint(ctx, "p", a.pastaG1())
	// sem claude, a sprint abre com um aviso
	a.comClaude(t, filepath.Join(t.TempDir(), "nao-existe", "claude"))
	a.p.core.FecharVagaExec(ctx, core.Humano, "GERENTE1")
	a.rel.avancar(24 * time.Hour)
	sp, err = a.s.AbrirSprint(ctx, "p", a.pastaG1(), a.expedienteAgora(), "")
	if err != nil || len(sp.Avisos) != 1 || !strings.Contains(sp.Avisos[0], "GERENTE1") {
		t.Fatalf("sprint sem claude = %+v, %v", sp, err)
	}
}

func TestFimDaSprintFechaVagas(t *testing.T) {
	a := novoAmbienteExec(t, "")
	a.comClaude(t, claudeFalso(t))
	sp, err := a.s.AbrirSprint(ctx, "p", a.pastaG1(), a.expedienteAgora(), "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.s.AbrirVaga(ctx, "p", a.pastaG1(), VagaAbrir{Vaga: "EXEC1", Papel: "EXECUTOR"}); err != nil {
		t.Fatal(err)
	}
	a.segurar("EXEC1")
	a.passo()
	a.esperarComecar(2) // GERENTE1 e EXEC1
	if _, err := a.s.EncerrarSprint(ctx, "p", a.pastaG1()); err != nil {
		t.Fatal(err)
	}
	a.passo()
	if v := a.vaga("EXEC1"); v.Estado != core.VagaFechada || v.TurnoDesde == nil || v.Sprint != sp.ID {
		t.Fatalf("EXEC1 com turno em curso = %+v", v)
	}
	a.f.soltarTodos()
	a.s.esperarTurnos()
	a.entrar("EXEC1", "EXECUTOR")
	a.p.core.Send(ctx, core.SendInput{De: "G1", Para: "EXEC1", Tipo: "nota", Corpo: "mais"})
	a.passoEEspera()
	if v := a.vaga("EXEC1"); v.Estado != core.VagaFechada || v.TurnoDesde != nil || v.NTurnos != 1 {
		t.Fatalf("EXEC1 depois = %+v", v)
	}
	if g := a.vaga("GERENTE1"); g.Estado != core.VagaFechada {
		t.Fatalf("GERENTE1 = %+v", g)
	}
	if n := len(a.f.vagas()); n != 2 {
		t.Fatalf("turno novo depois do fim da sprint: %v", a.f.vagas())
	}
}
