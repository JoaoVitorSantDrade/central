package process

import (
	"context"
	"fmt"
	"strings"

	"github.com/JoaoVitorSantDrade/central/internal/core"
)

// Cargo é o teto e o modelo de um cargo do [execucao] (já no canônico).
type Cargo struct {
	Max             int
	Modelo, Esforco string
}

// VagaInput são os dados de abrir uma vaga executada.
type VagaInput struct {
	Vaga, Papel, Area, Nota string
}

// canonicoDe aceita o nome do papel no projeto ou o canônico.
func (e *Engine) canonicoDe(papel string) (string, bool) {
	p := strings.ToUpper(strings.TrimSpace(papel))
	if c, ok := e.roles.Canonical(p); ok {
		return c, true
	}
	if IsCanonical(p) {
		return p, true
	}
	return "", false
}

// podeGerirVagas confere que quem é o GERENTE ou o humano.
func (e *Engine) podeGerirVagas(ctx context.Context, quem, acao string) error {
	if quem == core.Humano {
		return nil
	}
	_, _, err := e.vagaPapel(ctx, quem, acao, Gerente)
	return err
}

// AbrirVaga (GERENTE ou humano) abre uma vaga executada, ou reabre uma
// fechada ou suspensa com a mesma sessão. O cargo precisa estar em cargos, e
// as vagas abertas dele têm de ser menos que o teto. claudeOK é o resultado da
// conferência do Claude Code: com erro, nada é gravado.
func (e *Engine) AbrirVaga(ctx context.Context, quem string, in VagaInput, cargos map[string]Cargo, claudeOK error) (core.VagaExec, error) {
	if err := e.podeGerirVagas(ctx, quem, "abrir vagas"); err != nil {
		return core.VagaExec{}, err
	}
	can, ok := e.canonicoDe(in.Papel)
	if !ok || can == Humano {
		return core.VagaExec{}, fmt.Errorf("%w: papel desconhecido %q", core.ErrInvalid, in.Papel)
	}
	cargo, ok := cargos[can]
	if !ok {
		return core.VagaExec{}, fmt.Errorf("%w: o cargo %s não tem entrada em [execucao.cargos] do central.toml", core.ErrInvalid, e.roles.Name(can))
	}
	if claudeOK != nil {
		return core.VagaExec{}, fmt.Errorf("%w: o Claude Code não está disponível para abrir a vaga: %v", core.ErrInvalid, claudeOK)
	}
	vs, err := e.c.VagasExec(ctx)
	if err != nil {
		return core.VagaExec{}, err
	}
	abertas := 0
	for _, v := range vs {
		if v.Estado != core.VagaAberta || v.Vaga == in.Vaga {
			continue
		}
		if c, ok := e.canonicoDe(v.Papel); ok && c == can {
			abertas++
		}
	}
	if abertas >= cargo.Max {
		return core.VagaExec{}, fmt.Errorf("%w: o cargo %s já tem %d vaga(s) aberta(s), o teto do central.toml", core.ErrConflict, e.roles.Name(can), cargo.Max)
	}
	sprint := ""
	if s, err := e.c.CurrentSprint(ctx); err != nil {
		return core.VagaExec{}, err
	} else if s != nil {
		sprint = s.ID
	}
	return e.c.AbrirVagaExec(ctx, quem, core.VagaExec{Vaga: strings.TrimSpace(in.Vaga), Papel: e.roles.Name(can), Area: in.Area,
		Nota: in.Nota, Modelo: cargo.Modelo, Esforco: cargo.Esforco, Sprint: sprint})
}

// FecharVaga (GERENTE ou humano) fecha a vaga executada: o turno em curso
// termina, e nenhum novo começa.
func (e *Engine) FecharVaga(ctx context.Context, quem, vaga string) (core.VagaExec, error) {
	if err := e.podeGerirVagas(ctx, quem, "fechar vagas"); err != nil {
		return core.VagaExec{}, err
	}
	return e.c.FecharVagaExec(ctx, quem, vaga)
}
