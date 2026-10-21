package service

import (
	"testing"
	"time"

	"github.com/JoaoVitorSantDrade/central/internal/core"
)

func TestAmostrasDoPeriodo(t *testing.T) {
	s, _ := newService(t)
	if _, err := enterWith(s, "/a", "A1", configGPU); err != nil {
		t.Fatal(err)
	}
	p, err := s.project(ctx, "p")
	if err != nil {
		t.Fatal(err)
	}
	agora := p.core.Now()
	for i := 3; i >= 1; i-- {
		if err := p.core.RecordSample(ctx, "gpu", core.Sample{Quando: agora.Add(-time.Duration(i) * time.Minute), Valores: core.Dims{"vram_mb": float64(1000 * i)}}); err != nil {
			t.Fatal(err)
		}
	}
	sr, err := s.Amostras(ctx, "p", "gpu", agora.Add(-150*time.Second), agora)
	if err != nil {
		t.Fatal(err)
	}
	if len(sr.Amostras) != 2 || sr.Amostras[0].Valores["vram_mb"] != 2000 {
		t.Fatalf("amostras = %+v", sr.Amostras)
	}
	if _, err := s.Amostras(ctx, "p", "nada", agora.Add(-time.Hour), agora); err == nil {
		t.Fatal("recurso desconhecido deveria falhar")
	}
}

func TestProjetosNaoCaiPorUmProjetoQuebrado(t *testing.T) {
	s, _ := newService(t)
	if _, err := enterWith(s, "/a", "A1", configGPU); err != nil {
		t.Fatal(err)
	}
	if _, err := s.registry.Exec(`INSERT INTO projetos (id, nome, raiz, ultimo_acesso) VALUES ('Quebrado!', 'Q', '/q', '2026-10-21T00:00:00.000Z')`); err != nil {
		t.Fatal(err)
	}
	ps, err := s.Projetos(ctx)
	if err != nil {
		t.Fatalf("um projeto quebrado derrubou a lista: %v", err)
	}
	if len(ps) != 2 {
		t.Fatalf("projetos = %+v", ps)
	}
	var bom, ruim *ProjetoResumo
	for i := range ps {
		if ps[i].ID == "p" {
			bom = &ps[i]
		} else {
			ruim = &ps[i]
		}
	}
	if bom == nil || bom.Erro != "" || ruim == nil || ruim.Erro == "" {
		t.Fatalf("bom = %+v, ruim = %+v", bom, ruim)
	}
}
