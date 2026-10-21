package process

import (
	"context"
	"errors"
	"reflect"
	"slices"

	"github.com/JoaoVitorSantDrade/central/internal/core"
)

// Found é um item lido de uma fonte externa (arquivo), com o estado que a
// pasta representa e o texto de bloqueio, se houver.
type Found struct {
	Estado, Caminho, Hash string
	In                    ItemInput
	Bloqueio              string
}

// Invalid é um arquivo da fonte que não pôde ser lido como item.
type Invalid struct{ Caminho, Hash, Erro string }

// Resumo conta o que uma sincronização mudou.
type Resumo struct {
	Criados     int `json:"criados"`
	Atualizados int `json:"atualizados"`
	Avancados   int `json:"avancados"`
	Invalidos   int `json:"invalidos"`
}

// QuemSincroniza é o autor dos eventos de sincronização.
const QuemSincroniza = "adaptador"

// avanca diz se o estado da fonte leva o item adiante (nunca volta). Só
// proposta → pronta e qualquer estado vivo → integrada.
func avanca(atual, fonte string) bool {
	switch fonte {
	case Pronta:
		return atual == Proposta
	case Integrada:
		return atual != Integrada && atual != Validada
	}
	return false
}

// Sincronizar aplica à base o que a fonte mostra: cria os itens que faltam,
// atualiza os campos descritivos que mudaram e só avança o estado. Arquivos
// inválidos viram o evento item.fonte_invalida (uma vez por conteúdo).
func (e *Engine) Sincronizar(ctx context.Context, achados []Found, invalidos []Invalid) (Resumo, error) {
	var r Resumo
	err := e.c.WithItems(ctx, QuemSincroniza, func(tx core.ItemTx) error {
		for _, inv := range invalidos {
			if _, visto := e.invalidosVistos.LoadOrStore(inv.Caminho+"\x00"+inv.Hash, true); visto {
				continue
			}
			r.Invalidos++
			if err := tx.Event("item.fonte_invalida", inv.Caminho, map[string]any{"erro": inv.Erro}); err != nil {
				return err
			}
		}
		for _, f := range achados {
			if err := e.sincronizarUm(tx, f, &r); err != nil {
				return err
			}
		}
		return nil
	})
	return r, err
}

func (e *Engine) sincronizarUm(tx core.ItemTx, f Found, r *Resumo) error {
	in := f.In
	tipo := in.Tipo
	if tipo == "" {
		tipo = TipoTarefa
	}
	novo := core.Item{ID: in.ID, Tipo: tipo, Titulo: in.Titulo, Prioridade: in.Prioridade, Area: in.Area,
		Categoria: in.Categoria, Sprint: in.Sprint, Recurso: in.Recurso, Arquivos: in.Arquivos, Folga: in.Folga,
		Bloqueante: in.Bloqueante, Visivel: in.Visivel, Campos: in.Campos, Fonte: f.Caminho}
	it, err := tx.Get(in.ID)
	switch {
	case err == nil:
	case isNotFound(err):
		novo.Estado = f.Estado
		if novo.Estado == "" {
			novo.Estado = Proposta
		}
		if f.Bloqueio != "" {
			novo.Bloqueios = 1
		}
		if err := tx.Save(novo, "sincronizado", map[string]any{"fonte": f.Caminho, "criado": true}); err != nil {
			return err
		}
		r.Criados++
		if f.Bloqueio != "" {
			return tx.Note(in.ID, "bloqueio", f.Bloqueio)
		}
		return nil
	default:
		return err
	}

	antes := it
	it.Titulo, it.Prioridade, it.Area, it.Categoria, it.Sprint = novo.Titulo, novo.Prioridade, novo.Area, novo.Categoria, novo.Sprint
	it.Recurso, it.Arquivos, it.Folga, it.Bloqueante, it.Visivel, it.Fonte = novo.Recurso, novo.Arquivos, novo.Folga, novo.Bloqueante, novo.Visivel, novo.Fonte
	// Campos do arquivo por cima dos do item (os que o processo guarda, como revisor, ficam).
	campos := map[string]any{}
	for k, v := range it.Campos {
		campos[k] = v
	}
	for k, v := range novo.Campos {
		campos[k] = v
	}
	it.Campos = campos
	avancou := avanca(it.Estado, f.Estado)
	if avancou {
		it.Estado = f.Estado
		if f.Estado == Integrada {
			it.Dono = ""
		}
	}
	nota := false
	if f.Bloqueio != "" {
		notas, err := tx.Notes(in.ID)
		if err != nil {
			return err
		}
		if !slices.ContainsFunc(notas, func(n core.Note) bool { return n.Tipo == "bloqueio" && n.Texto == f.Bloqueio }) {
			nota = true
			it.Bloqueios++
		}
	}
	if !avancou && !nota && semMudanca(antes, it) {
		return nil
	}
	if err := tx.Save(it, "sincronizado", map[string]any{"fonte": f.Caminho, "avancou": avancou}); err != nil {
		return err
	}
	if avancou {
		r.Avancados++
	} else {
		r.Atualizados++
	}
	if nota {
		return tx.Note(in.ID, "bloqueio", f.Bloqueio)
	}
	return nil
}

func semMudanca(a, b core.Item) bool {
	a.AtualizadoEm = b.AtualizadoEm
	return reflect.DeepEqual(normal(a), normal(b))
}

// normal deixa listas e mapas vazios iguais a nil para a comparação.
func normal(it core.Item) core.Item {
	if len(it.Arquivos) == 0 {
		it.Arquivos = nil
	}
	if len(it.Campos) == 0 {
		it.Campos = nil
	}
	return it
}

func isNotFound(err error) bool { return errors.Is(err, core.ErrNotFound) }
