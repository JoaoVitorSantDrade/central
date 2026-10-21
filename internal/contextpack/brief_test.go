package contextpack

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/JoaoVitorSantDrade/central/internal/core"
)

func entrada() Input {
	agora := time.Date(2026, 10, 20, 22, 30, 0, 0, time.UTC)
	return Input{
		Agente: core.Agent{Vaga: "F1", Papel: "FIXER", Pasta: "/wt/f1", Area: "core"}, Canonico: "EXECUTOR",
		Projeto: "lyriccut", DataDir: "/dados",
		Sprint: &core.Sprint{ID: "S-1", Abertura: agora.Add(-time.Hour), Checkin: agora.Add(time.Hour), Escopo: agora.Add(3 * time.Hour),
			Congelamento: agora.Add(4 * time.Hour), Corte: agora.Add(5 * time.Hour), Retro: agora.Add(5*time.Hour + 10*time.Minute), Fim: agora.Add(6 * time.Hour)},
		Reserva: &core.Item{ID: "T-001", Titulo: "corrigir travas", Estado: "em_andamento"},
		Caixa:   []core.Message{{Seq: 3, De: "CENTRAL", Tipo: "item.rejeitada", Ref: "T-002", Corpo: "falta teste"}},
		Cartao:  "cartão do executor", Nucleo: "invariantes do projeto",
		Regras:  []core.Rule{{ID: "L-001", Pratica: "rode o teste na develop antes"}},
		Fatos:   []core.Fact{{Chave: "e2e.base", Valor: json.RawMessage(`{"commit":"abc"}`), Vaga: "I", Quando: agora}},
		Leitura: []Leitura{{Caminho: "docs/ARQ.md", Resumo: "arquitetura", Quando: "antes de mexer no backend"}},
		Agora:   agora,
	}
}

func TestBriefOrdemDosBlocos(t *testing.T) {
	b := Montar(entrada(), false, 8000)
	if len(b.Blocos) != 8 || len(b.Cortados) != 0 {
		t.Fatalf("blocos = %d cortados %v", len(b.Blocos), b.Cortados)
	}
	ultimo := -1
	for _, trecho := range []string{"F1", "S-1", "item.rejeitada", "cartão do executor", "invariantes", "L-001", "e2e.base", "docs/ARQ.md"} {
		i := strings.Index(b.Texto, trecho)
		if i < 0 || i < ultimo {
			t.Fatalf("%q fora de ordem ou ausente (%d depois de %d)\n%s", trecho, i, ultimo, b.Texto)
		}
		ultimo = i
	}
	if b.Tokens != len(b.Texto)/4 || !strings.Contains(b.Texto, "T-001") || !strings.Contains(b.Texto, "concluir") {
		t.Fatalf("tokens %d ou reserva ausente\n%s", b.Tokens, b.Texto)
	}
}

func TestBriefCurto(t *testing.T) {
	b := Montar(entrada(), true, 8000)
	if len(b.Blocos) != 3 || strings.Contains(b.Texto, "cartão do executor") || !strings.Contains(b.Texto, "item.rejeitada") {
		t.Fatalf("curto = %+v", b)
	}
}

func TestBriefCortaDoFim(t *testing.T) {
	in := entrada()
	in.Leitura = nil
	for i := 0; i < 40; i++ {
		in.Leitura = append(in.Leitura, Leitura{Caminho: strings.Repeat("x", 100), Resumo: strings.Repeat("r", 100)})
	}
	cheio := Montar(in, false, 100000)
	aviso := Tokens(fmt.Sprintf("## 8. Mapa de leitura\n\n%s\n", avisoCorte)) // o bloco cortado ainda leva o aviso
	sem8 := cheio.Tokens - cheio.Blocos[7].Tokens + aviso + 5
	b := Montar(in, false, sem8)
	if len(b.Cortados) != 1 || b.Cortados[0] != 8 || !b.Blocos[7].Cortado {
		t.Fatalf("cortados = %v", b.Cortados)
	}
	b = Montar(in, false, 50)
	if len(b.Cortados) != 3 || b.Cortados[0] != 8 || b.Cortados[2] != 6 {
		t.Fatalf("cortados = %v", b.Cortados)
	}
}

func TestBriefNucleoGrandeNuncaCortaBloco1a5(t *testing.T) {
	in := entrada()
	in.Nucleo = strings.Repeat("regra de ouro. ", 4000) // ~15 mil tokens
	b := Montar(in, false, 8000)
	if !strings.Contains(b.Texto, in.Nucleo) || len(b.Cortados) != 3 || !strings.Contains(b.Texto, "teto") {
		t.Fatalf("cortados %v, núcleo presente %v", b.Cortados, strings.Contains(b.Texto, in.Nucleo))
	}
	for _, bl := range b.Blocos[:5] {
		if bl.Cortado {
			t.Fatalf("bloco %d cortado", bl.N)
		}
	}
}

func TestBriefSemReservaSugereProxima(t *testing.T) {
	in := entrada()
	in.Reserva = nil
	if b := Montar(in, true, 8000); !strings.Contains(b.Texto, "central proxima") {
		t.Fatalf("sem reserva:\n%s", b.Texto)
	}
}

func TestFatosPorPadrao(t *testing.T) {
	fs := []core.Fact{{Chave: "e2e.base.nivel2"}, {Chave: "e2e.outro"}, {Chave: "gpu.zero"}}
	got := FiltrarFatos(fs, []string{"e2e.base*", "gpu.zero"})
	if len(got) != 2 || got[0].Chave != "e2e.base.nivel2" || got[1].Chave != "gpu.zero" {
		t.Fatalf("fatos = %+v", got)
	}
	if got := FiltrarFatos(fs, nil); len(got) != 0 {
		t.Fatalf("sem padrões = %+v", got)
	}
}
