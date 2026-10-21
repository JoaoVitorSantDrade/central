package config

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestInitEFindProject(t *testing.T) {
	d := t.TempDir()
	if _, err := InitProject(d, "lyriccut", "LyricCut"); err != nil {
		t.Fatal(err)
	}
	p, err := FindProject(d)
	if err != nil {
		t.Fatal(err)
	}
	want := Project{ID: "lyriccut", Name: "LyricCut", Root: filepath.Clean(d)}
	if p != want {
		t.Fatalf("got %+v want %+v", p, want)
	}
	if st, err := os.Stat(filepath.Join(d, ".central")); err != nil || !st.IsDir() {
		t.Fatalf(".central não criada: %v", err)
	}
}

func TestFindProjectSobePastas(t *testing.T) {
	d := t.TempDir()
	if _, err := InitProject(d, "x", "X"); err != nil {
		t.Fatal(err)
	}
	sub := filepath.Join(d, "a", "b")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	p, err := FindProject(sub)
	if err != nil {
		t.Fatal(err)
	}
	if p.Root != filepath.Clean(d) {
		t.Fatalf("Root = %q want %q", p.Root, d)
	}
}

func TestFindProjectForaDeProjeto(t *testing.T) {
	d := t.TempDir()
	for dir := d; ; dir = filepath.Dir(dir) {
		if _, err := os.Stat(filepath.Join(dir, FileName)); err == nil {
			t.Skip("há central.toml num ancestral do TempDir")
		}
		if filepath.Dir(dir) == dir {
			break
		}
	}
	if _, err := FindProject(d); !errors.Is(err, ErrNoProject) {
		t.Fatalf("err = %v, quer ErrNoProject", err)
	}
}

func TestInitRecusaExistente(t *testing.T) {
	d := t.TempDir()
	if _, err := InitProject(d, "x", "X"); err != nil {
		t.Fatal(err)
	}
	if _, err := InitProject(d, "x", "X"); !errors.Is(err, ErrExists) {
		t.Fatalf("err = %v, quer ErrExists", err)
	}
}

func TestIDInvalido(t *testing.T) {
	if _, err := InitProject(t.TempDir(), "Lyric Cut", "X"); !errors.Is(err, ErrInvalidID) {
		t.Fatalf("err = %v, quer ErrInvalidID", err)
	}
}

func TestDataDirEAddrPorVariavel(t *testing.T) {
	t.Setenv("CENTRAL_HOME", `C:\tmp\c`)
	t.Setenv("CENTRAL_ADDR", "127.0.0.1:9999")
	if d, err := DataDir(); err != nil || d != `C:\tmp\c` {
		t.Fatalf("DataDir = %q, %v", d, err)
	}
	if a := Addr(); a != "127.0.0.1:9999" {
		t.Fatalf("Addr = %q", a)
	}
	t.Setenv("CENTRAL_ADDR", "")
	if a := Addr(); a != "127.0.0.1:7420" {
		t.Fatalf("Addr padrão = %q", a)
	}
}
