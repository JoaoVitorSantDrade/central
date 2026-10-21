// Command eco é a extensão ext/v1 de exemplo da Central: lê o pedido do
// stdin e responde com os args em dados. Os testes do contrato a usam.
//
// Args que mudam o comportamento:
//   - evento: emite um evento com esse tipo (ref = args.ref).
//   - pedir: responde com um formulário pedindo o campo "nome" (até ele vir).
//   - dormir: dorme essa duração antes de responder (ex.: "5s").
//   - inundar: escreve esse número de bytes no stdout antes da resposta.
//   - falhar: responde ok=false com essa mensagem.
//   - quebrar: sai sem responder (código 3), com essa mensagem no stderr.
//   - linhas: imprime linhas de log antes da resposta.
//   - fato: pede para gravar o fato com essa chave (valor {"eco":true}).
//   - arquivos (vem da pasta de entrada): sem "nome", pede o nome por
//     formulário; com "nome", emite o evento "arquivos" com ref = nome.
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

type pedido struct {
	Versao  string         `json:"versao"`
	Acao    string         `json:"acao"`
	Args    map[string]any `json:"args"`
	Projeto map[string]any `json:"projeto"`
	Agente  map[string]any `json:"agente"`
}

func texto(v any) string {
	if v == nil {
		return ""
	}
	return fmt.Sprint(v)
}

func main() {
	linha, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil && linha == "" {
		fmt.Fprintln(os.Stderr, "eco: sem pedido no stdin")
		os.Exit(2)
	}
	var p pedido
	if err := json.Unmarshal([]byte(linha), &p); err != nil {
		fmt.Fprintln(os.Stderr, "eco: pedido inválido:", err)
		os.Exit(2)
	}
	a := p.Args
	if msg := texto(a["quebrar"]); msg != "" {
		fmt.Fprintln(os.Stderr, msg)
		os.Exit(3)
	}
	if d, err := time.ParseDuration(texto(a["dormir"])); err == nil {
		time.Sleep(d)
	}
	if n, err := strconv.Atoi(texto(a["inundar"])); err == nil {
		os.Stdout.WriteString(strings.Repeat("x", n) + "\n")
	}
	if texto(a["linhas"]) != "" {
		fmt.Println("log: começando")
		fmt.Println("log: pronto")
	}
	out := map[string]any{"ok": true, "dados": map[string]any{
		"acao": p.Acao, "args": a, "projeto": p.Projeto, "agente": p.Agente, "versao": p.Versao,
		"pasta": func() string { d, _ := os.Getwd(); return d }(), "ambiente": os.Getenv("CENTRAL_EXT"),
		"python_utf8": os.Getenv("PYTHONUTF8"),
	}}
	if msg := texto(a["falhar"]); msg != "" {
		out = map[string]any{"ok": false, "erro": map[string]any{"mensagem": msg}}
	}
	if tipo := texto(a["evento"]); tipo != "" {
		out["eventos"] = []map[string]any{{"tipo": tipo, "ref": texto(a["ref"]), "dados": map[string]any{"resumo": "eco emitiu " + tipo}}}
	}
	if chave := texto(a["fato"]); chave != "" {
		out["fatos"] = []map[string]any{{"chave": chave, "valor": map[string]any{"eco": true}, "motivo": "eco gravou"}}
	}
	if _, ok := a["arquivos"]; ok && texto(a["nome"]) != "" {
		out["eventos"] = []map[string]any{{"tipo": "arquivos", "ref": texto(a["nome"]), "dados": map[string]any{"arquivos": a["arquivos"]}}}
	}
	if _, ok := a["arquivos"]; (ok || texto(a["pedir"]) != "") && texto(a["nome"]) == "" {
		out["formulario"] = map[string]any{"titulo": "Como se chama?", "campos": []map[string]any{
			{"nome": "nome", "rotulo": "Nome", "tipo": "texto", "obrigatorio": true}}}
	}
	b, _ := json.Marshal(out)
	os.Stdout.Write(append(b, '\n'))
}
