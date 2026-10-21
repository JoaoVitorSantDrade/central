package process

import "testing"

func TestRolesRenomeiaEDesliga(t *testing.T) {
	r := NewRoles(map[string]string{Executor: "FIXER"}, []string{Validador}, []string{"FIXER_SENIOR"})
	if c, ok := r.Canonical("FIXER"); !ok || c != Executor {
		t.Fatalf("Canonical(FIXER) = %q, %v", c, ok)
	}
	if r.Name(Executor) != "FIXER" || r.Name(Revisor) != Revisor {
		t.Fatalf("Name = %q / %q", r.Name(Executor), r.Name(Revisor))
	}
	if r.Enabled(Validador) || !r.Enabled(Revisor) {
		t.Fatal("Enabled errado")
	}
	if !r.IsSenior("FIXER_SENIOR") || r.IsSenior("FIXER1") {
		t.Fatal("IsSenior errado")
	}
	// o nome canônico de um papel renomeado não vale mais como papel do projeto
	if _, ok := r.Canonical(Executor); ok {
		t.Fatal("EXECUTOR ainda aceito depois de renomeado")
	}
}

func TestRolesPapelDesconhecido(t *testing.T) {
	r := NewRoles(nil, nil, nil)
	if _, ok := r.Canonical("COZINHEIRO"); ok {
		t.Fatal("papel desconhecido aceito")
	}
	if c, ok := r.Canonical(Revisor); !ok || c != Revisor {
		t.Fatalf("Canonical(REVISOR) = %q, %v", c, ok)
	}
	if c, ok := r.Canonical(Humano); !ok || c != Humano {
		t.Fatalf("Canonical(HUMANO) = %q, %v", c, ok)
	}
}
