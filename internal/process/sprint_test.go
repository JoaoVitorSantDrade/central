package process

import (
	"errors"
	"testing"
	"time"

	"github.com/JoaoVitorSantDrade/central/internal/core"
)

func TestHorariosPadrao(t *testing.T) {
	ab := time.Date(2026, 10, 20, 22, 0, 0, 0, time.UTC)
	fim := ab.Add(8 * time.Hour)
	s, err := Horarios(ab, fim, defaultConfig().Sprint)
	if err != nil {
		t.Fatal(err)
	}
	cong := fim.Add(-60 * time.Minute)
	if !s.Congelamento.Equal(cong) || !s.Escopo.Equal(fim.Add(-75*time.Minute)) || !s.Corte.Equal(fim.Add(-30*time.Minute)) ||
		!s.Retro.Equal(fim.Add(-20*time.Minute)) || !s.Checkin.Equal(ab.Add(cong.Sub(ab)/2)) || !s.Fim.Equal(fim) {
		t.Fatalf("horários = %+v", s)
	}
	if _, err := Horarios(ab, ab.Add(50*time.Minute), defaultConfig().Sprint); !errors.Is(err, core.ErrInvalid) {
		t.Fatalf("expediente curto demais aceito: %v", err)
	}
}

func TestParseExpedienteMeiaNoite(t *testing.T) {
	agora := time.Date(2026, 10, 20, 22, 5, 0, 0, time.UTC)
	ini, fim, err := ParseExpediente("22:00-07:00", agora, time.UTC)
	if err != nil || !ini.Equal(time.Date(2026, 10, 20, 22, 0, 0, 0, time.UTC)) || !fim.Equal(time.Date(2026, 10, 21, 7, 0, 0, 0, time.UTC)) {
		t.Fatalf("ini %v fim %v err %v", ini, fim, err)
	}
	// aberto depois da meia-noite: o início mais perto do agora é o de ontem
	agora = time.Date(2026, 10, 21, 1, 0, 0, 0, time.UTC)
	ini, fim, err = ParseExpediente("22:00-07:00", agora, time.UTC)
	if err != nil || !ini.Equal(time.Date(2026, 10, 20, 22, 0, 0, 0, time.UTC)) || !fim.Equal(time.Date(2026, 10, 21, 7, 0, 0, 0, time.UTC)) {
		t.Fatalf("depois da meia-noite: ini %v fim %v err %v", ini, fim, err)
	}
	for _, ruim := range []string{"22-07", "25:00-07:00", "22:00", ""} {
		if _, _, err := ParseExpediente(ruim, agora, time.UTC); !errors.Is(err, core.ErrInvalid) {
			t.Errorf("%q aceito: %v", ruim, err)
		}
	}
}

func TestIdComSufixo(t *testing.T) {
	env := newEnv(t, NewRoles(nil, nil, nil), defaultConfig())
	env.enter("G", Gerente)
	s, err := env.e.AbrirSprint(ctx, "G", "22:00-23:30", "")
	if err != nil || s.ID != "S-20261020" {
		t.Fatalf("s = %+v, %v", s, err)
	}
	if _, err := env.e.EncerrarSprint(ctx, "G"); err != nil {
		t.Fatal(err)
	}
	s, err = env.e.AbrirSprint(ctx, "G", "22:00-23:30", "")
	if err != nil || s.ID != "S-20261020-2" {
		t.Fatalf("segunda = %+v, %v", s, err)
	}
}

func TestSoGerenteAbreSprint(t *testing.T) {
	env := newEnv(t, NewRoles(map[string]string{Executor: "FIXER"}, nil, nil), defaultConfig())
	env.enter("F1", "FIXER")
	if _, err := env.e.AbrirSprint(ctx, "F1", "22:00-23:30", ""); !errors.Is(err, core.ErrInvalid) {
		t.Fatalf("FIXER abriu sprint: %v", err)
	}
}

