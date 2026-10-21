// Package ext fala o contrato ext/v1 (spec §9): monta o pedido, roda o
// comando da extensão com prazo e limite de saída e lê a resposta. Não toca
// no banco nem decide permissões; isso é do serviço.
package ext

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/JoaoVitorSantDrade/central/internal/shell"
)

// Versao é a versão do contrato; mudar os campos exige ext/v2.
const Versao = "ext/v1"

// Limites da saída de uma chamada.
const (
	MaxSaida  = 1 << 20
	MaxStderr = 64 << 10
)

// Erros de uma chamada (sempre embrulhados com o resumo do stderr).
var (
	ErrPrazo       = errors.New("a extensão passou do prazo")
	ErrSaidaGrande = errors.New("a extensão escreveu mais de 1 MB no stdout")
	ErrResposta    = errors.New("a extensão não respondeu uma linha JSON ext/v1")
)

// Projeto identifica o projeto no pedido.
type Projeto struct {
	ID   string `json:"id"`
	Nome string `json:"nome"`
	Raiz string `json:"raiz"`
}

// Agente é quem chamou; nil quando foi o humano ou a própria Central.
type Agente struct {
	Vaga  string `json:"vaga"`
	Papel string `json:"papel"`
}

// Pedido é a linha JSON escrita no stdin da extensão.
type Pedido struct {
	Versao  string         `json:"versao"`
	Acao    string         `json:"acao"`
	Args    map[string]any `json:"args"`
	Projeto Projeto        `json:"projeto"`
	Agente  *Agente        `json:"agente"`
}

// Evento é um evento emitido pela extensão.
type Evento struct {
	Tipo  string         `json:"tipo"`
	Ref   string         `json:"ref"`
	Dados map[string]any `json:"dados"`
}

// Fato é um fato do projeto que a extensão pede para gravar.
type Fato struct {
	Chave  string          `json:"chave"`
	Valor  json.RawMessage `json:"valor"`
	Motivo string          `json:"motivo"`
}

// Resposta é a última linha JSON do stdout da extensão.
type Resposta struct {
	OK         bool            `json:"ok"`
	Dados      json.RawMessage `json:"dados,omitempty"`
	Eventos    []Evento        `json:"eventos,omitempty"`
	Fatos      []Fato          `json:"fatos,omitempty"`
	Formulario json.RawMessage `json:"formulario,omitempty"`
	Erro       *struct {
		Mensagem string `json:"mensagem"`
	} `json:"erro,omitempty"`
}

// TemFormulario diz se a resposta pede campos ao humano.
func (r Resposta) TemFormulario() bool {
	f := bytes.TrimSpace(r.Formulario)
	return len(f) > 0 && !bytes.Equal(f, []byte("null"))
}

// Execucao é como rodar a extensão.
type Execucao struct {
	Comando  string
	Pasta    string
	Ambiente []string // somado ao ambiente do daemon
	Timeout  time.Duration
}

// limitado guarda até max bytes e conta se passou.
type limitado struct {
	buf     bytes.Buffer
	max     int
	estouro bool
}

func (l *limitado) Write(p []byte) (int, error) {
	if room := l.max - l.buf.Len(); room > 0 {
		if len(p) > room {
			l.buf.Write(p[:room])
			l.estouro = true
		} else {
			l.buf.Write(p)
		}
	} else if len(p) > 0 {
		l.estouro = true
	}
	return len(p), nil
}

// resumo devolve o fim do stderr numa linha, para a mensagem de erro.
func resumo(stderr []byte) string {
	s := strings.Join(strings.Fields(string(stderr)), " ")
	if len(s) > 300 {
		s = "…" + s[len(s)-300:]
	}
	if s == "" {
		return ""
	}
	return " (stderr: " + s + ")"
}

