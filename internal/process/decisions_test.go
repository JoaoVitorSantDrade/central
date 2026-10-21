package process

import (
	"errors"
	"testing"

	"github.com/JoaoVitorSantDrade/central/internal/core"
)

func TestDecisaoDuasChaves(t *testing.T) {
	env := timeEnv(t, NewRoles(nil, nil, nil))
	d, err := env.e.AbrirDecisao(ctx, "P", "ligar o recorte novo", "contexto, opções, riscos")
	if err != nil || d.ID != "D-001" || d.Estado != Chave1 || d.Tipo != TipoDecisao {
		t.Fatalf("aberta = %+v, %v", d, err)
	}
	if ms := ofType(env.inbox("G"), "decisao.chave1"); len(ms) != 1 || ms[0].Ref != "D-001" {
		t.Fatalf("GERENTE recebeu %+v", ms)
	}
	d, err = env.e.Chave(ctx, "G", "D-001", true, "")
	if err != nil || d.Estado != Aprovada {
		t.Fatalf("chave2 = %+v, %v", d, err)
	}
	if ms := ofType(env.inbox("P"), "decisao.aprovada"); len(ms) != 1 {
		t.Fatalf("PLANEJADOR recebeu %+v", ms)
	}
}

func TestDecisaoObjecaoESegundaRodada(t *testing.T) {
	env := timeEnv(t, NewRoles(nil, nil, nil))
	env.e.AbrirDecisao(ctx, "P", "x", "y")
	if _, err := env.e.Chave(ctx, "G", "D-001", false, ""); !errors.Is(err, core.ErrInvalid) {
		t.Fatalf("objeção sem motivo: %v", err)
	}
	d, err := env.e.Chave(ctx, "G", "D-001", false, "conflita com a missão")
	if err != nil || d.Estado != Objecao {
		t.Fatalf("objeção = %+v, %v", d, err)
	}
	if ms := ofType(env.inbox("P"), "decisao.objecao"); len(ms) != 1 {
		t.Fatalf("PLANEJADOR recebeu %+v", ms)
	}
	d, err = env.e.RevisarDecisao(ctx, "P", "D-001", "versão menor, desligada por padrão")
	if err != nil || d.Estado != Chave1 {
		t.Fatalf("revista = %+v, %v", d, err)
	}
	// segundo desacordo: vai ao humano
	d, err = env.e.Chave(ctx, "G", "D-001", false, "ainda arriscado")
	if err != nil || d.Estado != AHumano {
		t.Fatalf("segunda objeção = %+v, %v", d, err)
	}
}

func TestDecisaoSoGerenteDaChave2(t *testing.T) {
	env := timeEnv(t, NewRoles(nil, nil, nil))
	env.e.AbrirDecisao(ctx, "P", "x", "y")
	if _, err := env.e.Chave(ctx, "P", "D-001", true, ""); !errors.Is(err, core.ErrInvalid) {
		t.Fatalf("PLANEJADOR deu a chave 2: %v", err)
	}
	if _, err := env.e.AbrirDecisao(ctx, "F1", "x", "y"); !errors.Is(err, core.ErrInvalid) {
		t.Fatalf("executor abriu decisão: %v", err)
	}
}

func TestVetoEmQualquerEstado(t *testing.T) {
	env := timeEnv(t, NewRoles(nil, nil, nil))
	env.e.AbrirDecisao(ctx, "P", "x", "y")
	env.e.Chave(ctx, "G", "D-001", true, "")
	d, err := env.e.Veto(ctx, "D-001", "muda o rumo do produto")
	if err != nil || d.Estado != Vetada {
		t.Fatalf("veto = %+v, %v", d, err)
	}
	if ms := ofType(env.inbox("P"), "decisao.vetada"); len(ms) != 1 {
		t.Fatalf("PLANEJADOR recebeu %+v", ms)
	}
	if ms := ofType(env.inbox("G"), "decisao.vetada"); len(ms) != 1 {
		t.Fatalf("GERENTE recebeu %+v", ms)
	}
	if _, err := env.e.Veto(ctx, "D-001", "de novo"); !errors.Is(err, core.ErrConflict) {
		t.Fatalf("vetou duas vezes: %v", err)
	}
	if _, err := env.e.Acao(ctx, "F1", AcaoReservar, "D-001", Args{}); !errors.Is(err, core.ErrInvalid) {
		t.Fatalf("reservou uma decisão: %v", err)
	}
}

func TestRespostaDoHumano(t *testing.T) {
	env := timeEnv(t, NewRoles(nil, nil, nil))
	env.e.AbrirDecisao(ctx, "P", "x", "y")
	env.e.EscalarDecisao(ctx, "G", "D-001")
	d, err := env.e.ResponderDecisao(ctx, "D-001", true, "pode seguir")
	if err != nil || d.Estado != Aprovada {
		t.Fatalf("resposta = %+v, %v", d, err)
	}
}
