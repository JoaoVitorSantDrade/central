//go:build !windows

package cli

import (
	"os"
	"path/filepath"
)

// pathUsuario no Linux e no macOS não edita os arquivos do shell: só lê o PATH
// atual, e o instalar avisa quando a pasta falta nele.
type pathUsuario struct{}

func (pathUsuario) Ler() (string, error) { return os.Getenv("PATH"), nil }

func (pathUsuario) Gravar(string) error { return errSemGravarPath }

func pastaInstalacaoPadrao() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".local", "bin"), nil
}

const pathWindows = false
