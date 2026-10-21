package process

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/JoaoVitorSantDrade/central/internal/core"
)

// Latencia resume os tempos de uma passagem entre estados, em segundos.
type Latencia struct {
	N        int     `json:"n"`
	MedianaS float64 `json:"mediana_s"`
	P75S     float64 `json:"p75_s"`
}

// Metricas são os números de uma janela (a sprint, ou as últimas 24 h),
// calculados dos eventos (spec §6.4).
type Metricas struct {
	Sprint                 string              `json:"sprint"`
	De                     time.Time           `json:"de"`
	Ate                    time.Time           `json:"ate"`
	Integradas             int                 `json:"integradas"`
	Rejeicoes              int                 `json:"rejeicoes"`
	Bloqueios              int                 `json:"bloqueios"`
	Latencias              map[string]Latencia `json:"latencias"`
	TarefasPorExecutorHora float64             `json:"tarefas_por_executor_hora"`
	UsoRecursos            map[string]float64  `json:"uso_recursos"`
	OciosidadeMin          map[string]int      `json:"ociosidade_min"`
}

// passagens medidas: nome → (estado de partida, estado de chegada).
var passagens = map[string][2]string{
	"pronta_em_andamento":  {Pronta, EmAndamento},
	"concluida_em_revisao": {Concluida, EmRevisao},
	"concluida_integrada":  {Concluida, Integrada},
}

// janela devolve o intervalo das métricas: a sprint dada (ou a aberta) até
// agora ou o fim dela; sem sprint, as últimas 24 h.
func (e *Engine) janela(ctx context.Context, sprintID string) (string, time.Time, time.Time, error) {
	agora := e.now()
	var s *core.Sprint
	if sprintID != "" {
		sp, err := e.c.SprintByID(ctx, sprintID)
		if err != nil {
			return "", time.Time{}, time.Time{}, err
		}
		s = &sp
	} else {
		var err error
		if s, err = e.c.CurrentSprint(ctx); err != nil {
			return "", time.Time{}, time.Time{}, err
		}
	}
	if s == nil {
		return "", agora.Add(-24 * time.Hour), agora, nil
	}
	ate := agora
	if s.EncerradaEm != nil && s.EncerradaEm.Before(ate) {
		ate = *s.EncerradaEm
	}
	return s.ID, s.Abertura, ate, nil
}

func resumir(vs []float64) Latencia {
	if len(vs) == 0 {
		return Latencia{}
	}
	sort.Float64s(vs)
	pct := func(p float64) float64 { return vs[int(p*float64(len(vs)-1)+0.5)] }
	return Latencia{N: len(vs), MedianaS: pct(0.5), P75S: pct(0.75)}
}

// Metricas calcula os números da sprint (ou da janela padrão).
func (e *Engine) Metricas(ctx context.Context, sprintID string) (Metricas, error) {
	id, de, ate, err := e.janela(ctx, sprintID)
	if err != nil {
		return Metricas{}, err
	}
	m := Metricas{Sprint: id, De: de, Ate: ate, Latencias: map[string]Latencia{}, UsoRecursos: map[string]float64{},
		OciosidadeMin: map[string]int{}}

	// Passagens entre estados: o evento de item traz o estado novo e o anterior.
	evs, err := e.c.Events(ctx, core.EventFilter{Prefixos: []string{"item."}, Ate: ate})
	if err != nil {
		return Metricas{}, err
	}
	entrou := map[string]map[string]time.Time{} // item → estado → quando entrou
	tempos := map[string][]float64{}
	for _, ev := range evs {
		estado, _ := ev.Dados["estado"].(string)
		if estado == "" {
			continue
		}
		deEstado, _ := ev.Dados["de"].(string)
		primeiro := entrou[ev.Ref] == nil
		if primeiro {
			entrou[ev.Ref] = map[string]time.Time{}
		}
		// Só é passagem o evento que muda o estado (traz "de") ou o que cria o
		// item; os demais (sincronização, edição) não mexem nas contas.
		mudou := (deEstado != "" && deEstado != estado) || primeiro
		if !mudou {
			continue
		}
		dentro := !ev.Quando.Before(de)
		if dentro {
			switch {
			case estado == Integrada && deEstado != Integrada:
				m.Integradas++
			case estado == Rejeitada:
				m.Rejeicoes++
			}
			if ev.Tipo == "item.bloqueada" {
				m.Bloqueios++
			}
			for nome, p := range passagens {
				if estado != p[1] || (nome == "pronta_em_andamento" && deEstado != Pronta) {
					continue
				}
				if t0, ok := entrou[ev.Ref][p[0]]; ok {
					tempos[nome] = append(tempos[nome], ev.Quando.Sub(t0).Seconds())
				}
			}
		}
		entrou[ev.Ref][estado] = ev.Quando
	}
	for nome := range passagens {
		m.Latencias[nome] = resumir(tempos[nome])
	}

	horas := ate.Sub(de).Hours()
	if as, err := e.c.Agents(ctx); err == nil && horas > 0 {
		exec := 0
		for _, a := range as {
			if c, _ := e.roles.Canonical(a.Papel); c == Executor {
				exec++
			}
		}
		if exec > 0 {
			m.TarefasPorExecutorHora = float64(m.Integradas) / (float64(exec) * horas)
		}
	} else if err != nil {
		return Metricas{}, err
	}

	for _, nome := range e.c.ResourceNames() {
		gs, err := e.c.GrantsBetween(ctx, nome, de, ate)
		if err != nil {
			return Metricas{}, err
		}
		m.UsoRecursos[nome] = usoPct(gs, de, ate)
	}

	if err := e.ociosidade(ctx, &m, de, ate); err != nil {
		return Metricas{}, err
	}
	return m, nil
}

