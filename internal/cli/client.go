package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"
)

// Códigos de saída públicos (spec §6.6).
const (
	ExitOK           = 0
	ExitErro         = 1
	ExitConflito     = 2
	ExitNaoAchado    = 3
	ExitRecusado     = 4
	ExitIndisponivel = 5
)

// cliError é um erro com código de saída e código público.
type cliError struct {
	exit     int
	codigo   string
	mensagem string
}

func (e *cliError) Error() string { return e.mensagem }

func newErr(exit int, codigo, format string, args ...any) *cliError {
	return &cliError{exit: exit, codigo: codigo, mensagem: fmt.Sprintf(format, args...)}
}

// errNetwork marca falhas de conexão com o daemon (podem ser retentadas).
var errNetwork = errors.New("daemon indisponível")

type client struct {
	base string
	http *http.Client
}

func newClient(addr string) *client {
	return &client{base: "http://" + addr, http: &http.Client{}}
}

func exitForStatus(status int) int {
	switch status {
	case http.StatusConflict:
		return ExitConflito
	case http.StatusNotFound:
		return ExitNaoAchado
	case http.StatusUnprocessableEntity:
		return ExitRecusado
	default:
		return ExitErro
	}
}

// call faz a requisição e devolve o corpo JSON decodificado. Erros HTTP viram *cliError;
// falhas de rede envolvem errNetwork.
func (c *client) call(method, path string, body any) (map[string]any, error) {
	var r io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		r = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, c.base+path, r)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", errNetwork, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", errNetwork, err)
	}
	if resp.StatusCode == http.StatusServiceUnavailable || len(bytes.TrimSpace(raw)) == 0 {
		return nil, fmt.Errorf("%w: daemon reiniciando (%d)", errNetwork, resp.StatusCode)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, newErr(ExitErro, "interno", "resposta inválida do daemon (%d): %s", resp.StatusCode, raw)
	}
	if resp.StatusCode != http.StatusOK {
		codigo, mensagem := "interno", string(raw)
		if e, ok := m["erro"].(map[string]any); ok {
			codigo, _ = e["codigo"].(string)
			mensagem, _ = e["mensagem"].(string)
		}
		return nil, newErr(exitForStatus(resp.StatusCode), codigo, "%s", mensagem)
	}
	return m, nil
}

// saude diz se há um daemon respondendo no endereço, com a pasta de dados, a
// versão e o pid que ele informa (vazios num daemon antigo).
func (c *client) saude() (saudeInfo, bool) {
	hc := &http.Client{Timeout: 300 * time.Millisecond}
	resp, err := hc.Get(c.base + "/v1/saude")
	if err != nil {
		return saudeInfo{}, false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return saudeInfo{}, false
	}
	return lerSaude(resp.Body), true
}
