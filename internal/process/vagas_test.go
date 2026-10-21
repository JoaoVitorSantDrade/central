package process

import (
	"errors"
	"strings"
	"testing"

	"github.com/JoaoVitorSantDrade/central/internal/core"
)

var cargosTeste = map[string]Cargo{
	Gerente:  {Max: 1, Modelo: "sonnet", Esforco: "medium"},
	Executor: {Max: 2, Modelo: "opus", Esforco: "high"},
}

func TestAbrirVagaSoGerenteDentroDoTeto(t *testing.T) {
	env := timeEnv(t, NewRoles(map[string]string{Executor: "FIXER"}, nil, nil))
	in := VagaInput{Vaga: "FIXER3", Papel: "FIXER", Area: "app", Nota: "T-1"}
	if _, err := env.e.AbrirVaga(ctx, "F1", in, cargosTeste, nil); !errors.Is(err, core.ErrInvalid) {
		t.Fatalf("EXECUTOR abrindo vaga = %v", err)
	}
	v, err := env.e.AbrirVaga(ctx, "G", in, cargosTeste, nil)
	if err != nil {
		t.Fatal(err)
	}
	if v.Papel != "FIXER" || v.Modelo != "opus" || v.Esforco != "high" || v.Area != "app" || v.Nota != "T-1" || v.Estado != core.VagaAberta {
		t.Fatalf("vaga = %+v", v)
	}
	if _, err := env.e.AbrirVaga(ctx, core.Humano, VagaInput{Vaga: "FIXER4", Papel: "executor"}, cargosTeste, nil); err != nil {
		t.Fatalf("humano abrindo a 2ª = %v", err)
	}
	_, err = env.e.AbrirVaga(ctx, "G", VagaInput{Vaga: "FIXER5", Papel: "EXECUTOR"}, cargosTeste, nil)
	if !errors.Is(err, core.ErrConflict) || !strings.Contains(err.Error(), "2") {
		t.Fatalf("passando do teto = %v", err)
	}
	if _, err := env.e.AbrirVaga(ctx, "G", VagaInput{Vaga: "REV1", Papel: "REVISOR"}, cargosTeste, nil); !errors.Is(err, core.ErrInvalid) {
		t.Fatalf("cargo sem entrada = %v", err)
	}
	if _, err := env.e.AbrirVaga(ctx, "G", VagaInput{Vaga: "X1", Papel: "FAXINEIRO"}, cargosTeste, nil); !errors.Is(err, core.ErrInvalid) {
		t.Fatalf("papel desconhecido = %v", err)
	}
	if _, err := env.e.FecharVaga(ctx, "F1", "FIXER3"); !errors.Is(err, core.ErrInvalid) {
		t.Fatalf("EXECUTOR fechando = %v", err)
	}
	if f, err := env.e.FecharVaga(ctx, "G", "FIXER3"); err != nil || f.Estado != core.VagaFechada {
		t.Fatalf("fechar = %+v, %v", f, err)
	}
	// fechada não conta no teto
	if _, err := env.e.AbrirVaga(ctx, "G", VagaInput{Vaga: "FIXER5", Papel: "EXECUTOR"}, cargosTeste, nil); err != nil {
		t.Fatalf("depois de fechar uma = %v", err)
	}
}

func TestReabrirRetomaSessao(t *testing.T) {
	env := timeEnv(t, NewRoles(nil, nil, nil))
	v, err := env.e.AbrirVaga(ctx, "G", VagaInput{Vaga: "EXEC1", Papel: "EXECUTOR"}, cargosTeste, nil)
	if err != nil {
		t.Fatal(err)
	}
	env.e.FecharVaga(ctx, "G", "EXEC1")
	r, err := env.e.AbrirVaga(ctx, "G", VagaInput{Vaga: "EXEC1", Papel: "EXECUTOR"}, cargosTeste, nil)
	if err != nil || r.Sessao != v.Sessao || r.Estado != core.VagaAberta {
		t.Fatalf("reaberta = %+v, %v", r, err)
	}
	if _, err := env.e.AbrirVaga(ctx, "G", VagaInput{Vaga: "EXEC1", Papel: "EXECUTOR"}, cargosTeste, nil); !errors.Is(err, core.ErrConflict) {
		t.Fatalf("abrir aberta = %v", err)
	}
}

func TestAbrirSemClaudeRecusa(t *testing.T) {
	env := timeEnv(t, NewRoles(nil, nil, nil))
	_, err := env.e.AbrirVaga(ctx, "G", VagaInput{Vaga: "EXEC1", Papel: "EXECUTOR"}, cargosTeste, errors.New(`"claude" não encontrado`))
	if !errors.Is(err, core.ErrInvalid) || !strings.Contains(err.Error(), "Claude Code") || !strings.Contains(err.Error(), "não encontrado") {
		t.Fatalf("sem claude = %v", err)
	}
	if vs, _ := env.c.VagasExec(ctx); len(vs) != 0 {
		t.Fatalf("vaga meio aberta: %+v", vs)
	}
}
