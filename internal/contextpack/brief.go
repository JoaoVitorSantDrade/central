package contextpack

import (
	"fmt"
	"strings"
	"time"

	"github.com/JoaoVitorSantDrade/central/internal/core"
)

// Leitura é uma linha do mapa de leitura.
type Leitura struct{ Caminho, Resumo, Quando string }

// Bloco é uma das oito partes do brief (spec §7).
type Bloco struct {
	N       int    `json:"n"`
	Titulo  string `json:"titulo"`
	Texto   string `json:"texto"`
	Tokens  int    `json:"tokens"`
	Cortado bool   `json:"cortado"`
}

// Brief é o contexto montado para uma vaga.
type Brief struct {
	Vaga     string  `json:"vaga"`
	Papel    string  `json:"papel"`
	Blocos   []Bloco `json:"blocos"`
	Texto    string  `json:"texto"`
	Tokens   int     `json:"tokens"`
	Cortados []int   `json:"cortados"`
}

// Input é tudo o que o brief mostra, já lido pelo serviço.
type Input struct {
	Agente               core.Agent
	Canonico, Projeto    string
	RaizProjeto, DataDir string
	Sprint               *core.Sprint
	Reserva              *core.Item
	Caixa                []core.Message
	NaoLidas             int
	Pendencias           core.Pendencias
	Cartao, FonteCartao  string
	Nucleo               string
	Regras               []core.Rule
	Fatos                []core.Fact
	// FatosSoChaves mostra só as chaves e o horário (o papel não marcou fatos relevantes).
	FatosSoChaves bool
	Leitura       []Leitura
	Agora         time.Time
}

// Tokens estima os tokens de um texto como bytes/4 (spec §7).
func Tokens(s string) int { return len(s) / 4 }

// maxCaixa é quantas mensagens não lidas o brief resume.
const maxCaixa = 10

func corta(s string, n int) string {
	s = strings.ReplaceAll(strings.TrimSpace(s), "\n", " ")
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}

func quando(agora, t time.Time) string {
	d := t.Sub(agora).Round(time.Minute)
	switch {
	case d > 0:
		return fmt.Sprintf("%s (em %v)", t.Local().Format("15:04"), d)
	case d < 0:
		return fmt.Sprintf("%s (há %v)", t.Local().Format("15:04"), -d)
	}
	return t.Local().Format("15:04") + " (agora)"
}

// proximoPasso diz o que o dono de uma reserva faz em seguida.
func proximoPasso(it core.Item) string {
	switch it.Estado {
	case "em_andamento":
		return "trabalhe e conclua com `central concluir " + it.ID + " --ponta <hash>` (ou `central bloquear " + it.ID + " \"<nota>\"`)"
	case "concluida", "em_revisao":
		return "esperando revisão; mudou a branch? `central reabrir " + it.ID + "`"
	case "aprovada":
		return "aprovada; esperando integração"
	case "rejeitada":
		return "rejeitada: leia o motivo (`central item ver " + it.ID + "`) e retome com `central reservar " + it.ID + "`"
	}
	return "estado " + it.Estado
}

func blocoQuem(in Input) string {
	a := in.Agente
	var b strings.Builder
	fmt.Fprintf(&b, "Vaga %s, papel %s", a.Vaga, a.Papel)
	if in.Canonico != "" && in.Canonico != a.Papel {
		fmt.Fprintf(&b, " (%s)", in.Canonico)
	}
	fmt.Fprintf(&b, ", projeto %s.\n", in.Projeto)
	fmt.Fprintf(&b, "- pasta da vaga: %s\n", a.Pasta)
	if in.RaizProjeto != "" && in.RaizProjeto != a.Pasta {
		fmt.Fprintf(&b, "- raiz registrada do projeto: %s\n", in.RaizProjeto)
	}
	if in.DataDir != "" {
		fmt.Fprintf(&b, "- dados da Central: %s\n", in.DataDir)
	}
	if a.Area != "" {
		fmt.Fprintf(&b, "- área preferida: %s\n", a.Area)
	}
	return b.String()
}

func blocoAgora(in Input) string {
	var b strings.Builder
	if s := in.Sprint; s != nil {
		fmt.Fprintf(&b, "Sprint %s, de %s a %s.\n", s.ID, s.Abertura.Local().Format("15:04"), s.Fim.Local().Format("15:04"))
		if !in.Agora.Before(s.Congelamento) {
			b.WriteString("- CONGELADA: ninguém reserva tarefa nova.\n")
		}
		for _, nome := range core.CeremonyNames {
			if t, _ := s.Horario(nome); t.After(in.Agora) {
				fmt.Fprintf(&b, "- próxima cerimônia: %s às %s\n", nome, quando(in.Agora, t))
				break
			}
		}
	} else {
		b.WriteString("Sem sprint aberta.\n")
	}
	if it := in.Reserva; it != nil {
		fmt.Fprintf(&b, "Sua reserva: %s (%s), %s: %s.\n", it.ID, corta(it.Titulo, 80), it.Estado, proximoPasso(*it))
	} else if in.Canonico == "EXECUTOR" {
		b.WriteString("Sem reserva: peça a próxima com `central proxima`.\n")
	} else {
		b.WriteString("Sem item reservado.\n")
	}
	return b.String()
}

