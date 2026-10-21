// Package lessons lê a tabela de lições do time (docs/agents/LICOES.md do
// LyricCut): | ID | Origem | Lição | Regra prática | Quem aplica | Estado |.
package lessons

import (
	"bytes"
	"fmt"
	"regexp"
	"strings"
)

// Lesson é uma linha da tabela. Aplica traz os nomes como estão no arquivo
// (TODOS ou papéis em caixa-alta); quem chama traduz para os canônicos.
type Lesson struct {
	ID, Origem, Texto, Pratica, Estado string
	Aplica                             []string
}

var (
	reID    = regexp.MustCompile(`^L-\d+$`)
	rePapel = regexp.MustCompile(`\b[A-Z][A-Z_]{2,}\b`)
)

// estado traduz o texto da coluna Estado.
func estado(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	switch {
	case strings.HasPrefix(s, "ativa"):
		return "ativa"
	case strings.HasPrefix(s, "no manual"):
		return "no_manual"
	case strings.HasPrefix(s, "no código"), strings.HasPrefix(s, "no codigo"), strings.HasPrefix(s, "na central"):
		return "no_codigo"
	case strings.HasPrefix(s, "aposentada"):
		return "aposentada"
	}
	return ""
}

func aplica(s string) []string {
	if strings.Contains(strings.ToLower(s), "todos") {
		return []string{"TODOS"}
	}
	var out []string
	vistos := map[string]bool{}
	for _, p := range rePapel.FindAllString(s, -1) {
		if !vistos[p] {
			vistos[p] = true
			out = append(out, p)
		}
	}
	return out
}

// Parse devolve as lições válidas e um aviso por linha que parece lição mas não fecha.
func Parse(md []byte) ([]Lesson, []string) {
	md = bytes.TrimPrefix(md, []byte("\xef\xbb\xbf"))
	var ls []Lesson
	var avisos []string
	for n, linha := range strings.Split(strings.ReplaceAll(string(md), "\r\n", "\n"), "\n") {
		linha = strings.TrimSpace(linha)
		if !strings.HasPrefix(linha, "|") {
			continue
		}
		cs := strings.Split(strings.Trim(linha, "|"), "|")
		for i := range cs {
			cs[i] = strings.TrimSpace(cs[i])
		}
		if len(cs) == 0 || !reID.MatchString(cs[0]) {
			continue // cabeçalho, separador ou outra tabela
		}
		if len(cs) != 6 {
			avisos = append(avisos, fmt.Sprintf("linha %d (%s): %d colunas, esperava 6", n+1, cs[0], len(cs)))
			continue
		}
		l := Lesson{ID: cs[0], Origem: cs[1], Texto: cs[2], Pratica: cs[3], Aplica: aplica(cs[4]), Estado: estado(cs[5])}
		if l.Estado == "" || len(l.Aplica) == 0 {
			avisos = append(avisos, fmt.Sprintf("linha %d (%s): estado %q ou quem aplica %q não reconhecido", n+1, l.ID, cs[5], cs[4]))
			continue
		}
		ls = append(ls, l)
	}
	return ls, avisos
}
