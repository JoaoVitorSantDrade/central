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

// FimDeTurno substitui o FimDeCiclo nas vagas que a Central executa: ela
// acorda a vaga com um turno novo, então a vaga não espera, termina o turno.
const FimDeTurno = `Ao terminar uma unidade de trabalho, termine o turno: a Central acorda você quando houver algo. Para esperar algo sem mensagem, use ` + "`central despertar --em <duração> \"<motivo>\"`" + `. Depois de um resumo automático da conversa, ` + "`central brief --curto --md`" + ` devolve onde você estava.`

// Card devolve o cartão do papel: .central/papeis/<papel do projeto>.md,
// senão .central/papeis/<papel canônico>.md, senão o padrão embutido. Todos
// terminam com a instrução de fim de ciclo (FimDeTurno numa vaga executada),
// salvo o cartão que já diz como esperar ou terminar o turno. fonte diz de onde veio.
func Card(pasta, papelProjeto, papelCanonico string, executada bool) (texto, fonte string) {
	fim := FimDeCiclo
	if executada {
		fim = FimDeTurno
	}
	for _, nome := range []string{papelProjeto, papelCanonico} {
		if nome == "" {
			continue
		}
		p := filepath.Join(pasta, ".central", "papeis", nome+".md")
		if b, err := os.ReadFile(p); err == nil {
			txt := strings.TrimSpace(string(b))
			// Como esperar é mecânica da Central: o cartão do projeto só a repete se quiser outra.
			if !strings.Contains(txt, "central esperar") && !strings.Contains(txt, "termine o turno") {
				txt += "\n\n" + fim
			}
			return txt, p
		}
	}
	if b, err := cartoes.ReadFile("cartoes/" + papelCanonico + ".md"); err == nil {
		return strings.TrimSpace(string(b)) + "\n\n" + fim, "embutido:" + papelCanonico
	}
	return "Papel sem cartão: siga o núcleo do projeto e os comandos da Central (`central --help`).\n\n" + fim, "embutido:generico"
}

// Nucleo devolve .central/nucleo.md da pasta (ok=false se não existe).
func Nucleo(pasta string) (string, bool) {
	b, err := os.ReadFile(filepath.Join(pasta, ".central", "nucleo.md"))
	if errors.Is(err, os.ErrNotExist) || err != nil {
		return "", false
	}
	return strings.TrimSpace(string(b)), true
}
