package service

import (
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/JoaoVitorSantDrade/central/internal/core"
)

const configA = `[projeto]
id = "p"
nome = "P"

[agentes]
sumido_apos = "45m"

[recursos.gpu]
dimensoes   = { vram_mb = 8192 }
prioridades = ["alta", "baixa"]
`

// configB é TOML válido, mas reservado cita uma dimensão que não existe.
const configB = `[projeto]
id = "p"

[recursos.gpu]
dimensoes   = { vram_mb = 8192 }
reservado   = { ram_mb = 1 }
prioridades = ["alta"]
`

func enterWith(s *Service, pasta, vaga, cfg string) (core.Agent, error) {
	return s.Enter(ctx, EnterInput{ProjetoID: "p", Nome: "P", Raiz: "/raiz/p", Pasta: pasta, Vaga: vaga, Papel: "PAPEL", Config: cfg})
}

func TestConfigInvalidaMantemAnterior(t *testing.T) {
	s, _ := newService(t)
	if _, err := enterWith(s, "/a", "A1", configA); err != nil {
		t.Fatal(err)
	}
	if _, err := enterWith(s, "/b", "B1", configB); !errors.Is(err, core.ErrInvalid) {
		t.Fatalf("err = %v, quer ErrInvalid", err)
	}
	c, err := s.ProjectConfig(ctx, "p")
	if err != nil {
		t.Fatal(err)
	}
	if c.Agentes.SumidoApos.Duration != 45*time.Minute || c.Recursos["gpu"].Dimensoes["vram_mb"] != 8192 {
		t.Fatalf("config = %+v, quer A", c)
	}
	// O Enter com configuração inválida não liga a vaga.
	if _, err := s.Inbox(ctx, "p", "/b"); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("B1 entrou mesmo com configuração inválida: %v", err)
	}
}

func TestConfigSoGravaQuandoMuda(t *testing.T) {
	s, _ := newService(t)
	for _, v := range []struct{ pasta, vaga string }{{"/a", "A1"}, {"/b", "B1"}} {
		if _, err := enterWith(s, v.pasta, v.vaga, configA); err != nil {
			t.Fatal(err)
		}
	}
	p, err := s.project(ctx, "p")
	if err != nil {
		t.Fatal(err)
	}
	// A contagem de versões e eventos é testada no core; aqui, o hash enviado.
	texto, hash, err := p.core.CurrentConfig(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if texto != configA || hash != configHash(configA) || len(hash) != 64 {
		t.Fatalf("vigente = %q %q", texto, hash)
	}
	if _, err := enterWith(s, "/a", "A1", configA+"\n# mudou\n"); err != nil {
		t.Fatal(err)
	}
	if texto, _, _ := p.core.CurrentConfig(ctx); texto != configA+"\n# mudou\n" {
		t.Fatalf("vigente = %q", texto)
	}
}

func TestConfigVaziaNaoTrocaAVigente(t *testing.T) {
	s, _ := newService(t)
	if _, err := enterWith(s, "/a", "A1", configA); err != nil {
		t.Fatal(err)
	}
	if _, err := enterWith(s, "/b", "B1", ""); err != nil {
		t.Fatal(err)
	}
	c, err := s.ProjectConfig(ctx, "p")
	if err != nil {
		t.Fatal(err)
	}
	if c.Agentes.SumidoApos.Duration != 45*time.Minute {
		t.Fatalf("config = %+v, quer A", c)
	}
}

func TestConfigPadraoSemTexto(t *testing.T) {
	s, _ := newService(t)
	if _, err := enterWith(s, "/a", "A1", ""); err != nil {
		t.Fatal(err)
	}
	c, err := s.ProjectConfig(ctx, "p")
	if err != nil {
		t.Fatal(err)
	}
	if c.Agentes.SumidoApos.Duration != 30*time.Minute || len(c.Recursos) != 0 {
		t.Fatalf("config = %+v, quer os padrões", c)
	}
	if _, err := s.ProjectConfig(ctx, "nada"); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("projeto desconhecido: %v", err)
	}
}

func TestConfigRecarregadaAoReabrir(t *testing.T) {
	dir := t.TempDir()
	s, err := New(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := enterWith(s, "/a", "A1", configA); err != nil {
		t.Fatal(err)
	}
	antes, err := s.ProjectConfig(ctx, "p")
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
	s2, err := New(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	depois, err := s2.ProjectConfig(ctx, "p")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(antes, depois) {
		t.Fatalf("antes = %+v\ndepois = %+v", antes, depois)
	}
	if depois.Agentes.SumidoApos.Duration != 45*time.Minute {
		t.Fatalf("depois = %+v", depois)
	}
}
