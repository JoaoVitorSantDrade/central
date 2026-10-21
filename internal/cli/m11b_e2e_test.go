package cli_test

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestInstalarEDesinstalar(t *testing.T) {
	e := newEnv(t)
	destino := filepath.Join(t.TempDir(), "Programs", "central")
	pathFalso := filepath.Join(t.TempDir(), "path.txt")
	sep := ":"
	if runtime.GOOS == "windows" {
		sep = ";"
	}
	os.WriteFile(pathFalso, []byte("/ja/estava"+sep+"/outra"), 0o644)
	t.Setenv("CENTRAL_INSTALAR_EM", destino)
	t.Setenv("CENTRAL_TESTE_PATH", pathFalso)
	dir := t.TempDir()
	m := e.ok(dir, "instalar", "--sem-plugin", "sim")
	nome := "central"
	if runtime.GOOS == "windows" {
		nome += ".exe"
	}
	alvo := filepath.Join(destino, nome)
	orig, _ := os.Stat(bin)
	inst, err := os.Stat(alvo)
	if err != nil || inst.Size() != orig.Size() {
		t.Fatalf("binário instalado = %v %v (resposta %v)", inst, err, m)
	}
	p, _ := os.ReadFile(pathFalso)
	if strings.Count(string(p), destino) != 1 || !strings.HasPrefix(string(p), "/ja/estava"+sep+"/outra") {
		t.Fatalf("PATH = %q", p)
	}
	e.ok(dir, "instalar", "--sem-plugin", "sim")
	if p, _ := os.ReadFile(pathFalso); strings.Count(string(p), destino) != 1 {
		t.Fatalf("instalar de novo duplicou o PATH: %q", p)
	}
	m = e.ok(dir, "desinstalar", "--sem-plugin", "sim")
	if p, _ := os.ReadFile(pathFalso); strings.Contains(string(p), destino) || string(p) != "/ja/estava"+sep+"/outra" {
		t.Fatalf("PATH depois de desinstalar = %q", p)
	}
	if _, err := os.Stat(alvo); !os.IsNotExist(err) {
		t.Fatalf("o binário instalado ficou: %v", err)
	}
	if m["dados"] == "" || m["dados"] == nil {
		t.Fatalf("desinstalar devia dizer onde ficaram os dados: %v", m)
	}
}
