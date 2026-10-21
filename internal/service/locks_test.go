package service

import (
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/JoaoVitorSantDrade/central/internal/adapters/lockdir"
	"github.com/JoaoVitorSantDrade/central/internal/core"
	"github.com/JoaoVitorSantDrade/central/internal/process"
)

// locksFixture escreve um $LOCKS no formato do manual (main/.git/lyriccut-agents).
func locksFixture(t *testing.T, fim time.Time) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "main", ".git", "lyriccut-agents")
	l := func(t time.Time) string { return t.Local().Format(lockdir.LayoutLocal) }
	snap := lockdir.Snapshot{
		Mensagens: []lockdir.Mensagem{
			{ID: "20261020T205624-FIXER2-REVISOR1", De: "FIXER2", Para: "REVISOR1", Assunto: "alerta", Ref: "T-113", Corpo: "T-113 concluida"},
			{ID: "20261020T205700-REVISOR1-FIXER2", De: "REVISOR1", Para: "FIXER2", Assunto: "duvida", Ref: "T-113", Corpo: "vejo já",
				Responde: "20261020T205624-FIXER2-REVISOR1"},
		},
		Pontos: []lockdir.Ponto{
			{ID: "wt-fixer2", Vaga: "FIXER2", Papel: "FIXER", Estado: "trabalhando", Atualizado: "2026-10-20T20:56"},
			{ID: "wt-rev1", Vaga: "REVISOR1", Papel: "REVISOR", Estado: "ocioso", Atualizado: "2026-10-20T20:57"},
		},
		Reservas: []lockdir.Reserva{
			{Tarefa: "T-113", Agente: "wt-fixer2", Estado: "concluida", CommitFinal: "40c34c0", Revisao: &lockdir.Revisao{Veredito: "sim", Ponta: "40c34c0"}},
			{Tarefa: "T-114", Agente: "wt-fixer2", Estado: "em_andamento"},
			{Tarefa: "M-001", Agente: "wt-fixer2", Estado: "concluida", Marco: "3", CommitFinal: "0c3f067"},
			{Tarefa: "T-115", Agente: "wt-fixer2", Estado: "bloqueada", Nota: "não reproduz"},
		},
		E2E:    map[string]json.RawMessage{"b0d54b4-2": json.RawMessage(`{"commit":"b0d54b4","nivel":2}`)},
		IDs:    []string{"T-116", "D-002"},
		Sprint: &lockdir.Sprint{Sprint: "S-20261020-4", Inicio: l(fim.Add(-5 * time.Hour)), Fim: l(fim)},
	}
	if err := lockdir.Export(dir, snap); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestImportarMapeiaEstadosEIdempotente(t *testing.T) {
	s, _ := newService(t)
	mustEnter(t, s, "p", "/g", "G")
	dir := locksFixture(t, time.Now().Add(2*time.Hour))
	r, err := s.ImportLocks(ctx, "p", "/g", dir)
	if err != nil {
		t.Fatal(err)
	}
	if r.Mensagens != 2 || r.Agentes != 2 || r.Itens != 4 || r.Fatos != 1 || r.Sprint != "S-20261020-4" || len(r.Erros) != 0 {
		t.Fatalf("resumo = %+v", r)
	}
	quer := map[string]string{"T-113": process.Aprovada, "T-114": process.EmAndamento, "M-001": process.Concluida, "T-115": process.Pronta}
	for id, estado := range quer {
		it, notas, err := s.VerItem(ctx, "p", "", id)
		if err != nil || it.Estado != estado {
			t.Fatalf("%s = %+v, %v (quer %s)", id, it, err, estado)
		}
		if id == "T-114" && it.Dono != "FIXER2" {
			t.Fatalf("dono de T-114 = %q", it.Dono)
		}
		if id == "M-001" && (it.Tipo != process.TipoMissao || it.Marco != 3) {
			t.Fatalf("M-001 = %+v", it)
		}
		if id == "T-115" && (it.Bloqueios != 1 || len(notas) != 1) {
			t.Fatalf("T-115 = %+v %+v", it, notas)
		}
	}
	// o histórico entra como lido, e a resposta fica ligada à original
	p, _ := s.project(ctx, "p")
	if ms, _ := p.core.Inbox(ctx, "REVISOR1"); len(ms) != 0 {
		t.Fatalf("histórico chegou como novo: %+v", ms)
	}
	all, _ := p.core.MessagesAll(ctx)
	if len(all) != 2 || all[1].RespondeA != all[0].Seq {
		t.Fatalf("mensagens = %+v", all)
	}
	// o próximo T é maior que o maior visto (T-116)
	if id, err := p.core.NextID(ctx, "G", "T", 1); err != nil || id != "T-117" {
		t.Fatalf("próximo id = %s, %v", id, err)
	}
	// reimportar não duplica
	r2, err := s.ImportLocks(ctx, "p", "/g", dir)
	if err != nil || r2.Mensagens != 0 || r2.Agentes != 0 || r2.Itens != 0 || r2.Fatos != 0 || r2.Sprint != "" {
		t.Fatalf("reimportação = %+v, %v", r2, err)
	}
}

func TestImportarIgnoraSprintTerminada(t *testing.T) {
	s, _ := newService(t)
	mustEnter(t, s, "p", "/g", "G")
	r, err := s.ImportLocks(ctx, "p", "/g", locksFixture(t, time.Now().Add(-time.Hour)))
	if err != nil || r.Sprint != "" {
		t.Fatalf("resumo = %+v, %v", r, err)
	}
}

func TestExportarEReimportar(t *testing.T) {
	s, _ := newService(t)
	mustEnter(t, s, "p", "/g", "G")
	if _, err := s.ImportLocks(ctx, "p", "/g", locksFixture(t, time.Now().Add(2*time.Hour))); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), "main", ".git", "lyriccut-agents")
	n, err := s.ExportLocks(ctx, "p", "/g", out)
	if err != nil || n["mensagens"] != 2 || n["reservas"] != 3 || n["e2e"] != 1 {
		t.Fatalf("exportou %+v, %v", n, err)
	}
	// num projeto novo, a reimportação do exportado reproduz o quadro
	mustEnter(t, s, "q", "/h", "G")
	r, err := s.ImportLocks(ctx, "q", "/h", out)
	if err != nil || r.Itens != 3 || r.Mensagens != 2 {
		t.Fatalf("reimportação = %+v, %v", r, err)
	}
	pq, _ := s.project(ctx, "q")
	its, _ := pq.core.Items(ctx, core.ItemFilter{})
	estados := map[string]string{}
	for _, it := range its {
		estados[it.ID] = it.Estado
	}
	if estados["T-113"] != process.Aprovada || estados["T-114"] != process.EmAndamento {
		t.Fatalf("quadro = %+v", estados)
	}
}
