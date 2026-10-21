package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/JoaoVitorSantDrade/central/internal/adapters/lockdir"
	"github.com/JoaoVitorSantDrade/central/internal/core"
	"github.com/JoaoVitorSantDrade/central/internal/process"
)

// ImportResumo conta o que a importação do $LOCKS trouxe.
type ImportResumo struct {
	Mensagens  int      `json:"mensagens"`
	Agentes    int      `json:"agentes"`
	Itens      int      `json:"itens"`
	Fatos      int      `json:"fatos"`
	Contadores int      `json:"contadores"`
	Sprint     string   `json:"sprint,omitempty"`
	Erros      []string `json:"erros"`
}

const chaveImportada = "importado:canal:"

var reContador = regexp.MustCompile(`^([A-Z]{1,5})-(\d+)$`)

// estadoReserva traduz o estado de uma reserva do manual para o ciclo da Central.
func estadoReserva(r lockdir.Reserva) string {
	switch r.Estado {
	case "em_andamento":
		return process.EmAndamento
	case "bloqueada":
		return process.Pronta
	case "rejeitada":
		return process.Rejeitada
	case "concluida":
		if r.Revisao == nil {
			return process.Concluida
		}
		switch r.Revisao.Veredito {
		case "sim", "parcial":
			if r.Revisao.Ponta == r.CommitFinal {
				return process.Aprovada
			}
		case "nao", "não":
			return process.Rejeitada
		}
		return process.Concluida
	}
	return ""
}

// ImportLocks lê o diretório de estado do LyricCut e grava na Central o que
// ainda não existe: vagas, mensagens (como lidas), reservas, registros de e2e
// (fatos), contadores de id e a sprint aberta. Reimportar não duplica nada.
func (s *Service) ImportLocks(ctx context.Context, projetoID, pasta, dir string) (ImportResumo, error) {
	p, a, err := s.agent(ctx, projetoID, pasta)
	if err != nil {
		return ImportResumo{}, err
	}
	snap, err := lockdir.Load(dir)
	if err != nil {
		return ImportResumo{}, fmt.Errorf("%w: %v", core.ErrInvalid, err)
	}
	r := ImportResumo{Erros: snap.Erros}
	if r.Erros == nil {
		r.Erros = []string{}
	}
	quem := a.Vaga
	agora := s.now()

	// vagas: id do worktree → vaga, e a pasta do worktree se ela existir
	main := filepath.Dir(filepath.Dir(dir))
	vagaDe := map[string]string{}
	if snap.Sprint != nil {
		for _, v := range snap.Sprint.Equipe {
			if v.ID != "" {
				vagaDe[v.ID] = v.Vaga
			}
		}
	}
	ultimo := map[string]lockdir.Ponto{}
	for _, pt := range snap.Pontos {
		vagaDe[pt.ID] = pt.Vaga
		if pt.Vaga == "" {
			continue
		}
		if o, ok := ultimo[pt.Vaga]; !ok || pt.Atualizado > o.Atualizado {
			ultimo[pt.Vaga] = pt
		}
	}
	for vaga, pt := range ultimo {
		wt := filepath.Join(main, ".claude", "worktrees", pt.ID)
		if st, err := os.Stat(wt); err != nil || !st.IsDir() {
			wt = ""
		}
		bat, ok := lockdir.ParseLocal(pt.Atualizado)
		if !ok {
			bat = agora
		}
		papel := pt.Papel
		if papel == "" {
			papel = "DESCONHECIDO"
		}
		criou, err := p.core.ImportAgent(ctx, quem, vaga, papel, wt, pt.Estado, bat)
		if err != nil {
			r.Erros = append(r.Erros, fmt.Sprintf("vaga %s: %v", vaga, err))
			continue
		}
		if criou {
			r.Agentes++
		}
	}

	// mensagens: o histórico entra como lido
	ms := make([]core.ImportedMessage, 0, len(snap.Mensagens))
	for _, m := range snap.Mensagens {
		quando, ok := lockdir.HoraDoID(m.ID)
		if !ok {
			quando = agora
		}
		tipo := m.Assunto
		if tipo == "" {
			tipo = "nota"
		}
		if m.De == "" || m.Para == "" {
			r.Erros = append(r.Erros, fmt.Sprintf("mensagem %s sem de/para", m.ID))
			continue
		}
		im := core.ImportedMessage{De: m.De, Para: m.Para, Tipo: tipo, Ref: m.Ref, Corpo: m.Corpo, CriadaEm: quando, Chave: chaveImportada + m.ID}
		if m.Responde != "" {
			im.RespondeChave = chaveImportada + m.Responde
		}
		ms = append(ms, im)
	}
	if r.Mensagens, err = p.core.ImportMessages(ctx, quem, ms); err != nil {
		return r, err
	}
	if err := p.core.MarkAllRead(ctx); err != nil {
		return r, err
	}

	// reservas: só preenchem itens que não existem ou que ainda não andaram
	err = p.core.WithItems(ctx, quem, func(tx core.ItemTx) error {
		for _, res := range snap.Reservas {
			estado := estadoReserva(res)
			if estado == "" {
				r.Erros = append(r.Erros, fmt.Sprintf("reserva %s: estado %q desconhecido", res.Tarefa, res.Estado))
				continue
			}
			it, err := tx.Get(res.Tarefa)
			novo := errors.Is(err, core.ErrNotFound)
			if err != nil && !novo {
				return err
			}
			if !novo && (it.Estado == estado || (it.Estado != process.Proposta && it.Estado != process.Pronta)) {
				continue // já está assim, ou já anda na Central: a reserva antiga não manda
			}
			if novo {
				tipo := process.TipoTarefa
				if strings.HasPrefix(res.Tarefa, "M-") {
					tipo = process.TipoMissao
				}
				it = core.Item{ID: res.Tarefa, Tipo: tipo, Titulo: res.Tarefa, Fonte: "importado:" + dir}
			}
			dono := vagaDe[res.Agente]
			if dono == "" {
				dono = res.Agente
			}
			it.Estado, it.Ponta, it.Marco = estado, res.CommitFinal, res.Marco.Int()
			it.Dono = dono
			if estado == process.Pronta {
				it.Dono = ""
				it.Bloqueios++
			}
			if estado == process.Aprovada {
				it.PontaRevisada = res.CommitFinal
			}
			if err := tx.Save(it, "importado", map[string]any{"origem": res.Estado}); err != nil {
				return err
			}
			if res.Estado == "bloqueada" && res.Nota != "" {
				if err := tx.Note(it.ID, "bloqueio", res.Nota); err != nil {
					return err
				}
			}
			r.Itens++
		}
		return nil
	})
	if err != nil {
		return r, err
	}

	// e2e → fatos e2e.<commit>-<nivel>
	for nome, raw := range snap.E2E {
		chave := "e2e." + strings.ToLower(nome)
		if f, err := p.core.GetFact(ctx, chave); err == nil && jsonIgual(f.Valor, raw) {
			continue
		}
		if _, err := p.core.SetFact(ctx, quem, chave, raw, "importado do $LOCKS"); err != nil {
			r.Erros = append(r.Erros, fmt.Sprintf("e2e %s: %v", nome, err))
			continue
		}
		r.Fatos++
	}

	// contadores de id: o próximo é maior que o maior reservado
	maior := map[string]int{}
	for _, id := range append(slicesIDs(snap), itemIDs(snap)...) {
		if m := reContador.FindStringSubmatch(id); m != nil {
			n, _ := strconv.Atoi(m[2])
			if n > maior[m[1]] {
				maior[m[1]] = n
			}
		}
	}
	for prefixo, n := range maior {
		if err := p.core.EnsureCounter(ctx, prefixo, n); err != nil {
			return r, err
		}
		r.Contadores++
	}

	// sprint: só a que ainda não terminou e se não há outra aberta
	if sp := snap.Sprint; sp != nil {
		if id, err := s.importarSprint(ctx, p, quem, *sp, agora); err != nil {
			r.Erros = append(r.Erros, fmt.Sprintf("sprint %s: %v", sp.Sprint, err))
		} else {
			r.Sprint = id
		}
	}
	return r, nil
}

