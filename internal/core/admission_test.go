package core

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"
)

var t0 = time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)

// gpuSpec é a GPU do exemplo da spec §5.3 (dimensões genéricas: o núcleo não sabe o que são).
func gpuSpec() ResourceSpec {
	return ResourceSpec{
		Nome:        "gpu",
		Dimensoes:   Dims{"vram_mb": 8192, "uso_pct": 100},
		Reservado:   Dims{"vram_mb": 2200, "uso_pct": 15},
		Margem:      Dims{"vram_mb": 512},
		Prioridades: []string{"versao", "bug-p0", "missao", "executor", "pesquisa"},
		PrazoMax:    30 * time.Minute,
	}
}

// ativa é uma concessão ativa que vence em prazo.
func ativa(id int64, decl Dims, prazo time.Duration) Grant {
	ini, fim := t0.Add(-time.Minute), t0.Add(prazo)
	return Grant{ID: id, Recurso: "gpu", Estado: GrantAtiva, Prioridade: "executor", Declarado: decl,
		Duracao: prazo + time.Minute, PedidoEm: ini, Inicio: &ini, Prazo: &fim}
}

// pedido é uma concessão na fila; ordem desempata a chegada.
func pedido(id int64, prio string, decl Dims, dur time.Duration, ordem int) Grant {
	return Grant{ID: id, Recurso: "gpu", Estado: GrantFila, Prioridade: prio, Declarado: decl,
		Duracao: dur, PedidoEm: t0.Add(-time.Hour + time.Duration(ordem)*time.Second)}
}

func admit(spec ResourceSpec, ativas, fila []Grant, medido Dims) []int64 {
	return Admit(AdmissionInput{Spec: spec, Ativas: ativas, Fila: fila, Medido: medido, Now: t0})
}

func TestAdmitDeclarado(t *testing.T) {
	// Livre: 8192 − 2200 = 5992; com 4000 ativo sobram 1992.
	ativas := []Grant{ativa(1, Dims{"vram_mb": 4000}, 20*time.Minute)}
	casos := []struct {
		nome string
		vram float64
		quer []int64
	}{
		{"cabe no limite", 1992, []int64{10}},
		{"passa por 8", 2000, []int64{}},
		{"passa", 2500, []int64{}},
	}
	for _, c := range casos {
		t.Run(c.nome, func(t *testing.T) {
			fila := []Grant{pedido(10, "executor", Dims{"vram_mb": c.vram}, 5*time.Minute, 0)}
			if got := admit(gpuSpec(), ativas, fila, nil); !slices.Equal(got, c.quer) {
				t.Fatalf("Admit = %v, quer %v", got, c.quer)
			}
		})
	}
}

func TestAdmitMedido(t *testing.T) {
	fila := []Grant{pedido(10, "executor", Dims{"vram_mb": 1000}, 5*time.Minute, 0)}
	// 7000 + 1000 + 512 = 8512 > 8192.
	if got := admit(gpuSpec(), nil, fila, Dims{"vram_mb": 7000}); len(got) != 0 {
		t.Fatalf("com medido alto, Admit = %v", got)
	}
	// Sem leitura recente vale só a conta declarada.
	if got := admit(gpuSpec(), nil, fila, nil); !slices.Equal(got, []int64{10}) {
		t.Fatalf("sem medido, Admit = %v", got)
	}
	// 6600 + 1000 + 512 = 8112 ≤ 8192.
	if got := admit(gpuSpec(), nil, fila, Dims{"vram_mb": 6600}); !slices.Equal(got, []int64{10}) {
		t.Fatalf("com medido que cabe, Admit = %v", got)
	}
}

func TestAdmitMedidoSomaOsAdmitidos(t *testing.T) {
	// Cada um cabe sozinho no medido (6000 + 1200 + 512 ≤ 8192); o segundo não (7200 + 1200 + 512 > 8192).
	fila := []Grant{
		pedido(10, "executor", Dims{"vram_mb": 1200}, 5*time.Minute, 0),
		pedido(11, "executor", Dims{"vram_mb": 1200}, 5*time.Minute, 1),
	}
	if got := admit(gpuSpec(), nil, fila, Dims{"vram_mb": 6000}); !slices.Equal(got, []int64{10}) {
		t.Fatalf("Admit = %v", got)
	}
}

