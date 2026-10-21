package service

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/JoaoVitorSantDrade/central/internal/adapters/lessons"
	"github.com/JoaoVitorSantDrade/central/internal/core"
	"github.com/JoaoVitorSantDrade/central/internal/process"
)

// AbrirSprint abre a sprint do expediente (GERENTE).
func (s *Service) AbrirSprint(ctx context.Context, projetoID, pasta, expediente, id string) (core.Sprint, error) {
	p, a, err := s.agent(ctx, projetoID, pasta)
	if err != nil {
		return core.Sprint{}, err
	}
	return p.engine().AbrirSprint(ctx, a.Vaga, expediente, id)
}

// VerSprint devolve a sprint aberta (nil se não há).
func (s *Service) VerSprint(ctx context.Context, projetoID, pasta string) (*core.Sprint, error) {
	p, err := s.reader(ctx, projetoID, pasta)
	if err != nil {
		return nil, err
	}
	return p.engine().VerSprint(ctx)
}

// AnteciparSprint antecipa o corte e o fim da sprint aberta (GERENTE).
func (s *Service) AnteciparSprint(ctx context.Context, projetoID, pasta string) (core.Sprint, error) {
	p, a, err := s.agent(ctx, projetoID, pasta)
	if err != nil {
		return core.Sprint{}, err
	}
	return p.engine().Antecipar(ctx, a.Vaga)
}

// EncerrarSprint fecha a sprint aberta (GERENTE).
func (s *Service) EncerrarSprint(ctx context.Context, projetoID, pasta string) (core.Sprint, error) {
	p, a, err := s.agent(ctx, projetoID, pasta)
	if err != nil {
		return core.Sprint{}, err
	}
	return p.engine().EncerrarSprint(ctx, a.Vaga)
}

// CriarItem propõe um item novo.
func (s *Service) CriarItem(ctx context.Context, projetoID, pasta string, in process.ItemInput) (core.Item, error) {
	p, a, err := s.agent(ctx, projetoID, pasta)
	if err != nil {
		return core.Item{}, err
	}
	return p.engine().Criar(ctx, a.Vaga, in)
}

// AcaoItem aplica uma ação do ciclo de vida (reservar, concluir…) ao item.
func (s *Service) AcaoItem(ctx context.Context, projetoID, pasta, acao, id string, args process.Args) (core.Item, error) {
	p, a, err := s.agent(ctx, projetoID, pasta)
	if err != nil {
		return core.Item{}, err
	}
	return p.engine().Acao(ctx, a.Vaga, acao, id, args)
}

// VerItem devolve o item e as notas dele.
func (s *Service) VerItem(ctx context.Context, projetoID, pasta, id string) (core.Item, []core.Note, error) {
	p, err := s.reader(ctx, projetoID, pasta)
	if err != nil {
		return core.Item{}, nil, err
	}
	return p.core.Item(ctx, id)
}

// Itens lista os itens pelo filtro.
func (s *Service) Itens(ctx context.Context, projetoID, pasta string, f core.ItemFilter) ([]core.Item, error) {
	p, err := s.reader(ctx, projetoID, pasta)
	if err != nil {
		return nil, err
	}
	return p.core.Items(ctx, f)
}

// Proxima escolhe a melhor tarefa pronta para a vaga (EXECUTOR).
func (s *Service) Proxima(ctx context.Context, projetoID, pasta string) (process.Escolha, error) {
	p, a, err := s.agent(ctx, projetoID, pasta)
	if err != nil {
		return process.Escolha{}, err
	}
	return p.engine().Proxima(ctx, a.Vaga)
}

// DecisaoInput são os dados de uma operação de decisão.
type DecisaoInput struct {
	Titulo, Corpo, Motivo, Texto string
	Acordo, Aprovada             bool
}

// Decisao aplica uma operação de decisão como a vaga da pasta: abrir, chave,
// revisar ou escalar. id vazio só em abrir.
func (s *Service) Decisao(ctx context.Context, projetoID, pasta, op, id string, in DecisaoInput) (core.Item, error) {
	p, a, err := s.agent(ctx, projetoID, pasta)
	if err != nil {
		return core.Item{}, err
	}
	e := p.engine()
	switch op {
	case "abrir":
		return e.AbrirDecisao(ctx, a.Vaga, in.Titulo, in.Corpo)
	case "chave":
		return e.Chave(ctx, a.Vaga, id, in.Acordo, in.Motivo)
	case "revisar":
		return e.RevisarDecisao(ctx, a.Vaga, id, in.Corpo)
	case "escalar":
		return e.EscalarDecisao(ctx, a.Vaga, id)
	}
	return core.Item{}, fmt.Errorf("%w: operação de decisão desconhecida %q", core.ErrInvalid, op)
}

