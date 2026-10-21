package service

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/JoaoVitorSantDrade/central/internal/core"
)

var ctx = context.Background()

func newService(t *testing.T) (*Service, string) {
	t.Helper()
	dir := t.TempDir()
	s, err := New(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s, dir
}

func mustEnter(t *testing.T, s *Service, proj, pasta, vaga string) {
	t.Helper()
	if _, err := s.Enter(ctx, EnterInput{ProjetoID: proj, Nome: "Projeto " + proj, Raiz: "/raiz/" + proj, Pasta: pasta, Vaga: vaga, Papel: "PAPEL"}); err != nil {
		t.Fatal(err)
	}
}

func TestEnterRegistraProjeto(t *testing.T) {
	s, dir := newService(t)
	mustEnter(t, s, "p1", "/a", "A1")
	var nome, raiz string
	if err := s.registry.QueryRow(`SELECT nome, raiz FROM projetos WHERE id = 'p1'`).Scan(&nome, &raiz); err != nil {
		t.Fatal(err)
	}
	if nome != "Projeto p1" || raiz != "/raiz/p1" {
		t.Fatalf("registro = %q %q", nome, raiz)
	}
	if _, err := os.Stat(filepath.Join(dir, "projetos", "p1.db")); err != nil {
		t.Fatal(err)
	}
}

func TestWaitRetornaNaHoraComPendente(t *testing.T) {
	s, _ := newService(t)
	mustEnter(t, s, "p", "/a", "A1")
	mustEnter(t, s, "p", "/b", "B1")
	if _, err := s.Send(ctx, "p", "/a", core.SendInput{Para: "B1", Tipo: "nota", Corpo: "oi"}); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	r, err := s.Wait(ctx, "p", "/b", 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if r.Motivo != "mensagem" || r.Novas != 1 || r.Sugestao != "central caixa" {
		t.Fatalf("r = %+v", r)
	}
	if d := time.Since(start); d > 100*time.Millisecond {
		t.Fatalf("demorou %v", d)
	}
}

func TestWaitAcordaQuandoChega(t *testing.T) {
	s, _ := newService(t)
	mustEnter(t, s, "p", "/a", "A1")
	mustEnter(t, s, "p", "/b", "B1")
	go func() {
		time.Sleep(200 * time.Millisecond)
		s.Send(ctx, "p", "/a", core.SendInput{Para: "B1", Tipo: "nota", Corpo: "oi"})
	}()
	r, err := s.Wait(ctx, "p", "/b", 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if r.Motivo != "mensagem" || r.EsperouMs < 150 || r.EsperouMs > 1000 {
		t.Fatalf("r = %+v", r)
	}
}

func TestWaitTempo(t *testing.T) {
	s, _ := newService(t)
	mustEnter(t, s, "p", "/b", "B1")
	r, err := s.Wait(ctx, "p", "/b", 300*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if r.Motivo != "tempo" {
		t.Fatalf("r = %+v", r)
	}
}

func TestWaitCancelado(t *testing.T) {
	s, _ := newService(t)
	mustEnter(t, s, "p", "/b", "B1")
	c, cancel := context.WithCancel(ctx)
	go func() { time.Sleep(100 * time.Millisecond); cancel() }()
	if _, err := s.Wait(c, "p", "/b", 5*time.Second); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v", err)
	}
}

func TestProjetosIsolados(t *testing.T) {
	s, _ := newService(t)
	mustEnter(t, s, "a", "/a1", "X")
	mustEnter(t, s, "a", "/a2", "Y")
	mustEnter(t, s, "b", "/b1", "Y")
	s.Send(ctx, "a", "/a1", core.SendInput{Para: "Y", Tipo: "nota", Corpo: "só no a"})
	ms, err := s.Inbox(ctx, "b", "/b1")
	if err != nil {
		t.Fatal(err)
	}
	if len(ms) != 0 {
		t.Fatalf("vazou para b: %+v", ms)
	}
}

func TestDesconhecidos(t *testing.T) {
	s, _ := newService(t)
	if _, err := s.Inbox(ctx, "nada", "/x"); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("projeto: %v", err)
	}
	mustEnter(t, s, "p", "/a", "A1")
	if _, err := s.Inbox(ctx, "p", "/x"); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("pasta: %v", err)
	}
	if _, err := s.Wait(ctx, "p", "/x", time.Second); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("wait: %v", err)
	}
}

