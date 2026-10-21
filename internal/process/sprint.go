package process

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/JoaoVitorSantDrade/central/internal/core"
)

// Tipos das mensagens de sprint.
const (
	TipoEncerrarAntes = "sprint.encerrar_antes"
	// AntecipadoApos é quanto as condições do encerramento antecipado precisam
	// valer seguidas para o GERENTE ser avisado (spec §6.4).
	AntecipadoApos = 10 * time.Minute
)

// acordarPadrao diz quem cada cerimônia acorda (papéis canônicos ou TODOS).
var acordarPadrao = map[string][]string{
	"abertura":     {core.Todos},
	"checkin":      {Gerente, Planejador},
	"escopo":       {Gerente, Planejador, Integrador},
	"congelamento": {core.Todos},
	"corte":        {Integrador},
	"retro":        {Gerente, Planejador},
	"fim":          {core.Todos},
}

// textoCerimonia é o que cada cerimônia pede.
var textoCerimonia = map[string]string{
	"abertura":     "a sprint %s começou: planeje, reserve e trabalhe (central proxima)",
	"checkin":      "check-in da sprint %s: confira o andamento e replaneje o que não cabe",
	"escopo":       "escopo da versão da sprint %s: combinem o que entra no corte",
	"congelamento": "congelamento da sprint %s: ninguém reserva tarefa nova; termine ou bloqueie a sua",
	"corte":        "corte da sprint %s: entregue o que foi aprovado e corte a versão",
	"retro":        "retrospectiva da sprint %s: registre números, problemas e ações",
	"fim":          "fim da sprint %s: encerre o plantão",
}

// Horarios calcula os horários da sprint a partir da abertura e do fim.
func Horarios(abertura, fim time.Time, cfg SprintCfg) (core.Sprint, error) {
	cong := fim.Add(-cfg.Congelamento)
	if !cong.After(abertura) {
		return core.Sprint{}, fmt.Errorf("%w: expediente curto demais: o congelamento (%v antes do fim) cai antes da abertura",
			core.ErrInvalid, cfg.Congelamento)
	}
	return core.Sprint{
		Abertura: abertura, Checkin: abertura.Add(cong.Sub(abertura) / 2),
		Escopo: maxTime(abertura, fim.Add(-cfg.Escopo)), Congelamento: cong,
		Corte: fim.Add(-cfg.Corte), Retro: fim.Add(-cfg.Retro), Fim: fim,
	}, nil
}

func maxTime(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}

// ParseExpediente lê "HH:MM-HH:MM" no fuso loc. O início é a ocorrência de
// HH:MM mais perto de agora; o fim é a primeira ocorrência depois do início
// (o expediente pode cruzar a meia-noite).
func ParseExpediente(s string, agora time.Time, loc *time.Location) (time.Time, time.Time, error) {
	ini, fim, ok := strings.Cut(s, "-")
	hi, err1 := time.Parse("15:04", strings.TrimSpace(ini))
	hf, err2 := time.Parse("15:04", strings.TrimSpace(fim))
	if !ok || err1 != nil || err2 != nil {
		return time.Time{}, time.Time{}, fmt.Errorf("%w: expediente %q (use HH:MM-HH:MM, ex.: 22:00-07:00)", core.ErrInvalid, s)
	}
	a := agora.In(loc)
	inicio := time.Date(a.Year(), a.Month(), a.Day(), hi.Hour(), hi.Minute(), 0, 0, loc)
	for _, d := range []int{-1, 1} {
		alt := inicio.AddDate(0, 0, d)
		if alt.Sub(a).Abs() < inicio.Sub(a).Abs() {
			inicio = alt
		}
	}
	final := time.Date(inicio.Year(), inicio.Month(), inicio.Day(), hf.Hour(), hf.Minute(), 0, 0, loc)
	if !final.After(inicio) {
		final = final.AddDate(0, 0, 1)
	}
	return inicio, final, nil
}

