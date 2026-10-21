package version

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

func TestVersao(t *testing.T) {
	if !regexp.MustCompile(`^\d+\.\d+\.\d+$`).MatchString(Versao) {
		t.Fatalf("Versao = %q, quer semver", Versao)
	}
	if API != "v1" {
		t.Fatalf("API = %q, quer v1", API)
	}
}

func TestVersaoPorLdflags(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "central")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	build := exec.Command("go", "build", "-ldflags", "-X github.com/JoaoVitorSantDrade/central/internal/version.Versao=9.8.7", "-o", bin, "../../cmd/central")
	build.Env = append(os.Environ(), "CGO_ENABLED=0")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	out, err := exec.Command(bin, "versao").Output()
	if err != nil || !strings.Contains(string(out), `"versao":"9.8.7"`) {
		t.Fatalf("versao = %s %v", out, err)
	}
}

func TestVersaoPadrao(t *testing.T) {
	if Versao != "1.2.0" {
		t.Fatalf("Versao = %q, quer 1.2.0", Versao)
	}
}
