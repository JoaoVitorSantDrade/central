// Package cli implementa o binário central como cliente do daemon (e o próprio daemon).
// A CLI não tem regra de negócio: traduz argumentos para o contrato /v1.
package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/JoaoVitorSantDrade/central/internal/config"
	"github.com/JoaoVitorSantDrade/central/internal/daemon"
	"github.com/JoaoVitorSantDrade/central/internal/version"
)

const uso = `uso: central <comando>
  versao
  daemon
  doctor
  painel [--so-url sim]   (abre o painel no navegador)
  parar                   (encerra o daemon; o próximo comando sobe outro)
  instalar [--sem-plugin sim]      (pasta do usuário, PATH e plugin do Claude Code)
  desinstalar [--sem-plugin sim]   (os dados ficam)
  atualizar [--versao X.Y.Z]       (baixa a release, confere o SHA256 e se troca)
  init --id <id> [--nome <nome>]
  entrar --vaga <VAGA> --papel <PAPEL> [--area <area>]
  enviar <para> <tipo> [--ref <ref>] [--chave <chave>] [--responder 30m] <corpo | ->
  responder <seq> <corpo | ->
  pendentes
  caixa
  esperar [--ate 30m]
  status [--estado ativo|ocioso|trabalhando] <texto>
  sair
  agentes
  trava <nome> --motivo <motivo>
  soltar <nome>
  travas
  fato set <chave> <valor> --motivo <motivo>
  fato get <chave>
  fato historico <chave>
  fatos
  id <PREFIXO> [--minimo N]
  recurso pedir <nome> [--tipo T] [--dims k=v,k=v] [--exclusivo sim] [--prioridade P] [--min N] [--motivo M] [--chave K] [--esperar 60m]
  recurso soltar <nome>
  recurso ver <nome>
  rodar --rotulo R [--recurso N --tipo T --prioridade P --min M --dims k=v,k=v] -- <comando> [args...]
processo:
  item criar --titulo T [--id --tipo tarefa|missao --area --prioridade --categoria --sprint --recurso --arquivos a,b --folga sim --bloqueante sim --visivel sim --criterio C]
  item pronto|ver <id>      item listar [--estado E --tipo T --dono V --sprint S]
  proxima                   reservar|revisar|reabrir <id>
  concluir|aprovar <id> --ponta <hash>      integrar <id> --commit <hash> [--final sim]
  bloquear|rejeitar <id> <texto | ->        validar <id> ligada|sem_caminho|sem_efeito|quebrada
  sprint abrir --expediente HH:MM-HH:MM [--id S]      sprint ver|antecipar|encerrar
  decisao abrir --titulo T <corpo | ->      decisao chave <id> --acordo sim | --objecao <motivo>
  decisao revisar <id> <corpo | ->          decisao escalar|ver <id>      decisao listar
  decisao veto <id> <motivo>                decisao responder <id> --aprovar sim|nao [texto]
  saude    orfas [--liberar sim]    metricas [--sprint S]
  importar-locks <pasta>    exportar-locks <pasta>
contexto:
  brief [--curto] [--md] [--max-tokens N]
  regra listar [--papel P --estado E]    regra criar --id L-XXX --aplica A,B --pratica P [--texto T --origem O]
  regra estado <id> <estado> [--comando C]    regra importar <LICOES.md>
  mcp    (servidor MCP em stdio, na pasta do projeto)
extensões (do central.toml):
  <extensao> <acao> [--k v ...] [posicionais] [-]`

