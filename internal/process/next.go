package process

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/JoaoVitorSantDrade/central/internal/core"
)

// TipoFilaVazia é o aviso ao PLANEJADOR de que um executor pediu tarefa e não havia.
const TipoFilaVazia = "fila.vazia"

// FilaVaziaACada limita o aviso de fila vazia.
const FilaVaziaACada = 30 * time.Minute

// Escolha é a resposta do proxima: o item (nil se nenhum), o porquê e o que
// ficou de fora com o motivo.
type Escolha struct {
	Item        *core.Item        `json:"item"`
	Motivo      string            `json:"motivo"`
	Descartados map[string]string `json:"descartados"`
}

// grupos do proxima, na ordem (spec §6.3).
const (
	grupoSenior = iota // vaga sênior: as que já bloquearam
	grupoBugP0
	grupoSprint
	grupoFolga
	grupoResto
)

var nomeGrupo = map[int]string{
	grupoSenior: "já bloqueou uma vez e a vaga é sênior",
	grupoBugP0:  "bug P0",
	grupoSprint: "da sprint atual",
	grupoFolga:  "fila de folga",
	grupoResto:  "maior prioridade fora da sprint",
}

func rankPrioridade(p string) int {
	switch p {
	case "P0":
		return 0
	case "P1":
		return 1
	case "P2":
		return 2
	case "P3":
		return 3
	}
	return 4
}

// Proxima escolhe a melhor tarefa pronta para o executor (spec §6.3). Não
// reserva: o executor reserva em seguida. Lê tudo fora de transação.
func (e *Engine) Proxima(ctx context.Context, vaga string) (Escolha, error) {
	agente, _, err := e.vagaPapel(ctx, vaga, "pedir tarefa", Executor)
	if err != nil {
		return Escolha{}, err
	}
	esc := Escolha{Descartados: map[string]string{}}
	agora := e.now()
	sprint, err := e.c.CurrentSprint(ctx)
	if err != nil {
		return Escolha{}, err
	}
	if sprint != nil && !agora.Before(sprint.Congelamento) {
		esc.Motivo = fmt.Sprintf("a sprint %s está congelada: ninguém reserva tarefa nova", sprint.ID)
		return esc, nil
	}
	prontas, err := e.c.Items(ctx, core.ItemFilter{Tipos: []string{TipoTarefa}, Estados: []string{Pronta}})
	if err != nil {
		return Escolha{}, err
	}
	if len(prontas) == 0 {
		esc.Motivo = "fila vazia: não há tarefa pronta"
		return esc, e.avisarFilaVazia(ctx, vaga, agora)
	}
	ativos, err := e.c.Items(ctx, core.ItemFilter{Estados: []string{EmAndamento}})
	if err != nil {
		return Escolha{}, err
	}
	saturado := map[string]bool{}
	senior := e.roles.IsSenior(vaga)

	type cand struct {
		it                        core.Item
		grupo, sat, area, prio, b int
		bug                       int
	}
	var cs []cand
	for _, it := range prontas {
		switch {
		case it.Bloqueios >= 2:
			esc.Descartados[it.ID] = "duas notas de bloqueio: o PLANEJADOR e o GERENTE decidem"
			continue
		case it.Bloqueios == 1 && e.roles.HasSenior() && !senior:
			esc.Descartados[it.ID] = "já bloqueou uma vez: fica com a vaga sênior"
			continue
		}
		if o, ok := conflitoCom(it, ativos); ok {
			esc.Descartados[it.ID] = fmt.Sprintf("arquivos em comum com %s (%s)", o.ID, o.Dono)
			continue
		}
		c := cand{it: it, prio: rankPrioridade(it.Prioridade)}
		switch {
		case senior && it.Bloqueios == 1:
			c.grupo = grupoSenior
		case it.Categoria == "bug" && it.Prioridade == "P0":
			c.grupo = grupoBugP0
		case sprint != nil && it.Sprint == sprint.ID:
			c.grupo = grupoSprint
		case it.Folga:
			c.grupo = grupoFolga
		default:
			c.grupo = grupoResto
		}
		if it.Recurso != "" {
			sat, ok := saturado[it.Recurso]
			if !ok {
				sat = e.recursoSaturado(ctx, it.Recurso)
				saturado[it.Recurso] = sat
			}
			if sat {
				c.sat = 1
			}
		}
		if agente.Area != "" && it.Area != agente.Area {
			c.area = 1
		}
		if !it.Bloqueante {
			c.b = 1
		}
		if it.Categoria != "bug" {
			c.bug = 1
		}
		cs = append(cs, c)
	}
	if len(cs) == 0 {
		esc.Motivo = "nenhuma tarefa pronta serve agora (veja descartados)"
		return esc, nil
	}
	sort.Slice(cs, func(i, j int) bool {
		a, b := cs[i], cs[j]
		for _, k := range [][2]int{{a.grupo, b.grupo}, {a.sat, b.sat}, {a.area, b.area}, {a.prio, b.prio}, {a.b, b.b}, {a.bug, b.bug}} {
			if k[0] != k[1] {
				return k[0] < k[1]
			}
		}
		return a.it.ID < b.it.ID
	})
	best := cs[0]
	esc.Item = &best.it
	esc.Motivo = nomeGrupo[best.grupo]
	if best.sat == 1 {
		esc.Motivo += "; o recurso " + best.it.Recurso + " está ocupado (vai esperar na fila)"
	}
	return esc, nil
}

func conflitoCom(it core.Item, ativos []core.Item) (core.Item, bool) {
	for _, o := range ativos {
		if _, _, ok := conflito(it.Arquivos, o.Arquivos); ok {
			return o, true
		}
	}
	return core.Item{}, false
}

// recursoSaturado: alguém já espera na fila ou uma concessão exclusiva está ativa.
func (e *Engine) recursoSaturado(ctx context.Context, nome string) bool {
	v, err := e.c.ResourceStatus(ctx, nome)
	if err != nil {
		return false
	}
	if len(v.Fila) > 0 {
		return true
	}
	for _, g := range v.Ativas {
		if g.Exclusivo {
			return true
		}
	}
	return false
}

// avisarFilaVazia avisa o PLANEJADOR, no máximo uma vez a cada FilaVaziaACada.
func (e *Engine) avisarFilaVazia(ctx context.Context, vaga string, agora time.Time) error {
	ultimo, ok, err := e.c.LastEvent(ctx, TipoFilaVazia+"_avisada")
	if err != nil {
		return err
	}
	if ok && agora.Sub(ultimo) < FilaVaziaACada {
		return nil
	}
	err = e.c.WithItems(ctx, core.Sistema, func(tx core.ItemTx) error {
		if err := tx.Event(TipoFilaVazia+"_avisada", vaga, nil); err != nil {
			return err
		}
		return tx.Notify(e.roles.Name(Planejador), TipoFilaVazia, vaga,
			fmt.Sprintf("%s pediu tarefa e não há nenhuma pronta: prepare a fila (item criar / item pronto)", vaga))
	})
	if errors.Is(err, core.ErrNotFound) {
		return nil
	}
	return err
}
