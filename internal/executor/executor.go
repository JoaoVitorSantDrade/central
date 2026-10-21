// Package executor roda um turno do Claude Code CLI (claude -p) para uma vaga
// executada e devolve o que ele informou no stream-json. Não toca no banco: o
// escalonador do service decide quando rodar e grava o resultado no core.
package executor

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/JoaoVitorSantDrade/central/internal/shell"
)

// Classificação da falha de um turno (spec do executor §3.1).
const (
	FalhaOK          = ""
	FalhaPrazo       = "prazo"
	FalhaLimitePlano = "limite_plano"
	FalhaErro        = "erro"
	FalhaSemClaude   = "sem_claude"
)

// Limites do que fica guardado de um turno.
const (
	MaxLog   = 20 << 20 // log do stream por turno
	MaxTexto = 2 << 10  // texto final no Resultado
	maxErro  = 2 << 10  // fim do stderr
)

// Turno é um turno a rodar. Primeiro cria a sessão (--session-id) e o worktree
// (-w) na raiz do projeto; os seguintes retomam a sessão na pasta da vaga.
type Turno struct {
	Claude, Pasta, Sessao, Worktree, Modelo, Esforco, Texto, Log string
	Primeiro                                                     bool
	Prazo                                                        time.Duration
	Ambiente                                                     []string
}

// Resultado é o que o turno informou, tirado das mensagens init e result.
type Resultado struct {
	Sessao, Pasta, Texto       string
	CustoUSD                   float64
	TokensEntrada, TokensSaida int
	TurnosModelo               int
	Negacoes                   []string
	Codigo                     int
	Falha                      string
	ResetEm                    *time.Time
	Stderr                     string
}

// Args monta a linha do claude, sem o texto: ele vai pelo stdin, para nenhum
// shell (o claude.cmd do npm passa pelo cmd no Windows) interpretar < > " &.
func Args(t Turno) []string {
	a := []string{"-p"}
	if t.Primeiro {
		a = append(a, "--session-id", t.Sessao, "-w", t.Worktree)
	} else {
		a = append(a, "--resume", t.Sessao)
	}
	return append(a, "--model", t.Modelo, "--effort", t.Esforco,
		"--permission-mode", "auto", "--permission-prompts", "none",
		"--output-format", "stream-json", "--verbose")
}

// ArquivoStderr é onde fica o stderr do turno, ao lado do log.
func ArquivoStderr(log string) string { return strings.TrimSuffix(log, ".jsonl") + ".stderr.txt" }

// Rodar executa o turno e espera ele acabar. O stdout e o stderr vão direto a
// arquivos, sem passar pelo daemon: um daemon que reinicia não derruba o turno,
// e a recuperação lê o log depois (LerLog). onStart recebe o pid. Passado o
// prazo, a árvore de processos morre e a falha é FalhaPrazo. O erro só volta
// quando nem o log pôde ser criado; falhas do turno vão no Resultado.
func Rodar(ctx context.Context, t Turno, onStart func(pid int)) (Resultado, error) {
	if t.Log == "" {
		dir, err := os.MkdirTemp("", "central-turno-")
		if err != nil {
			return Resultado{}, err
		}
		defer os.RemoveAll(dir)
		t.Log = filepath.Join(dir, "turno.jsonl")
	}
	if err := os.MkdirAll(filepath.Dir(t.Log), 0o755); err != nil {
		return Resultado{}, err
	}
	out, err := os.Create(t.Log)
	if err != nil {
		return Resultado{}, err
	}
	defer out.Close()
	errArq, err := os.Create(ArquivoStderr(t.Log))
	if err != nil {
		return Resultado{}, err
	}
	defer errArq.Close()

	rctx, cancel := ctx, context.CancelFunc(func() {})
	if t.Prazo > 0 {
		rctx, cancel = context.WithTimeout(ctx, t.Prazo)
	}
	defer cancel()
	cmd := shell.Exec(rctx, t.Claude, Args(t)...)
	cmd.Dir = t.Pasta
	cmd.Env = append(os.Environ(), t.Ambiente...)
	cmd.Stdin = strings.NewReader(t.Texto)
	cmd.Stdout, cmd.Stderr = out, errArq
	cmd.WaitDelay = 5 * time.Second
	if err := cmd.Start(); err != nil {
		r := Resultado{Falha: FalhaErro, Codigo: -1, Stderr: cauda(err.Error(), maxErro)}
		if errors.Is(err, exec.ErrNotFound) || errors.Is(err, fs.ErrNotExist) {
			r.Falha = FalhaSemClaude
		}
		return r, nil
	}
	if onStart != nil {
		onStart(cmd.Process.Pid)
	}
	werr := cmd.Wait()
	out.Close()
	errArq.Close()

	r, result, ok := lerLog(t.Log)
	_ = cortarLog(t.Log, result)
	r.Codigo = cmd.ProcessState.ExitCode()
	r.Stderr = caudaArquivo(ArquivoStderr(t.Log), maxErro)
	switch {
	case t.Prazo > 0 && errors.Is(rctx.Err(), context.DeadlineExceeded) && ctx.Err() == nil:
		r.Falha = FalhaPrazo
	case ctx.Err() != nil:
		r.Falha = FalhaErro
		r.Stderr = cauda(strings.TrimSpace(r.Stderr+"\ncancelado: "+ctx.Err().Error()), maxErro)
	case r.Falha == FalhaLimitePlano:
	case !ok || werr != nil:
		r.Falha = FalhaErro
	}
	return r, nil
}