// Main roda um comando e devolve o código de saída. Sempre imprime uma linha JSON.
// rodar devolve o código do comando que rodou e manda a saída dele ao stderr;
// doctor devolve 1 se alguma verificação falhou.
func Main(args []string, stdin io.Reader, stdout io.Writer) int {
	var out map[string]any
	var err error
	code := ExitOK
	if len(args) > 0 && args[0] == "rodar" {
		out, code, err = runRodar(args[1:], os.Stderr)
	} else if len(args) > 0 && args[0] == "doctor" {
		out, code, err = runDoctor(args[1:])
	} else if len(args) > 0 && args[0] == "mcp" {
		// O servidor MCP fala o protocolo em stdout; não imprime a linha JSON da CLI.
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
		defer stop()
		if err := ServeMCP(ctx, stdin, stdout); err != nil && ctx.Err() == nil {
			fmt.Fprintln(os.Stderr, "central mcp:", err)
			return ExitErro
		}
		return ExitOK
	} else {
		out, err = run(args, stdin)
	}
	if err != nil {
		ce := asCLIError(err)
		emit(stdout, map[string]any{"ok": false, "erro": errBody(ce)})
		return ce.exit
	}
	// Um comando que devolve _md (brief --md) imprime o markdown cru, para o agente ler.
	if md, ok := out[saidaMarkdown].(string); ok {
		fmt.Fprint(stdout, md)
		return code
	}
	emit(stdout, out)
	return code
}

// saidaMarkdown é a chave que pede a saída em markdown em vez de JSON.
const saidaMarkdown = "_md"

func asCLIError(err error) *cliError {
	var ce *cliError
	if !errors.As(err, &ce) {
		ce = newErr(ExitErro, "interno", "%v", err)
	}
	return ce
}

func errBody(ce *cliError) map[string]string {
	return map[string]string{"codigo": ce.codigo, "mensagem": ce.mensagem}
}

func emit(w io.Writer, v any) {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	enc.Encode(v)
}

func usage(format string, args ...any) *cliError {
	return newErr(ExitRecusado, "invalido", "%s\n%s", fmt.Sprintf(format, args...), uso)
}

// parseArgs separa flags --nome valor (em qualquer posição) de posicionais. "--" encerra as flags.
func parseArgs(args []string, known ...string) (map[string]string, []string, error) {
	flags, pos := map[string]string{}, []string{}
	// Um nome terminado em "?" é booleano: --nome sozinho vale "sim" (--nome=nao desliga).
	isKnown, isBool := map[string]bool{}, map[string]bool{}
	for _, k := range known {
		if strings.HasSuffix(k, "?") {
			k = strings.TrimSuffix(k, "?")
			isBool[k] = true
		}
		isKnown[k] = true
	}
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			pos = append(pos, args[i+1:]...)
			break
		}
		if strings.HasPrefix(a, "--") {
			name, val, hasVal := strings.Cut(a[2:], "=")
			if !isKnown[name] {
				return nil, nil, usage("opção desconhecida: --%s", name)
			}
			if !hasVal && isBool[name] {
				val, hasVal = "sim", true
			}
			if !hasVal {
				if i+1 >= len(args) {
					return nil, nil, usage("--%s precisa de um valor", name)
				}
				i++
				val = args[i]
			}
			flags[name] = val
			continue
		}
		pos = append(pos, a)
	}
	return flags, pos, nil
}

func run(args []string, stdin io.Reader) (map[string]any, error) {
	if len(args) == 0 {
		return nil, usage("falta o comando")
	}
	cmd, rest := args[0], args[1:]
	switch cmd {
	case "versao":
		return map[string]any{"ok": true, "versao": version.Versao, "api": version.API}, nil
	case "daemon":
		return runDaemon()
	case "init":
		return runInit(rest)
	case "painel":
		return runPainel(rest)
	case "parar":
		return runParar(rest)
	case "instalar":
		return runInstalar(rest)
	case "desinstalar":
		return runDesinstalar(rest)
	case "atualizar":
		return runAtualizar(rest)
	case "entrar", "enviar", "responder", "pendentes", "caixa", "esperar", "status", "sair", "agentes", "trava", "soltar", "travas", "fatos", "id":
		return runAgent(cmd, rest, stdin)
	case "fato":
		if len(rest) == 0 {
			return nil, usage("fato precisa de set, get ou historico")
		}
		switch rest[0] {
		case "set", "get", "historico":
			return runAgent("fato "+rest[0], rest[1:], stdin)
		}
		return nil, usage("subcomando de fato desconhecido: %s", rest[0])
	case "recurso":
		if len(rest) == 0 {
			return nil, usage("recurso precisa de pedir, soltar ou ver")
		}
		switch rest[0] {
		case "pedir", "soltar", "ver":
			return runAgent("recurso "+rest[0], rest[1:], stdin)
		}
		return nil, usage("subcomando de recurso desconhecido: %s", rest[0])
	default:
		if m, ok, err := runProcess(cmd, rest, stdin); ok {
			return m, err
		}
		if m, ok, err := runExtensao(cmd, rest, stdin); ok {
			return m, err
		}
		return nil, usage("comando desconhecido: %s", cmd)
	}
}

