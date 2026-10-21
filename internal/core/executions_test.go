package core

import (
	"encoding/json"
	"errors"
	"testing"
	"time"
)

func startExec(t *testing.T, c *Core, vaga string, concessao int64) Execution {
	t.Helper()
	e, err := c.StartExecution(ctx, vaga, "gate", "go test ./...", concessao)
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func TestExecucaoImpedeSumido(t *testing.T) {
	c, now := newCoreClock(t)
	c.SetSettings(Settings{SumidoApos: 30 * time.Minute})
	enter(t, c, "A1", "PAPEL", "/a")
	inicio := *now
	e := startExec(t, c, "A1", 0)
	*now = now.Add(25 * time.Minute)
	if err := c.BeatExecution(ctx, e.ID); err != nil {
		t.Fatal(err)
	}
	// O batimento da execução também renova a vaga; volta a vaga ao início
	// para só a execução contar.
	if _, err := c.db.Exec(`UPDATE agentes SET batimento = ? WHERE vaga = 'A1'`, inicio.Format(TimeLayout)); err != nil {
		t.Fatal(err)
	}
	*now = now.Add(10 * time.Minute)
	if a := agentByVaga(t, c, "A1"); a.Estado == EstadoSumido {
		t.Fatalf("com execução viva (batimento há 10 min): %+v", a)
	}
	tick(t, c)
	if n := count(t, c, `SELECT count(*) FROM eventos WHERE tipo = 'agente.sumiu'`); n != 0 {
		t.Fatalf("agente.sumiu com execução viva: %d", n)
	}
	// Sem batimento da execução por mais de SumidoApos, a vaga some.
	*now = now.Add(25 * time.Minute)
	if a := agentByVaga(t, c, "A1"); a.Estado != EstadoSumido {
		t.Fatalf("execução sem batimento há 35 min: %+v", a)
	}
	// Um batimento novo a traz de volta; encerrada, a execução não conta mais.
	if err := c.BeatExecution(ctx, e.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := c.FinishExecution(ctx, e.ID, 0, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := c.db.Exec(`UPDATE agentes SET batimento = ? WHERE vaga = 'A1'`, inicio.Format(TimeLayout)); err != nil {
		t.Fatal(err)
	}
	if a := agentByVaga(t, c, "A1"); a.Estado != EstadoSumido {
		t.Fatalf("depois do fim: %+v", a)
	}
}

func TestExecucaoCicloDeVida(t *testing.T) {
	c, now := newCoreClock(t)
	enter(t, c, "A1", "PAPEL", "/a")
	if _, err := c.StartExecution(ctx, "A1", "", "x", 0); !errors.Is(err, ErrInvalid) {
		t.Fatalf("sem rótulo: %v", err)
	}
	if _, err := c.StartExecution(ctx, "NINGUEM", "gate", "x", 0); !errors.Is(err, ErrNotFound) {
		t.Fatalf("vaga inexistente: %v", err)
	}
	if _, err := c.StartExecution(ctx, "A1", "gate", "x", 99); !errors.Is(err, ErrInvalid) {
		t.Fatalf("concessão inexistente: %v", err)
	}
	e := startExec(t, c, "A1", 0)
	if e.Fim != nil || e.Codigo != nil || e.Vaga != "A1" || e.Rotulo != "gate" || !e.Inicio.Equal(*now) {
		t.Fatalf("início = %+v", e)
	}
	*now = now.Add(90 * time.Second)
	if err := c.BeatExecution(ctx, e.ID); err != nil {
		t.Fatal(err)
	}
	if a := agentByVaga(t, c, "A1"); !a.Batimento.Equal(*now) {
		t.Fatalf("BeatExecution não renovou a vaga: %+v", a)
	}
	*now = now.Add(30 * time.Second)
	f, err := c.FinishExecution(ctx, e.ID, -1, "a\nb")
	if err != nil {
		t.Fatal(err)
	}
	if f.Fim == nil || !f.Fim.Equal(*now) || f.Codigo == nil || *f.Codigo != -1 || f.FinalLog != "a\nb" {
		t.Fatalf("fim = %+v", f)
	}
	if _, err := c.FinishExecution(ctx, e.ID, 0, ""); !errors.Is(err, ErrConflict) {
		t.Fatalf("fim repetido: %v", err)
	}
	if err := c.BeatExecution(ctx, e.ID); !errors.Is(err, ErrConflict) {
		t.Fatalf("batimento depois do fim: %v", err)
	}
	if err := c.BeatExecution(ctx, 99); !errors.Is(err, ErrNotFound) {
		t.Fatalf("batimento de execução inexistente: %v", err)
	}
	var dados string
	if err := c.db.QueryRow(`SELECT dados FROM eventos WHERE tipo = 'execucao.fim'`).Scan(&dados); err != nil {
		t.Fatal(err)
	}
	var d map[string]any
	json.Unmarshal([]byte(dados), &d)
	if d["duracao_ms"] != float64(120000) || d["codigo"] != float64(-1) {
		t.Fatalf("evento de fim = %s", dados)
	}
}

func TestFinishResumeAmostras(t *testing.T) {
	c, now := newMeterCore(t, nil)
	record(t, c, *now, Dims{"vram_mb": 2500, "uso_pct": 10})
	g := request(t, c, GrantRequest{Vaga: "A", Declarado: Dims{"vram_mb": 1000}})
	*now = now.Add(time.Second)
	e := startExec(t, c, "A", g.ID)
	// Leituras menos o reservado (2200 MB, 15 %), nunca negativas:
	// vram 1800, 3800, 2800; uso 0, 20, 40.
	usos := []float64{10, 35, 55}
	for i, v := range []float64{4000, 6000, 5000} {
		*now = now.Add(15 * time.Second)
		record(t, c, *now, Dims{"vram_mb": v, "uso_pct": usos[i]})
	}
	*now = now.Add(time.Second)
	if _, err := c.FinishExecution(ctx, e.ID, 0, ""); err != nil {
		t.Fatal(err)
	}
	var dados string
	if err := c.db.QueryRow(`SELECT dados FROM eventos WHERE tipo = 'execucao.fim'`).Scan(&dados); err != nil {
		t.Fatal(err)
	}
	var d struct {
		Recurso  string `json:"recurso"`
		Amostras struct {
			N     int  `json:"n"`
			Pico  Dims `json:"pico"`
			Media Dims `json:"media"`
		} `json:"amostras"`
	}
	if err := json.Unmarshal([]byte(dados), &d); err != nil {
		t.Fatal(err)
	}
	// A leitura de antes do início (2500) fica de fora.
	if d.Recurso != "gpu" || d.Amostras.N != 3 || d.Amostras.Pico["vram_mb"] != 3800 ||
		d.Amostras.Media["vram_mb"] != 2800 || d.Amostras.Pico["uso_pct"] != 40 || d.Amostras.Media["uso_pct"] != 20 {
		t.Fatalf("evento de fim = %s", dados)
	}
	// Concessão de outra vaga não serve.
	h := request(t, c, GrantRequest{Vaga: "B", Declarado: Dims{"vram_mb": 100}})
	if _, err := c.StartExecution(ctx, "A", "gate", "x", h.ID); !errors.Is(err, ErrInvalid) {
		t.Fatalf("concessão de B: %v", err)
	}
}

func TestTravaComDonoEmExecucaoViva(t *testing.T) {
	c, now := newCoreClock(t)
	c.SetSettings(Settings{SumidoApos: 30 * time.Minute})
	enter(t, c, "A1", "PAPEL", "/a")
	enter(t, c, "B1", "PAPEL", "/b")
	inicio := *now
	if _, err := c.Lock(ctx, "A1", "x", "gate"); err != nil {
		t.Fatal(err)
	}
	e := startExec(t, c, "A1", 0)
	*now = now.Add(40 * time.Minute)
	if err := c.BeatExecution(ctx, e.ID); err != nil {
		t.Fatal(err)
	}
	// Só a execução segura a vaga: o batimento dela volta ao início.
	if _, err := c.db.Exec(`UPDATE agentes SET batimento = ? WHERE vaga = 'A1'`, inicio.Format(TimeLayout)); err != nil {
		t.Fatal(err)
	}
	if err := c.Touch(ctx, "B1"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Lock(ctx, "B1", "x", "outro"); !errors.Is(err, ErrConflict) {
		t.Fatalf("trava de dono com execução viva: %v", err)
	}
	ls, err := c.Locks(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(ls) != 1 || ls[0].DonoEstado == EstadoSumido {
		t.Fatalf("travas = %+v", ls)
	}
}
