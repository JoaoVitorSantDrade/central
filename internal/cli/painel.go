package cli

import (
	"os/exec"
	"runtime"

	"github.com/JoaoVitorSantDrade/central/internal/config"
)

// runPainel garante o daemon e abre o painel no navegador padrão. Com
// --so-url sim, só imprime a URL.
func runPainel(rest []string) (map[string]any, error) {
	flags, pos, err := parseArgs(rest, "so-url")
	if err != nil {
		return nil, err
	}
	if len(pos) > 0 {
		return nil, usage("painel não recebe argumentos")
	}
	soURL := false
	switch flags["so-url"] {
	case "", "nao", "não":
	case "sim":
		soURL = true
	default:
		return nil, usage("--so-url aceita sim ou nao")
	}
	addr := config.Addr()
	c := newClient(addr)
	if err := c.ensureDaemon(); err != nil {
		return nil, err
	}
	url := "http://" + addr + "/"
	if soURL {
		return map[string]any{"ok": true, "url": url}, nil
	}
	if err := abrirNavegador(url); err != nil {
		return nil, newErr(ExitErro, "navegador", "não consegui abrir o navegador (%v); abra %s", err, url)
	}
	return map[string]any{"ok": true, "url": url, "aberto": true}, nil
}

// abrirNavegador abre url no navegador padrão do sistema, sem esperar por ele.
func abrirNavegador(url string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	case "darwin":
		cmd = exec.Command("open", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	go cmd.Wait()
	return nil
}
