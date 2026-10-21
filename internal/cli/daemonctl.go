package cli

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/JoaoVitorSantDrade/central/internal/config"
	"github.com/JoaoVitorSantDrade/central/internal/daemon"
	"github.com/JoaoVitorSantDrade/central/internal/version"
)

// pararPrazo é quanto parar espera o daemon sair antes de desistir.
const pararPrazo = 10 * time.Second

// saudeInfo é o que o daemon informa em /v1/saude.
type saudeInfo struct {
	Dados  string `json:"dados"`
	Versao string `json:"versao"`
	PID    int    `json:"pid"`
}

// compararVersoes compara duas versões semver X.Y.Z: -1, 0 ou 1. Uma versão
// que não é semver vale menos que qualquer uma que é.
func compararVersoes(a, b string) int {
	pa, oka := partesSemver(a)
	pb, okb := partesSemver(b)
	switch {
	case !oka && !okb:
		return 0
	case !oka:
		return -1
	case !okb:
		return 1
	}
	for i := range 3 {
		if pa[i] != pb[i] {
			if pa[i] < pb[i] {
				return -1
			}
			return 1
		}
	}
	return 0
}

func partesSemver(v string) ([3]int, bool) {
	var out [3]int
	ps := strings.Split(strings.TrimPrefix(v, "v"), ".")
	if len(ps) != 3 {
		return out, false
	}
	for i, p := range ps {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return out, false
		}
		out[i] = n
	}
	return out, true
}

// encerrarDaemon pede ao daemon que encerre e espera ele parar de responder.
// Um daemon sem a rota (1.0.0) só é encerrado pelo pid quando o daemon.json da
// nossa pasta de dados confirma o mesmo pid e endereço: nunca um pid qualquer.
func (c *client) encerrarDaemon(info saudeInfo) error {
	hc := &http.Client{Timeout: 3 * time.Second}
	resp, err := hc.Post(c.base+"/v1/daemon/encerrar", "application/json", strings.NewReader("{}"))
	pediu := err == nil && resp.StatusCode == http.StatusOK
	if resp != nil {
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
	}
	if !pediu {
		if !pidConfirmado(info.PID, c.base) {
			return newErr(ExitErro, "daemon_nao_parou", "o daemon em %s não aceitou encerrar e o pid %d não confere com o daemon.json; encerre-o à mão", c.base, info.PID)
		}
		if p, err := os.FindProcess(info.PID); err == nil {
			p.Kill()
		}
	}
	deadline := time.Now().Add(pararPrazo)
	for time.Now().Before(deadline) {
		if _, ok := c.saude(); !ok {
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return newErr(ExitErro, "daemon_nao_parou", "o daemon em %s não parou em %v (pid %d)", c.base, pararPrazo, info.PID)
}

// trocarSeAntigo encerra o daemon quando ele é de uma versão mais antiga que
// esta CLI, para o novo subir no lugar. Um daemon mais novo fica.
// Um daemon que não informa versão semver não é trocado.
func (c *client) trocarSeAntigo(info saudeInfo) (bool, error) {
	if _, ok := partesSemver(info.Versao); !ok || compararVersoes(version.Versao, info.Versao) <= 0 {
		return false, nil
	}
	return true, c.encerrarDaemon(info)
}

// pidConfirmado diz se o daemon.json da pasta de dados desta CLI registra esse
// pid no endereço base (o daemon o grava ao subir e apaga ao sair).
func pidConfirmado(pid int, base string) bool {
	if pid <= 0 {
		return false
	}
	dir, err := config.DataDir()
	if err != nil {
		return false
	}
	raw, err := os.ReadFile(filepath.Join(dir, daemon.InfoFile))
	if err != nil {
		return false
	}
	var info daemon.Info
	if json.Unmarshal(raw, &info) != nil {
		return false
	}
	return info.PID == pid && "http://"+info.Addr == base
}

// runParar encerra o daemon desta pasta de dados, se houver um rodando.
func runParar(rest []string) (map[string]any, error) {
	if _, pos, err := parseArgs(rest); err != nil {
		return nil, err
	} else if len(pos) > 0 {
		return nil, usage("parar não recebe argumentos")
	}
	c := newClient(config.Addr())
	info, ok := c.saude()
	if !ok {
		return map[string]any{"ok": true, "parado": false, "motivo": "nenhum daemon respondendo em " + c.base}, nil
	}
	if err := c.encerrarDaemon(info); err != nil {
		return nil, err
	}
	return map[string]any{"ok": true, "parado": true, "versao": info.Versao, "pid": info.PID}, nil
}

// lerSaude decodifica a resposta de /v1/saude.
func lerSaude(r io.Reader) saudeInfo {
	var s saudeInfo
	json.NewDecoder(io.LimitReader(r, 1<<16)).Decode(&s)
	return s
}