func TestAdmitTeto(t *testing.T) {
	spec := gpuSpec()
	spec.TetoMedido = Dims{"vram_mb": 0.95} // 0.95 × 8192 = 7782.4
	// O pedido só declara uso_pct: o que barra é o teto da vram medida.
	fila := []Grant{pedido(10, "executor", Dims{"uso_pct": 10}, 5*time.Minute, 0)}
	if got := admit(spec, nil, fila, Dims{"vram_mb": 7900, "uso_pct": 20}); len(got) != 0 {
		t.Fatalf("acima do teto, Admit = %v", got)
	}
	if got := admit(spec, nil, fila, Dims{"vram_mb": 7700, "uso_pct": 20}); !slices.Equal(got, []int64{10}) {
		t.Fatalf("abaixo do teto, Admit = %v", got)
	}
}

func TestAdmitExclusivo(t *testing.T) {
	excl := pedido(10, "executor", nil, 10*time.Minute, 0)
	excl.Exclusivo = true
	pequeno := pedido(11, "executor", Dims{"vram_mb": 100}, 5*time.Minute, 1)

	ativas := []Grant{ativa(1, Dims{"vram_mb": 100}, 20*time.Minute)}
	if got := admit(gpuSpec(), ativas, []Grant{excl}, nil); len(got) != 0 {
		t.Fatalf("exclusivo com ativo, Admit = %v", got)
	}
	if got := admit(gpuSpec(), nil, []Grant{excl, pequeno}, nil); !slices.Equal(got, []int64{10}) {
		t.Fatalf("exclusivo sem ativos, Admit = %v", got)
	}
	// Com um exclusivo ativo, nada entra.
	ex := ativa(2, nil, 20*time.Minute)
	ex.Exclusivo = true
	if got := admit(gpuSpec(), []Grant{ex}, []Grant{pequeno}, nil); len(got) != 0 {
		t.Fatalf("com exclusivo ativo, Admit = %v", got)
	}
}

func TestAdmitPrioridade(t *testing.T) {
	ativas := []Grant{ativa(1, Dims{"vram_mb": 3000}, 20*time.Minute)}
	fila := []Grant{
		pedido(10, "executor", Dims{"vram_mb": 2000}, 5*time.Minute, 0), // mais antigo
		pedido(11, "versao", Dims{"vram_mb": 2000}, 5*time.Minute, 1),
	}
	if got := admit(gpuSpec(), ativas, fila, nil); !slices.Equal(got, []int64{11}) {
		t.Fatalf("Admit = %v, quer [11]", got)
	}
}

func TestAdmitBackfill(t *testing.T) {
	// Ativo até now + 20m com 5000 dos 5992 livres; a cabeça (3000) só cabe depois dele.
	ativas := []Grant{ativa(1, Dims{"vram_mb": 5000}, 20*time.Minute)}
	cabeca := pedido(10, "executor", Dims{"vram_mb": 3000}, 10*time.Minute, 0)
	curto := pedido(11, "executor", Dims{"vram_mb": 500}, 10*time.Minute, 1)
	longo := pedido(12, "executor", Dims{"vram_mb": 500}, 30*time.Minute, 2)

	if got := EstimatedStart(gpuSpec(), ativas, cabeca, t0); !got.Equal(t0.Add(20 * time.Minute)) {
		t.Fatalf("EstimatedStart = %v", got)
	}
	if got := admit(gpuSpec(), ativas, []Grant{cabeca, curto, longo}, nil); !slices.Equal(got, []int64{11}) {
		t.Fatalf("Admit = %v, quer [11]", got)
	}
}

func TestAdmitDimensaoAusente(t *testing.T) {
	// uso_pct está todo tomado (15 reservado + 85 ativo) e o medido diz 100 %,
	// mas o pedido não declara uso_pct: as contas dessa dimensão não o barram.
	ativas := []Grant{ativa(1, Dims{"uso_pct": 85}, 20*time.Minute)}
	fila := []Grant{pedido(10, "executor", Dims{"vram_mb": 1000}, 5*time.Minute, 0)}
	if got := admit(gpuSpec(), ativas, fila, Dims{"vram_mb": 3000, "uso_pct": 100}); !slices.Equal(got, []int64{10}) {
		t.Fatalf("Admit = %v", got)
	}
}