func runDaemon() (map[string]any, error) {
	dir, err := config.DataDir()
	if err != nil {
		return nil, err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	err = daemon.Run(ctx, dir, config.Addr())
	if errors.Is(err, daemon.ErrAlreadyRunning) {
		return map[string]any{"ok": true, "ja_rodando": true}, nil
	}
	if err != nil {
		return nil, newErr(ExitIndisponivel, "daemon_indisponivel", "%v", err)
	}
	return map[string]any{"ok": true}, nil
}

func runInit(rest []string) (map[string]any, error) {
	flags, pos, err := parseArgs(rest, "id", "nome")
	if err != nil {
		return nil, err
	}
	if flags["id"] == "" || len(pos) > 0 {
		return nil, usage("init precisa de --id")
	}
	nome := flags["nome"]
	if nome == "" {
		nome = flags["id"]
	}
	cwd, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	p, err := config.InitProject(cwd, flags["id"], nome)
	switch {
	case errors.Is(err, config.ErrExists):
		return nil, newErr(ExitConflito, "conflito", "%v", err)
	case errors.Is(err, config.ErrInvalidID):
		return nil, newErr(ExitRecusado, "invalido", "%v", err)
	case err != nil:
		return nil, err
	}
	return map[string]any{"ok": true, "projeto": p}, nil
}

func runAgent(cmd string, rest []string, stdin io.Reader) (map[string]any, error) {
	known := map[string][]string{
		"entrar":         {"vaga", "papel", "area"},
		"enviar":         {"ref", "chave", "responder"},
		"responder":      {},
		"pendentes":      {},
		"caixa":          {},
		"esperar":        {"ate"},
		"status":         {"estado"},
		"sair":           {},
		"agentes":        {},
		"trava":          {"motivo"},
		"soltar":         {},
		"travas":         {},
		"fatos":          {},
		"id":             {"minimo"},
		"fato set":       {"motivo"},
		"fato get":       {},
		"fato historico": {},
		"recurso pedir":  {"tipo", "dims", "exclusivo", "prioridade", "min", "motivo", "chave", "esperar"},
		"recurso soltar": {},
		"recurso ver":    {},
	}[cmd]
	flags, pos, err := parseArgs(rest, known...)
	if err != nil {
		return nil, err
	}
	proj, c, base, err := projectClient()
	if err != nil {
		return nil, err
	}

	switch cmd {
	case "entrar":
		if flags["vaga"] == "" || flags["papel"] == "" || len(pos) > 0 {
			return nil, usage("entrar precisa de --vaga e --papel")
		}
		// O daemon valida e guarda a configuração; inválida, o entrar falha e a anterior continua.
		texto, err := os.ReadFile(filepath.Join(proj.Root, config.FileName))
		if err != nil {
			return nil, err
		}
		return callWithDaemon(c, "POST", base+"/agentes", map[string]string{
			"nome": proj.Name, "raiz": proj.Root, "pasta": proj.Root, "vaga": flags["vaga"], "papel": flags["papel"],
			"area": flags["area"], "config": string(texto)})
	case "enviar":
		if len(pos) != 3 {
			return nil, usage("enviar precisa de <para> <tipo> <corpo | ->")
		}
		corpo := pos[2]
		if corpo == "-" {
			if corpo, err = readBody(stdin); err != nil {
				return nil, err
			}
		}
		return hintEntrar(callWithDaemon(c, "POST", base+"/mensagens", map[string]string{
			"pasta": proj.Root, "para": pos[0], "tipo": pos[1], "ref": flags["ref"], "corpo": corpo, "chave": flags["chave"],
			"responder": flags["responder"]}))
	case "responder":
		if len(pos) != 2 {
			return nil, usage("responder precisa de <seq> <corpo | ->")
		}
		seq, err := strconv.ParseInt(pos[0], 10, 64)
		if err != nil || seq <= 0 {
			return nil, usage("seq inválido: %q", pos[0])
		}
		corpo := pos[1]
		if corpo == "-" {
			if corpo, err = readBody(stdin); err != nil {
				return nil, err
			}
		}
		return hintEntrar(callWithDaemon(c, "POST", base+"/responder", map[string]any{
			"pasta": proj.Root, "seq": seq, "corpo": corpo}))
	case "pendentes":
		if len(pos) > 0 {
			return nil, usage("pendentes não recebe argumentos")
		}
		return hintEntrar(callWithDaemon(c, "GET", base+"/pendentes?"+url.Values{"pasta": {proj.Root}}.Encode(), nil))
	case "caixa":
		if len(pos) > 0 {
			return nil, usage("caixa não recebe argumentos")
		}
		return hintEntrar(callWithDaemon(c, "POST", base+"/caixa", map[string]string{"pasta": proj.Root}))
	case "status":
		if len(pos) != 1 {
			return nil, usage("status precisa de <texto>")
		}
		return hintEntrar(callWithDaemon(c, "POST", base+"/status", map[string]string{
			"pasta": proj.Root, "estado": flags["estado"], "status": pos[0]}))
	case "sair":
		if len(pos) > 0 {
			return nil, usage("sair não recebe argumentos")
		}
		return hintEntrar(callWithDaemon(c, "POST", base+"/sair", map[string]string{"pasta": proj.Root}))
	case "agentes":
		if len(pos) > 0 {
			return nil, usage("agentes não recebe argumentos")
		}
		return hintEntrar(callWithDaemon(c, "GET", base+"/agentes?"+url.Values{"pasta": {proj.Root}}.Encode(), nil))
	case "trava":
		if len(pos) != 1 || flags["motivo"] == "" {
			return nil, usage("trava precisa de <nome> e --motivo")
		}
		return hintEntrar(callWithDaemon(c, "POST", base+"/travas", map[string]string{
			"pasta": proj.Root, "nome": pos[0], "motivo": flags["motivo"]}))
	case "soltar":
		if len(pos) != 1 {
			return nil, usage("soltar precisa de <nome>")
		}
		m, err := callWithDaemon(c, "DELETE", base+"/travas/"+url.PathEscape(pos[0])+"?"+
			url.Values{"pasta": {proj.Root}}.Encode(), nil)
		var ce *cliError
		if errors.As(err, &ce) && strings.HasPrefix(ce.mensagem, "trava ") {
			return m, err // a trava não existe; entrar não resolve
		}
		return hintEntrar(m, err)
	case "travas":
		if len(pos) > 0 {
			return nil, usage("travas não recebe argumentos")
		}
		return hintEntrar(callWithDaemon(c, "GET", base+"/travas?"+url.Values{"pasta": {proj.Root}}.Encode(), nil))
	case "fato set":
		if len(pos) != 2 || flags["motivo"] == "" {
			return nil, usage("fato set precisa de <chave> <valor> e --motivo")
		}
		return hintFato(callWithDaemon(c, "POST", base+"/fatos/"+url.PathEscape(pos[0]), map[string]any{
			"pasta": proj.Root, "valor": jsonValue(pos[1]), "motivo": flags["motivo"]}))
	case "fato get":
		if len(pos) != 1 {
			return nil, usage("fato get precisa de <chave>")
		}
		return hintFato(callWithDaemon(c, "GET", base+"/fatos/"+url.PathEscape(pos[0])+"?"+
			url.Values{"pasta": {proj.Root}}.Encode(), nil))
	case "fato historico":
		if len(pos) != 1 {
			return nil, usage("fato historico precisa de <chave>")
		}
		return hintFato(callWithDaemon(c, "GET", base+"/fatos/"+url.PathEscape(pos[0])+"/historico?"+
			url.Values{"pasta": {proj.Root}}.Encode(), nil))
	case "fatos":
		if len(pos) > 0 {
			return nil, usage("fatos não recebe argumentos")
		}
		return hintFato(callWithDaemon(c, "GET", base+"/fatos?"+url.Values{"pasta": {proj.Root}}.Encode(), nil))
	case "id":
		if len(pos) != 1 {
			return nil, usage("id precisa de <PREFIXO>")
		}
		minimo := 1
		if s := flags["minimo"]; s != "" {
			n, err := strconv.Atoi(s)
			if err != nil || n < 1 {
				return nil, usage("--minimo inválido: %q", s)
			}
			minimo = n
		}
		return hintEntrar(callWithDaemon(c, "POST", base+"/ids/"+url.PathEscape(pos[0]), map[string]any{
			"pasta": proj.Root, "minimo": minimo}))
	case "recurso pedir":
		if len(pos) != 1 {
			return nil, usage("recurso pedir precisa de <nome>")
		}
		return hintEntrar(requestGrant(c, base, proj.Root, pos[0], flags))
	case "recurso soltar":
		if len(pos) != 1 {
			return nil, usage("recurso soltar precisa de <nome>")
		}
		m, err := callWithDaemon(c, "DELETE", base+"/recursos/"+url.PathEscape(pos[0])+"/concessao?"+
			url.Values{"pasta": {proj.Root}}.Encode(), nil)
		var ce *cliError
		if errors.As(err, &ce) && strings.Contains(ce.mensagem, "não tem pedido") {
			return m, err // nada a soltar; entrar não resolve
		}
		return hintEntrar(m, err)
	case "recurso ver":
		if len(pos) != 1 {
			return nil, usage("recurso ver precisa de <nome>")
		}
		return hintEntrar(callWithDaemon(c, "GET", base+"/recursos/"+url.PathEscape(pos[0])+"?"+
			url.Values{"pasta": {proj.Root}}.Encode(), nil))
	default: // esperar
		ate := 30 * time.Minute
		if s := flags["ate"]; s != "" {
			d, err := time.ParseDuration(s)
			if err != nil || d <= 0 {
				return nil, usage("--ate inválido: %q (ex.: 30m)", s)
			}
			ate = d
		}
		if len(pos) > 0 {
			return nil, usage("esperar não recebe argumentos")
		}
		return hintEntrar(wait(c, base, proj.Root, ate))
	}
}

// projectClient acha o projeto da pasta atual e devolve o cliente do daemon e
// a base das rotas do projeto.
func projectClient() (config.Project, *client, string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return config.Project{}, nil, "", err
	}
	proj, err := config.FindProject(cwd)
	if errors.Is(err, config.ErrNoProject) {
		return config.Project{}, nil, "", newErr(ExitNaoAchado, "sem_projeto", "%v (rode central init na raiz do projeto)", err)
	}
	if err != nil {
		return config.Project{}, nil, "", newErr(ExitRecusado, "invalido", "%v", err)
	}
	return proj, newClient(config.Addr()), "/v1/projetos/" + url.PathEscape(proj.ID), nil
}

