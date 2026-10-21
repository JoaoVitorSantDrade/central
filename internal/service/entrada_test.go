package service

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/JoaoVitorSantDrade/central/internal/core"
)

// configEntrada é o eco com a pasta de entrada "in" chamando importar.
func configEntrada(t *testing.T) string {
	return `[projeto]
id = "p"

[[extensoes]]
nome    = "eco"
comando = '"` + ecoBin(t) + `"'
timeout = "20s"
aba     = { titulo = "Eco", visao = "galeria", acao = "listar" }
entrada = { pasta = "in", ao_chegar = "importar" }

[acordar]
arquivos = ["PESQUISADOR"]
`
}

// rodarEntradas liga o vigia da pasta de entrada com intervalo curto.
func rodarEntradas(t *testing.T, s *Service) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); s.RunEntradas(ctx, 30*time.Millisecond) }()
	t.Cleanup(func() { cancel(); <-done })
}

// esperarPendentes espera até haver n pendências da extensão.
func esperarPendentes(t *testing.T, s *Service, n int) EntradaEstado {
	t.Helper()
	prazo := time.Now().Add(10 * time.Second)
	for {
		st, err := s.Pendentes(ctx, "p", "eco")
		if err != nil {
			t.Fatal(err)
		}
		if len(st.Pendentes) == n {
			return st
		}
		if time.Now().After(prazo) {
			t.Fatalf("pendentes = %+v, quer %d", st, n)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func chamadas(t *testing.T, s *Service, acao string) int {
	t.Helper()
	p, _ := s.project(ctx, "p")
	evs, _ := p.core.Events(ctx, core.EventFilter{Tipos: []string{"ext.chamada"}})
	n := 0
	for _, ev := range evs {
		if ev.Dados["acao"] == acao {
			n++
		}
	}
	return n
}

func TestEntradaEntregaArquivoEstavel(t *testing.T) {
	s, _ := newService(t)
	raiz := t.TempDir()
	entrarExt(t, s, raiz, "/e", "EXEC1", "EXECUTOR", configEntrada(t))
	rodarEntradas(t, s)
	os.MkdirAll(filepath.Join(raiz, "in"), 0o755)
	os.WriteFile(filepath.Join(raiz, "in", "digimon.mp4"), []byte("video"), 0o644)
	os.WriteFile(filepath.Join(raiz, "in", "digimon.txt"), []byte("letra"), 0o644)
	st := esperarPendentes(t, s, 1)
	got := st.Pendentes[0].Args["arquivos"]
	if strings.Join(asStrings(got), ",") != "in/digimon.mp4,in/digimon.txt" {
		t.Fatalf("arquivos = %v", got)
	}
	if st.Pendentes[0].Acao != "importar" || len(st.Pendentes[0].Formulario) == 0 {
		t.Fatalf("pendente = %+v", st.Pendentes[0])
	}
}

func asStrings(v any) []string {
	var out []string
	switch x := v.(type) {
	case []string:
		out = x
	case []any:
		for _, e := range x {
			out = append(out, e.(string))
		}
	}
	return out
}

func TestEntradaNaoRepete(t *testing.T) {
	s, _ := newService(t)
	raiz := t.TempDir()
	entrarExt(t, s, raiz, "/e", "EXEC1", "EXECUTOR", configEntrada(t))
	rodarEntradas(t, s)
	os.MkdirAll(filepath.Join(raiz, "in"), 0o755)
	os.WriteFile(filepath.Join(raiz, "in", "a.mp4"), []byte("1"), 0o644)
	esperarPendentes(t, s, 1)
	time.Sleep(300 * time.Millisecond)
	if n := chamadas(t, s, "importar"); n != 1 {
		t.Fatalf("o mesmo arquivo foi entregue %d vezes", n)
	}
	// Um arquivo novo é entregue sozinho.
	os.WriteFile(filepath.Join(raiz, "in", "b.mp4"), []byte("2"), 0o644)
	st := esperarPendentes(t, s, 2)
	if got := asStrings(st.Pendentes[1].Args["arquivos"]); len(got) != 1 || got[0] != "in/b.mp4" {
		t.Fatalf("segunda entrega = %v", got)
	}
}

func TestEntradaIgnoraParcial(t *testing.T) {
	s, _ := newService(t)
	raiz := t.TempDir()
	entrarExt(t, s, raiz, "/e", "EXEC1", "EXECUTOR", configEntrada(t))
	rodarEntradas(t, s)
	os.MkdirAll(filepath.Join(raiz, "in"), 0o755)
	os.WriteFile(filepath.Join(raiz, "in", ".central-parcial-x.mp4"), []byte("1"), 0o644)
	os.WriteFile(filepath.Join(raiz, "in", ".DS_Store"), []byte("1"), 0o644)
	time.Sleep(400 * time.Millisecond)
	if n := chamadas(t, s, "importar"); n != 0 {
		t.Fatalf("arquivos ocultos foram entregues (%d)", n)
	}
	// O upload grava o parcial e renomeia no fim: aí ele é entregue.
	if err := s.SalvarEntrada(ctx, "p", "eco", "clip.mp4", strings.NewReader("dados")); err != nil {
		t.Fatal(err)
	}
	st := esperarPendentes(t, s, 1)
	if got := asStrings(st.Pendentes[0].Args["arquivos"]); len(got) != 1 || got[0] != "in/clip.mp4" {
		t.Fatalf("arquivos = %v", got)
	}
	if err := s.SalvarEntrada(ctx, "p", "eco", "clip.mp4", strings.NewReader("de novo")); !errors.Is(err, core.ErrConflict) {
		t.Fatalf("arquivo repetido: err = %v", err)
	}
}

func TestUploadNomeInvalido(t *testing.T) {
	s, _ := newService(t)
	entrarExt(t, s, t.TempDir(), "/e", "EXEC1", "EXECUTOR", configEntrada(t))
	for _, nome := range []string{"", ".", "..", "../x.mp4", `a\b.mp4`, "a/b.mp4", ".oculto", "CON", "nul.txt", "a:b.mp4", "x\x01.mp4"} {
		if err := s.SalvarEntrada(ctx, "p", "eco", nome, strings.NewReader("x")); !errors.Is(err, core.ErrInvalid) {
			t.Errorf("%q: err = %v", nome, err)
		}
	}
	if err := s.SalvarEntrada(ctx, "p", "nada", "a.mp4", strings.NewReader("x")); !errors.Is(err, core.ErrNotFound) {
		t.Errorf("extensão sem entrada: err = %v", err)
	}
}

func TestFormularioPendente(t *testing.T) {
	s, _ := newService(t)
	raiz := t.TempDir()
	entrarExt(t, s, raiz, "/e", "EXEC1", "EXECUTOR", configEntrada(t))
	entrarExt(t, s, raiz, "/q", "PESQ1", "PESQUISADOR", "")
	rodarEntradas(t, s)
	os.MkdirAll(filepath.Join(raiz, "in"), 0o755)
	os.WriteFile(filepath.Join(raiz, "in", "agumon.mp4"), []byte("v"), 0o644)
	st := esperarPendentes(t, s, 1)
	res, err := s.ResponderFormulario(ctx, "p", "eco", st.Pendentes[0].ID, map[string]any{"nome": "agumon"})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Formulario) != 0 || len(res.Eventos) != 1 || res.Eventos[0].Ref != "agumon" {
		t.Fatalf("resposta = %+v", res)
	}
	if st := esperarPendentes(t, s, 0); len(st.Pendentes) != 0 {
		t.Fatalf("pendência não saiu: %+v", st)
	}
	if w, _ := s.Wait(ctx, "p", "/q", 0); w.Motivo != "ext" {
		t.Fatalf("PESQ1 = %+v", w)
	}
	if _, err := s.ResponderFormulario(ctx, "p", "eco", 999, nil); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("pendência inexistente: %v", err)
	}
}

func TestEntradaNaoCriaRaizQueSumiu(t *testing.T) {
	s, _ := newService(t)
	raiz := filepath.Join(t.TempDir(), "apagado")
	entrarExt(t, s, raiz, "/e", "EXEC1", "EXECUTOR", configEntrada(t))
	rodarEntradas(t, s)
	time.Sleep(200 * time.Millisecond)
	if _, err := os.Stat(raiz); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("o vigia recriou a raiz que não existe: %v", err)
	}
	if err := s.SalvarEntrada(ctx, "p", "eco", "a.mp4", strings.NewReader("x")); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("upload numa raiz que sumiu: err = %v", err)
	}
}

