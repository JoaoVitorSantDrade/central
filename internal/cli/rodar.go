package cli

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const (
	// rodarBatimento é de quanto em quanto tempo rodar renova o batimento da execução.
	rodarBatimento = 60 * time.Second
	// rodarEsperaRecurso é quanto rodar espera a concessão antes de desistir (saída 2).
	rodarEsperaRecurso = "60m"
	// rodarLinhas é quantas linhas do fim da saída do comando a execução guarda.
	rodarLinhas = 50
	// rodarLinhaMax limita uma linha guardada, para uma saída sem quebras não crescer sem fim.
	rodarLinhaMax = 64 << 10
	// killTimeout é quanto o taskkill pode demorar antes de cair no Process.Kill.
	killTimeout = 2 * time.Second
	// interromperEspera é quanto, fora do Windows, o filho tem para sair depois
	// do Ctrl+C antes de ser morto.
	interromperEspera = 10 * time.Second
)

// runRodar executa `central rodar` (spec §5.4): pede o recurso (se houver),
// registra a execução, roda o comando com a saída no stderr, renova o
// batimento e, no fim (mesmo com erro ou Ctrl+C), encerra a execução e solta o
// recurso. Devolve a linha JSON e o código de saída (o do comando). err só
// antes de o comando rodar.
func runRodar(args []string, stderr io.Writer) (map[string]any, int, error) {
	sep := slices.Index(args, "--")
	if sep < 0 || sep == len(args)-1 {
		return nil, 0, usage("rodar precisa de -- <comando> [args...]")
	}
	flags, pos, err := parseArgs(args[:sep], "rotulo", "recurso", "tipo", "prioridade", "min", "dims")
	if err != nil {
		return nil, 0, err
	}
	if flags["rotulo"] == "" || len(pos) > 0 {
		return nil, 0, usage("rodar precisa de --rotulo e do comando depois de --")
	}
	if flags["recurso"] == "" && (flags["tipo"] != "" || flags["prioridade"] != "" || flags["min"] != "" || flags["dims"] != "") {
		return nil, 0, usage("--tipo, --prioridade, --min e --dims só valem com --recurso")
	}
	argv := args[sep+1:]
	proj, c, base, err := projectClient()
	if err != nil {
		return nil, 0, err
	}
	pasta, recurso := proj.Root, flags["recurso"]
	// SIGTERM: o Windows o manda ao fechar o console.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	var concessao int64
	if recurso != "" {
		if concessao, err = rodarPedir(ctx, c, base, pasta, recurso, flags); err != nil {
			return nil, 0, err
		}
	}
	soltar := func() {
		if concessao > 0 {
			// Só a concessão deste rodar; ela pode já ter sido liberada pela Central.
			releaseGrantByID(c, base, pasta, recurso, concessao)
		}
	}
	m, err := callWithDaemon(c, "POST", base+"/execucoes", map[string]any{
		"pasta": pasta, "rotulo": flags["rotulo"], "comando": commandLine(argv), "concessao": concessao})
	if err != nil {
		soltar()
		_, err = hintEntrar(m, err)
		return nil, 0, err
	}
	exe, _ := m["execucao"].(map[string]any)
	eid := fmt.Sprint(exe["id"])
	execPath := base + "/execucoes/" + url.PathEscape(eid)

	log := &tailWriter{w: stderr, max: rodarLinhas}
	codigo, runErr := runChild(ctx, argv, log, func() {
		c.call("POST", execPath+"/batimento", map[string]string{"pasta": pasta})
	})
	finalLog := log.String()
	if runErr != nil {
		finalLog = strings.TrimSpace(finalLog + "\n" + "central rodar: " + runErr.Error())
	}
	fim, fimErr := callWithDaemon(c, "POST", execPath+"/fim", map[string]any{"pasta": pasta, "codigo": codigo, "final_log": finalLog})
	soltar()

	exit := codigo
	if exit < 0 {
		exit = ExitErro // sem código do comando (não rodou ou foi morto por sinal)
	}
	out := map[string]any{"ok": runErr == nil && fimErr == nil, "execucao": exe, "codigo": codigo}
	switch {
	case fimErr != nil:
		out["erro"] = errBody(asCLIError(fimErr))
		if exit == ExitOK {
			exit = ExitErro
		}
	case runErr != nil:
		out["execucao"] = fim["execucao"]
		out["erro"] = errBody(newErr(ExitErro, "comando", "%v", runErr))
	default:
		out["execucao"] = fim["execucao"]
	}
	return out, exit, nil
}