func slicesIDs(s lockdir.Snapshot) []string { return s.IDs }

func itemIDs(s lockdir.Snapshot) []string {
	ids := make([]string, 0, len(s.Reservas))
	for _, r := range s.Reservas {
		ids = append(ids, r.Tarefa)
	}
	return ids
}

func jsonIgual(a, b json.RawMessage) bool {
	var x, y any
	if json.Unmarshal(a, &x) != nil || json.Unmarshal(b, &y) != nil {
		return false
	}
	ja, _ := json.Marshal(x)
	jb, _ := json.Marshal(y)
	return string(ja) == string(jb)
}

func (s *Service) importarSprint(ctx context.Context, p *project, quem string, sp lockdir.Sprint, agora time.Time) (string, error) {
	fim, ok := lockdir.ParseLocal(sp.Fim)
	if !ok || !fim.After(agora) {
		return "", nil
	}
	if cur, err := p.core.CurrentSprint(ctx); err != nil || cur != nil {
		return "", err
	}
	if _, err := p.core.SprintByID(ctx, sp.Sprint); err == nil {
		return "", nil
	}
	hora := func(v string, padrao time.Time) time.Time {
		if t, ok := lockdir.ParseLocal(v); ok {
			return t
		}
		return padrao
	}
	ab := hora(sp.Inicio, agora)
	cs := core.Sprint{ID: sp.Sprint, Abertura: ab, Checkin: hora(sp.Checkin, ab), Escopo: hora(sp.EscopoVersao, fim.Add(-75*time.Minute)),
		Congelamento: hora(sp.Congelamento, fim.Add(-time.Hour)), Corte: hora(sp.Corte, fim.Add(-30*time.Minute)),
		Retro: hora(sp.Retro, fim.Add(-20*time.Minute)), Fim: fim}
	if cs.Escopo.After(cs.Congelamento) {
		cs.Escopo = cs.Congelamento
	}
	if err := p.core.CreateSprint(ctx, quem, cs); err != nil {
		return "", err
	}
	return sp.Sprint, nil
}

