package service

import (
	"context"
	"fmt"

	"github.com/JoaoVitorSantDrade/central/internal/core"
)

// StartExecution grava o início de uma execução longa da vaga ligada à pasta.
// concessao > 0 é a concessão de recurso que ela usa.
func (s *Service) StartExecution(ctx context.Context, projetoID, pasta, rotulo, comando string, concessao int64) (core.Execution, error) {
	p, a, err := s.agent(ctx, projetoID, pasta)
	if err != nil {
		return core.Execution{}, err
	}
	return p.core.StartExecution(ctx, a.Vaga, rotulo, comando, concessao)
}

// ownExecution confere que a execução id é da vaga ligada à pasta.
func (s *Service) ownExecution(ctx context.Context, projetoID, pasta string, id int64) (*project, error) {
	p, a, err := s.agent(ctx, projetoID, pasta)
	if err != nil {
		return nil, err
	}
	e, err := p.core.ExecutionByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if e.Vaga != a.Vaga {
		return nil, fmt.Errorf("execução %d de %s: %w", id, a.Vaga, core.ErrNotFound)
	}
	return p, nil
}

// BeatExecution renova o batimento da execução id da vaga ligada à pasta.
func (s *Service) BeatExecution(ctx context.Context, projetoID, pasta string, id int64) error {
	p, err := s.ownExecution(ctx, projetoID, pasta, id)
	if err != nil {
		return err
	}
	return p.core.BeatExecution(ctx, id)
}

// FinishExecution encerra a execução id da vaga ligada à pasta.
func (s *Service) FinishExecution(ctx context.Context, projetoID, pasta string, id int64, codigo int, finalLog string) (core.Execution, error) {
	p, err := s.ownExecution(ctx, projetoID, pasta, id)
	if err != nil {
		return core.Execution{}, err
	}
	return p.core.FinishExecution(ctx, id, codigo, finalLog)
}
