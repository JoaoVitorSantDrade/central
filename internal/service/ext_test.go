package service

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"testing"

	"github.com/JoaoVitorSantDrade/central/internal/core"
)

var (
	ecoOnce sync.Once
	ecoPath string
	ecoErr  error
)

// ecoBin compila a extensão de exemplo uma vez por execução dos testes.
func ecoBin(t *testing.T) string {
	t.Helper()
	ecoOnce.Do(func() {
		dir, err := os.MkdirTemp("", "central-eco-")
		if err != nil {
			ecoErr = err
			return
		}
		ecoPath = filepath.Join(dir, "eco")
		if runtime.GOOS == "windows" {
			ecoPath += ".exe"
		}
		if out, err := exec.Command("go", "build", "-o", ecoPath, "../../examples/ext/eco").CombinedOutput(); err != nil {
			ecoErr = errors.New(string(out))
		}
	})
	if ecoErr != nil {
		t.Fatal(ecoErr)
	}
	return ecoPath
}

// configEco é um central.toml com o eco como extensão "eco".
func configEco(t *testing.T) string {
	return `[projeto]
id = "p"

[[extensoes]]
nome    = "eco"
comando = '"` + ecoBin(t) + `"'
timeout = "20s"
cli     = ["listar", "importar"]
aba     = { titulo = "Eco", visao = "tabela", acao = "listar" }

[acordar]
exemplo-novo = ["PESQUISADOR"]
`
}

// entrarExt registra o projeto p com raiz real (a pasta atual da extensão).
func entrarExt(t *testing.T, s *Service, raiz, pasta, vaga, papel, cfg string) {
	t.Helper()
	if _, err := s.Enter(ctx, EnterInput{ProjetoID: "p", Nome: "P", Raiz: raiz, Pasta: pasta, Vaga: vaga, Papel: papel, Config: cfg}); err != nil {
		t.Fatal(err)
	}
}

func TestExtensaoChamadaEAcorda(t *testing.T) {
	s, _ := newService(t)
	raiz := t.TempDir()
	entrarExt(t, s, raiz, "/e", "EXEC1", "EXECUTOR", configEco(t))
	entrarExt(t, s, raiz, "/q", "PESQ1", "PESQUISADOR", "")
	res, err := s.Extensao(ctx, "p", "/e", "eco", "importar", map[string]any{"evento": "exemplo-novo", "ref": "digimon"})
	if err != nil {
		t.Fatal(err)
	}
	var d struct {
		Acao, Pasta string
		Agente      map[string]any
		Projeto     map[string]any
	}
	if err := json.Unmarshal(res.Dados, &d); err != nil {
		t.Fatal(err)
	}
	if d.Acao != "importar" || d.Agente["vaga"] != "EXEC1" || d.Projeto["nome"] != "P" {
		t.Fatalf("dados = %+v", d)
	}
	if got, _ := filepath.EvalSymlinks(d.Pasta); got != mustEval(t, raiz) {
		t.Fatalf("pasta atual = %q, quer a raiz %q", d.Pasta, raiz)
	}
	w, err := s.Wait(ctx, "p", "/q", 0)
	if err != nil {
		t.Fatal(err)
	}
	if w.Motivo != "ext" || w.Novas != 1 {
		t.Fatalf("PESQ1 acordou com %+v", w)
	}
	p, _ := s.project(ctx, "p")
	evs, _ := p.core.Events(ctx, core.EventFilter{Tipos: []string{"ext.exemplo-novo", "ext.chamada"}})
	if len(evs) != 2 || evs[0].Quem != "EXEC1" || evs[1].Dados["ok"] != true {
		t.Fatalf("eventos = %+v", evs)
	}
}

func mustEval(t *testing.T, p string) string {
	t.Helper()
	r, err := filepath.EvalSymlinks(p)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestExtensaoAcaoRecusada(t *testing.T) {
	s, _ := newService(t)
	entrarExt(t, s, t.TempDir(), "/e", "EXEC1", "EXECUTOR", configEco(t))
	if _, err := s.Extensao(ctx, "p", "/e", "eco", "apagar-tudo", nil); !errors.Is(err, core.ErrInvalid) {
		t.Fatalf("ação fora da lista: err = %v", err)
	}
	if _, err := s.Extensao(ctx, "p", "/e", "nada", "listar", nil); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("extensão desconhecida: err = %v", err)
	}
}

func TestExtensaoErroNaoQuebra(t *testing.T) {
	s, _ := newService(t)
	entrarExt(t, s, t.TempDir(), "/e", "EXEC1", "EXECUTOR", configEco(t))
	for _, args := range []map[string]any{{"falhar": "sem vídeo"}, {"quebrar": "faltou o ffmpeg"}, {"evento": "Tipo Ruim"}} {
		if _, err := s.Extensao(ctx, "p", "/e", "eco", "listar", args); !errors.Is(err, ErrExtensao) {
			t.Fatalf("%v: err = %v, quer ErrExtensao", args, err)
		}
	}
	if _, err := s.Extensao(ctx, "p", "/e", "eco", "listar", nil); err != nil {
		t.Fatalf("depois das falhas: %v", err)
	}
	p, _ := s.project(ctx, "p")
	evs, _ := p.core.Events(ctx, core.EventFilter{Tipos: []string{"ext.chamada"}})
	if len(evs) != 4 || evs[0].Dados["ok"] != false || evs[0].Dados["erro"] == "" {
		t.Fatalf("chamadas = %+v", evs)
	}
}

func TestExtensaoHumano(t *testing.T) {
	s, _ := newService(t)
	entrarExt(t, s, t.TempDir(), "/e", "EXEC1", "EXECUTOR", configEco(t))
	res, err := s.Extensao(ctx, "p", "", "eco", "listar", map[string]any{"pedir": "sim"})
	if err != nil {
		t.Fatal(err)
	}
	var d struct{ Agente map[string]any }
	json.Unmarshal(res.Dados, &d)
	if d.Agente != nil || len(res.Formulario) == 0 {
		t.Fatalf("humano: dados = %s, formulario = %s", res.Dados, res.Formulario)
	}
	es, err := s.Extensoes(ctx, "p")
	if err != nil || len(es) != 1 || es[0].Aba == nil || len(es[0].Acoes) != 2 {
		t.Fatalf("extensoes = %+v %v", es, err)
	}
}

func TestExtensaoGravaFatos(t *testing.T) {
	s, _ := newService(t)
	entrarExt(t, s, t.TempDir(), "/e", "EXEC1", "EXECUTOR", configEco(t))
	if _, err := s.Extensao(ctx, "p", "/e", "eco", "listar", map[string]any{"fato": "e2e.abc1234-2"}); err != nil {
		t.Fatal(err)
	}
	f, err := s.GetFact(ctx, "p", "", "e2e.abc1234-2")
	if err != nil || f.Vaga != "EXEC1" || string(f.Valor) != `{"eco":true}` {
		t.Fatalf("fato = %+v %v", f, err)
	}
	if _, err := s.Extensao(ctx, "p", "", "eco", "listar", map[string]any{"fato": "Chave Ruim"}); !errors.Is(err, ErrExtensao) {
		t.Fatalf("fato inválido: err = %v", err)
	}
}