func blocoPendencias(in Input) string {
	var b strings.Builder
	switch n := in.NaoLidas; {
	case n == 0 && len(in.Caixa) == 0:
		b.WriteString("Caixa vazia.\n")
	default:
		if n < len(in.Caixa) {
			n = len(in.Caixa)
		}
		fmt.Fprintf(&b, "%d mensagens não lidas (leia com `central caixa`):\n", n)
		for i, m := range in.Caixa {
			if i == maxCaixa {
				fmt.Fprintf(&b, "- … e mais %d\n", n-maxCaixa)
				break
			}
			ref := ""
			if m.Ref != "" {
				ref = " [" + m.Ref + "]"
			}
			fmt.Fprintf(&b, "- %d %s %s%s: %s\n", m.Seq, m.De, m.Tipo, ref, corta(m.Corpo, 140))
		}
	}
	if len(in.Pendencias.Devo) > 0 {
		b.WriteString("Você deve responder:\n")
		for _, m := range in.Pendencias.Devo {
			prazo := ""
			if m.ResponderAte != nil {
				prazo = " até " + quando(in.Agora, *m.ResponderAte)
			}
			fmt.Fprintf(&b, "- %d de %s%s: %s (`central responder %d \"…\"`)\n", m.Seq, m.De, prazo, corta(m.Corpo, 100), m.Seq)
		}
	}
	if n := len(in.Pendencias.Esperam); n > 0 {
		fmt.Fprintf(&b, "Você espera %d respostas (`central pendentes`).\n", n)
	}
	return b.String()
}

func blocoRegras(in Input) string {
	if len(in.Regras) == 0 {
		return "Nenhuma regra ativa para o seu papel.\n"
	}
	var b strings.Builder
	for _, r := range in.Regras {
		fmt.Fprintf(&b, "- %s: %s\n", r.ID, strings.TrimSpace(r.Pratica))
	}
	return b.String()
}

func blocoFatos(in Input) string {
	if len(in.Fatos) == 0 {
		return "Nenhum fato registrado para o seu papel.\n"
	}
	var b strings.Builder
	if in.FatosSoChaves {
		b.WriteString("Fatos do projeto (leia com `central fato get <chave>`):\n")
	}
	for _, f := range in.Fatos {
		if in.FatosSoChaves {
			fmt.Fprintf(&b, "- %s (%s, por %s)\n", f.Chave, f.Quando.Local().Format("02/01 15:04"), f.Vaga)
			continue
		}
		fmt.Fprintf(&b, "- %s = %s (%s, por %s)\n", f.Chave, corta(string(f.Valor), 200), f.Quando.Local().Format("02/01 15:04"), f.Vaga)
	}
	return b.String()
}

func blocoLeitura(in Input) string {
	if len(in.Leitura) == 0 {
		return "Sem mapa de leitura no central.toml.\n"
	}
	var b strings.Builder
	b.WriteString("Leia só quando precisar:\n")
	for _, l := range in.Leitura {
		fmt.Fprintf(&b, "- %s", l.Caminho)
		if l.Resumo != "" {
			fmt.Fprintf(&b, ": %s", l.Resumo)
		}
		if l.Quando != "" {
			fmt.Fprintf(&b, " (leia %s)", l.Quando)
		}
		b.WriteString("\n")
	}
	return b.String()
}

// FiltrarFatos devolve os fatos cujas chaves casam com algum padrão ("*" no fim = prefixo).
func FiltrarFatos(fs []core.Fact, padroes []string) []core.Fact {
	var out []core.Fact
	for _, f := range fs {
		for _, p := range padroes {
			if (strings.HasSuffix(p, "*") && strings.HasPrefix(f.Chave, strings.TrimSuffix(p, "*"))) || f.Chave == p {
				out = append(out, f)
				break
			}
		}
	}
	return out
}

const avisoCorte = "(cortado: o brief passou do teto; peça este bloco com `central brief --max-tokens <maior>`)\n"

// Montar monta o brief. curto = blocos 1–3. Acima de maxTokens, corta do fim
// (8 → 7 → 6) e nunca os blocos 1–5.
func Montar(in Input, curto bool, maxTokens int) Brief {
	nucleo := in.Nucleo
	if nucleo == "" {
		nucleo = "(o projeto não tem .central/nucleo.md)"
	}
	defs := []struct {
		titulo, texto string
	}{
		{"Quem você é", blocoQuem(in)},
		{"Agora", blocoAgora(in)},
		{"Pendências", blocoPendencias(in)},
		{"Seu papel", in.Cartao + "\n"},
		{"Núcleo do projeto", nucleo + "\n"},
		{"Regras que valem para você", blocoRegras(in)},
		{"Fatos", blocoFatos(in)},
		{"Mapa de leitura", blocoLeitura(in)},
	}
	if curto {
		defs = defs[:3]
	}
	b := Brief{Vaga: in.Agente.Vaga, Papel: in.Agente.Papel, Cortados: []int{}}
	for i, d := range defs {
		txt := fmt.Sprintf("## %d. %s\n\n%s\n\n", i+1, d.titulo, strings.TrimRight(d.texto, "\n"))
		b.Blocos = append(b.Blocos, Bloco{N: i + 1, Titulo: d.titulo, Texto: txt, Tokens: Tokens(txt)})
	}
	total := func() int {
		n := 0
		for _, bl := range b.Blocos {
			n += bl.Tokens
		}
		return n
	}
	for n := len(b.Blocos); n >= 6 && maxTokens > 0 && total() > maxTokens; n-- {
		bl := &b.Blocos[n-1]
		bl.Cortado = true
		bl.Texto = fmt.Sprintf("## %d. %s\n\n%s\n", bl.N, bl.Titulo, avisoCorte)
		bl.Tokens = Tokens(bl.Texto)
		b.Cortados = append(b.Cortados, bl.N)
	}
	var sb strings.Builder
	for _, bl := range b.Blocos {
		sb.WriteString(bl.Texto)
	}
	b.Texto = sb.String()
	b.Tokens = Tokens(b.Texto)
	return b
}
