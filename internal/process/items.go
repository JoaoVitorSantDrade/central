package process

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/JoaoVitorSantDrade/central/internal/core"
)

// Ações sobre tarefas e missões (spec §6.2 e Decisão 3 do plano).
const (
	AcaoPronto   = "pronto"
	AcaoReservar = "reservar"
	AcaoConcluir = "concluir"
	AcaoBloquear = "bloquear"
	AcaoRevisar  = "revisar"
	AcaoAprovar  = "aprovar"
	AcaoRejeitar = "rejeitar"
	AcaoIntegrar = "integrar"
	AcaoValidar  = "validar"
	AcaoReabrir  = "reabrir"
)

// Acoes lista as ações aceitas por Acao.
var Acoes = []string{AcaoPronto, AcaoReservar, AcaoConcluir, AcaoBloquear, AcaoRevisar, AcaoAprovar, AcaoRejeitar,
	AcaoIntegrar, AcaoValidar, AcaoReabrir}

// ResultadosValidacao são os resultados do VALIDADOR (spec §6.2).
var ResultadosValidacao = []string{"ligada", "sem_caminho", "sem_efeito", "quebrada"}

// ItemInput é um item novo.
type ItemInput struct {
	ID, Tipo, Titulo, Area, Prioridade, Categoria, Sprint, Recurso string
	Arquivos                                                       []string
	Folga, Bloqueante, Visivel                                     bool
	Campos                                                         map[string]any
}

// Args são os dados de uma ação (cada ação usa os seus).
type Args struct {
	Ponta, Nota, Motivo, Commit, Resultado string
	// Final marca o último marco de uma missão no integrar.
	Final bool
}

// Criar grava um item novo em proposta (qualquer papel pode propor).
// Sem ID, gera T-XXX (ou M-XXX para missão).
func (e *Engine) Criar(ctx context.Context, vaga string, in ItemInput) (core.Item, error) {
	if in.Tipo == "" {
		in.Tipo = TipoTarefa
	}
	if in.Tipo != TipoTarefa && in.Tipo != TipoMissao {
		return core.Item{}, fmt.Errorf("%w: tipo de item %q (use %s ou %s; decisões têm comando próprio)", core.ErrInvalid, in.Tipo, TipoTarefa, TipoMissao)
	}
	if strings.TrimSpace(in.Titulo) == "" {
		return core.Item{}, fmt.Errorf("%w: o item precisa de título", core.ErrInvalid)
	}
	if in.ID == "" {
		prefixo := "T"
		if in.Tipo == TipoMissao {
			prefixo = "M"
		}
		id, err := e.c.NextID(ctx, vaga, prefixo, 1)
		if err != nil {
			return core.Item{}, err
		}
		in.ID = id
	}
	var out core.Item
	err := e.c.WithItems(ctx, vaga, func(tx core.ItemTx) error {
		if _, _, err := e.papelDe(tx, vaga); err != nil {
			return err
		}
		if _, err := tx.Get(in.ID); err == nil {
			return fmt.Errorf("%w: o item %s já existe", core.ErrConflict, in.ID)
		} else if !errors.Is(err, core.ErrNotFound) {
			return err
		}
		out = core.Item{ID: in.ID, Tipo: in.Tipo, Titulo: in.Titulo, Estado: Proposta, Prioridade: in.Prioridade,
			Area: in.Area, Categoria: in.Categoria, Sprint: in.Sprint, Recurso: in.Recurso, Arquivos: in.Arquivos,
			Folga: in.Folga, Bloqueante: in.Bloqueante, Visivel: in.Visivel, Campos: in.Campos}
		return tx.Save(out, "criado", nil)
	})
	if err != nil {
		return core.Item{}, err
	}
	it, _, err := e.c.Item(ctx, out.ID)
	return it, err
}

// Acao aplica uma ação do ciclo de vida ao item, verificando estado, papel e
// condições, e acorda quem deve, tudo numa transação.
func (e *Engine) Acao(ctx context.Context, vaga, acao, id string, a Args) (core.Item, error) {
	if !slices.Contains(Acoes, acao) {
		return core.Item{}, fmt.Errorf("%w: ação desconhecida %q (use %v)", core.ErrInvalid, acao, Acoes)
	}
	var out core.Item
	err := e.c.WithItems(ctx, vaga, func(tx core.ItemTx) error {
		it, err := tx.Get(id)
		if err != nil {
			return err
		}
		if it.Tipo != TipoTarefa && it.Tipo != TipoMissao {
			return fmt.Errorf("%w: %s é um item do tipo %s; use os comandos de decisão", core.ErrInvalid, id, it.Tipo)
		}
		agente, papel, err := e.papelDe(tx, vaga)
		if err != nil {
			return err
		}
		t := transicao{e: e, tx: tx, it: &it, vaga: vaga, papel: papel, agente: agente, a: a}
		if err := t.aplicar(ctx, acao); err != nil {
			return err
		}
		out = it
		return nil
	})
	if err != nil {
		return core.Item{}, err
	}
	it, _, err := e.c.Item(ctx, out.ID)
	return it, err
}

