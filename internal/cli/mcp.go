package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/JoaoVitorSantDrade/central/internal/version"
)

// EsperarMaxMCP é o teto do esperar pelo MCP: o cliente pode ter timeout de requisição.
const EsperarMaxMCP = 5 * time.Minute

// param é um argumento de uma ferramenta MCP: vira um posicional (flag
// vazia) ou uma flag da CLI.
type param struct {
	nome, tipo, desc string
	obrigatorio      bool
	flag             string
}

// ferramenta é uma operação de agente exposta pelo MCP com o mesmo nome da CLI.
type ferramenta struct {
	nome, desc string
	cmd        []string
	params     []param
}

func p(nome, desc string) param {
	return param{nome: nome, tipo: "string", desc: desc, obrigatorio: true}
}
func opt(nome, flag, desc string) param {
	return param{nome: nome, tipo: "string", desc: desc, flag: flag}
}
func req(nome, flag, desc string) param {
	return param{nome: nome, tipo: "string", desc: desc, flag: flag, obrigatorio: true}
}
func optBool(nome, flag, desc string) param {
	return param{nome: nome, tipo: "boolean", desc: desc, flag: flag}
}

var idItem = p("id", "id do item (ex.: T-012)")

// ferramentasMCP são as operações de agente expostas (spec §10).
var ferramentasMCP = []ferramenta{
	{"entrar", "Liga esta sessão a uma vaga do projeto (uma vez por sessão).", []string{"entrar"},
		[]param{req("vaga", "vaga", "vaga (ex.: FIXER1)"), req("papel", "papel", "papel no projeto (ex.: FIXER)"), opt("area", "area", "área preferida")}},
	{"brief", "O contexto da vaga em markdown: quem você é, agora, pendências, papel, núcleo, regras, fatos e mapa de leitura.",
		[]string{"brief", "--md"}, []param{optBool("curto", "curto", "só os blocos 1–3 (depois de um resumo automático)")}},
	{"caixa", "Lê as mensagens novas (e marca como lidas).", []string{"caixa"}, nil},
	{"enviar", "Envia uma mensagem a uma vaga, papel ou TODOS.", []string{"enviar"},
		[]param{p("para", "vaga, papel ou TODOS"), p("tipo", "tipo (duvida, alerta, nota…)"), p("corpo", "texto"),
			opt("ref", "ref", "item ou assunto (ex.: T-012)"), opt("responder", "responder", "prazo de resposta (ex.: 30m)")}},
	{"responder", "Responde a uma mensagem pelo seq.", []string{"responder"}, []param{p("seq", "seq da mensagem"), p("corpo", "texto")}},
	{"pendentes", "As respostas que você deve e as que espera.", []string{"pendentes"}, nil},
	{"esperar", "Bloqueia até chegar algo para você (no máximo 5 min por chamada).", []string{"esperar"},
		[]param{opt("ate", "ate", "prazo (ex.: 5m; máximo 5m)")}},
	{"status", "Diz o que você está fazendo.", []string{"status"},
		[]param{p("texto", "o que está fazendo"), opt("estado", "estado", "ativo, ocioso ou trabalhando")}},
	{"agentes", "As vagas do projeto e o estado de cada uma.", []string{"agentes"}, nil},
	{"proxima", "A melhor tarefa pronta para você e o porquê.", []string{"proxima"}, nil},
	{"reservar", "Reserva a tarefa (ou retoma uma rejeitada).", []string{"reservar"}, []param{idItem}},
	{"concluir", "Conclui a tarefa na ponta dada (acorda o REVISOR).", []string{"concluir"},
		[]param{idItem, req("ponta", "ponta", "hash da ponta da branch")}},
	{"bloquear", "Devolve a tarefa à fila com uma nota.", []string{"bloquear"}, []param{idItem, p("nota", "o que aconteceu")}},
	{"revisar", "Começa a revisão.", []string{"revisar"}, []param{idItem}},
	{"aprovar", "Aprova a ponta revisada (acorda o INTEGRADOR).", []string{"aprovar"},
		[]param{idItem, req("ponta", "ponta", "hash revisado")}},
	{"rejeitar", "Rejeita com motivo (acorda o dono).", []string{"rejeitar"}, []param{idItem, p("motivo", "o que falta")}},
	{"integrar", "Registra a integração.", []string{"integrar"},
		[]param{idItem, req("commit", "commit", "hash do merge"), optBool("final", "final", "último marco da missão")}},
	{"validar", "Registra a validação na interface.", []string{"validar"},
		[]param{idItem, p("resultado", "ligada, sem_caminho, sem_efeito ou quebrada")}},
	{"reabrir", "Reabre a tarefa (anula a revisão).", []string{"reabrir"}, []param{idItem}},
	{"item_ver", "Mostra um item e as notas.", []string{"item", "ver"}, []param{idItem}},
	{"item_listar", "Lista itens.", []string{"item", "listar"},
		[]param{opt("estado", "estado", "estados separados por vírgula"), opt("dono", "dono", "vaga dona")}},
	{"item_criar", "Propõe um item novo.", []string{"item", "criar"},
		[]param{req("titulo", "titulo", "título"), opt("area", "area", "área"), opt("prioridade", "prioridade", "P0..P3"),
			opt("categoria", "categoria", "bug, melhoria…"), opt("criterio", "criterio", "critério de pronto"),
			opt("arquivos", "arquivos", "arquivos separados por vírgula")}},
	{"trava", "Toma uma trava (ex.: um arquivo compartilhado).", []string{"trava"},
		[]param{p("nome", "nome da trava"), req("motivo", "motivo", "por quê")}},
	{"soltar", "Solta uma trava sua.", []string{"soltar"}, []param{p("nome", "nome da trava")}},
	{"recurso_pedir", "Pede um recurso (ex.: a GPU).", []string{"recurso", "pedir"},
		[]param{p("nome", "recurso"), opt("tipo", "tipo", "tipo do trabalho"), opt("dims", "dims", "k=v,k=v"),
			optBool("exclusivo", "exclusivo", "precisa do recurso inteiro"), opt("prioridade", "prioridade", "prioridade"),
			opt("min", "min", "minutos"), opt("motivo", "motivo", "por quê"), opt("esperar", "esperar", "esperar a vez (ex.: 5m)")}},
	{"recurso_soltar", "Solta o seu pedido ou concessão no recurso.", []string{"recurso", "soltar"}, []param{p("nome", "recurso")}},
	{"fato_get", "Lê um fato do projeto.", []string{"fato", "get"}, []param{p("chave", "chave")}},
	{"fato_set", "Grava um fato do projeto.", []string{"fato", "set"},
		[]param{p("chave", "chave"), p("valor", "valor (JSON ou texto)"), req("motivo", "motivo", "por quê")}},
	{"saude", "O que precisa de atenção no projeto agora.", []string{"saude"}, nil},
}

