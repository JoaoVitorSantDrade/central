package core

import (
	"errors"
	"strconv"
	"testing"
	"time"
)

func pending(t *testing.T, c *Core, vaga string) Pendencias {
	t.Helper()
	p, err := c.Pending(ctx, vaga)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func sendPrazo(t *testing.T, c *Core, de, para string, prazo time.Duration) Message {
	t.Helper()
	m, err := c.Send(ctx, SendInput{De: de, Para: para, Tipo: "pergunta", Ref: "T-1", Corpo: "pode revisar?", ResponderEm: prazo})
	if err != nil {
		t.Fatal(err)
	}
	return m
}

// systemMessages filtra as mensagens de CENTRAL com o tipo dado.
func systemMessages(ms []Message, tipo string) []Message {
	out := []Message{}
	for _, m := range ms {
		if m.De == Sistema && m.Tipo == tipo {
			out = append(out, m)
		}
	}
	return out
}

func TestResponderFechaPendencia(t *testing.T) {
	c, now := newCoreClock(t)
	enter(t, c, "A", "FIXER", "/a")
	enter(t, c, "B", "REVISOR", "/b")
	m := sendPrazo(t, c, "A", "B", 30*time.Minute)
	if m.ResponderAte == nil || !m.ResponderAte.Equal(now.Add(30*time.Minute)) {
		t.Fatalf("responder_ate = %v", m.ResponderAte)
	}
	if p := pending(t, c, "B"); len(p.Devo) != 1 || p.Devo[0].Seq != m.Seq || len(p.Esperam) != 0 {
		t.Fatalf("pendências de B = %+v", p)
	}
	if p := pending(t, c, "A"); len(p.Esperam) != 1 || p.Esperam[0].Seq != m.Seq || len(p.Devo) != 0 {
		t.Fatalf("pendências de A = %+v", p)
	}
	r, err := c.Reply(ctx, "B", m.Seq, "aprovado")
	if err != nil {
		t.Fatal(err)
	}
	if r.Para != "A" || r.Tipo != "resposta" || r.Ref != "T-1" || r.RespondeA != m.Seq || r.ResponderAte != nil {
		t.Fatalf("resposta = %+v", r)
	}
	if p := pending(t, c, "B"); len(p.Devo) != 0 || len(p.Esperam) != 0 {
		t.Fatalf("pendências de B depois = %+v", p)
	}
	if p := pending(t, c, "A"); len(p.Devo) != 0 || len(p.Esperam) != 0 {
		t.Fatalf("pendências de A depois = %+v", p)
	}
	ms := inbox(t, c, "A")
	if len(ms) != 1 || ms[0].Tipo != "resposta" || ms[0].RespondeA != m.Seq || ms[0].Corpo != "aprovado" {
		t.Fatalf("caixa de A = %+v", ms)
	}
	// A pergunta lida na caixa traz o prazo.
	if ms := inbox(t, c, "B"); len(ms) != 1 || ms[0].ResponderAte == nil {
		t.Fatalf("caixa de B = %+v", ms)
	}
}

func TestResponderPorPapelUmBasta(t *testing.T) {
	c, _ := newCoreClock(t)
	enter(t, c, "FIXER1", "FIXER", "/f")
	enter(t, c, "REVISOR1", "REVISOR", "/r1")
	enter(t, c, "REVISOR2", "REVISOR", "/r2")
	m := sendPrazo(t, c, "FIXER1", "REVISOR", 30*time.Minute)
	if p := pending(t, c, "REVISOR1"); len(p.Devo) != 1 {
		t.Fatalf("REVISOR1 = %+v", p)
	}
	if _, err := c.Reply(ctx, "REVISOR2", m.Seq, "eu pego"); err != nil {
		t.Fatal(err)
	}
	for _, v := range []string{"REVISOR1", "REVISOR2", "FIXER1"} {
		if p := pending(t, c, v); len(p.Devo) != 0 || len(p.Esperam) != 0 {
			t.Fatalf("%s ainda tem pendências: %+v", v, p)
		}
	}
}

func TestSoDestinatarioResponde(t *testing.T) {
	c, _ := newCoreClock(t)
	enter(t, c, "A", "FIXER", "/a")
	enter(t, c, "B", "REVISOR", "/b")
	enter(t, c, "C", "PLANEJADOR", "/c")
	m := sendPrazo(t, c, "A", "B", 30*time.Minute)
	if _, err := c.Reply(ctx, "C", m.Seq, "não era comigo"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("C respondeu: %v", err)
	}
	// O remetente também não responde a si mesmo.
	if _, err := c.Reply(ctx, "A", m.Seq, "eu mesmo"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("A respondeu a si: %v", err)
	}
	if _, err := c.Reply(ctx, "B", 9999, "x"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("seq inexistente: %v", err)
	}
	if p := pending(t, c, "B"); len(p.Devo) != 1 {
		t.Fatalf("pendência fechada por quem não podia: %+v", p)
	}
}

func TestEnviarPrazoNegativoRecusado(t *testing.T) {
	c, _ := newCoreClock(t)
	if _, err := c.Send(ctx, SendInput{De: "A", Para: "B", Tipo: "nota", Corpo: "x", ResponderEm: -time.Minute}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("err = %v", err)
	}
}

func TestTickAvisaPrazoUmaVez(t *testing.T) {
	c, now := newCoreClock(t)
	enter(t, c, "A", "FIXER", "/a")
	enter(t, c, "B", "REVISOR", "/b")
	m := sendPrazo(t, c, "A", "B", 30*time.Minute)
	respondida := sendPrazo(t, c, "A", "B", 30*time.Minute)
	if _, err := c.Reply(ctx, "B", respondida.Seq, "feito"); err != nil {
		t.Fatal(err)
	}
	*now = now.Add(29 * time.Minute)
	if err := c.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	if got := systemMessages(inbox(t, c, "B"), "resposta.prazo"); len(got) != 0 {
		t.Fatalf("avisou antes do prazo: %+v", got)
	}
	*now = now.Add(2 * time.Minute)
	for range 2 {
		if err := c.Tick(ctx); err != nil {
			t.Fatal(err)
		}
	}
	got := systemMessages(inbox(t, c, "B"), "resposta.prazo")
	if len(got) != 1 || got[0].Ref != strconv.FormatInt(m.Seq, 10) || got[0].Para != "B" {
		t.Fatalf("avisos = %+v", got)
	}
}

func TestTickAvisaSumidoAoGerente(t *testing.T) {
	c, now := newCoreClock(t)
	c.SetSettings(Settings{SumidoApos: 30 * time.Minute})
	enter(t, c, "GERENTE", "GERENTE", "/g")
	enter(t, c, "FIXER1", "FIXER", "/f")
	tick := func() {
		t.Helper()
		if err := c.Tick(ctx); err != nil {
			t.Fatal(err)
		}
	}
	sumiu := func() []Message {
		t.Helper()
		// A caixa do GERENTE também renova o batimento dele: só o FIXER some.
		return systemMessages(inbox(t, c, "GERENTE"), "agente.sumiu")
	}
	*now = now.Add(20 * time.Minute)
	inbox(t, c, "GERENTE")
	*now = now.Add(11 * time.Minute)
	tick()
	tick()
	got := sumiu()
	if len(got) != 1 || got[0].Ref != "FIXER1" || got[0].Para != "GERENTE" {
		t.Fatalf("avisos = %+v", got)
	}
	*now = now.Add(time.Minute)
	tick()
	if got := sumiu(); len(got) != 0 {
		t.Fatalf("avisou de novo no mesmo sumiço: %+v", got)
	}
	// Volta e some de novo: novo aviso.
	if err := c.Touch(ctx, "FIXER1"); err != nil {
		t.Fatal(err)
	}
	*now = now.Add(20 * time.Minute)
	inbox(t, c, "GERENTE")
	*now = now.Add(11 * time.Minute)
	tick()
	if got := sumiu(); len(got) != 1 || got[0].Ref != "FIXER1" {
		t.Fatalf("avisos do segundo sumiço = %+v", got)
	}
}

func TestTickAvisaSumidoATodosSemGerente(t *testing.T) {
	c, now := newCoreClock(t)
	enter(t, c, "FIXER1", "FIXER", "/f1")
	enter(t, c, "FIXER2", "FIXER", "/f2")
	if _, err := c.Leave(ctx, "FIXER2"); err != nil {
		t.Fatal(err)
	}
	*now = now.Add(31 * time.Minute)
	if err := c.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	// Só o FIXER1 sumiu (encerrado não some); o aviso vai a TODOS.
	got := systemMessages(inbox(t, c, "FIXER2"), "agente.sumiu")
	if len(got) != 1 || got[0].Ref != "FIXER1" || got[0].Para != Todos {
		t.Fatalf("avisos = %+v", got)
	}
}

func TestFirstUnread(t *testing.T) {
	c, _ := newCoreClock(t)
	enter(t, c, "A", "FIXER", "/a")
	enter(t, c, "B", "REVISOR", "/b")
	if m, n, err := c.FirstUnread(ctx, "B"); err != nil || n != 0 || m.Seq != 0 {
		t.Fatalf("vazia = %+v %d %v", m, n, err)
	}
	m1 := send(t, c, "A", "B", "1")
	send(t, c, "A", "B", "2")
	m, n, err := c.FirstUnread(ctx, "B")
	if err != nil || n != 2 || m.Seq != m1.Seq {
		t.Fatalf("FirstUnread = %+v %d %v", m, n, err)
	}
	if _, _, err := c.FirstUnread(ctx, "NINGUEM"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("vaga inexistente: %v", err)
	}
}