// transicao é uma ação em curso dentro da transação.
type transicao struct {
	e      *Engine
	tx     core.ItemTx
	it     *core.Item
	vaga   string
	papel  string
	agente core.Agent
	a      Args
}

// estado recusa com ErrConflict quando o item não está num dos estados.
func (t *transicao) estado(acao string, estados ...string) error {
	if slices.Contains(estados, t.it.Estado) {
		return nil
	}
	return fmt.Errorf("%w: %s está em %s; %s exige %s", core.ErrConflict, t.it.ID, t.it.Estado, acao, strings.Join(estados, " ou "))
}

// papelOK recusa com ErrInvalid quando a vaga não tem o papel canônico.
func (t *transicao) papelOK(acao string, papeis ...string) error {
	if slices.Contains(papeis, t.papel) {
		return nil
	}
	nomes := make([]string, len(papeis))
	for i, p := range papeis {
		nomes[i] = t.e.roles.Name(p)
	}
	return fmt.Errorf("%w: o papel %s não pode %s %s (só %s)", core.ErrInvalid, t.agente.Papel, acao, t.it.ID, strings.Join(nomes, " ou "))
}

func (t *transicao) dono(acao string) error {
	if t.it.Dono == t.vaga {
		return nil
	}
	return fmt.Errorf("%w: só o dono (%s) pode %s %s", core.ErrInvalid, t.it.Dono, acao, t.it.ID)
}

func obrigatorio(campo, valor, acao string) error {
	if strings.TrimSpace(valor) == "" {
		return fmt.Errorf("%w: %s precisa de %s", core.ErrInvalid, acao, campo)
	}
	return nil
}

// acordar avisa o papel canônico (se ligado) com item.<estado>.
func (t *transicao) acordar(papel, corpo string) error {
	if !t.e.roles.Enabled(papel) {
		return nil
	}
	return t.tx.Notify(t.e.roles.Name(papel), "item."+t.it.Estado, t.it.ID, corpo)
}

func (t *transicao) acordarVaga(vaga, corpo string) error {
	if vaga == "" {
		return nil
	}
	return t.tx.Notify(vaga, "item."+t.it.Estado, t.it.ID, corpo)
}

func (t *transicao) salvar(evento string, dados map[string]any) error {
	return t.tx.Save(*t.it, evento, dados)
}

