package service

import (
	"context"
	"path/filepath"
	"sync"
	"time"

	"github.com/JoaoVitorSantDrade/central/internal/adapters/files"
	"github.com/JoaoVitorSantDrade/central/internal/process"
)

// adapterHeads guarda, por projeto, o último head sincronizado e a hora da última leitura.
type adapterHeads struct {
	mu    sync.Mutex
	heads map[string]string
	lidos map[string]time.Time
}

// devido diz se o projeto já pode ser lido de novo (e marca a leitura).
func (h *adapterHeads) devido(id string, agora time.Time, intervalo time.Duration, forcar bool) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.lidos == nil {
		h.lidos = map[string]time.Time{}
	}
	if !forcar && agora.Sub(h.lidos[id]) < intervalo {
		return false
	}
	h.lidos[id] = agora
	return true
}

// SyncAdapters faz uma passada do adaptador de arquivos em cada projeto
// aberto que o configura: lê a fonte e, se o head mudou, sincroniza.
// Devolve o resumo por projeto que sincronizou.
func (s *Service) SyncAdapters(ctx context.Context) (map[string]process.Resumo, error) {
	return s.syncAdapters(ctx, true)
}

func (s *Service) syncAdapters(ctx context.Context, forcar bool) (map[string]process.Resumo, error) {
	s.mu.Lock()
	type alvo struct {
		id string
		p  *project
	}
	var alvos []alvo
	for id, p := range s.projects {
		alvos = append(alvos, alvo{id, p})
	}
	s.mu.Unlock()
	out := map[string]process.Resumo{}
	for _, a := range alvos {
		a.p.cfgMu.Lock()
		cfg := a.p.cfg.Itens.Arquivos
		a.p.cfgMu.Unlock()
		if cfg == nil || !s.adapters.devido(a.id, time.Now(), cfg.Intervalo.Duration, forcar) {
			continue
		}
		dir := cfg.Repo
		if dir == "" {
			dir = a.p.raiz
		} else if !filepath.IsAbs(dir) {
			dir = filepath.Join(a.p.raiz, dir)
		}
		src := files.Source{Dir: dir, Ref: cfg.Ref, Padrao: cfg.Padrao, Pastas: cfg.Pastas, Mapa: cfg.Mapa, RecursoSe: cfg.RecursoSe}
		head, achados, invalidos, err := files.Read(ctx, src)
		if err != nil {
			s.logOnce("adaptador:"+a.id, "adaptador de %s: %v", a.id, err)
			continue
		}
		s.adapters.mu.Lock()
		if s.adapters.heads == nil {
			s.adapters.heads = map[string]string{}
		}
		igual := s.adapters.heads[a.id] == head
		s.adapters.mu.Unlock()
		if igual {
			continue
		}
		r, err := a.p.engine().Sincronizar(ctx, achados, invalidos)
		if err != nil {
			s.logOnce("adaptador:"+a.id, "sincronizar %s: %v", a.id, err)
			continue
		}
		s.adapters.mu.Lock()
		s.adapters.heads[a.id] = head
		s.adapters.mu.Unlock()
		out[a.id] = r
	}
	return out, nil
}

// RunAdapters confere a cada passo quais projetos já podem ser lidos de novo
// (pelo intervalo de cada um) e sincroniza, até ctx terminar.
func (s *Service) RunAdapters(ctx context.Context, passo time.Duration) {
	t := time.NewTicker(passo)
	defer t.Stop()
	for {
		s.syncAdapters(ctx, false)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}
