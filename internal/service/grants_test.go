package service

import (
	"testing"
	"time"

	"github.com/JoaoVitorSantDrade/central/internal/core"
)

const configGPU = `[projeto]
id = "p"

[recursos.gpu]
dimensoes   = { vram_mb = 8192, uso_pct = 100 }
reservado   = { vram_mb = 2200, uso_pct = 15 }
prioridades = ["versao", "executor"]
`

func TestConcessoesSobrevivemReinicio(t *testing.T) {
	dir := t.TempDir()
	s, err := New(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := enterWith(s, "/a", "A1", configGPU); err != nil {
		t.Fatal(err)
	}
	if _, err := enterWith(s, "/b", "B1", ""); err != nil {
		t.Fatal(err)
	}
	a, err := s.RequestGrant(ctx, "p", "/a", core.GrantRequest{Recurso: "gpu", Exclusivo: true})
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.RequestGrant(ctx, "p", "/b", core.GrantRequest{Recurso: "gpu", Declarado: core.Dims{"vram_mb": 1000}})
	if err != nil {
		t.Fatal(err)
	}
	if a.Estado != core.GrantAtiva || b.Estado != core.GrantFila {
		t.Fatalf("a = %s, b = %s", a.Estado, b.Estado)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	s, err = New(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	if err := s.OpenAll(ctx); err != nil {
		t.Fatal(err)
	}
	if err := s.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	v, err := s.ResourceStatus(ctx, "p", "", "gpu")
	if err != nil {
		t.Fatal(err)
	}
	if len(v.Ativas) != 1 || v.Ativas[0].ID != a.ID || len(v.Fila) != 1 || v.Fila[0].ID != b.ID {
		t.Fatalf("depois de reabrir: %+v", v)
	}
	if _, err := s.ReleaseGrant(ctx, "p", "/a", "gpu"); err != nil {
		t.Fatal(err)
	}
	r, err := s.WaitGrant(ctx, "p", "/b", "gpu", b.ID, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if r.Motivo != MotivoConcedida || r.Concessao.Estado != core.GrantAtiva {
		t.Fatalf("b depois de soltar a: %+v", r)
	}
}

func TestWaitGrantAcordaAoSoltar(t *testing.T) {
	s, _ := newService(t)
	if _, err := enterWith(s, "/a", "A1", configGPU); err != nil {
		t.Fatal(err)
	}
	if _, err := enterWith(s, "/b", "B1", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RequestGrant(ctx, "p", "/a", core.GrantRequest{Recurso: "gpu", Exclusivo: true}); err != nil {
		t.Fatal(err)
	}
	b, err := s.RequestGrant(ctx, "p", "/b", core.GrantRequest{Recurso: "gpu", Exclusivo: true})
	if err != nil {
		t.Fatal(err)
	}
	if r, err := s.WaitGrant(ctx, "p", "/b", "gpu", b.ID, 50*time.Millisecond); err != nil || r.Motivo != MotivoTempo {
		t.Fatalf("na fila: %+v %v", r, err)
	}
	go func() {
		time.Sleep(100 * time.Millisecond)
		s.ReleaseGrant(ctx, "p", "/a", "gpu")
	}()
	start := time.Now()
	r, err := s.WaitGrant(ctx, "p", "/b", "gpu", b.ID, 5*time.Second)
	if err != nil || r.Motivo != MotivoConcedida || time.Since(start) > 2*time.Second {
		t.Fatalf("r = %+v, err = %v, %v", r, err, time.Since(start))
	}
}