// hintEntrar acrescenta a dica da CLI quando o projeto ou a pasta não estão
// registrados: os dois casos se resolvem com central entrar.
func hintEntrar(m map[string]any, err error) (map[string]any, error) {
	var ce *cliError
	if errors.As(err, &ce) && ce.exit == ExitNaoAchado {
		ce.mensagem += " (rode central entrar)"
	}
	return m, err
}

func callWithDaemon(c *client, method, path string, body any) (map[string]any, error) {
	if err := c.ensureDaemon(); err != nil {
		return nil, err
	}
	m, err := c.call(method, path, body)
	if errors.Is(err, errNetwork) {
		return nil, newErr(ExitIndisponivel, "daemon_indisponivel", "%v", err)
	}
	return m, err
}

// maxFalhasAutostart é quantas vezes seguidas esperar tenta subir o daemon antes de desistir.
const maxFalhasAutostart = 3

// wait faz o long-poll até o prazo, refazendo a conexão (e o daemon) se ela cair.
func wait(c *client, base, pasta string, ate time.Duration) (map[string]any, error) {
	start := time.Now()
	deadline := start.Add(ate)
	falhas := 0
	for {
		remaining := time.Until(deadline)
		// Abaixo de 1 ms o prazo arredondado vira 0s, que o daemon recusa.
		if remaining < time.Millisecond {
			return map[string]any{"ok": true, "motivo": "tempo", "novas": 0, "sugestao": "", "esperou_ms": time.Since(start).Milliseconds()}, nil
		}
		if remaining > 60*time.Minute {
			remaining = 60 * time.Minute
		}
		if err := c.ensureDaemon(); err != nil {
			falhas++
			if falhas >= maxFalhasAutostart || time.Until(deadline) <= 0 {
				return nil, err
			}
			time.Sleep(200 * time.Millisecond)
			continue
		}
		falhas = 0
		q := url.Values{"pasta": {pasta}, "ate": {remaining.Round(time.Millisecond).String()}}
		m, err := c.call("GET", base+"/esperar?"+q.Encode(), nil)
		if errors.Is(err, errNetwork) {
			time.Sleep(200 * time.Millisecond)
			continue
		}
		if err != nil {
			return nil, err
		}
		if m["motivo"] == "tempo" && time.Until(deadline) > 0 {
			continue // o daemon limitou o prazo; ainda há tempo
		}
		m["esperou_ms"] = time.Since(start).Milliseconds()
		return m, nil
	}
}