// AbrirSprint (GERENTE) calcula os horários do expediente e abre a sprint.
// id vazio: S-<AAAAMMDD da abertura>, com -2, -3… se já existir.
func (e *Engine) AbrirSprint(ctx context.Context, vaga, expediente, id string) (core.Sprint, error) {
	if _, _, err := e.vagaPapel(ctx, vaga, "abrir a sprint", Gerente); err != nil {
		return core.Sprint{}, err
	}
	ini, fim, err := ParseExpediente(expediente, e.now(), e.loc)
	if err != nil {
		return core.Sprint{}, err
	}
	s, err := Horarios(ini, fim, e.cfg.Sprint)
	if err != nil {
		return core.Sprint{}, err
	}
	if id == "" {
		if id, err = e.proximoID(ctx, ini); err != nil {
			return core.Sprint{}, err
		}
	}
	s.ID = id
	if err := e.c.CreateSprint(ctx, vaga, s); err != nil {
		return core.Sprint{}, err
	}
	return e.c.SprintByID(ctx, id)
}

// now é o relógio do núcleo (o mesmo dos eventos).
func (e *Engine) now() time.Time { return e.c.Now() }

func (e *Engine) proximoID(ctx context.Context, abertura time.Time) (string, error) {
	ss, err := e.c.Sprints(ctx)
	if err != nil {
		return "", err
	}
	usados := map[string]bool{}
	for _, s := range ss {
		usados[s.ID] = true
	}
	base := "S-" + abertura.In(e.loc).Format("20060102")
	if !usados[base] {
		return base, nil
	}
	for n := 2; ; n++ {
		if id := fmt.Sprintf("%s-%d", base, n); !usados[id] {
			return id, nil
		}
	}
}

// VerSprint devolve a sprint aberta (nil se não há).
func (e *Engine) VerSprint(ctx context.Context) (*core.Sprint, error) { return e.c.CurrentSprint(ctx) }

func (e *Engine) sprintAberta(ctx context.Context) (core.Sprint, error) {
	s, err := e.c.CurrentSprint(ctx)
	if err != nil {
		return core.Sprint{}, err
	}
	if s == nil {
		return core.Sprint{}, fmt.Errorf("%w: não há sprint aberta", core.ErrNotFound)
	}
	return *s, nil
}

// Antecipar (GERENTE) leva o congelamento e o corte para agora, a retro para
// agora + 10 min e o fim para agora + 30 min, guardando os horários originais.
func (e *Engine) Antecipar(ctx context.Context, vaga string) (core.Sprint, error) {
	if _, _, err := e.vagaPapel(ctx, vaga, "antecipar a sprint", Gerente); err != nil {
		return core.Sprint{}, err
	}
	s, err := e.sprintAberta(ctx)
	if err != nil {
		return core.Sprint{}, err
	}
	agora := e.now()
	if s.Original == nil {
		s.Original = map[string]time.Time{"escopo": s.Escopo, "congelamento": s.Congelamento, "corte": s.Corte,
			"retro": s.Retro, "fim": s.Fim}
	}
	s.Checkin = minTime(s.Checkin, agora)
	s.Escopo = minTime(s.Escopo, agora)
	s.Congelamento, s.Corte = agora, agora
	s.Retro, s.Fim = agora.Add(10*time.Minute), agora.Add(30*time.Minute)
	s.AntecipadaEm = &agora
	if err := e.c.UpdateSprint(ctx, vaga, s, "antecipada"); err != nil {
		return core.Sprint{}, err
	}
	return s, nil
}

func minTime(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}

// EncerrarSprint (GERENTE) fecha a sprint aberta.
func (e *Engine) EncerrarSprint(ctx context.Context, vaga string) (core.Sprint, error) {
	if _, _, err := e.vagaPapel(ctx, vaga, "encerrar a sprint", Gerente); err != nil {
		return core.Sprint{}, err
	}
	s, err := e.sprintAberta(ctx)
	if err != nil {
		return core.Sprint{}, err
	}
	agora := e.now()
	s.EncerradaEm = &agora
	if err := e.c.UpdateSprint(ctx, vaga, s, "encerrada"); err != nil {
		return core.Sprint{}, err
	}
	return s, nil
}