func TestReabreProjetoDoDisco(t *testing.T) {
	dir := t.TempDir()
	s, err := New(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	mustEnter(t, s, "p", "/a", "A1")
	mustEnter(t, s, "p", "/b", "B1")
	s.Send(ctx, "p", "/a", core.SendInput{Para: "B1", Tipo: "nota", Corpo: "persistiu"})
	s.Close()
	s2, err := New(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	ms, err := s2.Inbox(ctx, "p", "/b")
	if err != nil || len(ms) != 1 {
		t.Fatalf("ms = %+v, %v", ms, err)
	}
}

func TestIDDeProjetoInvalido(t *testing.T) {
	s, dir := newService(t)
	for _, id := range []string{"../fora", "a/b", "X:Y"} {
		if _, err := s.Enter(ctx, EnterInput{ProjetoID: id, Nome: "n", Raiz: "/r", Pasta: "/p", Vaga: "V", Papel: "P"}); !errors.Is(err, core.ErrInvalid) {
			t.Errorf("Enter(%q) = %v", id, err)
		}
		if _, err := s.Inbox(ctx, id, "/p"); !errors.Is(err, core.ErrInvalid) {
			t.Errorf("Inbox(%q) = %v", id, err)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "fora.db")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("criou banco fora da pasta de projetos: %v", err)
	}
}

func TestRegistroGravaMsEMantemRaiz(t *testing.T) {
	s, _ := newService(t)
	if _, err := s.Enter(ctx, EnterInput{ProjetoID: "p", Nome: "P", Raiz: "/raiz/primeira", Pasta: "/a", Vaga: "A1", Papel: "X"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Enter(ctx, EnterInput{ProjetoID: "p", Nome: "P", Raiz: "/raiz/segunda", Pasta: "/b", Vaga: "B1", Papel: "X"}); err != nil {
		t.Fatal(err)
	}
	var raiz, acesso string
	if err := s.registry.QueryRow(`SELECT raiz, ultimo_acesso FROM projetos WHERE id = 'p'`).Scan(&raiz, &acesso); err != nil {
		t.Fatal(err)
	}
	if raiz != "/raiz/primeira" {
		t.Fatalf("raiz = %q, quer a do primeiro Enter", raiz)
	}
	if !regexp.MustCompile(`\.\d{3}Z$`).MatchString(acesso) {
		t.Fatalf("ultimo_acesso = %q, quer milissegundos", acesso)
	}
}

func TestErroDoServicoSemVocabularioDaCLI(t *testing.T) {
	s, _ := newService(t)
	mustEnter(t, s, "p", "/a", "A1")
	_, err := s.Inbox(ctx, "p", "/x")
	if !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("err = %v", err)
	}
	if strings.Contains(err.Error(), "central entrar") {
		t.Fatalf("mensagem cita a CLI: %v", err)
	}
}

func TestDataDirAbsoluto(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(filepath.Dir(dir))
	s, err := New(filepath.Base(dir), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if s.DataDir() != dir {
		t.Fatalf("DataDir = %q, quer %q", s.DataDir(), dir)
	}
}

func TestBackupAllCopiaRegistroEProjetos(t *testing.T) {
	s, dir := newService(t)
	mustEnter(t, s, "p1", "/a", "A1")
	if err := s.BackupAll(ctx); err != nil {
		t.Fatal(err)
	}
	for _, pat := range []string{filepath.Join(dir, BackupDir, "registro-*.db"), filepath.Join(dir, BackupDir, "projetos", "p1-*.db")} {
		if files, _ := filepath.Glob(pat); len(files) != 1 {
			t.Fatalf("%s = %v, quer 1", pat, files)
		}
	}
}

func TestNextIDGravaAVaga(t *testing.T) {
	s, _ := newService(t)
	mustEnter(t, s, "p", "/a", "A1")
	id, err := s.NextID(ctx, "p", "/a", "T", 1)
	if err != nil || id != "T-001" {
		t.Fatalf("NextID = %q, %v", id, err)
	}
	p, _ := s.project(ctx, "p")
	var n int
	if err := p.db.QueryRow(`SELECT count(*) FROM eventos WHERE tipo = 'id.gerado' AND quem = 'A1'`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("id.gerado de A1 = %d, %v", n, err)
	}
}
