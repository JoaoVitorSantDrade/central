package cli

import (
	"io"
	"net/url"
	"path/filepath"
	"strings"
)

// comandoProcesso é um comando do processo: as flags que aceita e o que faz
// com o projeto, o cliente e a base da URL já resolvidos.
type comandoProcesso struct {
	flags []string
	run   func(a processArgs) (map[string]any, error)
}

// processArgs é o contexto de um comando do processo.
type processArgs struct {
	flags map[string]string
	pos   []string
	stdin io.Reader
	c     *client
	base  string
	pasta string
}

func (a processArgs) post(path string, body map[string]any) (map[string]any, error) {
	if body == nil {
		body = map[string]any{}
	}
	body["pasta"] = a.pasta
	return hintEntrar(callWithDaemon(a.c, "POST", a.base+path, body))
}

func (a processArgs) get(path string, q url.Values) (map[string]any, error) {
	if q == nil {
		q = url.Values{}
	}
	q.Set("pasta", a.pasta)
	return callWithDaemon(a.c, "GET", a.base+path+"?"+q.Encode(), nil)
}

// comandosProcesso indexa os comandos do processo pelo nome (com subcomando).
var comandosProcesso = map[string]comandoProcesso{
	"sprint abrir": {flags: []string{"expediente", "id"}, run: func(a processArgs) (map[string]any, error) {
		if a.flags["expediente"] == "" || len(a.pos) > 0 {
			return nil, usage("sprint abrir precisa de --expediente HH:MM-HH:MM")
		}
		return a.post("/sprint", map[string]any{"expediente": a.flags["expediente"], "id": a.flags["id"]})
	}},
	"sprint ver":       {run: func(a processArgs) (map[string]any, error) { return a.get("/sprint", nil) }},
	"sprint antecipar": {run: func(a processArgs) (map[string]any, error) { return a.post("/sprint/antecipar", nil) }},
	"sprint encerrar":  {run: func(a processArgs) (map[string]any, error) { return a.post("/sprint/encerrar", nil) }},

	"item criar": {flags: []string{"id", "tipo", "titulo", "area", "prioridade", "categoria", "sprint", "recurso", "arquivos",
		"folga", "bloqueante", "visivel", "criterio"}, run: func(a processArgs) (map[string]any, error) {
		if a.flags["titulo"] == "" || len(a.pos) > 0 {
			return nil, usage("item criar precisa de --titulo")
		}
		in := map[string]any{"id": a.flags["id"], "tipo": a.flags["tipo"], "titulo": a.flags["titulo"], "area": a.flags["area"],
			"prioridade": a.flags["prioridade"], "categoria": a.flags["categoria"], "sprint": a.flags["sprint"],
			"recurso": a.flags["recurso"], "arquivos": lista(a.flags["arquivos"]), "folga": sim(a.flags["folga"]),
			"bloqueante": sim(a.flags["bloqueante"]), "visivel": sim(a.flags["visivel"])}
		if c := a.flags["criterio"]; c != "" {
			in["campos"] = map[string]any{"criterio_de_pronto": c}
		}
		return a.post("/itens", in)
	}},
	"item ver": {run: verItem},
	"item listar": {flags: []string{"estado", "tipo", "dono", "sprint"}, run: func(a processArgs) (map[string]any, error) {
		q := url.Values{}
		for _, k := range []string{"estado", "tipo", "dono", "sprint"} {
			if v := a.flags[k]; v != "" {
				q.Set(k, v)
			}
		}
		return a.get("/itens", q)
	}},
	"item pronto": acaoItem("pronto"),
	"reservar":    acaoItem("reservar"),
	"concluir":    acaoItem("concluir", "ponta"),
	"bloquear":    acaoItemTexto("bloquear", "nota"),
	"revisar":     acaoItem("revisar"),
	"aprovar":     acaoItem("aprovar", "ponta"),
	"rejeitar":    acaoItemTexto("rejeitar", "motivo"),
	"integrar":    acaoItem("integrar", "commit", "final"),
	"validar":     acaoItemTexto("validar", "resultado"),
	"reabrir":     acaoItem("reabrir"),
	"decisao abrir": {flags: []string{"titulo"}, run: func(a processArgs) (map[string]any, error) {
		if a.flags["titulo"] == "" || len(a.pos) != 1 {
			return nil, usage("decisao abrir precisa de --titulo e <corpo | ->")
		}
		corpo, err := textoOuStdin(a, a.pos[0])
		if err != nil {
			return nil, err
		}
		return a.post("/decisoes", map[string]any{"titulo": a.flags["titulo"], "corpo": corpo})
	}},
	"decisao chave": {flags: []string{"acordo", "objecao"}, run: func(a processArgs) (map[string]any, error) {
		if len(a.pos) != 1 || (a.flags["acordo"] == "") == (a.flags["objecao"] == "") {
			return nil, usage("decisao chave precisa de <id> e --acordo sim ou --objecao \"motivo\"")
		}
		return a.post("/decisoes/"+url.PathEscape(a.pos[0])+"/chave", map[string]any{"acordo": sim(a.flags["acordo"]), "motivo": a.flags["objecao"]})
	}},
	"decisao revisar": {run: func(a processArgs) (map[string]any, error) {
		if len(a.pos) != 2 {
			return nil, usage("decisao revisar precisa de <id> e <corpo | ->")
		}
		corpo, err := textoOuStdin(a, a.pos[1])
		if err != nil {
			return nil, err
		}
		return a.post("/decisoes/"+url.PathEscape(a.pos[0])+"/revisar", map[string]any{"corpo": corpo})
	}},
	"decisao escalar": {run: func(a processArgs) (map[string]any, error) {
		if len(a.pos) != 1 {
			return nil, usage("decisao escalar precisa de <id>")
		}
		return a.post("/decisoes/"+url.PathEscape(a.pos[0])+"/escalar", nil)
	}},
	"decisao veto": {run: func(a processArgs) (map[string]any, error) {
		if len(a.pos) != 2 {
			return nil, usage("decisao veto precisa de <id> e <motivo>")
		}
		return a.post("/decisoes/"+url.PathEscape(a.pos[0])+"/veto", map[string]any{"motivo": a.pos[1]})
	}},
	"decisao responder": {flags: []string{"aprovar"}, run: func(a processArgs) (map[string]any, error) {
		if len(a.pos) < 1 || len(a.pos) > 2 || a.flags["aprovar"] == "" {
			return nil, usage("decisao responder precisa de <id>, --aprovar sim|nao e, opcional, <texto>")
		}
		texto := ""
		if len(a.pos) == 2 {
			texto = a.pos[1]
		}
		return a.post("/decisoes/"+url.PathEscape(a.pos[0])+"/responder", map[string]any{"aprovada": sim(a.flags["aprovar"]), "texto": texto})
	}},
	"decisao ver":    {run: verItem},
	"decisao listar": {run: func(a processArgs) (map[string]any, error) { return a.get("/itens", url.Values{"tipo": {"decisao"}}) }},
	"saude":          {run: func(a processArgs) (map[string]any, error) { return a.get("/saude", nil) }},
	"brief": {flags: []string{"curto?", "md?", "max-tokens", "se-projeto?"}, run: func(a processArgs) (map[string]any, error) {
		if len(a.pos) > 0 {
			return nil, usage("brief não recebe argumentos")
		}
		q := url.Values{}
		if sim(a.flags["curto"]) {
			q.Set("curto", "1")
		}
		if v := a.flags["max-tokens"]; v != "" {
			q.Set("max_tokens", v)
		}
		m, err := a.get("/brief", q)
		if err != nil && sim(a.flags["se-projeto"]) && asCLIError(err).exit == ExitNaoAchado {
			return map[string]any{saidaMarkdown: ""}, nil // a vaga não entrou: o hook fica em silêncio
		}
		if err != nil || !sim(a.flags["md"]) {
			return m, err
		}
		b, _ := m["brief"].(map[string]any)
		texto, _ := b["texto"].(string)
		return map[string]any{saidaMarkdown: texto}, nil
	}},
	"orfas": {flags: []string{"liberar"}, run: func(a processArgs) (map[string]any, error) {
		if sim(a.flags["liberar"]) {
			return a.post("/orfas/liberar", nil)
		}
		return a.get("/orfas", nil)
	}},
	"metricas": {flags: []string{"sprint"}, run: func(a processArgs) (map[string]any, error) {
		q := url.Values{}
		if s := a.flags["sprint"]; s != "" {
			q.Set("sprint", s)
		}
		return a.get("/metricas", q)
	}},
	"importar-locks": {run: func(a processArgs) (map[string]any, error) { return pastaAbs(a, "/importar-locks") }},
	"regra listar": {flags: []string{"papel", "estado"}, run: func(a processArgs) (map[string]any, error) {
		q := url.Values{}
		for _, k := range []string{"papel", "estado"} {
			if v := a.flags[k]; v != "" {
				q.Set(k, v)
			}
		}
		return a.get("/regras", q)
	}},
	"regra criar": {flags: []string{"id", "aplica", "texto", "pratica", "origem", "estado"}, run: func(a processArgs) (map[string]any, error) {
		if a.flags["id"] == "" || a.flags["aplica"] == "" || a.flags["pratica"] == "" || len(a.pos) > 0 {
			return nil, usage("regra criar precisa de --id, --aplica e --pratica")
		}
		return a.post("/regras", map[string]any{"id": a.flags["id"], "aplica": lista(a.flags["aplica"]), "texto": a.flags["texto"],
			"pratica": a.flags["pratica"], "origem": a.flags["origem"], "estado": a.flags["estado"]})
	}},
	"regra estado": {flags: []string{"comando"}, run: func(a processArgs) (map[string]any, error) {
		if len(a.pos) != 2 {
			return nil, usage("regra estado precisa de <id> e <ativa|no_manual|no_codigo|aposentada>")
		}
		return a.post("/regras/"+url.PathEscape(a.pos[0])+"/estado", map[string]any{"estado": a.pos[1], "comando": a.flags["comando"]})
	}},
	"regra importar": {run: func(a processArgs) (map[string]any, error) {
		if len(a.pos) != 1 {
			return nil, usage("regra importar precisa de <arquivo LICOES.md>")
		}
		arq, err := filepath.Abs(a.pos[0])
		if err != nil {
			return nil, err
		}
		return a.post("/regras/importar", map[string]any{"arquivo": arq})
	}},
	"exportar-locks": {run: func(a processArgs) (map[string]any, error) { return pastaAbs(a, "/exportar-locks") }},
	"proxima": {run: func(a processArgs) (map[string]any, error) {
		if len(a.pos) > 0 {
			return nil, usage("proxima não recebe argumentos")
		}
		return a.get("/proxima", nil)
	}},
}

