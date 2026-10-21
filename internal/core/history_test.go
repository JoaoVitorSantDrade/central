package core

import (
	"context"
	"testing"
	"time"
)

func TestHistoricoDeMensagens(t *testing.T) {
	c := newCore(t)
	ctx := context.Background()
	enter(t, c, "A", "EXECUTOR", "/a")
	enter(t, c, "B", "EXECUTOR", "/b")
	for _, corpo := range []string{"um", "dois", "tres", "quatro"} {
		send(t, c, "A", "B", corpo)
	}
	ms, err := c.Messages(ctx, 0, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(ms) != 2 || ms[0].Corpo != "tres" || ms[1].Corpo != "quatro" {
		t.Fatalf("limite devolve as mais novas em ordem: %+v", ms)
	}
	ms, err = c.Messages(ctx, ms[0].Seq, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(ms) != 1 || ms[0].Corpo != "quatro" {
		t.Fatalf("depois filtra por seq: %+v", ms)
	}
}

func TestPendenciasAbertas(t *testing.T) {
	c := newCore(t)
	ctx := context.Background()
	enter(t, c, "A", "EXECUTOR", "/a")
	enter(t, c, "B", "EXECUTOR", "/b")
	p1, err := c.Send(ctx, SendInput{De: "A", Para: "B", Tipo: "pergunta", Corpo: "x?", ResponderEm: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Send(ctx, SendInput{De: "B", Para: "A", Tipo: "pergunta", Corpo: "y?", ResponderEm: time.Hour}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Reply(ctx, "B", p1.Seq, "sim"); err != nil {
		t.Fatal(err)
	}
	abertas, err := c.OpenReplies(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(abertas) != 1 || abertas[0].Corpo != "y?" {
		t.Fatalf("só a pendência sem resposta: %+v", abertas)
	}
}
