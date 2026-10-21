package service

import (
	"context"
	"time"

	"github.com/JoaoVitorSantDrade/central/internal/core"
)

// ProjetoResumo é o cartão de um projeto na visão da máquina do painel.
type ProjetoResumo struct {
	ID           string         `json:"id"`
	Nome         string         `json:"nome"`
	Raiz         string         `json:"raiz"`
	UltimoAcesso time.Time      `json:"ultimo_acesso"`
	Sprint       *core.Sprint   `json:"sprint"`
	Vagas        map[string]int `json:"vagas"`
	FilaQA       int            `json:"fila_qa"`
	Alertas      int            `json:"alertas"`
	// Erro diz por que o resumo não saiu (o projeto não abriu); os outros seguem.
	Erro string `json:"erro,omitempty"`
}

// Projetos lista os projetos da máquina com um resumo de cada um, por id.
func (s *Service) Projetos(ctx context.Context) ([]ProjetoResumo, error) {
	rows, err := s.registry.QueryContext(ctx, `SELECT id, nome, raiz, ultimo_acesso FROM projetos ORDER BY id`)
	if err != nil {
		return nil, err
	}
	var out []ProjetoResumo
	for rows.Next() {
		var r ProjetoResumo
		var acesso string
		if err := rows.Scan(&r.ID, &r.Nome, &r.Raiz, &acesso); err != nil {
			rows.Close()
			return nil, err
		}
		r.UltimoAcesso, _ = time.Parse(core.TimeLayout, acesso)
		out = append(out, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range out {
		if err := s.resumir(ctx, &out[i]); err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			out[i].Erro = err.Error()
		}
	}
	if out == nil {
		out = []ProjetoResumo{}
	}
	return out, nil
}

func (s *Service) resumir(ctx context.Context, r *ProjetoResumo) error {
	p, err := s.project(ctx, r.ID)
	if err != nil {
		return err
	}
	as, err := p.core.Agents(ctx)
	if err != nil {
		return err
	}
	r.Vagas = map[string]int{}
	for _, a := range as {
		r.Vagas[a.Estado]++
	}
	if r.Sprint, err = p.core.CurrentSprint(ctx); err != nil {
		return err
	}
	sd, err := p.engine().Saude(ctx)
	if err != nil {
		return err
	}
	r.FilaQA, r.Alertas = sd.FilaQA, len(sd.Alertas)
	return nil
}

// HumanoEnviar manda uma mensagem do humano (remetente HUMANO) e acorda o destino.
func (s *Service) HumanoEnviar(ctx context.Context, projetoID string, in core.SendInput) (core.Message, error) {
	p, err := s.project(ctx, projetoID)
	if err != nil {
		return core.Message{}, err
	}
	in.De, in.RespondeA = core.Humano, 0
	return p.core.Send(ctx, in)
}

// Historico é a conversa do projeto: as mensagens e as pendências em aberto.
type Historico struct {
	Mensagens  []core.Message `json:"mensagens"`
	Pendencias []core.Message `json:"pendencias"`
}

// Mensagens devolve o histórico do projeto (sem mexer em cursor nem batimento).
func (s *Service) Mensagens(ctx context.Context, projetoID string, depois int64, limite int) (Historico, error) {
	p, err := s.project(ctx, projetoID)
	if err != nil {
		return Historico{}, err
	}
	var h Historico
	if h.Mensagens, err = p.core.Messages(ctx, depois, limite); err != nil {
		return Historico{}, err
	}
	if h.Pendencias, err = p.core.OpenReplies(ctx); err != nil {
		return Historico{}, err
	}
	return h, nil
}

// Eventos lê os eventos do projeto pelo filtro e o seq do último evento do
// projeto (para quem vai acompanhar ao vivo a partir dali).
func (s *Service) Eventos(ctx context.Context, projetoID string, f core.EventFilter) ([]core.Event, int64, error) {
	p, err := s.project(ctx, projetoID)
	if err != nil {
		return nil, 0, err
	}
	ultimo, err := p.core.LastSeq(ctx)
	if err != nil {
		return nil, 0, err
	}
	evs, err := p.core.Events(ctx, f)
	return evs, ultimo, err
}

// Mudancas devolve um canal que fecha na próxima mudança notificada do projeto.
// Nem toda gravação notifica: quem acompanha também relê de tempos em tempos.
func (s *Service) Mudancas(ctx context.Context, projetoID string) (<-chan struct{}, error) {
	p, err := s.project(ctx, projetoID)
	if err != nil {
		return nil, err
	}
	return p.core.Changed(), nil
}

// Recursos devolve o estado de cada recurso do projeto, por nome.
func (s *Service) Recursos(ctx context.Context, projetoID string) (map[string]core.ResourceView, error) {
	p, err := s.project(ctx, projetoID)
	if err != nil {
		return nil, err
	}
	out := map[string]core.ResourceView{}
	for _, nome := range p.core.ResourceNames() {
		v, err := p.core.ResourceStatus(ctx, nome)
		if err != nil {
			return nil, err
		}
		out[nome] = v
	}
	return out, nil
}

// Serie é o histórico de um recurso num período: as leituras do medidor e as
// concessões que estiveram ativas nele (o declarado).
type Serie struct {
	Amostras   []core.Sample `json:"amostras"`
	Concessoes []core.Grant  `json:"concessoes"`
}

// Amostras devolve a série do recurso entre de e ate; recurso fora do central.toml, ErrNotFound.
func (s *Service) Amostras(ctx context.Context, projetoID, nome string, de, ate time.Time) (Serie, error) {
	p, err := s.project(ctx, projetoID)
	if err != nil {
		return Serie{}, err
	}
	if _, err := p.core.ResourceStatus(ctx, nome); err != nil {
		return Serie{}, err
	}
	var sr Serie
	if sr.Amostras, err = p.core.Samples(ctx, nome, de, ate); err != nil {
		return Serie{}, err
	}
	if sr.Concessoes, err = p.core.GrantsBetween(ctx, nome, de, ate); err != nil {
		return Serie{}, err
	}
	return sr, nil
}
