// Package contextpack monta o contexto que cada agente lê ao começar e depois
// de um resumo automático (spec §7): o brief por papel. Não toca no banco:
// recebe tudo pronto do serviço.
package contextpack

import (
	"embed"
	"errors"
	"os"
	"path/filepath"
	"strings"
)

//go:embed cartoes/*.md
var cartoes embed.FS

// FimDeCiclo é a instrução comum a todos os papéis: como esperar sem polling
// (resultado do piloto M0, docs/piloto-m0.md).
const FimDeCiclo = `Sem trabalho: rode ` + "`central esperar --ate 30m`" + ` pelo shell em segundo plano, agende um despertar de reserva de 30 min e termine o turno. Quando o comando terminar você é acordado: comece o ciclo por ` + "`central caixa`" + `. Depois de um resumo automático da conversa, ` + "`central brief --curto --md`" + ` devolve onde você estava.`

// Card devolve o cartão do papel: .central/papeis/<papel do projeto>.md,
// senão .central/papeis/<papel canônico>.md, senão o padrão embutido. Todos
// terminam com a instrução de fim de ciclo, salvo o cartão que já diz como
// esperar. fonte diz de onde veio.
func Card(pasta, papelProjeto, papelCanonico string) (texto, fonte string) {
	for _, nome := range []string{papelProjeto, papelCanonico} {
		if nome == "" {
			continue
		}
		p := filepath.Join(pasta, ".central", "papeis", nome+".md")
		if b, err := os.ReadFile(p); err == nil {
			txt := strings.TrimSpace(string(b))
			// Como esperar é mecânica da Central: o cartão do projeto só a repete se quiser outra.
			if !strings.Contains(txt, "central esperar") {
				txt += "\n\n" + FimDeCiclo
			}
			return txt, p
		}
	}
	if b, err := cartoes.ReadFile("cartoes/" + papelCanonico + ".md"); err == nil {
		return strings.TrimSpace(string(b)) + "\n\n" + FimDeCiclo, "embutido:" + papelCanonico
	}
	return "Papel sem cartão: siga o núcleo do projeto e os comandos da Central (`central --help`).\n\n" + FimDeCiclo, "embutido:generico"
}

// Nucleo devolve .central/nucleo.md da pasta (ok=false se não existe).
func Nucleo(pasta string) (string, bool) {
	b, err := os.ReadFile(filepath.Join(pasta, ".central", "nucleo.md"))
	if errors.Is(err, os.ErrNotExist) || err != nil {
		return "", false
	}
	return strings.TrimSpace(string(b)), true
}
