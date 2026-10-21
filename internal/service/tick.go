package service

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// OpenAll abre todos os projetos registrados, para o Tick alcançá-los mesmo
// antes de algum agente chamar. Um projeto que não abre não impede os outros.
func (s *Service) OpenAll(ctx context.Context) error {
	rows, err := s.registry.QueryContext(ctx, `SELECT id FROM projetos ORDER BY id`)
	if err != nil {
		return err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	var errs []error
	for _, id := range ids {
		if _, err := s.project(ctx, id); err != nil {
			errs = append(errs, fmt.Errorf("abrir projeto %q: %w", id, err))
		}
	}
	return errors.Join(errs...)
}

// Tick roda o trabalho periódico de cada projeto aberto.
func (s *Service) Tick(ctx context.Context) error {
	s.mu.Lock()
	ps := make(map[string]*project, len(s.projects))
	for id, p := range s.projects {
		ps[id] = p
	}
	s.mu.Unlock()
	var errs []error
	for id, p := range ps {
		if err := p.core.Tick(ctx); err != nil {
			errs = append(errs, fmt.Errorf("tick do projeto %q: %w", id, err))
		}
	}
	return errors.Join(errs...)
}

// SetLogf define quem recebe os erros do RunTicker.
func (s *Service) SetLogf(logf func(format string, args ...any)) { s.logf = logf }

// RunTicker chama Tick a cada every até ctx terminar. Um erro igual ao
// anterior não é registrado de novo, para não encher o log a cada volta.
func (s *Service) RunTicker(ctx context.Context, every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	ultimo := ""
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		err := s.Tick(ctx)
		if err == nil || ctx.Err() != nil {
			ultimo = ""
			continue
		}
		if msg := err.Error(); msg != ultimo {
			ultimo = msg
			if s.logf != nil {
				s.logf("tick: %v", err)
			}
		}
	}
}
