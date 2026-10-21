package core

import (
	"encoding/json"
	"errors"
	"math"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

// newGrantCore cria um Core com relógio controlável e o recurso gpu do exemplo
// da spec (sem medidor), com as vagas A e B.
func newGrantCore(t *testing.T) (*Core, *time.Time) {
	t.Helper()
	c, now := newCoreClock(t)
	spec := gpuSpec()
	spec.Tipos = map[string]TipoSpec{
		"e2e-nivel1": {Dims: Dims{"vram_mb": 5000, "uso_pct": 80}},
		"e2e-nivel2": {Exclusivo: true},
	}
	spec.PrazoMaxPorPrioridade = map[string]time.Duration{"versao": 120 * time.Minute}
	c.SetSettings(Settings{Resources: map[string]ResourceSpec{"gpu": spec}})
	enter(t, c, "A", "FIXER", "/a")
	enter(t, c, "B", "FIXER", "/b")
	return c, now
}

func request(t *testing.T, c *Core, r GrantRequest) Grant {
	t.Helper()
	if r.Recurso == "" {
		r.Recurso = "gpu"
	}
	if r.Prioridade == "" {
		r.Prioridade = "executor"
	}
	g, err := c.RequestGrant(ctx, r)
	if err != nil {
		t.Fatal(err)
	}
	return g
}

func grantOf(t *testing.T, c *Core, id int64) Grant {
	t.Helper()
	g, err := c.GrantByID(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	return g
}

func tick(t *testing.T, c *Core) {
	t.Helper()
	if err := c.Tick(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestPedidoEntraNaHora(t *testing.T) {
	c, now := newGrantCore(t)
	g := request(t, c, GrantRequest{Vaga: "A", Tipo: "e2e-nivel1", Motivo: "gate", Duracao: 10 * time.Minute})
	if g.Estado != GrantAtiva || g.Inicio == nil || g.Prazo == nil || !g.Prazo.Equal(now.Add(10*time.Minute)) || !g.Sozinha {
		t.Fatalf("concessão = %+v", g)
	}
	ms := systemMessages(inbox(t, c, "A"), "recurso.concedida")
	if len(ms) != 1 || !strings.Contains(ms[0].Corpo, "gpu") || !strings.Contains(ms[0].Corpo, "22:10") {
		t.Fatalf("mensagens = %+v", ms)
	}
	if n := count(t, c, `SELECT count(*) FROM eventos WHERE tipo = 'recurso.concedida'`); n != 1 {
		t.Fatalf("eventos recurso.concedida = %d", n)
	}
}

func TestFilaEAtivacaoAoSoltar(t *testing.T) {
	c, _ := newGrantCore(t)
	a := request(t, c, GrantRequest{Vaga: "A", Exclusivo: true})
	b := request(t, c, GrantRequest{Vaga: "B", Declarado: Dims{"vram_mb": 1000}})
	if a.Estado != GrantAtiva || b.Estado != GrantFila {
		t.Fatalf("a = %s, b = %s", a.Estado, b.Estado)
	}
	if ms := systemMessages(inbox(t, c, "B"), "recurso.concedida"); len(ms) != 0 {
		t.Fatalf("B recebeu antes da vez: %+v", ms)
	}
	solta, err := c.ReleaseGrant(ctx, "A", "gpu")
	if err != nil {
		t.Fatal(err)
	}
	if solta.Estado != GrantEncerrada || solta.FimMotivo != "solta" || solta.Fim == nil {
		t.Fatalf("solta = %+v", solta)
	}
	if g := grantOf(t, c, b.ID); g.Estado != GrantAtiva {
		t.Fatalf("b = %+v", g)
	}
	if ms := systemMessages(inbox(t, c, "B"), "recurso.concedida"); len(ms) != 1 {
		t.Fatalf("B: %+v", ms)
	}
	if _, err := c.ReleaseGrant(ctx, "A", "gpu"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("soltar de novo = %v", err)
	}
}

// runAlone ativa e solta uma concessão do tipo t sozinha, com o pico dado.
func runAlone(t *testing.T, c *Core, tipo string, pico string) {
	t.Helper()
	g := request(t, c, GrantRequest{Vaga: "A", Tipo: tipo, Declarado: Dims{"vram_mb": 100}})
	if g.Estado != GrantAtiva || !g.Sozinha {
		t.Fatalf("g = %+v", g)
	}
	if _, err := c.db.Exec(`UPDATE concessoes SET pico = ? WHERE id = ?`, pico, g.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := c.ReleaseGrant(ctx, "A", "gpu"); err != nil {
		t.Fatal(err)
	}
}

func TestEstimativaPorHistorico(t *testing.T) {
	c, _ := newGrantCore(t)
	runAlone(t, c, "t", `{"vram_mb":3000}`)
	runAlone(t, c, "t", `{"vram_mb":4000}`)
	g := request(t, c, GrantRequest{Vaga: "B", Tipo: "t"})
	if g.Estimativa != EstimativaHistorico || g.Declarado["vram_mb"] != 4800 || g.Exclusivo {
		t.Fatalf("g = %+v", g)
	}
	// O histórico de outro tipo não conta; e a folga nunca passa da capacidade livre.
	if _, err := c.ReleaseGrant(ctx, "B", "gpu"); err != nil {
		t.Fatal(err)
	}
	runAlone(t, c, "grande", `{"vram_mb":5900}`)
	g = request(t, c, GrantRequest{Vaga: "B", Tipo: "grande"})
	if g.Declarado["vram_mb"] != 8192-2200 {
		t.Fatalf("estimativa sem teto: %+v", g)
	}
}

func TestEstimativaTipoEExclusivo(t *testing.T) {
	c, _ := newGrantCore(t)
	g := request(t, c, GrantRequest{Vaga: "A", Tipo: "e2e-nivel1"})
	if g.Estimativa != EstimativaTipo || g.Declarado["vram_mb"] != 5000 || g.Declarado["uso_pct"] != 80 || g.Exclusivo {
		t.Fatalf("tipo: %+v", g)
	}
	g = request(t, c, GrantRequest{Vaga: "B", Tipo: "desconhecido"})
	if g.Estimativa != EstimativaExclusivo || !g.Exclusivo || g.Estado != GrantFila {
		t.Fatalf("exclusivo: %+v", g)
	}
	enter(t, c, "C", "FIXER", "/c")
	g = request(t, c, GrantRequest{Vaga: "C", Tipo: "e2e-nivel1", Declarado: Dims{"vram_mb": 700}})
	if g.Estimativa != EstimativaDeclarada || g.Declarado["vram_mb"] != 700 || len(g.Declarado) != 1 {
		t.Fatalf("declarado: %+v", g)
	}
}

func TestPicoNaoAlimentaQuandoAcompanhada(t *testing.T) {
	c, _ := newGrantCore(t)
	a := request(t, c, GrantRequest{Vaga: "A", Tipo: "t", Declarado: Dims{"vram_mb": 1000}})
	b := request(t, c, GrantRequest{Vaga: "B", Tipo: "t", Declarado: Dims{"vram_mb": 1000}})
	if b.Estado != GrantAtiva || b.Sozinha {
		t.Fatalf("b = %+v", b)
	}
	if g := grantOf(t, c, a.ID); g.Sozinha {
		t.Fatalf("a continua sozinha: %+v", g)
	}
	if _, err := c.db.Exec(`UPDATE concessoes SET pico = '{"vram_mb":900}'`); err != nil {
		t.Fatal(err)
	}
	for _, v := range []string{"A", "B"} {
		if _, err := c.ReleaseGrant(ctx, v, "gpu"); err != nil {
			t.Fatal(err)
		}
	}
	if n := count(t, c, `SELECT count(*) FROM historico_recursos`); n != 0 {
		t.Fatalf("historico = %d linhas", n)
	}
}

func TestPrazoAcimaDoTeto(t *testing.T) {
	c, _ := newGrantCore(t)
	_, err := c.RequestGrant(ctx, GrantRequest{Recurso: "gpu", Vaga: "A", Prioridade: "executor", Declarado: Dims{"vram_mb": 100}, Duracao: 45 * time.Minute})
	if !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "divida") {
		t.Fatalf("45 min executor = %v", err)
	}
	g := request(t, c, GrantRequest{Vaga: "A", Prioridade: "versao", Declarado: Dims{"vram_mb": 100}, Duracao: 90 * time.Minute})
	if g.Duracao != 90*time.Minute || g.Estado != GrantAtiva {
		t.Fatalf("versao = %+v", g)
	}
	g = request(t, c, GrantRequest{Vaga: "B", Declarado: Dims{"vram_mb": 100}})
	if g.Duracao != 30*time.Minute {
		t.Fatalf("sem duração usa o teto: %+v", g)
	}
}

func TestPedidoInvalido(t *testing.T) {
	c, _ := newGrantCore(t)
	casos := []GrantRequest{
		{Recurso: "tpu", Vaga: "A", Prioridade: "executor"},
		{Recurso: "gpu", Vaga: "A", Prioridade: "urgente"},
		{Recurso: "gpu", Vaga: "A", Prioridade: "executor", Declarado: Dims{"vram_mb": 6000}},
		{Recurso: "gpu", Vaga: "A", Prioridade: "executor", Declarado: Dims{"ram_mb": 1}},
		{Recurso: "gpu", Vaga: "A", Prioridade: "executor", Declarado: Dims{"vram_mb": -1}},
		{Recurso: "gpu", Vaga: "A", Prioridade: "executor", Declarado: Dims{"vram_mb": math.NaN()}},
		{Recurso: "gpu", Vaga: "A", Prioridade: "executor", Duracao: -time.Minute},
	}
	for _, r := range casos {
		if _, err := c.RequestGrant(ctx, r); !errors.Is(err, ErrInvalid) {
			t.Errorf("%+v: err = %v", r, err)
		}
	}
	if _, err := c.RequestGrant(ctx, GrantRequest{Recurso: "gpu", Vaga: "X", Prioridade: "executor"}); !errors.Is(err, ErrNotFound) {
		t.Errorf("vaga inexistente: %v", err)
	}
}

func TestSegundoPedidoMesmaVaga(t *testing.T) {
	c, _ := newGrantCore(t)
	g := request(t, c, GrantRequest{Vaga: "A", Declarado: Dims{"vram_mb": 100}, Chave: "k1"})
	_, err := c.RequestGrant(ctx, GrantRequest{Recurso: "gpu", Vaga: "A", Prioridade: "executor", Declarado: Dims{"vram_mb": 100}})
	if !errors.Is(err, ErrConflict) || !strings.Contains(err.Error(), "1") {
		t.Fatalf("segundo pedido = %v", err)
	}
	// A mesma chave devolve o pedido original sem duplicar.
	if again := request(t, c, GrantRequest{Vaga: "A", Declarado: Dims{"vram_mb": 100}, Chave: "k1"}); again.ID != g.ID {
		t.Fatalf("chave repetida = %+v", again)
	}
}

// sysCount conta as mensagens de CENTRAL para a vaga com o tipo, sem ler a caixa.
func sysCount(t *testing.T, c *Core, para, tipo string) int {
	t.Helper()
	var n int
	if err := c.db.QueryRow(`SELECT count(*) FROM mensagens WHERE de = ? AND para = ? AND tipo = ?`, Sistema, para, tipo).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestConcessaoExpiraSemBatimento(t *testing.T) {
	for _, toca := range []bool{false, true} {
		c, now := newGrantCore(t)
		a := request(t, c, GrantRequest{Vaga: "A", Exclusivo: true, Duracao: 10 * time.Minute})
		b := request(t, c, GrantRequest{Vaga: "B", Declarado: Dims{"vram_mb": 100}})
		*now = now.Add(11 * time.Minute)
		if err := c.Touch(ctx, "B"); err != nil {
			t.Fatal(err)
		}
		tick(t, c)
		tick(t, c) // o aviso sai uma vez só
		// Sem ler a caixa de A: ler renova o batimento.
		if n := sysCount(t, c, "A", "recurso.prazo"); n != 1 {
			t.Fatalf("avisos de prazo = %d", n)
		}
		*now = now.Add(4 * time.Minute) // +15 min
		if toca {
			if err := c.Touch(ctx, "A"); err != nil {
				t.Fatal(err)
			}
		}
		*now = now.Add(6 * time.Minute) // +21 min
		tick(t, c)
		ga, gb := grantOf(t, c, a.ID), grantOf(t, c, b.ID)
		if toca {
			if ga.Estado != GrantAtiva || gb.Estado != GrantFila {
				t.Fatalf("com batimento: a = %+v, b = %+v", ga, gb)
			}
			continue
		}
		if ga.Estado != GrantEncerrada || ga.FimMotivo != "expirada" || gb.Estado != GrantAtiva {
			t.Fatalf("sem batimento: a = %+v, b = %+v", ga, gb)
		}
		if ms := systemMessages(inbox(t, c, "A"), "recurso.liberada"); len(ms) != 1 {
			t.Fatalf("aviso de liberação: %+v", ms)
		}
		if ms := systemMessages(inbox(t, c, "B"), "recurso.concedida"); len(ms) != 1 {
			t.Fatalf("B: %+v", ms)
		}
	}
}

func TestLeaveSoltaConcessoes(t *testing.T) {
	c, _ := newGrantCore(t)
	a := request(t, c, GrantRequest{Vaga: "A", Exclusivo: true})
	b := request(t, c, GrantRequest{Vaga: "B", Declarado: Dims{"vram_mb": 100}})
	if _, err := c.Leave(ctx, "A"); err != nil {
		t.Fatal(err)
	}
	if g := grantOf(t, c, a.ID); g.Estado != GrantEncerrada || g.FimMotivo != "solta" {
		t.Fatalf("a = %+v", g)
	}
	if g := grantOf(t, c, b.ID); g.Estado != GrantAtiva {
		t.Fatalf("b = %+v", g)
	}
}

func TestResourceStatus(t *testing.T) {
	c, _ := newGrantCore(t)
	request(t, c, GrantRequest{Vaga: "A", Declarado: Dims{"vram_mb": 1000}})
	request(t, c, GrantRequest{Vaga: "B", Declarado: Dims{"vram_mb": 5000}})
	v, err := c.ResourceStatus(ctx, "gpu")
	if err != nil {
		t.Fatal(err)
	}
	if len(v.Ativas) != 1 || len(v.Fila) != 1 || v.LivreDeclarado["vram_mb"] != 8192-2200-1000 || v.LivreDeclarado["uso_pct"] != 85 || v.Ultima != nil {
		t.Fatalf("view = %+v", v)
	}
	if _, err := c.ResourceStatus(ctx, "tpu"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("recurso desconhecido: %v", err)
	}
}

func TestChaveEmOutroRecurso(t *testing.T) {
	c, _ := newGrantCore(t)
	s := c.currentSettings()
	s.Resources["cpu"] = ResourceSpec{Nome: "cpu", Dimensoes: Dims{"nucleos": 16}, Prioridades: []string{"executor"}, PrazoMax: 30 * time.Minute}
	c.SetSettings(s)
	g := request(t, c, GrantRequest{Vaga: "A", Declarado: Dims{"vram_mb": 100}, Chave: "k1"})
	_, err := c.RequestGrant(ctx, GrantRequest{Recurso: "cpu", Vaga: "A", Prioridade: "executor", Declarado: Dims{"nucleos": 1}, Chave: "k1"})
	if !errors.Is(err, ErrConflict) || !strings.Contains(err.Error(), "gpu") {
		t.Fatalf("chave em outro recurso = %v", err)
	}
	// Outra vaga pode usar a mesma chave; e a original continua idempotente mesmo encerrada.
	if g2 := request(t, c, GrantRequest{Recurso: "cpu", Vaga: "B", Declarado: Dims{"nucleos": 1}, Chave: "k1"}); g2.ID == g.ID {
		t.Fatalf("chave de outra vaga devolveu o pedido de A: %+v", g2)
	}
	if _, err := c.ReleaseGrant(ctx, "A", "gpu"); err != nil {
		t.Fatal(err)
	}
	if again := request(t, c, GrantRequest{Vaga: "A", Declarado: Dims{"vram_mb": 100}, Chave: "k1"}); again.ID != g.ID || again.Estado != GrantEncerrada {
		t.Fatalf("chave repetida depois de soltar = %+v", again)
	}
}

func TestResourceSpecJSON(t *testing.T) {
	spec := gpuSpec()
	spec.TetoMedido = Dims{"vram_mb": 0.95}
	spec.PrazoMaxPorPrioridade = map[string]time.Duration{"versao": 2 * time.Hour}
	spec.Tipos = map[string]TipoSpec{"e2e": {Dims: Dims{"vram_mb": 5000}}, "pesado": {Exclusivo: true}}
	spec.SemUso = &SemUsoSpec{Dimensao: "uso_pct", Limite: 5, Apos: 5 * time.Minute}
	spec.MedidorIntervalo = 15 * time.Second
	b, err := json.Marshal(ResourceView{Spec: spec})
	if err != nil {
		t.Fatal(err)
	}
	var v struct {
		Spec map[string]json.RawMessage `json:"spec"`
	}
	if err := json.Unmarshal(b, &v); err != nil {
		t.Fatal(err)
	}
	var keys []string
	for k := range v.Spec {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	want := []string{"dimensoes", "margem", "medidor_intervalo_ms", "nome", "prazo_max_ms", "prazo_max_por_prioridade_ms",
		"prioridades", "reservado", "sem_uso", "teto_medido", "tipos"}
	if !slices.Equal(keys, want) {
		t.Fatalf("chaves = %v", keys)
	}
	for k, w := range map[string]string{
		"prazo_max_ms":                `1800000`,
		"prazo_max_por_prioridade_ms": `{"versao":7200000}`,
		"medidor_intervalo_ms":        `15000`,
		"sem_uso":                     `{"dimensao":"uso_pct","limite":5,"apos_ms":300000}`,
		"tipos":                       `{"e2e":{"dims":{"vram_mb":5000},"exclusivo":false},"pesado":{"exclusivo":true}}`,
	} {
		if got := string(v.Spec[k]); got != w {
			t.Errorf("%s = %s, quer %s", k, got, w)
		}
	}
	var back ResourceSpec
	raw, _ := json.Marshal(spec)
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatal(err)
	}
	if back.PrazoMax != spec.PrazoMax || back.PrazoMaxPorPrioridade["versao"] != 2*time.Hour ||
		back.MedidorIntervalo != spec.MedidorIntervalo || back.SemUso.Apos != 5*time.Minute {
		t.Fatalf("ida e volta = %+v", back)
	}
}

func TestReleaseGrantByID(t *testing.T) {
	c, _ := newGrantCore(t)
	a := request(t, c, GrantRequest{Vaga: "A", Exclusivo: true})
	b := request(t, c, GrantRequest{Vaga: "B", Declarado: Dims{"vram_mb": 100}})
	if _, err := c.ReleaseGrantByID(ctx, "A", b.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("pedido de outra vaga: %v", err)
	}
	g, err := c.ReleaseGrantByID(ctx, "A", a.ID)
	if err != nil || g.Estado != GrantEncerrada || g.FimMotivo != FimSolta {
		t.Fatalf("soltar o próprio: %+v %v", g, err)
	}
	if g := grantOf(t, c, b.ID); g.Estado != GrantAtiva {
		t.Fatalf("a admissão não rodou: %+v", g)
	}
	// Encerrado, o mesmo id não solta o pedido novo da vaga no recurso.
	novo := request(t, c, GrantRequest{Vaga: "A", Declarado: Dims{"vram_mb": 100}})
	if _, err := c.ReleaseGrantByID(ctx, "A", a.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("pedido já encerrado: %v", err)
	}
	if g := grantOf(t, c, novo.ID); g.Estado == GrantEncerrada {
		t.Fatalf("o pedido novo foi solto: %+v", g)
	}
}

func TestMensagensDeRecursoUsamIDComoRef(t *testing.T) {
	c, now := newGrantCore(t)
	g := request(t, c, GrantRequest{Vaga: "A", Tipo: "e2e-nivel1", Motivo: "gate", Duracao: 10 * time.Minute})
	ms := systemMessages(inbox(t, c, "A"), "recurso.concedida")
	if len(ms) != 1 || ms[0].Ref != strconv.FormatInt(g.ID, 10) {
		t.Fatalf("ref de recurso.concedida = %+v, quer o id %d", ms, g.ID)
	}
	*now = now.Add(11 * time.Minute)
	tick(t, c)
	ms = systemMessages(inbox(t, c, "A"), "recurso.prazo")
	if len(ms) != 1 || strings.Contains(ms[0].Corpo, "m0s") || !strings.Contains(ms[0].Corpo, "10 min") {
		t.Fatalf("corpo de recurso.prazo = %+v", ms)
	}
}

func TestSoltarAcordaQuemEspera(t *testing.T) {
	c, _ := newGrantCore(t)
	request(t, c, GrantRequest{Vaga: "A", Exclusivo: true, Motivo: "x", Duracao: 10 * time.Minute})
	b := request(t, c, GrantRequest{Vaga: "B", Exclusivo: true, Motivo: "y", Duracao: 10 * time.Minute})
	ch := c.Changed()
	// B solta o próprio pedido da fila: nada é admitido, mas quem espera por ele precisa acordar.
	if _, err := c.ReleaseGrantByID(ctx, "B", b.ID); err != nil {
		t.Fatal(err)
	}
	select {
	case <-ch:
	default:
		t.Fatal("soltar um pedido sem admitir ninguém não acordou quem espera")
	}
}