// usoPct é a porcentagem da janela com ao menos uma concessão ativa.
func usoPct(gs []core.Grant, de, ate time.Time) float64 {
	total := ate.Sub(de)
	if total <= 0 {
		return 0
	}
	type iv struct{ a, b time.Time }
	var ivs []iv
	for _, g := range gs {
		a, b := *g.Inicio, ate
		if g.Fim != nil {
			b = *g.Fim
		}
		if a.Before(de) {
			a = de
		}
		if b.After(ate) {
			b = ate
		}
		if b.After(a) {
			ivs = append(ivs, iv{a, b})
		}
	}
	sort.Slice(ivs, func(i, j int) bool { return ivs[i].a.Before(ivs[j].a) })
	var soma time.Duration
	var cur *iv
	for i := range ivs {
		switch {
		case cur == nil:
			cur = &ivs[i]
		case !ivs[i].a.After(cur.b):
			if ivs[i].b.After(cur.b) {
				cur.b = ivs[i].b
			}
		default:
			soma += cur.b.Sub(cur.a)
			cur = &ivs[i]
		}
	}
	if cur != nil {
		soma += cur.b.Sub(cur.a)
	}
	return 100 * soma.Seconds() / total.Seconds()
}

// ociosidade soma, por vaga, os minutos em ocioso dentro da janela (eventos agente.status).
func (e *Engine) ociosidade(ctx context.Context, m *Metricas, de, ate time.Time) error {
	evs, err := e.c.Events(ctx, core.EventFilter{Tipos: []string{"agente.status", "agente.entrou", "agente.saiu"}, Ate: ate})
	if err != nil {
		return err
	}
	desde := map[string]time.Time{} // vaga → início do ocioso em curso
	somar := func(vaga string, fim time.Time) {
		ini, ok := desde[vaga]
		if !ok {
			return
		}
		delete(desde, vaga)
		if ini.Before(de) {
			ini = de
		}
		if fim.After(ini) {
			m.OciosidadeMin[vaga] += int(fim.Sub(ini).Round(time.Minute) / time.Minute)
		}
	}
	for _, ev := range evs {
		estado := ""
		switch ev.Tipo {
		case "agente.status":
			estado, _ = ev.Dados["estado"].(string)
		case "agente.entrou":
			estado = core.EstadoAtivo
		case "agente.saiu":
			estado = core.EstadoEncerrado
		}
		if estado == "" {
			continue
		}
		if estado == core.EstadoOcioso {
			if _, ok := desde[ev.Quem]; !ok {
				desde[ev.Quem] = ev.Quando
			}
			continue
		}
		somar(ev.Quem, ev.Quando)
	}
	for vaga := range desde {
		somar(vaga, ate)
	}
	return nil
}

// Texto é um resumo curto das métricas (para a CLI com --texto e para o brief).
func (m Metricas) Texto() string {
	var b strings.Builder
	fmt.Fprintf(&b, "sprint %s: %d integradas, %d rejeições, %d bloqueios", m.Sprint, m.Integradas, m.Rejeicoes, m.Bloqueios)
	if l := m.Latencias["concluida_integrada"]; l.N > 0 {
		fmt.Fprintf(&b, "; concluída→integrada mediana %v", time.Duration(l.MedianaS*float64(time.Second)).Round(time.Minute))
	}
	return b.String()
}
