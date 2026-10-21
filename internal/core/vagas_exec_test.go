package core

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/JoaoVitorSantDrade/central/internal/store"
)

// relogio é um relógio que o teste avança.
type relogio struct{ t time.Time }

func (r *relogio) now() time.Time           { return r.t }
func (r *relogio) avancar(d time.Duration) { r.t = r.t.Add(d) }

func newCoreRelogio(t *testing.T) (*Core, *relogio) {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "p.db"), ProjectMigrations, store.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	r := &relogio{t: time.Date(2026, 10, 20, 15, 0, 0, 0, time.UTC)}
	return New(db, r.now), r
}

func abrirVaga(t *testing.T, c *Core, vaga, papel string) VagaExec {
	t.Helper()
	v, err := c.AbrirVagaExec(ctx, "GERENTE1", VagaExec{Vaga: vaga, Papel: papel, Area: "app", Nota: "comece por T-1", Modelo: "opus", Esforco: "high", Sprint: "S1"})
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func eventosDoTipo(t *testing.T, c *Core, tipo string) []Event {
	t.Helper()
	es, err := c.Events(ctx, EventFilter{Tipos: []string{tipo}})
	if err != nil {
		t.Fatal(err)
	}
	return es
}

func TestAbrirFecharReabrirMantemSessao(t *testing.T) {
	c, _ := newCoreRelogio(t)
	v := abrirVaga(t, c, "EXEC1", "EXECUTOR")
	if v.Estado != VagaAberta || len(v.Sessao) != 36 || v.AbertaEm == nil || v.Sprint != "S1" || v.Nota != "comece por T-1" {
		t.Fatalf("aberta = %+v", v)
	}
	if _, err := c.AbrirVagaExec(ctx, "GERENTE1", VagaExec{Vaga: "EXEC1", Papel: "EXECUTOR", Modelo: "opus", Esforco: "high"}); !errors.Is(err, ErrConflict) {
		t.Fatalf("abrir de novo = %v", err)
	}
	f, err := c.FecharVagaExec(ctx, "GERENTE1", "EXEC1")
	if err != nil || f.Estado != VagaFechada || f.FechadaEm == nil {
		t.Fatalf("fechada = %+v, %v", f, err)
	}
	r, err := c.AbrirVagaExec(ctx, "HUMANO", VagaExec{Vaga: "EXEC1", Papel: "EXECUTOR", Modelo: "sonnet", Esforco: "medium"})
	if err != nil || r.Estado != VagaAberta || r.Sessao != v.Sessao || r.FechadaEm != nil || r.Modelo != "sonnet" {
		t.Fatalf("reaberta = %+v, %v", r, err)
	}
	if n := len(eventosDoTipo(t, c, "vaga.aberta")); n != 1 {
		t.Fatalf("vaga.aberta = %d", n)
	}
	if es := eventosDoTipo(t, c, "vaga.retomada"); len(es) != 1 || es[0].Quem != "HUMANO" {
		t.Fatalf("vaga.retomada = %+v", es)
	}
	if n := len(eventosDoTipo(t, c, "vaga.fechada")); n != 1 {
		t.Fatalf("vaga.fechada = %d", n)
	}
	if _, err := c.FecharVagaExec(ctx, "GERENTE1", "NAO1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("fechar inexistente = %v", err)
	}
	vs, err := c.VagasExec(ctx)
	if err != nil || len(vs) != 1 {
		t.Fatalf("VagasExec = %+v, %v", vs, err)
	}
}

func TestTurnoIniciaETermina(t *testing.T) {
	c, rel := newCoreRelogio(t)
	abrirVaga(t, c, "EXEC1", "EXECUTOR")
	n, err := c.IniciarTurno(ctx, "EXEC1", "primeiro turno", 0)
	if err != nil || n != 1 {
		t.Fatalf("IniciarTurno = %d, %v", n, err)
	}
	if _, err := c.IniciarTurno(ctx, "EXEC1", "outro", 0); !errors.Is(err, ErrConflict) {
		t.Fatalf("turno sobreposto = %v", err)
	}
	if err := c.RegistrarPID(ctx, "EXEC1", 4242); err != nil {
		t.Fatal(err)
	}
	v, _ := c.VagaExec(ctx, "EXEC1")
	if v.TurnoDesde == nil || v.PID != 4242 || v.NTurnos != 1 {
		t.Fatalf("em curso = %+v", v)
	}
	rel.avancar(3 * time.Minute)
	v, err = c.TerminarTurno(ctx, "EXEC1", TurnoFim{Sessao: v.Sessao, Pasta: "/raiz/.claude/worktrees/EXEC1", CustoUSD: 0.25,
		TokensEntrada: 100, TokensSaida: 20, Negacoes: []string{"Bash rm"}, Texto: "feito", Dur: 3 * time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	if v.TurnoDesde != nil || v.PID != 0 || v.Pasta != "/raiz/.claude/worktrees/EXEC1" || v.CustoDia != 0.25 || v.UltimaNegacao != "Bash rm" {
		t.Fatalf("terminado = %+v", v)
	}
	n, _ = c.IniciarTurno(ctx, "EXEC1", "mensagem", 0)
	c.TerminarTurno(ctx, "EXEC1", TurnoFim{CustoUSD: 0.5})
	if hoje, err := c.CustoHoje(ctx); err != nil || n != 2 || hoje != 0.75 {
		t.Fatalf("CustoHoje = %v, %v (n=%d)", hoje, err, n)
	}
	es := eventosDoTipo(t, c, "turno.iniciado")
	if len(es) != 2 || !strings.Contains(dadosJSON(es[0]), `"motivo":"primeiro turno"`) || !strings.Contains(dadosJSON(es[0]), `"n":1`) {
		t.Fatalf("turno.iniciado = %+v", es)
	}
	es = eventosDoTipo(t, c, "turno.terminou")
	if len(es) != 2 || !strings.Contains(dadosJSON(es[0]), `"custo_usd":0.25`) || !strings.Contains(dadosJSON(es[0]), `"negacoes":["Bash rm"]`) {
		t.Fatalf("turno.terminou = %+v", es)
	}
	// o custo do dia recomeça num dia novo
	rel.avancar(24 * time.Hour)
	c.IniciarTurno(ctx, "EXEC1", "x", 0)
	v, _ = c.TerminarTurno(ctx, "EXEC1", TurnoFim{CustoUSD: 0.1})
	if hoje, _ := c.CustoHoje(ctx); v.CustoDia != 0.1 || hoje != 0.1 {
		t.Fatalf("dia novo: custo da vaga %v, hoje %v", v.CustoDia, hoje)
	}
	if _, err := c.TerminarTurno(ctx, "EXEC1", TurnoFim{}); !errors.Is(err, ErrConflict) {
		t.Fatalf("terminar sem turno = %v", err)
	}
}

func TestFalhaESuspensao(t *testing.T) {
	c, rel := newCoreRelogio(t)
	enter(t, c, "GERENTE1", "GERENTE", "/g")
	abrirVaga(t, c, "EXEC1", "EXECUTOR")
	prox := rel.t.Add(time.Minute)
	c.IniciarTurno(ctx, "EXEC1", "x", 0)
	v, err := c.TerminarTurno(ctx, "EXEC1", TurnoFim{Falha: "erro", Codigo: 3, Stderr: "quebrou", ProximoEm: &prox})
	if err != nil || v.Falhas != 1 || v.Estado != VagaAberta || v.ProximoEm == nil || !v.ProximoEm.Equal(prox) || v.FalhaAnterior != "erro" {
		t.Fatalf("1ª falha = %+v, %v", v, err)
	}
	if es := eventosDoTipo(t, c, "turno.falhou"); len(es) != 1 || !strings.Contains(dadosJSON(es[0]), `"stderr":"quebrou"`) {
		t.Fatalf("turno.falhou = %+v", es)
	}
	c.IniciarTurno(ctx, "EXEC1", "x", 0)
	v, _ = c.TerminarTurno(ctx, "EXEC1", TurnoFim{Falha: "limite_plano"})
	if v.Falhas != 1 {
		t.Fatalf("limite do plano não conta como falha: %d", v.Falhas)
	}
	c.IniciarTurno(ctx, "EXEC1", "x", 0)
	v, _ = c.TerminarTurno(ctx, "EXEC1", TurnoFim{Falha: "prazo", Suspender: true})
	if v.Falhas != 2 || v.Estado != VagaSuspensa {
		t.Fatalf("suspensa = %+v", v)
	}
	if n := len(eventosDoTipo(t, c, "vaga.suspensa")); n != 1 {
		t.Fatalf("vaga.suspensa = %d", n)
	}
	ms := inbox(t, c, "GERENTE1")
	if len(ms) != 1 || ms[0].Tipo != "vaga.suspensa" || ms[0].Ref != "EXEC1" {
		t.Fatalf("caixa do gerente = %+v", ms)
	}
	if _, err := c.IniciarTurno(ctx, "EXEC1", "x", 0); !errors.Is(err, ErrConflict) {
		t.Fatalf("turno em vaga suspensa = %v", err)
	}
	// reabrir zera as falhas
	v, _ = c.AbrirVagaExec(ctx, "GERENTE1", VagaExec{Vaga: "EXEC1", Papel: "EXECUTOR", Modelo: "opus", Esforco: "high"})
	if v.Estado != VagaAberta || v.Falhas != 0 || v.ProximoEm != nil {
		t.Fatalf("retomada = %+v", v)
	}
	// um ok zera as falhas
	c.IniciarTurno(ctx, "EXEC1", "x", 0)
	c.TerminarTurno(ctx, "EXEC1", TurnoFim{Falha: "erro"})
	c.IniciarTurno(ctx, "EXEC1", "x", 0)
	if v, _ = c.TerminarTurno(ctx, "EXEC1", TurnoFim{}); v.Falhas != 0 || v.FalhaAnterior != "" {
		t.Fatalf("ok = %+v", v)
	}
}

func TestDespertaresVencidosMarcaFeito(t *testing.T) {
	c, rel := newCoreRelogio(t)
	abrirVaga(t, c, "EXEC1", "EXECUTOR")
	abrirVaga(t, c, "EXEC2", "EXECUTOR")
	if err := c.Despertar(ctx, "EXEC1", rel.t.Add(10*time.Minute), "ver o CI"); err != nil {
		t.Fatal(err)
	}
	c.Despertar(ctx, "EXEC2", rel.t.Add(time.Hour), "mais tarde")
	if err := c.Despertar(ctx, "NAO1", rel.t, "x"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("despertar de vaga inexistente = %v", err)
	}
	if d, _ := c.DespertaresVencidos(ctx, rel.t); len(d) != 0 {
		t.Fatalf("nada vencido ainda: %v", d)
	}
	rel.avancar(11 * time.Minute)
	d, err := c.DespertaresVencidos(ctx, rel.t)
	if err != nil || len(d) != 1 || d["EXEC1"] != "ver o CI" {
		t.Fatalf("vencidos = %v, %v", d, err)
	}
	// o turno consome os despertares vencidos da vaga
	c.IniciarTurno(ctx, "EXEC1", "despertar: ver o CI", 0)
	if d, _ := c.DespertaresVencidos(ctx, rel.t); len(d) != 0 {
		t.Fatalf("depois do turno = %v", d)
	}
	rel.avancar(time.Hour)
	if d, _ := c.DespertaresVencidos(ctx, rel.t); len(d) != 1 || d["EXEC2"] != "mais tarde" {
		t.Fatalf("EXEC2 = %v", d)
	}
}

func TestPlanoPausado(t *testing.T) {
	c, rel := newCoreRelogio(t)
	if ate, err := c.PlanoPausadoAte(ctx); err != nil || !ate.IsZero() {
		t.Fatalf("sem pausa = %v, %v", ate, err)
	}
	ate := rel.t.Add(30 * time.Minute)
	if err := c.PausarPlano(ctx, ate, "limite_plano"); err != nil {
		t.Fatal(err)
	}
	if got, _ := c.PlanoPausadoAte(ctx); !got.Equal(ate) {
		t.Fatalf("pausado até %v", got)
	}
	if es := eventosDoTipo(t, c, "plano.pausado"); len(es) != 1 || !strings.Contains(dadosJSON(es[0]), "limite_plano") {
		t.Fatalf("plano.pausado = %+v", es)
	}
	if err := c.RetomarPlano(ctx); err != nil {
		t.Fatal(err)
	}
	if got, _ := c.PlanoPausadoAte(ctx); !got.IsZero() {
		t.Fatalf("retomado ainda pausado até %v", got)
	}
	c.RetomarPlano(ctx)
	if n := len(eventosDoTipo(t, c, "plano.retomado")); n != 1 {
		t.Fatalf("plano.retomado = %d", n)
	}
}

func TestVagaExecutadaNuncaSumida(t *testing.T) {
	c, rel := newCoreRelogio(t)
	abrirVaga(t, c, "EXEC1", "EXECUTOR")
	enter(t, c, "EXEC1", "EXECUTOR", "/raiz/.claude/worktrees/EXEC1")
	enter(t, c, "SOLTO1", "EXECUTOR", "/solto")
	rel.avancar(2 * time.Hour)
	estado := func(vaga string) string {
		as, err := c.Agents(ctx)
		if err != nil {
			t.Fatal(err)
		}
		for _, a := range as {
			if a.Vaga == vaga {
				return a.Estado
			}
		}
		return ""
	}
	if e := estado("EXEC1"); e != EstadoOcioso {
		t.Fatalf("aberta sem turno = %q", e)
	}
	if e := estado("SOLTO1"); e != EstadoSumido {
		t.Fatalf("vaga comum sem batimento = %q", e)
	}
	c.IniciarTurno(ctx, "EXEC1", "x", 0)
	if e := estado("EXEC1"); e != EstadoTrabalhando {
		t.Fatalf("com turno = %q", e)
	}
	c.TerminarTurno(ctx, "EXEC1", TurnoFim{Falha: "erro", Suspender: true})
	if e := estado("EXEC1"); e != EstadoSuspenso {
		t.Fatalf("suspensa = %q", e)
	}
	c.FecharVagaExec(ctx, "GERENTE1", "EXEC1")
	if e := estado("EXEC1"); e != EstadoEncerrado {
		t.Fatalf("fechada = %q", e)
	}
	// o tick não avisa o sumiço de uma vaga executada
	if err := c.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	for _, e := range eventosDoTipo(t, c, "agente.sumiu") {
		if e.Quem == "EXEC1" {
			t.Fatalf("aviso de sumiço da vaga executada: %+v", e)
		}
	}
}

func dadosJSON(e Event) string {
	b, _ := json.Marshal(e.Dados)
	return string(b)
}

func TestAbrirVagaRecusaNomeInvalido(t *testing.T) {
	c, _ := newCoreRelogio(t)
	for _, nome := range []string{"../x", "-w", "a/b", `a\b`, "a b", "", strings.Repeat("A", 41)} {
		if _, err := c.AbrirVagaExec(ctx, "HUMANO", VagaExec{Vaga: nome, Papel: "EXECUTOR", Modelo: "opus", Esforco: "high"}); !errors.Is(err, ErrInvalid) {
			t.Fatalf("vaga %q: err = %v", nome, err)
		}
	}
}

func TestNaoLidasDesde(t *testing.T) {
	c, rel := newCoreRelogio(t)
	enter(t, c, "EXEC1", "EXECUTOR", "/w")
	send(t, c, "G1", "EXEC1", "antiga")
	rel.avancar(time.Minute)
	desde := rel.t
	if n, err := c.NaoLidasDesde(ctx, "EXEC1", desde); err != nil || n != 0 {
		t.Fatalf("antes = %d, %v", n, err)
	}
	rel.avancar(time.Second)
	send(t, c, "G1", "EXEC1", "nova")
	if n, _ := c.NaoLidasDesde(ctx, "EXEC1", desde); n != 1 {
		t.Fatalf("depois = %d", n)
	}
	if n, _ := c.NaoLidasDesde(ctx, "EXEC1", time.Time{}); n != 2 {
		t.Fatalf("todas = %d", n)
	}
	if _, err := c.NaoLidasDesde(ctx, "NAO1", desde); !errors.Is(err, ErrNotFound) {
		t.Fatalf("vaga que não entrou = %v", err)
	}
}