func (t *transicao) aplicar(ctx context.Context, acao string) error {
	it := t.it
	switch acao {
	case AcaoPronto:
		if err := t.estado(acao, Proposta); err != nil {
			return err
		}
		if err := t.papelOK("deixar pronto", Planejador); err != nil {
			return err
		}
		if falta := t.e.faltando(*it); len(falta) > 0 {
			return fmt.Errorf("%w: %s não está pronto: faltam %s", core.ErrInvalid, it.ID, strings.Join(falta, ", "))
		}
		it.Estado = Pronta
		return t.salvar("pronta", nil)

	case AcaoReservar:
		if it.Estado == Rejeitada {
			if err := t.dono("retomar"); err != nil {
				return err
			}
			it.Estado = EmAndamento
			return t.salvar("retomada", nil)
		}
		if err := t.estado(acao, Pronta); err != nil {
			return err
		}
		if err := t.papelOK("reservar", Executor); err != nil {
			return err
		}
		if err := t.podeReservar(ctx); err != nil {
			return err
		}
		it.Estado, it.Dono = EmAndamento, t.vaga
		return t.salvar("reservada", nil)

	case AcaoConcluir:
		if err := t.estado(acao, EmAndamento); err != nil {
			return err
		}
		if err := t.dono("concluir"); err != nil {
			return err
		}
		if err := obrigatorio("--ponta", t.a.Ponta, "concluir"); err != nil {
			return err
		}
		it.Estado, it.Ponta, it.PontaRevisada = Concluida, t.a.Ponta, ""
		if err := t.salvar("concluida", map[string]any{"ponta": t.a.Ponta}); err != nil {
			return err
		}
		quem := Revisor
		if !t.e.roles.Enabled(Revisor) {
			quem = Integrador
		}
		return t.acordar(quem, fmt.Sprintf("%s concluída por %s na ponta %s: revise com central revisar %s", it.ID, t.vaga, t.a.Ponta, it.ID))

	case AcaoBloquear:
		if err := t.estado(acao, EmAndamento); err != nil {
			return err
		}
		if err := t.dono("bloquear"); err != nil {
			return err
		}
		if err := obrigatorio("uma nota", t.a.Nota, "bloquear"); err != nil {
			return err
		}
		if err := t.tx.Note(it.ID, "bloqueio", t.a.Nota); err != nil {
			return err
		}
		// A passagem por bloqueada fica no evento; o item volta a pronto na mesma transação.
		it.Estado, it.Dono, it.Bloqueios = Bloqueada, "", it.Bloqueios+1
		if err := t.acordar(Planejador, fmt.Sprintf("%s bloqueada por %s: %s", it.ID, t.vaga, t.a.Nota)); err != nil {
			return err
		}
		it.Estado = Pronta
		return t.salvar("bloqueada", map[string]any{"nota": t.a.Nota, "bloqueios": it.Bloqueios})

	case AcaoRevisar:
		if err := t.estado(acao, Concluida); err != nil {
			return err
		}
		if err := t.papelOK("revisar", Revisor); err != nil {
			return err
		}
		it.Estado = EmRevisao
		if it.Campos == nil {
			it.Campos = map[string]any{}
		}
		it.Campos["revisor"] = t.vaga
		return t.salvar("em_revisao", nil)

	case AcaoAprovar:
		de, papel := EmRevisao, Revisor
		if !t.e.roles.Enabled(Revisor) {
			de, papel = Concluida, Integrador
		}
		if err := t.estado(acao, de); err != nil {
			return err
		}
		if err := t.papelOK("aprovar", papel); err != nil {
			return err
		}
		if err := obrigatorio("--ponta", t.a.Ponta, "aprovar"); err != nil {
			return err
		}
		if t.a.Ponta != it.Ponta {
			return fmt.Errorf("%w: a ponta revisada (%s) não é a atual de %s (%s): revise de novo", core.ErrConflict, t.a.Ponta, it.ID, it.Ponta)
		}
		it.Estado, it.PontaRevisada = Aprovada, t.a.Ponta
		if err := t.salvar("aprovada", map[string]any{"ponta": t.a.Ponta}); err != nil {
			return err
		}
		if papel == Integrador {
			return nil // quem aprovou é quem integra
		}
		return t.acordar(Integrador, fmt.Sprintf("%s aprovada na ponta %s: integre com central integrar %s --commit <hash>", it.ID, t.a.Ponta, it.ID))

	case AcaoRejeitar:
		if err := t.estado(acao, EmRevisao); err != nil {
			return err
		}
		if err := t.papelOK("rejeitar", Revisor); err != nil {
			return err
		}
		if err := obrigatorio("um motivo", t.a.Motivo, "rejeitar"); err != nil {
			return err
		}
		if err := t.tx.Note(it.ID, "rejeicao", t.a.Motivo); err != nil {
			return err
		}
		it.Estado = Rejeitada
		if err := t.salvar("rejeitada", map[string]any{"motivo": t.a.Motivo}); err != nil {
			return err
		}
		return t.acordarVaga(it.Dono, fmt.Sprintf("%s rejeitada por %s: %s (retome com central reservar %s)", it.ID, t.vaga, t.a.Motivo, it.ID))

	case AcaoIntegrar:
		if err := t.estado(acao, Aprovada); err != nil {
			return err
		}
		if err := t.papelOK("integrar", Integrador); err != nil {
			return err
		}
		if err := obrigatorio("--commit", t.a.Commit, "integrar"); err != nil {
			return err
		}
		it.Commit, it.Marco = t.a.Commit, it.Marco+1
		if it.Tipo == TipoMissao && !t.a.Final {
			// Missão: cada marco integrado volta ao dono para o próximo.
			it.Estado, it.Ponta, it.PontaRevisada = EmAndamento, "", ""
			if err := t.salvar("marco_integrado", map[string]any{"commit": t.a.Commit, "marco": it.Marco}); err != nil {
				return err
			}
			return t.acordarVaga(it.Dono, fmt.Sprintf("marco %d de %s integrado (%s): siga para o próximo", it.Marco, it.ID, t.a.Commit))
		}
		it.Estado = Integrada
		if err := t.salvar("integrada", map[string]any{"commit": t.a.Commit}); err != nil {
			return err
		}
		corpo := fmt.Sprintf("%s integrada no commit %s", it.ID, t.a.Commit)
		if it.Visivel {
			if err := t.acordar(Validador, corpo+": valide na interface com central validar "+it.ID+" <resultado>"); err != nil {
				return err
			}
		}
		return t.acordar(Planejador, corpo+": faça o aceite de produto")

	case AcaoValidar:
		if err := t.estado(acao, Integrada); err != nil {
			return err
		}
		if err := t.papelOK("validar", Validador); err != nil {
			return err
		}
		if !slices.Contains(ResultadosValidacao, t.a.Resultado) {
			return fmt.Errorf("%w: resultado %q (use %v)", core.ErrInvalid, t.a.Resultado, ResultadosValidacao)
		}
		if err := t.tx.Note(it.ID, "validacao", t.a.Resultado); err != nil {
			return err
		}
		it.Estado = Validada
		if it.Campos == nil {
			it.Campos = map[string]any{}
		}
		it.Campos["validacao"] = t.a.Resultado
		if err := t.salvar("validada", map[string]any{"resultado": t.a.Resultado}); err != nil {
			return err
		}
		if t.a.Resultado == "ligada" {
			return nil
		}
		return t.acordar(Planejador, fmt.Sprintf("%s validada como %s: a funcionalidade não chega ao usuário", it.ID, t.a.Resultado))

	case AcaoReabrir:
		if err := t.estado(acao, Concluida, EmRevisao, Aprovada); err != nil {
			return err
		}
		if err := t.dono("reabrir"); err != nil {
			return err
		}
		it.Estado, it.PontaRevisada = EmAndamento, ""
		if err := t.salvar("reaberta", nil); err != nil {
			return err
		}
		return t.acordar(Revisor, fmt.Sprintf("%s foi reaberta por %s: a revisão foi anulada", it.ID, t.vaga))
	}
	return fmt.Errorf("%w: ação %s sem regra", core.ErrInvalid, acao)
}