func TestCerimoniasDisparamUmaVez(t *testing.T) {
	env := newEnv(t, NewRoles(nil, nil, nil), defaultConfig())
	env.enter("G", Gerente)
	env.enter("P", Planejador)
	env.enter("F", Executor)
	if _, err := env.e.AbrirSprint(ctx, "G", "22:00-06:00", ""); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if err := env.e.TickSprint(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if ms := ofType(env.inbox("F"), "cerimonia.abertura"); len(ms) != 1 {
		t.Fatalf("abertura para F = %+v", ms)
	}
	env.advance(4 * time.Hour) // passa do checkin (22:00 + 3h30)
	if err := env.e.TickSprint(ctx); err != nil {
		t.Fatal(err)
	}
	if ms := ofType(env.inbox("P"), "cerimonia.checkin"); len(ms) != 1 {
		t.Fatalf("checkin para P = %+v", ms)
	}
	if ms := ofType(env.inbox("F"), "cerimonia.checkin"); len(ms) != 0 {
		t.Fatalf("checkin acordou o executor: %+v", ms)
	}
}

func TestSprintCruzaMeiaNoiteEReinicio(t *testing.T) {
	roles := NewRoles(nil, nil, nil)
	env := newEnv(t, roles, defaultConfig())
	env.enter("G", Gerente)
	env.enter("F", Executor)
	if _, err := env.e.AbrirSprint(ctx, "G", "22:00-07:00", ""); err != nil {
		t.Fatal(err)
	}
	if err := env.e.TickSprint(ctx); err != nil {
		t.Fatal(err)
	}
	env.inbox("F")
	// o daemon cai e volta às 06:10: escopo (05:45) e congelamento (06:00) vencidos
	env.advance(8*time.Hour + 10*time.Minute)
	env.open(roles, defaultConfig())
	if err := env.e.TickSprint(ctx); err != nil {
		t.Fatal(err)
	}
	if err := env.e.TickSprint(ctx); err != nil {
		t.Fatal(err)
	}
	ms := env.inbox("F")
	if len(ofType(ms, "cerimonia.congelamento")) != 1 || len(ofType(ms, "cerimonia.abertura")) != 0 {
		t.Fatalf("depois do reinício F recebeu %+v", ms)
	}
	if len(ofType(env.inbox("G"), "cerimonia.escopo")) != 1 {
		t.Fatal("escopo vencido não disparou para o GERENTE")
	}
}

func TestEncerramentoAntecipado(t *testing.T) {
	env := newEnv(t, NewRoles(nil, nil, nil), defaultConfig())
	env.enter("G", Gerente)
	env.enter("F", Executor)
	if _, err := env.c.SetStatus(ctx, "F", core.EstadoOcioso, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := env.e.AbrirSprint(ctx, "G", "22:00-06:00", ""); err != nil {
		t.Fatal(err)
	}
	tickN := func(d time.Duration) {
		env.advance(d)
		if err := env.c.Touch(ctx, "F"); err != nil { // F segue vivo e ocioso
			t.Fatal(err)
		}
		if err := env.e.TickSprint(ctx); err != nil {
			t.Fatal(err)
		}
	}
	tickN(0)
	tickN(9 * time.Minute)
	if ms := ofType(env.inbox("G"), "sprint.encerrar_antes"); len(ms) != 0 {
		t.Fatalf("avisou com 9 min: %+v", ms)
	}
	tickN(time.Minute)
	if ms := ofType(env.inbox("G"), "sprint.encerrar_antes"); len(ms) != 1 {
		t.Fatalf("aos 10 min: %+v", ms)
	}
	tickN(5 * time.Minute)
	if ms := ofType(env.inbox("G"), "sprint.encerrar_antes"); len(ms) != 0 {
		t.Fatalf("avisou de novo: %+v", ms)
	}
	// a condição cai (F trabalhando) e volta: novo aviso só depois de mais 10 min
	if _, err := env.c.SetStatus(ctx, "F", core.EstadoTrabalhando, "x"); err != nil {
		t.Fatal(err)
	}
	tickN(time.Minute)
	if _, err := env.c.SetStatus(ctx, "F", core.EstadoOcioso, ""); err != nil {
		t.Fatal(err)
	}
	tickN(time.Minute)
	tickN(9 * time.Minute)
	if ms := ofType(env.inbox("G"), "sprint.encerrar_antes"); len(ms) != 0 {
		t.Fatalf("avisou antes de 10 min contínuos: %+v", ms)
	}
	tickN(time.Minute)
	if ms := ofType(env.inbox("G"), "sprint.encerrar_antes"); len(ms) != 1 {
		t.Fatalf("segundo aviso: %+v", ms)
	}
}

func TestAnteciparGuardaOriginais(t *testing.T) {
	env := newEnv(t, NewRoles(nil, nil, nil), defaultConfig())
	env.enter("G", Gerente)
	s0, err := env.e.AbrirSprint(ctx, "G", "22:00-06:00", "")
	if err != nil {
		t.Fatal(err)
	}
	env.advance(time.Hour)
	s, err := env.e.Antecipar(ctx, "G")
	if err != nil {
		t.Fatal(err)
	}
	agora := *env.now
	if !s.Congelamento.Equal(agora) || !s.Corte.Equal(agora) || !s.Retro.Equal(agora.Add(10*time.Minute)) ||
		!s.Fim.Equal(agora.Add(30*time.Minute)) || !s.Original["fim"].Equal(s0.Fim) || s.AntecipadaEm == nil {
		t.Fatalf("antecipada = %+v", s)
	}
}