func TestEstimatedStart(t *testing.T) {
	ativas := []Grant{
		ativa(1, Dims{"vram_mb": 2000}, 30*time.Minute),
		ativa(2, Dims{"vram_mb": 2000}, 10*time.Minute),
		ativa(3, Dims{"vram_mb": 1500}, 20*time.Minute),
	}
	casos := []struct {
		nome string
		g    Grant
		quer time.Time
	}{
		{"já cabe", pedido(10, "executor", Dims{"vram_mb": 400}, time.Minute, 0), t0},
		{"libera o primeiro", pedido(10, "executor", Dims{"vram_mb": 2000}, time.Minute, 0), t0.Add(10 * time.Minute)},
		{"libera dois", pedido(10, "executor", Dims{"vram_mb": 3000}, time.Minute, 0), t0.Add(20 * time.Minute)},
		{"exclusivo espera todos", Grant{ID: 10, Exclusivo: true}, t0.Add(30 * time.Minute)},
	}
	for _, c := range casos {
		t.Run(c.nome, func(t *testing.T) {
			if got := EstimatedStart(gpuSpec(), ativas, c.g, t0); !got.Equal(c.quer) {
				t.Fatalf("EstimatedStart = %v, quer %v", got, c.quer)
			}
		})
	}
	if got := EstimatedStart(gpuSpec(), nil, Grant{Exclusivo: true}, t0); !got.Equal(t0) {
		t.Fatalf("exclusivo sem ativos = %v", got)
	}
}

func TestSortQueue(t *testing.T) {
	fila := []Grant{
		pedido(4, "pesquisa", nil, time.Minute, 0),
		pedido(3, "executor", nil, time.Minute, 2),
		pedido(2, "executor", nil, time.Minute, 1),
		pedido(1, "executor", nil, time.Minute, 1), // mesma chegada que o 2: desempata pelo ID
		pedido(5, "versao", nil, time.Minute, 9),
	}
	SortQueue(gpuSpec(), fila)
	var ids []int64
	for _, g := range fila {
		ids = append(ids, g.ID)
	}
	if !slices.Equal(ids, []int64{5, 1, 2, 3, 4}) {
		t.Fatalf("ordem = %v", ids)
	}
}

func TestGrantJSON(t *testing.T) {
	g := ativa(7, Dims{"vram_mb": 4500}, 25*time.Minute)
	g.Tipo, g.Estimativa = "e2e-nivel1", EstimativaDeclarada
	b, err := json.Marshal(g)
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	for _, q := range []string{`"id":7`, `"recurso":"gpu"`, `"estado":"ativa"`, `"declarado":{"vram_mb":4500}`,
		`"duracao_ms":1560000`, `"pedido_em":`, `"prazo":`, `"estimativa":"declarada"`} {
		if !strings.Contains(s, q) {
			t.Errorf("JSON %s não contém %s", s, q)
		}
	}
	var volta Grant
	if err := json.Unmarshal(b, &volta); err != nil {
		t.Fatal(err)
	}
	if volta.Duracao != g.Duracao || volta.ID != 7 || !volta.Prazo.Equal(*g.Prazo) {
		t.Fatalf("ida e volta = %+v", volta)
	}
}

func TestAdmitCabecaBarradaSoPeloMedidoParaOBackfill(t *testing.T) {
	// A cabeça cabe na conta declarada e só a medida a barra (7000 + 1000 + 512 > 8192).
	// Pela conta declarada ela "começaria agora", então nenhum pedido de prazo
	// positivo termina antes disso: o backfill para, mesmo o pedido 11 cabendo
	// na medida (7000 + 100 + 512 ≤ 8192). Comportamento intencional, documentado em Admit.
	fila := []Grant{
		pedido(10, "executor", Dims{"vram_mb": 1000}, 5*time.Minute, 0),
		pedido(11, "executor", Dims{"vram_mb": 100}, time.Minute, 1),
	}
	if got := admit(gpuSpec(), nil, fila, Dims{"vram_mb": 7000}); len(got) != 0 {
		t.Fatalf("Admit = %v, quer nenhum", got)
	}
}