func TestFormularioRespondidoDuasVezes(t *testing.T) {
	s, _ := newService(t)
	raiz := t.TempDir()
	entrarExt(t, s, raiz, "/e", "EXEC1", "EXECUTOR", configEntrada(t))
	rodarEntradas(t, s)
	os.MkdirAll(filepath.Join(raiz, "in"), 0o755)
	os.WriteFile(filepath.Join(raiz, "in", "x.mp4"), []byte("v"), 0o644)
	st := esperarPendentes(t, s, 1)
	id := st.Pendentes[0].ID
	primeira := make(chan error, 1)
	go func() {
		_, err := s.ResponderFormulario(ctx, "p", "eco", id, map[string]any{"nome": "x", "dormir": "800ms"})
		primeira <- err
	}()
	time.Sleep(200 * time.Millisecond)
	if _, err := s.ResponderFormulario(ctx, "p", "eco", id, map[string]any{"nome": "x"}); !errors.Is(err, core.ErrConflict) {
		t.Fatalf("segunda resposta: err = %v, quer conflito", err)
	}
	if err := <-primeira; err != nil {
		t.Fatal(err)
	}
	if n := chamadas(t, s, "importar"); n != 2 {
		t.Fatalf("importar rodou %d vezes, quer 2 (entrega + uma resposta)", n)
	}
}