// parseDims lê --dims k=v,k=v (Decisão 4: dimensões genéricas).
func parseDims(s string) (map[string]float64, error) {
	dims := map[string]float64{}
	if s == "" {
		return dims, nil
	}
	for _, par := range strings.Split(s, ",") {
		k, v, ok := strings.Cut(strings.TrimSpace(par), "=")
		f, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
		if !ok || strings.TrimSpace(k) == "" || err != nil {
			return nil, usage("--dims inválido: %q (ex.: vram_mb=4500,uso_pct=80)", par)
		}
		dims[strings.TrimSpace(k)] = f
	}
	return dims, nil
}

// requestGrant faz o pedido e, com --esperar, o long-poll até a concessão
// ficar ativa. Ainda na fila no prazo: saída 2 com ainda_na_fila (o pedido
// continua na fila; repita com a mesma --chave para voltar a esperar).
func requestGrant(c *client, base, pasta, nome string, flags map[string]string) (map[string]any, error) {
	body, ate, err := grantBody(pasta, flags)
	if err != nil {
		return nil, err
	}
	start := time.Now()
	m, id, err := postGrant(c, base, nome, body)
	if err != nil {
		return m, err
	}
	g, _ := m["concessao"].(map[string]any)
	if ate == 0 || g["estado"] != "fila" {
		return m, nil
	}
	return awaitGrant(c, base, pasta, nome, id, start, ate)
}