// rodarPedir pede o recurso e espera a concessão até rodarEsperaRecurso. O
// pedido é gravado fora da parte cancelável; depois dele, todo caminho de erro
// (ainda na fila no prazo, falha na espera ou Ctrl+C) solta o pedido e nada roda.
func rodarPedir(ctx context.Context, c *client, base, pasta, recurso string, flags map[string]string) (int64, error) {
	body, ate, err := grantBody(pasta, map[string]string{"tipo": flags["tipo"], "prioridade": flags["prioridade"],
		"min": flags["min"], "dims": flags["dims"], "motivo": "rodar " + flags["rotulo"], "esperar": rodarEsperaRecurso})
	if err != nil {
		return 0, err
	}
	start := time.Now()
	m, id, err := postGrant(c, base, recurso, body)
	if err != nil {
		_, err = hintEntrar(m, err)
		return 0, err
	}
	g, _ := m["concessao"].(map[string]any)
	gid, _ := g["id"].(float64)
	falhou := func(err error) (int64, error) {
		releaseGrantByID(c, base, pasta, recurso, int64(gid))
		return 0, err
	}
	interrompido := func() (int64, error) {
		return falhou(newErr(ExitErro, "interrompido", "interrompido enquanto esperava o recurso %s; o pedido %s foi solto", recurso, id))
	}
	if g["estado"] != "fila" {
		if ctx.Err() != nil {
			return interrompido()
		}
		return int64(gid), nil
	}
	type result struct {
		m   map[string]any
		err error
	}
	done := make(chan result, 1)
	go func() {
		m, err := awaitGrant(c, base, pasta, recurso, id, start, ate)
		done <- result{m, err}
	}()
	select {
	case r := <-done:
		if r.err != nil {
			if ce := asCLIError(r.err); ce.codigo == "ainda_na_fila" {
				ce.mensagem = fmt.Sprintf("o recurso %s não ficou livre em %s; o pedido %s foi solto e nada rodou", recurso, ate, id)
			}
			return falhou(r.err)
		}
		if ctx.Err() != nil {
			return interrompido()
		}
		return int64(gid), nil
	case <-ctx.Done():
		return interrompido()
	}
}

// releaseGrantByID solta o pedido id deste rodar (nunca outro da vaga no
// recurso). O erro é ignorado: o pedido pode já ter sido encerrado.
func releaseGrantByID(c *client, base, pasta, recurso string, id int64) {
	c.call("DELETE", base+"/recursos/"+url.PathEscape(recurso)+"/pedidos/"+strconv.FormatInt(id, 10)+"?"+
		url.Values{"pasta": {pasta}}.Encode(), nil)
}

// runChild roda argv sem shell, com stdout e stderr em out, chamando beat a
// cada rodarBatimento. Com ctx cancelado (Ctrl+C), mata a árvore do filho e
// espera ele sair. Devolve o código de saída; -1 com err quando o comando não
// começou, e -1 sem err quando ele morreu sem código (sinal).
func runChild(ctx context.Context, argv []string, out io.Writer, beat func()) (int, error) {
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Stdin = os.Stdin
	cmd.Stdout, cmd.Stderr = out, out
	// Um neto que herde a saída não deve prender o rodar depois de o filho sair.
	cmd.WaitDelay = 5 * time.Second
	if err := cmd.Start(); err != nil {
		return -1, err
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	ticker := time.NewTicker(rodarBatimento)
	defer ticker.Stop()
	for {
		select {
		case <-done:
			return cmd.ProcessState.ExitCode(), nil
		case <-ticker.C:
			go beat()
		case <-ctx.Done():
			interrupt(cmd, done)
			return cmd.ProcessState.ExitCode(), nil
		}
	}
}

// interrupt encerra o filho depois do Ctrl+C e espera o Wait (done). No
// Windows mata a árvore com taskkill (o filho do console já recebeu o Ctrl+C);
// fora dele, repassa a interrupção e mata depois de interromperEspera.
func interrupt(cmd *exec.Cmd, done <-chan error) {
	if runtime.GOOS == "windows" {
		kctx, cancel := context.WithTimeout(context.Background(), killTimeout)
		defer cancel()
		kill := exec.CommandContext(kctx, "taskkill", "/T", "/F", "/PID", strconv.Itoa(cmd.Process.Pid))
		if kill.Run() != nil {
			cmd.Process.Kill()
		}
		<-done
		return
	}
	cmd.Process.Signal(os.Interrupt)
	select {
	case <-done:
	case <-time.After(interromperEspera):
		cmd.Process.Kill()
		<-done
	}
}

// commandLine é o comando como texto, para a execução: argumentos com espaço
// ou aspas vão entre aspas.
func commandLine(argv []string) string {
	parts := make([]string, len(argv))
	for i, a := range argv {
		if a == "" || strings.ContainsAny(a, " \t\"") {
			a = strconv.Quote(a)
		}
		parts[i] = a
	}
	return strings.Join(parts, " ")
}

// tailWriter repassa tudo a w e guarda as últimas max linhas (sem o \r do
// fim). Uma linha que passa de rodarLinhaMax é cortada.
type tailWriter struct {
	w       io.Writer
	max     int
	lines   []string
	partial []byte
}

func (t *tailWriter) Write(p []byte) (int, error) {
	n := len(p)
	t.w.Write(p) // um stderr fechado não deve derrubar o comando
	for len(p) > 0 {
		i := bytes.IndexByte(p, '\n')
		if i < 0 {
			t.appendPartial(p)
			break
		}
		t.appendPartial(p[:i])
		t.push()
		p = p[i+1:]
	}
	return n, nil
}

func (t *tailWriter) appendPartial(b []byte) {
	if room := rodarLinhaMax - len(t.partial); room > 0 {
		t.partial = append(t.partial, b[:min(len(b), room)]...)
	}
}

func (t *tailWriter) push() {
	t.lines = append(t.lines, strings.TrimSuffix(string(t.partial), "\r"))
	t.partial = t.partial[:0]
	if len(t.lines) > t.max {
		t.lines = t.lines[len(t.lines)-t.max:]
	}
}

// String devolve as linhas guardadas (com a última sem quebra, se houver).
func (t *tailWriter) String() string {
	lines := t.lines
	if len(t.partial) > 0 {
		lines = append(slices.Clone(lines), strings.TrimSuffix(string(t.partial), "\r"))
		if len(lines) > t.max {
			lines = lines[len(lines)-t.max:]
		}
	}
	return strings.Join(lines, "\n")
}