// DecisaoHumano é o veto ou a resposta do humano (não precisa ser uma vaga).
func (s *Service) DecisaoHumano(ctx context.Context, projetoID, op, id string, in DecisaoInput) (core.Item, error) {
	p, err := s.project(ctx, projetoID)
	if err != nil {
		return core.Item{}, err
	}
	switch op {
	case "veto":
		return p.engine().Veto(ctx, id, in.Motivo)
	case "responder":
		return p.engine().ResponderDecisao(ctx, id, in.Aprovada, in.Texto)
	}
	return core.Item{}, fmt.Errorf("%w: operação do humano desconhecida %q", core.ErrInvalid, op)
}

// Saude devolve o que precisa de atenção no projeto.
func (s *Service) Saude(ctx context.Context, projetoID, pasta string) (process.Saude, error) {
	p, err := s.reader(ctx, projetoID, pasta)
	if err != nil {
		return process.Saude{}, err
	}
	return p.engine().Saude(ctx)
}

// Orfas lista os itens órfãos; com liberar, devolve a pronta os que dá (GERENTE ou INTEGRADOR).
func (s *Service) Orfas(ctx context.Context, projetoID, pasta string, liberar bool) ([]core.Item, error) {
	if !liberar {
		p, err := s.reader(ctx, projetoID, pasta)
		if err != nil {
			return nil, err
		}
		return p.engine().Orfas(ctx)
	}
	p, a, err := s.agent(ctx, projetoID, pasta)
	if err != nil {
		return nil, err
	}
	return p.engine().LiberarOrfas(ctx, a.Vaga)
}

// Metricas calcula os números da sprint (vazio: a aberta, ou as últimas 24 h).
func (s *Service) Metricas(ctx context.Context, projetoID, pasta, sprint string) (process.Metricas, error) {
	p, err := s.reader(ctx, projetoID, pasta)
	if err != nil {
		return process.Metricas{}, err
	}
	return p.engine().Metricas(ctx, sprint)
}

// SalvarRegra grava uma regra (GERENTE ou PLANEJADOR).
func (s *Service) SalvarRegra(ctx context.Context, projetoID, pasta string, r core.Rule) (core.Rule, error) {
	p, a, err := s.agent(ctx, projetoID, pasta)
	if err != nil {
		return core.Rule{}, err
	}
	out, _, err := p.engine().SalvarRegra(ctx, a.Vaga, r)
	return out, err
}

// EstadoRegra muda o estado de uma regra (GERENTE ou PLANEJADOR).
func (s *Service) EstadoRegra(ctx context.Context, projetoID, pasta, id, estado, comando string) (core.Rule, error) {
	p, a, err := s.agent(ctx, projetoID, pasta)
	if err != nil {
		return core.Rule{}, err
	}
	return p.engine().EstadoRegra(ctx, a.Vaga, id, estado, comando)
}

// Regras lista as regras pelo filtro (papel canônico ou do projeto).
func (s *Service) Regras(ctx context.Context, projetoID, pasta string, f core.RuleFilter) ([]core.Rule, error) {
	p, err := s.reader(ctx, projetoID, pasta)
	if err != nil {
		return nil, err
	}
	if f.Papel != "" {
		if c, ok := p.engine().Roles().Canonical(strings.ToUpper(f.Papel)); ok {
			f.Papel = c
		}
	}
	return p.core.Rules(ctx, f)
}

// ImportarLicoes grava as lições de um LICOES.md; reimportar não duplica.
func (s *Service) ImportarLicoes(ctx context.Context, projetoID, pasta, arquivo string) (map[string]any, error) {
	b, err := os.ReadFile(arquivo)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", core.ErrInvalid, err)
	}
	ls, avisos := lessons.Parse(b)
	p, a, err := s.agent(ctx, projetoID, pasta)
	if err != nil {
		return nil, err
	}
	gravadas := 0
	if avisos == nil {
		avisos = []string{}
	}
	for _, l := range ls {
		_, mudou, err := p.engine().SalvarRegra(ctx, a.Vaga, core.Rule{ID: l.ID, Origem: l.Origem, Texto: l.Texto,
			Pratica: l.Pratica, Aplica: l.Aplica, Estado: l.Estado})
		if err != nil {
			avisos = append(avisos, err.Error())
			continue
		}
		if mudou {
			gravadas++
		}
	}
	return map[string]any{"lidas": len(ls), "gravadas": gravadas, "avisos": avisos}, nil
}