// grantBody valida as opções de recurso pedir e monta o corpo do pedido e o
// prazo de --esperar (0 sem espera).
func grantBody(pasta string, flags map[string]string) (map[string]any, time.Duration, error) {
	dims, err := parseDims(flags["dims"])
	if err != nil {
		return nil, 0, err
	}
	exclusivo := false
	switch flags["exclusivo"] {
	case "", "nao", "não":
	case "sim":
		exclusivo = true
	default:
		return nil, 0, usage("--exclusivo aceita sim ou nao")
	}
	var minutos int64
	if s := flags["min"]; s != "" {
		if minutos, err = strconv.ParseInt(s, 10, 64); err != nil || minutos <= 0 {
			return nil, 0, usage("--min inválido: %q (minutos, ex.: 25)", s)
		}
	}
	var ate time.Duration
	if s := flags["esperar"]; s != "" {
		if ate, err = time.ParseDuration(s); err != nil || ate <= 0 {
			return nil, 0, usage("--esperar inválido: %q (ex.: 60m)", s)
		}
	}
	return map[string]any{
		"pasta": pasta, "tipo": flags["tipo"], "prioridade": flags["prioridade"], "motivo": flags["motivo"],
		"chave": flags["chave"], "dims": dims, "exclusivo": exclusivo, "min": minutos}, ate, nil
}