func (f ferramenta) schema() map[string]any {
	props := map[string]any{}
	reqs := []string{}
	for _, pr := range f.params {
		props[pr.nome] = map[string]any{"type": pr.tipo, "description": pr.desc}
		if pr.obrigatorio {
			reqs = append(reqs, pr.nome)
		}
	}
	return map[string]any{"type": "object", "properties": props, "required": reqs}
}

// argsMCP monta a linha de comando da ferramenta a partir dos argumentos.
func (f ferramenta) argsMCP(raw json.RawMessage) ([]string, error) {
	in := map[string]any{}
	if len(raw) > 0 && string(raw) != "null" {
		if err := json.Unmarshal(raw, &in); err != nil {
			return nil, usage("argumentos não são um objeto JSON: %v", err)
		}
	}
	args := append([]string{}, f.cmd...)
	var flags []string
	for _, pr := range f.params {
		v, ok := in[pr.nome]
		if !ok || v == nil || v == "" {
			if pr.obrigatorio {
				return nil, usage("%s precisa de %s", f.nome, pr.nome)
			}
			continue
		}
		var s string
		switch x := v.(type) {
		case string:
			s = x
		case bool:
			if !x {
				continue
			}
			s = "sim"
		case float64:
			s = fmt.Sprintf("%v", x)
		default:
			return nil, usage("%s: %s precisa ser %s", f.nome, pr.nome, pr.tipo)
		}
		if pr.tipo == "boolean" {
			if _, isBool := v.(bool); !isBool {
				return nil, usage("%s: %s precisa ser boolean", f.nome, pr.nome)
			}
		}
		if f.nome == "esperar" && pr.nome == "ate" {
			d, err := time.ParseDuration(s)
			if err != nil || d <= 0 || d > EsperarMaxMCP {
				return nil, usage("esperar pelo MCP vai até %v (recebi %q)", EsperarMaxMCP, s)
			}
		}
		if pr.flag == "" {
			args = append(args, s)
		} else {
			flags = append(flags, "--"+pr.flag+"="+s)
		}
	}
	if f.nome == "esperar" {
		if _, ok := in["ate"]; !ok {
			flags = append(flags, "--ate="+EsperarMaxMCP.String())
		}
	}
	return append(args, flags...), nil
}

type nopWriteCloser struct{ io.Writer }

func (nopWriteCloser) Close() error { return nil }

// ServeMCP serve as ferramentas pelo protocolo MCP em stdio, na pasta atual
// (o projeto). Cada ferramenta roda o mesmo comando da CLI.
func ServeMCP(ctx context.Context, in io.Reader, out io.Writer) error {
	srv := mcp.NewServer(&mcp.Implementation{Name: "central", Version: version.Versao}, nil)
	for _, f := range ferramentasMCP {
		f := f
		srv.AddTool(&mcp.Tool{Name: f.nome, Description: f.desc, InputSchema: f.schema()},
			func(ctx context.Context, r *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				return f.chamar(r.Params.Arguments), nil
			})
	}
	ferramentaExtensao(srv)
	return srv.Run(ctx, &mcp.IOTransport{Reader: io.NopCloser(in), Writer: nopWriteCloser{out}})
}

func (f ferramenta) chamar(raw json.RawMessage) *mcp.CallToolResult {
	args, err := f.argsMCP(raw)
	if err != nil {
		return resultadoMCP(nil, err)
	}
	return resultadoMCP(run(args, strings.NewReader("")))
}

// resultadoMCP converte a saída de um comando no resultado da ferramenta.
func resultadoMCP(m map[string]any, err error) *mcp.CallToolResult {
	if err != nil {
		ce := asCLIError(err)
		b, _ := json.Marshal(map[string]any{"ok": false, "erro": errBody(ce)})
		return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: string(b)}}}
	}
	if md, ok := m[saidaMarkdown].(string); ok {
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: md}}}
	}
	b, _ := json.Marshal(m)
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: string(b)}}}
}
