package service

import (
	"context"
	"slices"

	"github.com/JoaoVitorSantDrade/central/internal/contextpack"
	"github.com/JoaoVitorSantDrade/central/internal/core"
	"github.com/JoaoVitorSantDrade/central/internal/process"
)

// estadosComDono são os estados em que o item é reserva da vaga.
var estadosComDono = []string{process.EmAndamento, process.Concluida, process.EmRevisao, process.Aprovada, process.Rejeitada}

// Brief monta o contexto da vaga (spec §7) e registra o tamanho (brief.gerado)
// e o que foi cortado (brief.truncado). Não avança o cursor da caixa.
func (s *Service) Brief(ctx context.Context, projetoID, pasta string, curto bool, maxTokens int) (contextpack.Brief, error) {
	p, a, err := s.agent(ctx, projetoID, pasta)
	if err != nil {
		return contextpack.Brief{}, err
	}
	p.cfgMu.Lock()
	cfg := p.cfg
	p.cfgMu.Unlock()
	e := p.engine()
	canon, _ := e.Roles().Canonical(a.Papel)
	in := contextpack.Input{Agente: a, Canonico: canon, Projeto: projetoID, RaizProjeto: p.raiz, DataDir: s.dataDir, Agora: p.core.Now()}
	if in.Sprint, err = p.core.CurrentSprint(ctx); err != nil {
		return contextpack.Brief{}, err
	}
	its, err := p.core.Items(ctx, core.ItemFilter{Dono: a.Vaga, Estados: estadosComDono})
	if err != nil {
		return contextpack.Brief{}, err
	}
	if len(its) > 0 {
		in.Reserva = &its[0]
	}
	if in.Caixa, in.NaoLidas, err = p.core.UnreadMessages(ctx, a.Vaga, 10); err != nil {
		return contextpack.Brief{}, err
	}
	if in.Pendencias, err = p.core.Pending(ctx, a.Vaga); err != nil {
		return contextpack.Brief{}, err
	}
	if !curto {
		in.Cartao, in.FonteCartao = contextpack.Card(a.Pasta, a.Papel, canon)
		in.Nucleo, _ = contextpack.Nucleo(a.Pasta)
		if in.Regras, err = e.RegrasDoPapel(ctx, canon); err != nil {
			return contextpack.Brief{}, err
		}
		fatos, err := p.core.Facts(ctx)
		if err != nil {
			return contextpack.Brief{}, err
		}
		padroes := append(slices.Clone(cfg.Brief.Fatos[canon]), cfg.Brief.Fatos[core.Todos]...)
		if len(padroes) > 0 {
			in.Fatos = contextpack.FiltrarFatos(fatos, padroes)
		} else {
			in.Fatos, in.FatosSoChaves = fatos, true
		}
		for _, l := range cfg.Leitura {
			if len(l.Papeis) == 0 || slices.Contains(l.Papeis, canon) || slices.Contains(l.Papeis, core.Todos) {
				in.Leitura = append(in.Leitura, contextpack.Leitura{Caminho: l.Caminho, Resumo: l.Resumo, Quando: l.Quando})
			}
		}
	}
	if maxTokens <= 0 {
		maxTokens = cfg.Brief.MaxTokens
	}
	b := contextpack.Montar(in, curto, maxTokens)
	dados := map[string]any{"papel": a.Papel, "canonico": canon, "bytes": len(b.Texto), "tokens": b.Tokens, "curto": curto,
		"cartao": in.FonteCartao}
	if err := p.core.RecordEvent(ctx, a.Vaga, "brief.gerado", a.Vaga, dados); err != nil {
		return contextpack.Brief{}, err
	}
	if len(b.Cortados) > 0 {
		if err := p.core.RecordEvent(ctx, a.Vaga, "brief.truncado", a.Vaga, map[string]any{"cortados": b.Cortados, "teto": maxTokens}); err != nil {
			return contextpack.Brief{}, err
		}
	}
	return b, nil
}