// acaoItem é um comando "<acao> <id> [--flag valor]" que vira POST .../itens/<id>/<acao>.
func acaoItem(acao string, campos ...string) comandoProcesso {
	return comandoProcesso{flags: campos, run: func(a processArgs) (map[string]any, error) {
		if len(a.pos) != 1 {
			return nil, usage("%s precisa de <id>", acao)
		}
		body := map[string]any{}
		for _, c := range campos {
			if c == "final" {
				body[c] = sim(a.flags[c])
			} else {
				body[c] = a.flags[c]
			}
		}
		return a.post("/itens/"+url.PathEscape(a.pos[0])+"/"+acao, body)
	}}
}

// acaoItemTexto é "<acao> <id> <texto | ->", com o texto indo no campo dado.
func acaoItemTexto(acao, campo string) comandoProcesso {
	return comandoProcesso{run: func(a processArgs) (map[string]any, error) {
		if len(a.pos) != 2 {
			return nil, usage("%s precisa de <id> e <%s | ->", acao, campo)
		}
		texto := a.pos[1]
		if texto == "-" {
			var err error
			if texto, err = readBody(a.stdin); err != nil {
				return nil, err
			}
		}
		return a.post("/itens/"+url.PathEscape(a.pos[0])+"/"+acao, map[string]any{campo: texto})
	}}
}

