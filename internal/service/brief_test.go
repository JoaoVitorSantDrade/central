package service

import (
	"strings"
	"testing"

	"github.com/JoaoVitorSantDrade/central/internal/core"
)

func TestBriefNaoAvancaCursor(t *testing.T) {
	s, _ := newService(t)
	mustEnter(t, s, "p", "/a", "A1")
	mustEnter(t, s, "p", "/b", "B1")
	if _, err := s.Send(ctx, "p", "/a", core.SendInput{Para: "B1", Tipo: "nota", Corpo: "leia isto"}); err != nil {
		t.Fatal(err)
	}
	b, err := s.Brief(ctx, "p", "/b", true, 0)
	if err != nil || !strings.Contains(b.Texto, "leia isto") || len(b.Blocos) != 3 {
		t.Fatalf("brief = %+v, %v", b, err)
	}
	if ms, _ := s.Inbox(ctx, "p", "/b"); len(ms) != 1 {
		t.Fatalf("o brief consumiu a caixa: %+v", ms)
	}
	p, _ := s.project(ctx, "p")
	evs, _ := p.core.Events(ctx, core.EventFilter{Tipos: []string{"brief.gerado"}})
	if len(evs) != 1 || evs[0].Dados["tokens"].(float64) <= 0 {
		t.Fatalf("eventos = %+v", evs)
	}
}