// faltando lista os campos obrigatórios vazios do item.
func (e *Engine) faltando(it core.Item) []string {
	var falta []string
	for _, campo := range e.cfg.Obrigatorios {
		var v any
		switch campo {
		case "titulo":
			v = it.Titulo
		case "area":
			v = it.Area
		case "prioridade":
			v = it.Prioridade
		case "categoria", "tipo":
			v = it.Categoria
		default:
			v = it.Campos[campo]
		}
		if v == nil || strings.TrimSpace(fmt.Sprint(v)) == "" {
			falta = append(falta, campo)
		}
	}
	return falta
}

// podeReservar aplica as condições da reserva: congelamento, notas de
// bloqueio (vaga sênior) e conflito de arquivos com itens em andamento.
func (t *transicao) podeReservar(ctx context.Context) error {
	s, err := t.tx.Sprint()
	if err != nil {
		return err
	}
	if s != nil && !t.tx.Now().Before(s.Congelamento) {
		return fmt.Errorf("%w: a sprint está congelada: ninguém reserva tarefa nova", core.ErrConflict)
	}
	switch {
	case t.it.Bloqueios >= 2:
		return fmt.Errorf("%w: %s tem %d notas de bloqueio: o PLANEJADOR e o GERENTE decidem o destino dela", core.ErrConflict, t.it.ID, t.it.Bloqueios)
	case t.it.Bloqueios == 1 && t.e.roles.HasSenior() && !t.e.roles.IsSenior(t.vaga):
		return fmt.Errorf("%w: %s já bloqueou uma vez e fica com a vaga sênior", core.ErrConflict, t.it.ID)
	}
	ativos, err := t.tx.List(core.ItemFilter{Estados: []string{EmAndamento}})
	if err != nil {
		return err
	}
	for _, o := range ativos {
		if o.ID == t.it.ID {
			continue
		}
		if a, b, ok := conflito(t.it.Arquivos, o.Arquivos); ok {
			return fmt.Errorf("%w: %s (%s) conflita com %s de %s, em andamento com %s", core.ErrConflict, t.it.ID, a, b, o.ID, o.Dono)
		}
	}
	return nil
}

// conflito diz se dois conjuntos de arquivos se sobrepõem: o mesmo caminho,
// ou uma pasta (terminada em /) que contém o outro.
func conflito(as, bs []string) (string, string, bool) {
	for _, a := range as {
		for _, b := range bs {
			if sobrepoe(a, b) {
				return a, b, true
			}
		}
	}
	return "", "", false
}

func sobrepoe(a, b string) bool {
	a, b = strings.ReplaceAll(a, "\\", "/"), strings.ReplaceAll(b, "\\", "/")
	if a == b {
		return true
	}
	return (strings.HasSuffix(a, "/") && strings.HasPrefix(b, a)) || (strings.HasSuffix(b, "/") && strings.HasPrefix(a, b))
}