// Rodar executa a extensão com o pedido e devolve a resposta lida.
// Uma resposta com ok=false volta sem erro: quem chama decide.
func Rodar(ctx context.Context, x Execucao, p Pedido) (Resposta, error) {
	if p.Versao == "" {
		p.Versao = Versao
	}
	if p.Args == nil {
		p.Args = map[string]any{}
	}
	linha, err := json.Marshal(p)
	if err != nil {
		return Resposta{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, x.Timeout)
	defer cancel()
	cmd := shell.Command(ctx, x.Comando)
	cmd.Dir = x.Pasta
	// O contrato é UTF-8 nos dois sentidos; PYTHONUTF8 faz o Python do
	// Windows (cp1252 num pipe) segui-lo sem cada extensão lembrar disso.
	cmd.Env = append(append(os.Environ(), "CENTRAL_EXT="+Versao, "PYTHONUTF8=1"), x.Ambiente...)
	cmd.Stdin = bytes.NewReader(append(linha, '\n'))
	stdout := &limitado{max: MaxSaida}
	stderr := &limitado{max: MaxStderr}
	cmd.Stdout, cmd.Stderr = stdout, stderr
	// Um neto que herda o stdout não segura a chamada depois do prazo.
	cmd.WaitDelay = 2 * time.Second
	runErr := cmd.Run()
	if ctx.Err() == context.DeadlineExceeded {
		return Resposta{}, fmt.Errorf("%w (%v)%s", ErrPrazo, x.Timeout, resumo(stderr.buf.Bytes()))
	}
	if ctx.Err() != nil {
		return Resposta{}, ctx.Err()
	}
	if stdout.estouro {
		return Resposta{}, fmt.Errorf("%w%s", ErrSaidaGrande, resumo(stderr.buf.Bytes()))
	}
	r, perr := lerResposta(stdout.buf.Bytes())
	if perr != nil {
		detalhe := perr.Error()
		var ee *exec.ExitError
		if errors.As(runErr, &ee) {
			detalhe = fmt.Sprintf("saiu com código %d", ee.ExitCode())
		} else if runErr != nil && !errors.Is(runErr, io.EOF) {
			detalhe = runErr.Error()
		}
		return Resposta{}, fmt.Errorf("%w: %s%s", ErrResposta, detalhe, resumo(stderr.buf.Bytes()))
	}
	return r, nil
}

// lerResposta lê a última linha não vazia do stdout como a resposta.
func lerResposta(out []byte) (Resposta, error) {
	linhas := strings.Split(strings.TrimRight(string(out), "\r\n\t "), "\n")
	ultima := strings.TrimSpace(linhas[len(linhas)-1])
	if ultima == "" {
		return Resposta{}, errors.New("stdout vazio")
	}
	var campos map[string]json.RawMessage
	if err := json.Unmarshal([]byte(ultima), &campos); err != nil {
		return Resposta{}, fmt.Errorf("última linha não é JSON: %.80q", ultima)
	}
	if _, ok := campos["ok"]; !ok {
		return Resposta{}, errors.New(`a resposta não tem "ok"`)
	}
	var r Resposta
	if err := json.Unmarshal([]byte(ultima), &r); err != nil {
		return Resposta{}, fmt.Errorf("resposta fora do formato: %v", err)
	}
	return r, nil
}

// Programa devolve o primeiro termo do comando (o executável), respeitando aspas.
func Programa(comando string) string {
	s := strings.TrimSpace(comando)
	if strings.HasPrefix(s, `"`) {
		if fim := strings.Index(s[1:], `"`); fim >= 0 {
			return s[1 : fim+1]
		}
		return s[1:]
	}
	if i := strings.IndexAny(s, " \t"); i >= 0 {
		return s[:i]
	}
	return s
}

// Localizar diz onde está o executável do comando: no PATH ou relativo à raiz.
func Localizar(comando, raiz string) (string, error) {
	prog := Programa(comando)
	if prog == "" {
		return "", errors.New("comando vazio")
	}
	if strings.ContainsAny(prog, `/\`) {
		caminho := prog
		if !filepath.IsAbs(caminho) {
			caminho = filepath.Join(raiz, caminho)
		}
		if _, err := os.Stat(caminho); err != nil {
			return "", fmt.Errorf("%s não existe", caminho)
		}
		return caminho, nil
	}
	caminho, err := exec.LookPath(prog)
	if err != nil {
		return "", fmt.Errorf("%s não está no PATH", prog)
	}
	return caminho, nil
}