// postGrant grava o pedido e devolve a resposta e o id dele. A mesma --chave
// de um pedido já encerrado dá saída 2 com encerrada.
func postGrant(c *client, base, nome string, body map[string]any) (map[string]any, string, error) {
	m, err := callWithDaemon(c, "POST", base+"/recursos/"+url.PathEscape(nome)+"/pedidos", body)
	if err != nil {
		return m, "", err
	}
	g, _ := m["concessao"].(map[string]any)
	id := fmt.Sprint(g["id"])
	if g["estado"] == "encerrada" {
		return nil, id, newErr(ExitConflito, "encerrada", "o pedido %s em %s (desta --chave) já foi encerrado; use outra chave para pedir de novo", id, nome)
	}
	return m, id, nil
}

// awaitGrant espera o pedido id na fila até start+ate. Ainda na fila: saída 2
// com ainda_na_fila; encerrado antes de entrar: saída 2 com encerrada.
func awaitGrant(c *client, base, pasta, nome, id string, start time.Time, ate time.Duration) (map[string]any, error) {
	m, err := waitGrant(c, base+"/recursos/"+url.PathEscape(nome)+"/pedidos/"+id+"/esperar", pasta, start.Add(ate))
	if err != nil {
		return nil, err
	}
	switch m["motivo"] {
	case "concedida":
		return map[string]any{"ok": true, "concessao": m["concessao"], "esperou_ms": time.Since(start).Milliseconds()}, nil
	case "encerrada":
		return nil, newErr(ExitConflito, "encerrada", "o pedido %s em %s foi encerrado antes de entrar", id, nome)
	}
	return nil, newErr(ExitConflito, "ainda_na_fila",
		"o pedido %s continua na fila de %s depois de %v; repita com a mesma --chave para esperar de novo, ou solte com central recurso soltar %s",
		id, nome, ate, nome)
}

// waitGrant faz o long-poll do pedido até deadline, refazendo a conexão (e o
// daemon) se ela cair. Devolve a última resposta (motivo tempo no prazo).
func waitGrant(c *client, path, pasta string, deadline time.Time) (map[string]any, error) {
	falhas := 0
	for {
		remaining := time.Until(deadline)
		if remaining < time.Millisecond {
			return map[string]any{"ok": true, "motivo": "tempo"}, nil
		}
		if remaining > 60*time.Minute {
			remaining = 60 * time.Minute
		}
		if err := c.ensureDaemon(); err != nil {
			falhas++
			if falhas >= maxFalhasAutostart {
				return nil, err
			}
			time.Sleep(200 * time.Millisecond)
			continue
		}
		falhas = 0
		q := url.Values{"pasta": {pasta}, "ate": {remaining.Round(time.Millisecond).String()}}
		m, err := c.call("GET", path+"?"+q.Encode(), nil)
		if errors.Is(err, errNetwork) {
			time.Sleep(200 * time.Millisecond)
			continue
		}
		if err != nil {
			return nil, err
		}
		if m["motivo"] == "tempo" && time.Until(deadline) > 0 {
			continue // o daemon limitou o prazo; ainda há tempo
		}
		return m, nil
	}
}

// readBody lê o corpo do stdin: tira o BOM (o PowerShell 5.1 o acrescenta ao canalizar)
// e as quebras de linha finais, e recusa bytes que não são UTF-8.
func readBody(r io.Reader) (string, error) {
	b, err := io.ReadAll(r)
	if err != nil {
		return "", err
	}
	b = bytes.TrimPrefix(b, []byte("\xef\xbb\xbf"))
	if !utf8.Valid(b) {
		return "", newErr(ExitRecusado, "invalido",
			"o corpo do stdin não é UTF-8; no PowerShell 5.1 rode antes: $OutputEncoding = [Text.UTF8Encoding]::new($false)")
	}
	return strings.TrimRight(string(b), "\r\n"), nil
}

// jsonValue devolve s como JSON: o próprio texto se já for JSON válido, senão uma string JSON.
func jsonValue(s string) json.RawMessage {
	if json.Valid([]byte(s)) {
		return json.RawMessage(s)
	}
	b, _ := json.Marshal(s)
	return b
}

// hintFato é o hintEntrar dos fatos: a chave inexistente ("fato ...") não se resolve com entrar.
func hintFato(m map[string]any, err error) (map[string]any, error) {
	var ce *cliError
	if errors.As(err, &ce) && strings.HasPrefix(ce.mensagem, "fato ") {
		return m, err
	}
	return hintEntrar(m, err)
}
