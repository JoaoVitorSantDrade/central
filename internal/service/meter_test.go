package service

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/JoaoVitorSantDrade/central/internal/core"
)

func TestParseMeterOutput(t *testing.T) {
	mapa := []string{"uso_pct", "vram_mb"}
	d, err := ParseMeterOutput("45, 3120\n", mapa)
	if err != nil || len(d) != 2 || d["uso_pct"] != 45 || d["vram_mb"] != 3120 {
		t.Fatalf("d = %v, %v", d, err)
	}
	// Linhas vazias antes e \r\n do Windows são toleradas; vale a primeira linha.
	d, err = ParseMeterOutput("\r\n  7 ,  12.5\r\n99, 99\r\n", mapa)
	if err != nil || d["uso_pct"] != 7 || d["vram_mb"] != 12.5 {
		t.Fatalf("d = %v, %v", d, err)
	}
	for _, out := range []string{"45\n", "45, 3120, 1\n", "N/A, 3120\n", "", "  \n\n", "45, \n", "NaN, 1\n", "Inf, 1\n"} {
		if d, err := ParseMeterOutput(out, mapa); err == nil {
			t.Errorf("ParseMeterOutput(%q) = %v, quer erro", out, d)
		}
	}
}

func TestRunMeter(t *testing.T) {
	d, err := RunMeter(ctx, t.TempDir(), "echo 45, 3120", []string{"uso_pct", "vram_mb"}, 5*time.Second)
	if err != nil || d["uso_pct"] != 45 || d["vram_mb"] != 3120 {
		t.Fatalf("d = %v, %v", d, err)
	}
	if _, err := RunMeter(ctx, t.TempDir(), "exit 3", []string{"x"}, 5*time.Second); err == nil {
		t.Fatal("comando que falha não deu erro")
	}
}

// sleepCommand dorme 30 s no shell do sistema.
func sleepCommand() string {
	if runtime.GOOS == "windows" {
		return "ping -n 31 127.0.0.1 >nul"
	}
	return "sleep 30"
}

func TestMedidorTimeout(t *testing.T) {
	start := time.Now()
	_, err := RunMeter(ctx, t.TempDir(), sleepCommand(), []string{"x"}, 200*time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "tempo") {
		t.Fatalf("err = %v", err)
	}
	if d := time.Since(start); d > 5*time.Second {
		t.Fatalf("RunMeter levou %v com timeout de 200 ms", d)
	}
}

const configMedidor = `[projeto]
id = "p"

[recursos.gpu]
dimensoes   = { vram_mb = 8192, uso_pct = 100 }
reservado   = { vram_mb = 2200, uso_pct = 15 }
prioridades = ["executor"]
medidor     = { comando = "%s", mapa = ["uso_pct", "vram_mb"], intervalo = "1s" }
`

// runMeters roda RunMeters por d e espera ele terminar.
func runMeters(s *Service, d time.Duration) {
	mctx, cancel := context.WithTimeout(ctx, d)
	defer cancel()
	s.RunMeters(mctx)
}

func TestMedidorSaidaInvalida(t *testing.T) {
	s, _ := newService(t)
	if _, err := enterWith(s, "/a", "A1", strings.Replace(configMedidor, "%s", "echo lixo", 1)); err != nil {
		t.Fatal(err)
	}
	runMeters(s, 300*time.Millisecond)
	c := coreOf(t, s, "p")
	ss, err := c.Samples(ctx, "gpu", time.Time{}, time.Now().Add(time.Hour))
	if err != nil || len(ss) != 0 {
		t.Fatalf("amostras = %+v, %v", ss, err)
	}
	p, _ := s.project(ctx, "p")
	var n int
	if err := p.db.QueryRow(`SELECT count(*) FROM eventos WHERE tipo = 'medidor.falhou' AND ref = 'gpu'`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("medidor.falhou = %d, %v", n, err)
	}
	g, err := s.RequestGrant(ctx, "p", "/a", core.GrantRequest{Recurso: "gpu", Declarado: core.Dims{"vram_mb": 1000}})
	if err != nil || g.Estado != core.GrantAtiva {
		t.Fatalf("pedido sem leitura: %+v, %v", g, err)
	}
}

func TestMedidorGravaAmostrasEReiniciaComConfig(t *testing.T) {
	s, _ := newService(t)
	if _, err := enterWith(s, "/a", "A1", strings.Replace(configMedidor, "%s", "echo 20, 3000", 1)); err != nil {
		t.Fatal(err)
	}
	mctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() { defer close(done); s.RunMeters(mctx) }()
	defer func() { cancel(); <-done }()
	c := coreOf(t, s, "p")
	ultima := func(vram float64) bool {
		smp, err := c.LatestSample(ctx, "gpu", 0)
		return err == nil && smp != nil && smp.Valores["vram_mb"] == vram && smp.Valores["uso_pct"] == 20
	}
	waitFor(t, func() bool { return ultima(3000) })
	// Uma configuração nova troca o comando do medidor que já roda.
	if _, err := enterWith(s, "/b", "B1", strings.Replace(configMedidor, "%s", "echo 20, 4000", 1)); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return ultima(4000) })
}

func waitFor(t *testing.T, ok func() bool) {
	t.Helper()
	limite := time.Now().Add(10 * time.Second)
	for !ok() {
		if time.Now().After(limite) {
			t.Fatal("a condição não aconteceu em 10 s")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// catCommand mostra o arquivo (caminho relativo) no shell do sistema.
func catCommand(arquivo string) string {
	if runtime.GOOS == "windows" {
		return "type " + arquivo
	}
	return "cat " + arquivo
}

func TestMeterDir(t *testing.T) {
	raiz, dados := t.TempDir(), t.TempDir()
	if d := MeterDir(raiz, dados); d != raiz {
		t.Fatalf("raiz existente: %q", d)
	}
	if d := MeterDir(filepath.Join(raiz, "nao-existe"), dados); d != dados {
		t.Fatalf("raiz inexistente: %q", d)
	}
	if d := MeterDir("", dados); d != dados {
		t.Fatalf("raiz vazia: %q", d)
	}
}

// O medidor do daemon roda na raiz registrada do projeto: um caminho relativo
// no comando se resolve a partir dela.
func TestMedidorRodaNaRaiz(t *testing.T) {
	s, _ := newService(t)
	raiz := t.TempDir()
	if err := os.WriteFile(filepath.Join(raiz, "leitura.txt"), []byte("20, 3000"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := strings.Replace(configMedidor, "%s", catCommand("leitura.txt"), 1)
	if _, err := s.Enter(ctx, EnterInput{ProjetoID: "p", Nome: "P", Raiz: raiz, Pasta: "/a", Vaga: "A1", Papel: "PAPEL", Config: cfg}); err != nil {
		t.Fatal(err)
	}
	mctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() { defer close(done); s.RunMeters(mctx) }()
	defer func() { cancel(); <-done }()
	c := coreOf(t, s, "p")
	waitFor(t, func() bool {
		smp, err := c.LatestSample(ctx, "gpu", 0)
		return err == nil && smp != nil && smp.Valores["vram_mb"] == 3000
	})
}
