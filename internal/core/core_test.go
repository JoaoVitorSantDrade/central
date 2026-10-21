package core

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/JoaoVitorSantDrade/central/internal/store"
)

var ctx = context.Background()

func newCore(t *testing.T) *Core {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "p.db"), ProjectMigrations, store.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	clock := time.Date(2026, 10, 20, 22, 0, 0, 0, time.UTC)
	return New(db, func() time.Time { return clock })
}

func enter(t *testing.T, c *Core, vaga, papel, pasta string) {
	t.Helper()
	if _, err := c.Enter(ctx, vaga, papel, pasta); err != nil {
		t.Fatal(err)
	}
}

func send(t *testing.T, c *Core, de, para, corpo string) Message {
	t.Helper()
	m, err := c.Send(ctx, SendInput{De: de, Para: para, Tipo: "nota", Corpo: corpo})
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func inbox(t *testing.T, c *Core, vaga string) []Message {
	t.Helper()
	ms, err := c.Inbox(ctx, vaga)
	if err != nil {
		t.Fatal(err)
	}
	return ms
}

func TestEnviarECaixa(t *testing.T) {
	c := newCore(t)
	enter(t, c, "FIXER1", "FIXER", "/a")
	enter(t, c, "REVISOR1", "REVISOR", "/b")
	send(t, c, "FIXER1", "REVISOR1", "T-1 pronta")
	ms := inbox(t, c, "REVISOR1")
	if len(ms) != 1 || ms[0].Corpo != "T-1 pronta" || ms[0].De != "FIXER1" {
		t.Fatalf("caixa = %+v", ms)
	}
	if ms := inbox(t, c, "REVISOR1"); len(ms) != 0 {
		t.Fatalf("segunda caixa = %+v", ms)
	}
}

func TestEnderecamentoPorPapelETodos(t *testing.T) {
	c := newCore(t)
	enter(t, c, "GERENTE", "GERENTE", "/g")
	enter(t, c, "REVISOR1", "REVISOR", "/r1")
	enter(t, c, "REVISOR2", "REVISOR", "/r2")
	send(t, c, "GERENTE", "REVISOR", "papel")
	send(t, c, "GERENTE", "TODOS", "todos")
	for _, v := range []string{"REVISOR1", "REVISOR2"} {
		if ms := inbox(t, c, v); len(ms) != 2 {
			t.Fatalf("%s recebeu %d", v, len(ms))
		}
	}
	if ms := inbox(t, c, "GERENTE"); len(ms) != 0 {
		t.Fatalf("remetente recebeu a própria mensagem: %+v", ms)
	}
}

func TestCaixaEntregaMensagemAnteriorAEntrada(t *testing.T) {
	c := newCore(t)
	enter(t, c, "GERENTE", "GERENTE", "/g")
	send(t, c, "GERENTE", "REVISOR2", "abra a fila")
	enter(t, c, "REVISOR2", "REVISOR", "/r2")
	if ms := inbox(t, c, "REVISOR2"); len(ms) != 1 {
		t.Fatalf("caixa = %+v", ms)
	}
}

func TestVagaSubstitutaHerdaCursor(t *testing.T) {
	c := newCore(t)
	enter(t, c, "G", "GERENTE", "/g")
	enter(t, c, "REVISOR1", "REVISOR", "/old")
	send(t, c, "G", "REVISOR1", "1")
	inbox(t, c, "REVISOR1")
	enter(t, c, "REVISOR1", "REVISOR", "/new")
	send(t, c, "G", "REVISOR1", "2")
	ms := inbox(t, c, "REVISOR1")
	if len(ms) != 1 || ms[0].Corpo != "2" {
		t.Fatalf("caixa = %+v", ms)
	}
	a, err := c.AgentByPasta(ctx, "/new")
	if err != nil || a.Vaga != "REVISOR1" {
		t.Fatalf("AgentByPasta = %+v, %v", a, err)
	}
	if _, err := c.AgentByPasta(ctx, "/old"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("pasta antiga ainda ligada: %v", err)
	}
}

func TestPastaReusadaPorOutraVaga(t *testing.T) {
	c := newCore(t)
	enter(t, c, "FIXER1", "FIXER", "/w")
	enter(t, c, "FIXER2", "FIXER", "/w")
	a, err := c.AgentByPasta(ctx, "/w")
	if err != nil || a.Vaga != "FIXER2" {
		t.Fatalf("AgentByPasta = %+v, %v", a, err)
	}
}

func TestCaixaConcorrenteEntregaUmaVez(t *testing.T) {
	c := newCore(t)
	enter(t, c, "A", "A", "/a")
	enter(t, c, "X", "X", "/x")
	for i := 0; i < 100; i++ {
		send(t, c, "A", "X", "m")
	}
	var mu sync.Mutex
	seen := map[int64]int{}
	var wg sync.WaitGroup
	for g := 0; g < 10; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				ms, err := c.Inbox(ctx, "X")
				if err != nil {
					t.Error(err)
					return
				}
				if len(ms) == 0 {
					return
				}
				mu.Lock()
				for _, m := range ms {
					seen[m.Seq]++
				}
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if len(seen) != 100 {
		t.Fatalf("entregues %d distintas", len(seen))
	}
	for s, n := range seen {
		if n != 1 {
			t.Fatalf("seq %d entregue %d vezes", s, n)
		}
	}
}

func TestChaveIdempotente(t *testing.T) {
	c := newCore(t)
	enter(t, c, "A", "A", "/a")
	m1, err := c.Send(ctx, SendInput{De: "A", Para: "B", Tipo: "nota", Corpo: "x", Chave: "k1"})
	if err != nil {
		t.Fatal(err)
	}
	m2, err := c.Send(ctx, SendInput{De: "A", Para: "B", Tipo: "nota", Corpo: "x", Chave: "k1"})
	if err != nil {
		t.Fatal(err)
	}
	if m1.Seq != m2.Seq {
		t.Fatalf("seq %d != %d", m1.Seq, m2.Seq)
	}
	var n int
	c.db.QueryRow(`SELECT count(*) FROM mensagens`).Scan(&n)
	if n != 1 {
		t.Fatalf("linhas = %d", n)
	}
}

func TestEventosGravados(t *testing.T) {
	c := newCore(t)
	enter(t, c, "A", "A", "/a")
	send(t, c, "A", "TODOS", "x")
	inbox(t, c, "A")
	rows, err := c.db.Query(`SELECT tipo FROM eventos ORDER BY seq`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var got []string
	for rows.Next() {
		var s string
		rows.Scan(&s)
		got = append(got, s)
	}
	want := []string{"agente.entrou", "mensagem.enviada", "caixa.lida"}
	if len(got) != 3 || got[0] != want[0] || got[1] != want[1] || got[2] != want[2] {
		t.Fatalf("eventos = %v", got)
	}
}

func TestUnread(t *testing.T) {
	c := newCore(t)
	enter(t, c, "A", "A", "/a")
	enter(t, c, "B", "B", "/b")
	send(t, c, "A", "B", "1")
	send(t, c, "A", "C", "outro")
	send(t, c, "A", "B", "2")
	if n, err := c.Unread(ctx, "B"); err != nil || n != 2 {
		t.Fatalf("Unread = %d, %v", n, err)
	}
	inbox(t, c, "B")
	if n, _ := c.Unread(ctx, "B"); n != 0 {
		t.Fatalf("Unread após caixa = %d", n)
	}
}

func TestChangedDisparaNoSend(t *testing.T) {
	c := newCore(t)
	enter(t, c, "A", "A", "/a")
	ch := c.Changed()
	send(t, c, "A", "B", "x")
	select {
	case <-ch:
	default:
		t.Fatal("Changed não disparou")
	}
}

func TestValidacao(t *testing.T) {
	c := newCore(t)
	enter(t, c, "A", "A", "/a")
	if _, err := c.Send(ctx, SendInput{De: "A", Para: "B", Tipo: "nota", Corpo: ""}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("corpo vazio: %v", err)
	}
	if _, err := c.Enter(ctx, "", "X", "/z"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("vaga vazia: %v", err)
	}
	if _, err := c.AgentByPasta(ctx, "/nada"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("pasta desconhecida: %v", err)
	}
}

func TestChavePorRemetente(t *testing.T) {
	c := newCore(t)
	enter(t, c, "A", "A", "/a")
	enter(t, c, "B", "B", "/b")
	ma, err := c.Send(ctx, SendInput{De: "A", Para: "X", Tipo: "nota", Corpo: "de A", Chave: "T-12-concluida"})
	if err != nil {
		t.Fatal(err)
	}
	mb, err := c.Send(ctx, SendInput{De: "B", Para: "X", Tipo: "nota", Corpo: "de B", Chave: "T-12-concluida"})
	if err != nil {
		t.Fatal(err)
	}
	if ma.Seq == mb.Seq || mb.Corpo != "de B" {
		t.Fatalf("chave de outro remetente devolveu a mensagem errada: %+v", mb)
	}
}

func TestValidProjectID(t *testing.T) {
	for _, ok := range []string{"lyriccut", "a", "piloto-2"} {
		if !ValidProjectID(ok) {
			t.Errorf("%q deveria ser válido", ok)
		}
	}
	for _, bad := range []string{"", "../x", "a/b", "A", "x:y", "-x", "a b"} {
		if ValidProjectID(bad) {
			t.Errorf("%q deveria ser inválido", bad)
		}
	}
}