// TickSprint dispara as cerimônias vencidas e cuida do encerramento antecipado.
func (e *Engine) TickSprint(ctx context.Context) error {
	agora := e.now()
	due, err := e.c.DueCeremonies(ctx, agora)
	if err != nil {
		return err
	}
	for _, cer := range due {
		para := e.destinatarios(cer.Nome)
		corpo := fmt.Sprintf(textoCerimonia[cer.Nome], cer.Sprint)
		if _, err := e.c.MarkCeremony(ctx, cer.Sprint, cer.Nome, cer.Quando, para, corpo); err != nil {
			return err
		}
	}
	return e.tickAntecipado(ctx, agora)
}

func (e *Engine) destinatarios(cerimonia string) []string {
	papeis := acordarPadrao[cerimonia]
	if v, ok := e.cfg.Sprint.Acordar[cerimonia]; ok {
		papeis = v
	}
	var para []string
	for _, p := range papeis {
		if p == core.Todos {
			para = append(para, core.Todos)
			continue
		}
		if e.roles.Enabled(p) {
			para = append(para, e.roles.Name(p))
		}
	}
	return para
}

// tickAntecipado avisa o GERENTE quando as condições do encerramento
// antecipado valem por AntecipadoApos seguidos (spec §6.4).
func (e *Engine) tickAntecipado(ctx context.Context, agora time.Time) error {
	s, err := e.c.CurrentSprint(ctx)
	if err != nil || s == nil || !agora.Before(s.Corte) {
		return err
	}
	cond, err := e.condicaoAntecipado(ctx)
	if err != nil {
		return err
	}
	switch {
	case !cond && (s.AnteciparDesde != nil || s.AnteciparAvisado):
		s.AnteciparDesde, s.AnteciparAvisado = nil, false
		return e.c.UpdateSprint(ctx, core.Sistema, *s, "encerramento_condicao")
	case cond && s.AnteciparDesde == nil:
		s.AnteciparDesde = &agora
		return e.c.UpdateSprint(ctx, core.Sistema, *s, "encerramento_condicao")
	case cond && !s.AnteciparAvisado && agora.Sub(*s.AnteciparDesde) >= AntecipadoApos:
		s.AnteciparAvisado = true
		corpo := fmt.Sprintf("a sprint %s está sem trabalho há %v (nada em andamento, nada esperando QA ou entrega, executores ociosos): "+
			"confirme com o PLANEJADOR e o INTEGRADOR e, se for o caso, central sprint antecipar", s.ID, agora.Sub(*s.AnteciparDesde).Round(time.Minute))
		return e.c.UpdateSprint(ctx, core.Sistema, *s, "encerramento_avisado",
			core.SystemMsg{Para: e.roles.Name(Gerente), Tipo: TipoEncerrarAntes, Ref: s.ID, Corpo: corpo})
	}
	return nil
}

// condicaoAntecipado: nada em andamento, nada concluído/em revisão/aprovado
// esperando, e todos os executores vivos ociosos (há ao menos um executor).
func (e *Engine) condicaoAntecipado(ctx context.Context) (bool, error) {
	cond := false
	err := e.c.WithItems(ctx, core.Sistema, func(tx core.ItemTx) error {
		ativos, err := tx.List(core.ItemFilter{Tipos: []string{TipoTarefa, TipoMissao},
			Estados: []string{EmAndamento, Concluida, EmRevisao, Aprovada}})
		if err != nil || len(ativos) > 0 {
			return err
		}
		as, err := tx.Agents()
		if err != nil {
			return err
		}
		executores := 0
		for _, a := range as {
			if c, _ := e.roles.Canonical(a.Papel); c != Executor || a.Estado == core.EstadoEncerrado {
				continue
			}
			executores++
			if a.Estado != core.EstadoOcioso && a.Estado != core.EstadoSumido && a.Estado != core.EstadoSuspenso {
				return nil
			}
		}
		cond = executores > 0
		return nil
	})
	if errors.Is(err, core.ErrNotFound) {
		return false, nil
	}
	return cond, err
}
