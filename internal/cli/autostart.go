package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/JoaoVitorSantDrade/central/internal/config"
)

// Prazos do autostart.
const (
	autostartPrazo     = 5 * time.Second
	autostartIntervalo = 100 * time.Millisecond
)

// ensureDaemon garante um daemon respondendo, subindo um desanexado se preciso,
// e recusa um daemon que use outra pasta de dados.
func (c *client) ensureDaemon() error {
	dataDir, err := config.DataDir()
	if err == nil {
		dataDir, err = filepath.Abs(dataDir)
	}
	if err != nil {
		return newErr(ExitIndisponivel, "daemon_indisponivel", "pasta de dados: %v", err)
	}
	if info, ok := c.saude(); ok {
		if err := c.checkDataDir(dataDir, info.Dados); err != nil {
			return err
		}
		// Um daemon mais antigo que esta CLI dá lugar ao novo (o binário foi atualizado).
		trocou, err := c.trocarSeAntigo(info)
		if err != nil {
			return err
		}
		if !trocou {
			return nil
		}
	}
	exe, err := os.Executable()
	if err != nil {
		return newErr(ExitIndisponivel, "daemon_indisponivel", "não achei o próprio executável: %v", err)
	}
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return newErr(ExitIndisponivel, "daemon_indisponivel", "pasta de dados: %v", err)
	}
	// No Windows, a primeira tentativa tira o daemon do job do terminal (senão fechar
	// o terminal o mata); se o job não permitir, sobe sem isso.
	cmd := daemonCommand(exe, dataDir, true)
	err = cmd.Start()
	if err != nil && runtime.GOOS == "windows" {
		cmd = daemonCommand(exe, dataDir, false)
		err = cmd.Start()
	}
	if err != nil {
		return newErr(ExitIndisponivel, "daemon_indisponivel", "não consegui subir o daemon: %v", err)
	}
	cmd.Process.Release()
	deadline := time.Now().Add(autostartPrazo)
	for time.Now().Before(deadline) {
		if info, ok := c.saude(); ok {
			return c.checkDataDir(dataDir, info.Dados)
		}
		time.Sleep(autostartIntervalo)
	}
	return newErr(ExitIndisponivel, "daemon_indisponivel",
		"o daemon não respondeu em %v em %s (veja logs/daemon.log na pasta de dados; outro daemon com outra porta?)", autostartPrazo, c.base)
}

// checkDataDir recusa um daemon cuja pasta de dados não é a desta CLI. Um daemon
// que não informa a pasta (versão antiga) é aceito.
func (c *client) checkDataDir(minha, dados string) error {
	if dados == "" || samePath(minha, dados) {
		return nil
	}
	return newErr(ExitIndisponivel, "daemon_indisponivel",
		"o daemon em %s usa a pasta de dados %s, mas esta CLI usa %s (CENTRAL_HOME diferente? use outro CENTRAL_ADDR ou encerre aquele daemon)",
		c.base, dados, minha)
}

// samePath compara caminhos limpos; no Windows, sem diferenciar maiúsculas.
func samePath(a, b string) bool {
	a, b = filepath.Clean(a), filepath.Clean(b)
	if runtime.GOOS == "windows" {
		return strings.EqualFold(a, b)
	}
	return a == b
}

// daemonCommand monta o processo do daemon com a pasta de dados como pasta atual,
// para ele não prender (no Windows) o worktree de onde foi lançado. CENTRAL_HOME vai
// absoluto: um valor relativo seria resolvido a partir da nova pasta atual.
// breakaway só vale no Windows.
func daemonCommand(exe, dataDir string, breakaway bool) *exec.Cmd {
	cmd := exec.Command(exe, "daemon")
	cmd.Dir = dataDir
	cmd.Env = append(os.Environ(), "CENTRAL_HOME="+dataDir)
	detach(cmd, breakaway)
	return cmd
}
