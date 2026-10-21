package process

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"time"

	"github.com/JoaoVitorSantDrade/central/internal/core"
)

// ItemParado é um item que passou do limite do estado em que está.
type ItemParado struct {
	Item   string    `json:"item"`
	Estado string    `json:"estado"`
	Desde  time.Time `json:"desde"`
	Limite string    `json:"limite"`
	Dono   string    `json:"dono"`
}

// Cerimonia é a próxima cerimônia da sprint aberta.
type Cerimonia struct {
	Nome   string    `json:"nome"`
	Quando time.Time `json:"quando"`
}

// Saude é o retrato do que precisa de atenção no projeto agora.
type Saude struct {
	Sumidos            []core.Agent   `json:"sumidos"`
	Parados            []ItemParado   `json:"parados"`
	FilaQA             int            `json:"fila_qa"`
	ConcessoesVencidas []core.Grant   `json:"concessoes_vencidas"`
	PendenciasVencidas []core.Message `json:"pendencias_vencidas"`
	ProximaCerimonia   *Cerimonia     `json:"proxima_cerimonia"`
	Alertas            []string       `json:"alertas"`
}

// Saude reúne sumidos, itens parados, fila de QA, concessões vencidas,
// respostas atrasadas e a próxima cerimônia, com uma lista de alertas em texto.
func (e *Engine) Saude(ctx context.Context) (Saude, error) {
	agora := e.now()
	s := Saude{Sumidos: []core.Agent{}, Parados: []ItemParado{}, ConcessoesVencidas: []core.Grant{}, Alertas: []string{}}
	as, err := e.c.Agents(ctx)
	if err != nil {
		return Saude{}, err
	}
	for _, a := range as {
		if a.Estado == core.EstadoSumido {
			s.Sumidos = append(s.Sumidos, a)
			s.Alertas = append(s.Alertas, fmt.Sprintf("%s (%s) sumida desde %s", a.Vaga, a.Papel, a.Batimento.Format("15:04")))
		}
	}
	estados := make([]string, 0, len(e.cfg.LimiteParado))
	for est := range e.cfg.LimiteParado {
		estados = append(estados, est)
	}
	itens, err := e.c.Items(ctx, core.ItemFilter{Estados: append(estados, Concluida)})
	if err != nil {
		return Saude{}, err
	}
	for _, it := range itens {
		if it.Estado == Concluida {
			s.FilaQA++
		}
		lim, ok := e.cfg.LimiteParado[it.Estado]
		if !ok || agora.Sub(it.EstadoDesde) <= lim {
			continue
		}
		s.Parados = append(s.Parados, ItemParado{Item: it.ID, Estado: it.Estado, Desde: it.EstadoDesde, Limite: lim.String(), Dono: it.Dono})
		s.Alertas = append(s.Alertas, fmt.Sprintf("%s parada em %s há %v (limite %v)", it.ID, it.Estado, agora.Sub(it.EstadoDesde).Round(time.Minute), lim))
	}
	if s.FilaQA > 3 {
		s.Alertas = append(s.Alertas, fmt.Sprintf("fila de QA com %d tarefas", s.FilaQA))
	}
	for _, nome := range e.c.ResourceNames() {
		v, err := e.c.ResourceStatus(ctx, nome)
		if err != nil {
			return Saude{}, err
		}
		for _, g := range v.Ativas {
			if g.Prazo != nil && agora.After(*g.Prazo) {
				s.ConcessoesVencidas = append(s.ConcessoesVencidas, g)
				s.Alertas = append(s.Alertas, fmt.Sprintf("concessão %d de %s em %s passou do prazo (%s)", g.ID, g.Vaga, nome, g.Prazo.Format("15:04")))
			}
		}
	}
	if s.PendenciasVencidas, err = e.c.OverdueReplies(ctx); err != nil {
		return Saude{}, err
	}
	for _, m := range s.PendenciasVencidas {
		s.Alertas = append(s.Alertas, fmt.Sprintf("resposta atrasada: mensagem %d de %s para %s", m.Seq, m.De, m.Para))
	}
	if sp, err := e.c.CurrentSprint(ctx); err != nil {
		return Saude{}, err
	} else if sp != nil {
		for _, nome := range core.CeremonyNames {
			if q, _ := sp.Horario(nome); q.After(agora) {
				s.ProximaCerimonia = &Cerimonia{Nome: nome, Quando: q}
				break
			}
		}
	}
	return s, nil
}

// estadosReservados são os estados em que um item tem dono.
var estadosReservados = []string{EmAndamento, Concluida, EmRevisao, Aprovada, Rejeitada}

// Orfas lista os itens com dono encerrado, inexistente ou sumido há mais de OrfasApos.
func (e *Engine) Orfas(ctx context.Context) ([]core.Item, error) {
	agora := e.now()
	itens, err := e.c.Items(ctx, core.ItemFilter{Estados: estadosReservados})
	if err != nil {
		return nil, err
	}
	as, err := e.c.Agents(ctx)
	if err != nil {
		return nil, err
	}
	por := map[string]core.Agent{}
	for _, a := range as {
		por[a.Vaga] = a
	}
	orfas := []core.Item{}
	for _, it := range itens {
		if it.Dono == "" {
			continue
		}
		a, ok := por[it.Dono]
		switch {
		case !ok, a.Estado == core.EstadoEncerrado:
		case a.Estado == core.EstadoSumido && agora.Sub(a.Batimento) > e.cfg.OrfasApos:
		default:
			continue
		}
		orfas = append(orfas, it)
	}
	sort.Slice(orfas, func(i, j int) bool { return orfas[i].ID < orfas[j].ID })
	return orfas, nil
}

// LiberarOrfas (GERENTE ou INTEGRADOR) devolve a pronta as órfãs em andamento
// ou rejeitadas, com uma nota; as que esperam QA ou entrega só são listadas
// por Orfas (alguém ainda pode revisá-las e integrá-las sem o dono).
func (e *Engine) LiberarOrfas(ctx context.Context, vaga string) ([]core.Item, error) {
	if _, _, err := e.vagaPapel(ctx, vaga, "liberar órfãs", Gerente, Integrador); err != nil {
		return nil, err
	}
	orfas, err := e.Orfas(ctx)
	if err != nil {
		return nil, err
	}
	liberadas := []core.Item{}
	err = e.c.WithItems(ctx, vaga, func(tx core.ItemTx) error {
		for _, o := range orfas {
			it, err := tx.Get(o.ID)
			if err != nil {
				return err
			}
			if !slices.Contains([]string{EmAndamento, Rejeitada}, it.Estado) || it.Dono != o.Dono {
				continue
			}
			if err := tx.Note(it.ID, "orfa", fmt.Sprintf("liberada por %s: o dono %s sumiu", vaga, it.Dono)); err != nil {
				return err
			}
			antigo := it.Dono
			it.Estado, it.Dono = Pronta, ""
			if err := tx.Save(it, "orfa_liberada", map[string]any{"dono_anterior": antigo}); err != nil {
				return err
			}
			if err := tx.Notify(e.roles.Name(Planejador), "item."+Pronta, it.ID,
				fmt.Sprintf("%s voltou a pronta: o dono %s sumiu", it.ID, antigo)); err != nil {
				return err
			}
			liberadas = append(liberadas, it)
		}
		return nil
	})
	return liberadas, err
}
