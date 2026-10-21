package lockdir

import (
	"os"
	"path/filepath"
	"testing"
)

const bom = "\xef\xbb\xbf"

func write(t *testing.T, p, s string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
		t.Fatal(err)
	}
}

// Fixture escreve um $LOCKS sintético no formato do manual (com BOM, como o PowerShell grava).
func Fixture(t *testing.T) string {
	t.Helper()
	d := t.TempDir()
	write(t, filepath.Join(d, "canal", "20261020T205624-FIXER2-REVISOR1.json"), bom+`{"id":"20261020T205624-FIXER2-REVISOR1","de":"FIXER2","para":"REVISOR1","assunto":"alerta","ref":"T-113","corpo":"T-113 concluida","precisa_resposta":true,"responde":null}`)
	write(t, filepath.Join(d, "canal", "20261020T205700-REVISOR1-FIXER2.json"), bom+`{"id":"20261020T205700-REVISOR1-FIXER2","de":"REVISOR1","para":"FIXER2","assunto":"duvida","ref":"T-113","corpo":"vejo já","precisa_resposta":false,"responde":"20261020T205624-FIXER2-REVISOR1"}`)
	write(t, filepath.Join(d, "canal", "quebrado.json"), "{nao e json")
	write(t, filepath.Join(d, "equipe", "wt-fixer2.json"), bom+`{"id":"wt-fixer2","vaga":"FIXER2","papel":"FIXER","estado":"trabalhando","tarefa":"T-113","atualizado":"2026-10-20T20:56"}`)
	write(t, filepath.Join(d, "equipe", "wt-rev1.json"), bom+`{"id":"wt-rev1","vaga":"REVISOR1","papel":"REVISOR","estado":"ocioso","atualizado":"2026-10-20T20:57"}`)
	write(t, filepath.Join(d, "T-113", "owner.json"), bom+`{"tarefa":"T-113","agente":"wt-fixer2","estado":"concluida","commit_final":"40c34c0","inicio":"2026-10-20T20:20","revisao":{"veredito":"sim","ponta":"40c34c0","revisor":"REVISOR1"}}`)
	write(t, filepath.Join(d, "T-114", "owner.json"), bom+`{"tarefa":"T-114","agente":"wt-fixer2","estado":"em_andamento","inicio":"2026-10-20T20:58"}`)
	write(t, filepath.Join(d, "M-001", "owner.json"), bom+`{"tarefa":"M-001","agente":"wt-fixer2","estado":"concluida","marco":"3","commit_final":"0c3f067"}`)
	write(t, filepath.Join(d, "e2e", "b0d54b4-2.json"), bom+`{"commit":"b0d54b4","nivel":2,"gates":"14/15"}`)
	if err := os.MkdirAll(filepath.Join(d, "ids", "T-114"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(d, "ids", "D-002"), 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(d, "sprint.json"), bom+`{"sprint":"S-20261020-4","inicio":"2026-10-20T18:45","checkin":"2026-10-20T20:45","congelamento":"2026-10-20T22:45","corte":"2026-10-20T23:15","retro":"2026-10-20T23:25","fim":"2026-10-20T23:45","equipe":[{"vaga":"FIXER2","papel":"FIXER","id":"wt-fixer2"}]}`)
	return d
}

func TestLoadToleraBOMEQuebrados(t *testing.T) {
	s, err := Load(Fixture(t))
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Mensagens) != 2 || len(s.Pontos) != 2 || len(s.Reservas) != 3 || len(s.E2E) != 1 || len(s.IDs) != 2 || s.Sprint == nil {
		t.Fatalf("snapshot = %+v", s)
	}
	if len(s.Erros) != 1 {
		t.Fatalf("erros = %v", s.Erros)
	}
	for _, r := range s.Reservas {
		if r.Tarefa == "M-001" && r.Marco.Int() != 3 {
			t.Fatalf("marco = %q", r.Marco)
		}
	}
	if s.Mensagens[1].Responde != "20261020T205624-FIXER2-REVISOR1" {
		t.Fatalf("responde = %q", s.Mensagens[1].Responde)
	}
}

func TestExportarERelerIgual(t *testing.T) {
	s, _ := Load(Fixture(t))
	s.Erros = nil
	out := t.TempDir()
	if err := Export(out, s); err != nil {
		t.Fatal(err)
	}
	s2, err := Load(out)
	if err != nil {
		t.Fatal(err)
	}
	if len(s2.Mensagens) != 2 || len(s2.Pontos) != 2 || len(s2.Reservas) != 3 || len(s2.E2E) != 1 || len(s2.IDs) != 2 ||
		s2.Sprint == nil || s2.Sprint.Sprint != "S-20261020-4" || len(s2.Erros) != 0 {
		t.Fatalf("relido = %+v", s2)
	}
}

func TestHoraDoID(t *testing.T) {
	h, ok := HoraDoID("20261020T205624-FIXER2-REVISOR1")
	if !ok || h.Hour() != 20 || h.Minute() != 56 || h.Second() != 24 {
		t.Fatalf("hora = %v %v", h, ok)
	}
	if _, ok := HoraDoID("curto"); ok {
		t.Fatal("aceitou id curto")
	}
}
