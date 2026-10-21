package process

import "testing"

func found(estado, id string) Found {
	return Found{Estado: estado, Caminho: "backlog/x/" + id + ".md", In: ItemInput{ID: id, Titulo: "t " + id, Prioridade: "P1"}}
}

func (env *testEnv) sync(fs []Found, inv ...Invalid) Resumo {
	env.t.Helper()
	r, err := env.e.Sincronizar(ctx, fs, inv)
	if err != nil {
		env.t.Fatal(err)
	}
	return r
}

func TestAdaptadorCriaEAvança(t *testing.T) {
	env := timeEnv(t, NewRoles(nil, nil, nil))
	r := env.sync([]Found{found(Proposta, "T-001"), found(Pronta, "T-002")})
	if r.Criados != 2 {
		t.Fatalf("r = %+v", r)
	}
	if r := env.sync([]Found{found(Proposta, "T-001"), found(Pronta, "T-002")}); r != (Resumo{}) {
		t.Fatalf("segunda passada igual mudou algo: %+v", r)
	}
	r = env.sync([]Found{found(Pronta, "T-001"), found(Integrada, "T-002")})
	if r.Avancados != 2 {
		t.Fatalf("r = %+v", r)
	}
	it, _, _ := env.c.Item(ctx, "T-002")
	if it.Estado != Integrada {
		t.Fatalf("T-002 = %+v", it)
	}
}

func TestAdaptadorNuncaVolta(t *testing.T) {
	env := timeEnv(t, NewRoles(nil, nil, nil))
	env.sync([]Found{found(Pronta, "T-001")})
	env.acao("F1", AcaoReservar, "T-001", Args{})
	f := found(Pronta, "T-001")
	f.In.Titulo = "título novo"
	env.sync([]Found{f})
	it, _, _ := env.c.Item(ctx, "T-001")
	if it.Estado != EmAndamento || it.Dono != "F1" || it.Titulo != "título novo" {
		t.Fatalf("reservada depois da sincronização = %+v", it)
	}
	// o arquivo vai para done: integra mesmo reservada
	env.sync([]Found{found(Integrada, "T-001")})
	it, _, _ = env.c.Item(ctx, "T-001")
	if it.Estado != Integrada {
		t.Fatalf("done não integrou: %+v", it)
	}
	// e a fonte voltando para todo não desfaz
	env.sync([]Found{found(Pronta, "T-001")})
	if it, _, _ = env.c.Item(ctx, "T-001"); it.Estado != Integrada {
		t.Fatalf("voltou: %+v", it)
	}
}

func TestAdaptadorArquivoInvalido(t *testing.T) {
	env := timeEnv(t, NewRoles(nil, nil, nil))
	inv := Invalid{Caminho: "backlog/todo/T-009.md", Hash: "abc", Erro: "frontmatter sem id"}
	r := env.sync([]Found{found(Pronta, "T-001")}, inv)
	if r.Criados != 1 || r.Invalidos != 1 {
		t.Fatalf("r = %+v", r)
	}
	if r := env.sync(nil, inv); r.Invalidos != 0 {
		t.Fatalf("repetiu o evento do mesmo arquivo: %+v", r)
	}
}

func TestAdaptadorBloqueioViraNota(t *testing.T) {
	env := timeEnv(t, NewRoles(nil, nil, nil))
	f := found(Pronta, "T-001")
	f.Bloqueio = "não reproduz"
	env.sync([]Found{f})
	env.sync([]Found{f})
	it, notas, _ := env.c.Item(ctx, "T-001")
	if it.Bloqueios != 1 || len(notas) != 1 {
		t.Fatalf("item %+v notas %+v", it, notas)
	}
}