// ExportLocks escreve o estado da Central em dir, no formato do $LOCKS do
// LyricCut, para voltar aos arquivos se for preciso (spec §13).
func (s *Service) ExportLocks(ctx context.Context, projetoID, pasta, dir string) (map[string]int, error) {
	p, err := s.reader(ctx, projetoID, pasta)
	if err != nil {
		return nil, err
	}
	var snap lockdir.Snapshot
	ms, err := p.core.MessagesAll(ctx)
	if err != nil {
		return nil, err
	}
	idDe := map[int64]string{}
	for _, m := range ms {
		id := strings.TrimPrefix(m.Chave, chaveImportada)
		if !strings.HasPrefix(m.Chave, chaveImportada) {
			id = fmt.Sprintf("%s-%s-%s-%d", m.CriadaEm.Local().Format("20060102T150405"), m.De, m.Para, m.Seq)
		}
		idDe[m.Seq] = id
		lm := lockdir.Mensagem{ID: id, De: m.De, Para: m.Para, Assunto: m.Tipo, Ref: m.Ref, Corpo: m.Corpo,
			PrecisaResposta: m.ResponderAte != nil}
		if m.RespondeA != 0 {
			lm.Responde = idDe[m.RespondeA]
		}
		snap.Mensagens = append(snap.Mensagens, lm)
	}
	as, err := p.core.Agents(ctx)
	if err != nil {
		return nil, err
	}
	idAgente := map[string]string{}
	for _, a := range as {
		id := filepath.Base(a.Pasta)
		if strings.HasPrefix(a.Pasta, "(") || id == "." || id == string(filepath.Separator) {
			id = a.Vaga
		}
		idAgente[a.Vaga] = id
		snap.Pontos = append(snap.Pontos, lockdir.Ponto{ID: id, Vaga: a.Vaga, Papel: a.Papel, Estado: a.Estado,
			Atualizado: a.Batimento.Local().Format(lockdir.LayoutLocal), Nota: a.Status})
	}
	its, err := p.core.Items(ctx, core.ItemFilter{Estados: []string{process.EmAndamento, process.Concluida, process.EmRevisao,
		process.Aprovada, process.Rejeitada}})
	if err != nil {
		return nil, err
	}
	for _, it := range its {
		res := lockdir.Reserva{Tarefa: it.ID, Agente: idAgente[it.Dono], Estado: it.Estado, CommitFinal: it.Ponta,
			Marco: lockdir.Flexivel(strconv.Itoa(it.Marco)), Atualizado: it.AtualizadoEm.Local().Format(lockdir.LayoutLocal)}
		switch it.Estado {
		case process.EmRevisao:
			res.Estado = "concluida"
		case process.Aprovada:
			res.Estado = "concluida"
			res.Revisao = &lockdir.Revisao{Veredito: "sim", Ponta: it.PontaRevisada}
		case process.Rejeitada:
			res.Estado = "rejeitada"
			res.Revisao = &lockdir.Revisao{Veredito: "nao", Ponta: it.Ponta}
		}
		snap.Reservas = append(snap.Reservas, res)
	}
	fs, err := p.core.Facts(ctx)
	if err != nil {
		return nil, err
	}
	snap.E2E = map[string]json.RawMessage{}
	for _, f := range fs {
		if strings.HasPrefix(f.Chave, "e2e.") {
			snap.E2E[strings.TrimPrefix(f.Chave, "e2e.")] = f.Valor
		}
	}
	if sp, err := p.core.CurrentSprint(ctx); err != nil {
		return nil, err
	} else if sp != nil {
		l := func(t time.Time) string { return t.Local().Format(lockdir.LayoutLocal) }
		ls := &lockdir.Sprint{Sprint: sp.ID, Inicio: l(sp.Abertura), Checkin: l(sp.Checkin), EscopoVersao: l(sp.Escopo),
			Congelamento: l(sp.Congelamento), Corte: l(sp.Corte), Retro: l(sp.Retro), Fim: l(sp.Fim)}
		for _, a := range as {
			ls.Equipe = append(ls.Equipe, lockdir.Vaga{Vaga: a.Vaga, Papel: a.Papel, ID: idAgente[a.Vaga]})
		}
		snap.Sprint = ls
	}
	if err := lockdir.Export(dir, snap); err != nil {
		return nil, err
	}
	return map[string]int{"mensagens": len(snap.Mensagens), "agentes": len(snap.Pontos), "reservas": len(snap.Reservas),
		"e2e": len(snap.E2E)}, nil
}