// lista separa "a,b" em ["a","b"] (vazio → lista vazia).
func lista(s string) []string {
	out := []string{}
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// sim lê um booleano da CLI: sim/s/true/1.
func sim(s string) bool {
	switch strings.ToLower(s) {
	case "sim", "s", "true", "1", "yes":
		return true
	}
	return false
}

// gruposProcesso são os comandos que têm subcomando.
var gruposProcesso = map[string]bool{"sprint": true, "item": true, "decisao": true, "regra": true}

// runProcess roda um comando do processo; ok=false quando o comando não é do processo.
func runProcess(cmd string, rest []string, stdin io.Reader) (m map[string]any, ok bool, err error) {
	nome := cmd
	if gruposProcesso[cmd] {
		if len(rest) == 0 {
			return nil, true, usage("%s precisa de um subcomando", cmd)
		}
		nome, rest = cmd+" "+rest[0], rest[1:]
	}
	def, ok := comandosProcesso[nome]
	if !ok {
		if gruposProcesso[cmd] {
			return nil, true, usage("subcomando desconhecido: %s", nome)
		}
		return nil, false, nil
	}
	flags, pos, err := parseArgs(rest, def.flags...)
	if err != nil {
		return nil, true, err
	}
	proj, c, base, err := projectClient()
	if err != nil {
		// Fora de um projeto, --se-projeto deixa o comando em silêncio (para hooks).
		if sim(flags["se-projeto"]) && asCLIError(err).exit == ExitNaoAchado {
			return map[string]any{saidaMarkdown: ""}, true, nil
		}
		return nil, true, err
	}
	m, err = def.run(processArgs{flags: flags, pos: pos, stdin: stdin, c: c, base: base, pasta: proj.Root})
	return m, true, err
}

// textoOuStdin devolve o texto dado ou, com "-", o corpo lido do stdin.
func textoOuStdin(a processArgs, texto string) (string, error) {
	if texto == "-" {
		return readBody(a.stdin)
	}
	return texto, nil
}

// verItem é "item ver <id>" (também serve para decisões).
func verItem(a processArgs) (map[string]any, error) {
	if len(a.pos) != 1 {
		return nil, usage("ver precisa de <id>")
	}
	return a.get("/itens/"+url.PathEscape(a.pos[0]), nil)
}

// pastaAbs manda o caminho absoluto da pasta dada (o daemon roda noutra pasta).
func pastaAbs(a processArgs, rota string) (map[string]any, error) {
	if len(a.pos) != 1 {
		return nil, usage("%s precisa de <pasta>", strings.TrimPrefix(rota, "/"))
	}
	dir, err := filepath.Abs(a.pos[0])
	if err != nil {
		return nil, err
	}
	return a.post(rota, map[string]any{"dir": dir})
}
