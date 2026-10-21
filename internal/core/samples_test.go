package core

import (
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

// newMeterCore é o newGrantCore com o recurso gpu medido a cada 15 s.
func newMeterCore(t *testing.T, mudar func(*ResourceSpec)) (*Core, *time.Time) {
	t.Helper()
	c, now := newCoreClock(t)
	spec := gpuSpec()
	spec.MedidorIntervalo = 15 * time.Second
	if mudar != nil {
		mudar(&spec)
	}
	c.SetSettings(Settings{Resources: map[string]ResourceSpec{"gpu": spec}})
	enter(t, c, "A", "FIXER", "/a")
	enter(t, c, "B", "FIXER", "/b")
	return c, now
}

func record(t *testing.T, c *Core, quando time.Time, valores Dims) {
	t.Helper()
	if err := c.RecordSample(ctx, "gpu", Sample{Quando: quando, Valores: valores}); err != nil {
		t.Fatal(err)
	}
}

func TestAdmissaoUsaMedido(t *testing.T) {
	c, now := newMeterCore(t, nil)
	record(t, c, *now, Dims{"vram_mb": 7000, "uso_pct": 30})
	// 7000 + 1000 + 512 > 8192: fica na fila, embora caiba na conta declarada.
	g := request(t, c, GrantRequest{Vaga: "A", Declarado: Dims{"vram_mb": 1000}})
	if g.Estado != GrantFila {
		t.Fatalf("com 7000 medidos: %+v", g)
	}
	*now = now.Add(15 * time.Second)
	record(t, c, *now, Dims{"vram_mb": 3000, "uso_pct": 30})
	if g := grantOf(t, c, g.ID); g.Estado != GrantAtiva {
		t.Fatalf("com 3000 medidos: %+v", g)
	}
	if n := count(t, c, `SELECT count(*) FROM eventos WHERE tipo = 'medidor.sem_leitura'`); n != 0 {
		t.Fatalf("medidor.sem_leitura com leitura recente: %d", n)
	}
}

func TestAdmissaoSomaAtivasDepoisDaAmostra(t *testing.T) {
	c, now := newMeterCore(t, nil)
	record(t, c, *now, Dims{"vram_mb": 5000})
	*now = now.Add(time.Second)
	// 5000 + 1500 + 512 ≤ 8192: A entra. A leitura ainda não viu A, então
	// B conta com ela: 5000 + 1500 + 1500 + 512 > 8192.
	a := request(t, c, GrantRequest{Vaga: "A", Declarado: Dims{"vram_mb": 1500}})
	b := request(t, c, GrantRequest{Vaga: "B", Declarado: Dims{"vram_mb": 1500}})
	if a.Estado != GrantAtiva || b.Estado != GrantFila {
		t.Fatalf("a = %s, b = %s", a.Estado, b.Estado)
	}
	// Uma leitura depois do início de A já a inclui: 6500 + 1500 + 512 ≤ 8192.
	*now = now.Add(time.Second)
	record(t, c, *now, Dims{"vram_mb": 6100})
	if g := grantOf(t, c, b.ID); g.Estado != GrantAtiva {
		t.Fatalf("b depois da leitura nova: %+v", g)
	}
}

func TestAmostraVelhaIgnorada(t *testing.T) {
	c, now := newMeterCore(t, nil)
	record(t, c, *now, Dims{"vram_mb": 7000})
	*now = now.Add(46 * time.Second) // mais de 3 × 15 s
	if s, err := c.LatestSample(ctx, "gpu", 45*time.Second); err != nil || s != nil {
		t.Fatalf("LatestSample = %+v, %v", s, err)
	}
	if s, err := c.LatestSample(ctx, "gpu", 0); err != nil || s == nil || s.Valores["vram_mb"] != 7000 {
		t.Fatalf("LatestSample sem limite = %+v, %v", s, err)
	}
	g := request(t, c, GrantRequest{Vaga: "A", Declarado: Dims{"vram_mb": 1000}})
	if g.Estado != GrantAtiva {
		t.Fatalf("com leitura velha vale só a conta declarada: %+v", g)
	}
	if n := count(t, c, `SELECT count(*) FROM eventos WHERE tipo = 'medidor.sem_leitura' AND ref = 'gpu'`); n != 1 {
		t.Fatalf("medidor.sem_leitura = %d", n)
	}
	// Outra admissão sem leitura dentro do mesmo minuto não grava outro evento.
	*now = now.Add(30 * time.Second)
	request(t, c, GrantRequest{Vaga: "B", Declarado: Dims{"vram_mb": 1000}})
	if n := count(t, c, `SELECT count(*) FROM eventos WHERE tipo = 'medidor.sem_leitura'`); n != 1 {
		t.Fatalf("medidor.sem_leitura no mesmo minuto = %d", n)
	}
	if _, err := c.ReleaseGrant(ctx, "B", "gpu"); err != nil {
		t.Fatal(err)
	}
	*now = now.Add(31 * time.Second)
	request(t, c, GrantRequest{Vaga: "B", Declarado: Dims{"vram_mb": 1000}})
	if n := count(t, c, `SELECT count(*) FROM eventos WHERE tipo = 'medidor.sem_leitura'`); n != 2 {
		t.Fatalf("medidor.sem_leitura depois de um minuto = %d", n)
	}
}

func TestPicoEHistorico(t *testing.T) {
	c, now := newMeterCore(t, nil)
	g := request(t, c, GrantRequest{Vaga: "A", Tipo: "e2e", Declarado: Dims{"vram_mb": 4000}})
	if g.Estado != GrantAtiva || !g.Sozinha {
		t.Fatalf("g = %+v", g)
	}
	*now = now.Add(15 * time.Second)
	record(t, c, *now, Dims{"vram_mb": 2200 + 1000, "uso_pct": 10})
	*now = now.Add(15 * time.Second)
	record(t, c, *now, Dims{"vram_mb": 2200 + 3500, "uso_pct": 60})
	*now = now.Add(15 * time.Second)
	record(t, c, *now, Dims{"vram_mb": 2200 + 2000, "uso_pct": 40})
	if g := grantOf(t, c, g.ID); g.Pico["vram_mb"] != 3500 || g.Pico["uso_pct"] != 45 {
		t.Fatalf("pico = %v", g.Pico)
	}
	if _, err := c.ReleaseGrant(ctx, "A", "gpu"); err != nil {
		t.Fatal(err)
	}
	var pico string
	if err := c.db.QueryRow(`SELECT pico FROM historico_recursos WHERE recurso = 'gpu' AND tipo = 'e2e'`).Scan(&pico); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(pico, `"vram_mb":3500`) || !strings.Contains(pico, `"uso_pct":45`) {
		t.Fatalf("historico = %s", pico)
	}
	// O pico nunca é negativo (leitura abaixo do reservado).
	g = request(t, c, GrantRequest{Vaga: "B", Tipo: "e2e", Declarado: Dims{"vram_mb": 100}})
	*now = now.Add(15 * time.Second)
	record(t, c, *now, Dims{"vram_mb": 1000})
	if g := grantOf(t, c, g.ID); g.Pico["vram_mb"] != 0 {
		t.Fatalf("pico negativo: %v", g.Pico)
	}
}

func TestSemUsoAvisaUmaVez(t *testing.T) {
	c, now := newMeterCore(t, func(s *ResourceSpec) {
		s.SemUso = &SemUsoSpec{Dimensao: "uso_pct", Limite: 5, Apos: 5 * time.Minute}
	})
	g := request(t, c, GrantRequest{Vaga: "A", Declarado: Dims{"vram_mb": 4000, "uso_pct": 80}})
	// B não declarou uso: nunca é avisada.
	request(t, c, GrantRequest{Vaga: "B", Declarado: Dims{"vram_mb": 500}})
	inbox(t, c, "A")
	inbox(t, c, "B")
	amostra := func(uso float64) {
		t.Helper()
		record(t, c, *now, Dims{"vram_mb": 3000, "uso_pct": uso})
		*now = now.Add(30 * time.Second)
	}
	// 0:00–2:00 com 16 % (1 acima do reservado), 2:30 com 60 % (reinicia), de 3:00 em diante 16 %.
	for range 5 {
		amostra(16)
	}
	amostra(60)
	for i := 0; i < 10; i++ { // 3:00 … 7:30: 4,5 min abaixo do limite
		amostra(16)
	}
	if ms := systemMessages(inbox(t, c, "A"), "recurso.sem_uso"); len(ms) != 0 {
		t.Fatalf("avisou antes de 5 min contínuos: %+v", ms)
	}
	for range 6 { // 8:00 (5 min contínuos) … 10:30
		amostra(16)
	}
	ms := systemMessages(inbox(t, c, "A"), "recurso.sem_uso")
	if len(ms) != 1 || ms[0].Ref != strconv.FormatInt(g.ID, 10) || !strings.Contains(ms[0].Corpo, "uso_pct") {
		t.Fatalf("A recebeu %+v", ms)
	}
	if ms := systemMessages(inbox(t, c, "B"), "recurso.sem_uso"); len(ms) != 0 {
		t.Fatalf("B recebeu %+v", ms)
	}
	if n := count(t, c, `SELECT count(*) FROM eventos WHERE tipo = 'recurso.sem_uso'`); n != 1 {
		t.Fatalf("eventos recurso.sem_uso = %d", n)
	}
	if g := grantOf(t, c, g.ID); g.Estado != GrantAtiva {
		t.Fatalf("o aviso não mexe na concessão: %+v", g)
	}
}

func TestAmostrasPorPeriodoEInvalidas(t *testing.T) {
	c, now := newMeterCore(t, nil)
	t0 := *now
	for i := range 4 {
		record(t, c, t0.Add(time.Duration(i)*time.Minute), Dims{"vram_mb": float64(1000 * (i + 1))})
	}
	ss, err := c.Samples(ctx, "gpu", t0.Add(time.Minute), t0.Add(2*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	got := []float64{}
	for _, s := range ss {
		got = append(got, s.Valores["vram_mb"])
	}
	if !slices.Equal(got, []float64{2000, 3000}) || !ss[0].Quando.Equal(t0.Add(time.Minute)) {
		t.Fatalf("Samples = %+v", ss)
	}
	for _, s := range []Sample{
		{Quando: t0, Valores: Dims{}},
		{Quando: t0, Valores: Dims{"ram_mb": 1}},
	} {
		if err := c.RecordSample(ctx, "gpu", s); err == nil {
			t.Fatalf("RecordSample(%+v) aceitou", s)
		}
	}
	if err := c.RecordSample(ctx, "tpu", Sample{Quando: t0, Valores: Dims{"x": 1}}); err == nil {
		t.Fatal("RecordSample de recurso desconhecido aceitou")
	}
	// Quando zero usa o relógio do núcleo.
	*now = t0.Add(time.Hour)
	if err := c.RecordSample(ctx, "gpu", Sample{Valores: Dims{"vram_mb": 1}}); err != nil {
		t.Fatal(err)
	}
	if s, err := c.LatestSample(ctx, "gpu", time.Second); err != nil || s == nil || !s.Quando.Equal(*now) {
		t.Fatalf("LatestSample = %+v, %v", s, err)
	}
	v, err := c.ResourceStatus(ctx, "gpu")
	if err != nil || v.Ultima == nil || v.Ultima.Valores["vram_mb"] != 1 {
		t.Fatalf("ResourceStatus.Ultima = %+v, %v", v.Ultima, err)
	}
}

func TestMedidorFalhouUmPorMinuto(t *testing.T) {
	c, now := newMeterCore(t, nil)
	for i, quer := range []bool{true, false, true} {
		gravou, err := c.MeterFailed(ctx, "gpu", "saída inválida")
		if err != nil || gravou != quer {
			t.Fatalf("chamada %d: gravou = %v, %v", i, gravou, err)
		}
		*now = now.Add(31 * time.Second)
	}
	if n := count(t, c, `SELECT count(*) FROM eventos WHERE tipo = 'medidor.falhou' AND ref = 'gpu'`); n != 2 {
		t.Fatalf("medidor.falhou = %d", n)
	}
}

func TestTickApagaAmostrasVelhas(t *testing.T) {
	c, now := newMeterCore(t, nil)
	t0 := *now
	record(t, c, t0, Dims{"vram_mb": 1})
	*now = t0.Add(8 * 24 * time.Hour)
	record(t, c, *now, Dims{"vram_mb": 2})
	tick(t, c)
	if n := count(t, c, `SELECT count(*) FROM amostras`); n != 1 {
		t.Fatalf("depois do tick: %d amostras", n)
	}
	// Só uma limpeza por hora.
	record(t, c, t0, Dims{"vram_mb": 3})
	*now = now.Add(30 * time.Minute)
	tick(t, c)
	if n := count(t, c, `SELECT count(*) FROM amostras`); n != 2 {
		t.Fatalf("limpou antes de uma hora: %d amostras", n)
	}
	*now = now.Add(31 * time.Minute)
	tick(t, c)
	if n := count(t, c, `SELECT count(*) FROM amostras`); n != 1 {
		t.Fatalf("depois de uma hora: %d amostras", n)
	}
}

func TestAmostraAnteriorAoInicioNaoContaParaAConcessao(t *testing.T) {
	c, now := newMeterCore(t, func(s *ResourceSpec) {
		s.SemUso = &SemUsoSpec{Dimensao: "uso_pct", Limite: 5, Apos: 5 * time.Minute}
	})
	// A leitura começou em t0; A foi ativada durante ela, em t0 + 1 s.
	t0 := *now
	*now = t0.Add(time.Second)
	a := request(t, c, GrantRequest{Vaga: "A", Declarado: Dims{"vram_mb": 1500, "uso_pct": 50}})
	if a.Estado != GrantAtiva {
		t.Fatalf("a = %+v", a)
	}
	*now = t0.Add(2 * time.Second)
	record(t, c, t0, Dims{"vram_mb": 5000, "uso_pct": 16})
	if g := grantOf(t, c, a.ID); len(g.Pico) != 0 {
		t.Fatalf("leitura de antes do início mexeu no pico: %v", g.Pico)
	}
	var desde *string
	if err := c.db.QueryRow(`SELECT abaixo_desde FROM concessoes WHERE id = ?`, a.ID).Scan(&desde); err != nil || desde != nil {
		t.Fatalf("abaixo_desde = %v, %v", desde, err)
	}
	// A ainda soma ao medido: 5000 + 1500 + 1500 + 512 > 8192.
	b := request(t, c, GrantRequest{Vaga: "B", Declarado: Dims{"vram_mb": 1500}})
	if b.Estado != GrantFila {
		t.Fatalf("b = %+v", b)
	}
}

func TestSemUsoReiniciaDepoisDeLacunaSemLeitura(t *testing.T) {
	c, now := newMeterCore(t, func(s *ResourceSpec) {
		s.SemUso = &SemUsoSpec{Dimensao: "uso_pct", Limite: 5, Apos: 5 * time.Minute}
	})
	request(t, c, GrantRequest{Vaga: "A", Declarado: Dims{"vram_mb": 1000, "uso_pct": 80}})
	inbox(t, c, "A")
	record(t, c, *now, Dims{"uso_pct": 16})
	// 6 min sem leitura (mais que 3 × 15 s): a contagem recomeça na próxima.
	*now = now.Add(6 * time.Minute)
	record(t, c, *now, Dims{"uso_pct": 16})
	if ms := systemMessages(inbox(t, c, "A"), "recurso.sem_uso"); len(ms) != 0 {
		t.Fatalf("avisou sem ter visto 5 min contínuos: %+v", ms)
	}
	for range 10 { // 6:30 … 11:00
		*now = now.Add(30 * time.Second)
		record(t, c, *now, Dims{"uso_pct": 16})
	}
	if ms := systemMessages(inbox(t, c, "A"), "recurso.sem_uso"); len(ms) != 1 {
		t.Fatalf("depois de 5 min contínuos: %+v", ms)
	}
}
