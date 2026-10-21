package config

import (
	"slices"
	"strings"
	"testing"
	"time"
)

// exemploSpec é o exemplo da spec §5.3, com sem_uso e prazo_max_por_prioridade.
const exemploSpec = `
[projeto]
id = "lyriccut"
nome = "LyricCut"

[agentes]
sumido_apos = "45m"

[recursos.gpu]
dimensoes   = { vram_mb = 8192, uso_pct = 100 }
reservado   = { vram_mb = 2200, uso_pct = 15 }
medidor     = { comando = "nvidia-smi --query-gpu=utilization.gpu,memory.used --format=csv,noheader,nounits", mapa = ["uso_pct", "vram_mb"], intervalo = "15s" }
margem      = { vram_mb = 512 }
teto_medido = { vram_mb = 0.95 }
prazo_max   = "30m"
prazo_max_por_prioridade = { versao = "120m" }
prioridades = ["versao", "bug-p0", "missao", "executor", "pesquisa"]
sem_uso     = { dimensao = "uso_pct", limite = 5, apos = "5m" }

[recursos.gpu.tipos]
e2e-nivel1 = { vram_mb = 5000, uso_pct = 80 }
e2e-nivel2 = { exclusivo = true }
studio     = { vram_mb = 1500, uso_pct = 30 }
`

func TestParseConfigCompleta(t *testing.T) {
	c, err := ParseProjectConfig([]byte(exemploSpec))
	if err != nil {
		t.Fatal(err)
	}
	if c.Projeto.ID != "lyriccut" || c.Projeto.Nome != "LyricCut" {
		t.Fatalf("projeto = %+v", c.Projeto)
	}
	if c.Agentes.SumidoApos.Duration != 45*time.Minute {
		t.Fatalf("sumido_apos = %v", c.Agentes.SumidoApos)
	}
	g, ok := c.Recursos["gpu"]
	if !ok {
		t.Fatalf("recursos = %+v", c.Recursos)
	}
	if g.Dimensoes["vram_mb"] != 8192 || g.Dimensoes["uso_pct"] != 100 {
		t.Fatalf("dimensoes = %v", g.Dimensoes)
	}
	if g.Reservado["vram_mb"] != 2200 || g.Margem["vram_mb"] != 512 || g.TetoMedido["vram_mb"] != 0.95 {
		t.Fatalf("reservado/margem/teto = %v %v %v", g.Reservado, g.Margem, g.TetoMedido)
	}
	if g.Medidor == nil || g.Medidor.Intervalo.Duration != 15*time.Second ||
		!strings.HasPrefix(g.Medidor.Comando, "nvidia-smi") || strings.Join(g.Medidor.Mapa, ",") != "uso_pct,vram_mb" {
		t.Fatalf("medidor = %+v", g.Medidor)
	}
	if g.PrazoMax.Duration != 30*time.Minute || g.PrazoMaxPorPrioridade["versao"].Duration != 120*time.Minute {
		t.Fatalf("prazos = %v %v", g.PrazoMax, g.PrazoMaxPorPrioridade)
	}
	if len(g.Prioridades) != 5 || g.Prioridades[0] != "versao" {
		t.Fatalf("prioridades = %v", g.Prioridades)
	}
	if g.SemUso == nil || g.SemUso.Dimensao != "uso_pct" || g.SemUso.Limite != 5 || g.SemUso.Apos.Duration != 5*time.Minute {
		t.Fatalf("sem_uso = %+v", g.SemUso)
	}
	if g.Tipos["e2e-nivel2"]["exclusivo"] != true {
		t.Fatalf("tipos = %v", g.Tipos)
	}
	if len(g.Tipos) != 3 {
		t.Fatalf("tipos = %v", g.Tipos)
	}
}

func TestSpecDeConfig(t *testing.T) {
	c, err := ParseProjectConfig([]byte(exemploSpec))
	if err != nil {
		t.Fatal(err)
	}
	s, err := c.Recursos["gpu"].Spec("gpu")
	if err != nil {
		t.Fatal(err)
	}
	if s.Nome != "gpu" || s.Dimensoes["vram_mb"] != 8192 || s.Reservado["vram_mb"] != 2200 ||
		s.Margem["vram_mb"] != 512 || s.TetoMedido["vram_mb"] != 0.95 {
		t.Fatalf("spec = %+v", s)
	}
	if !s.Tipos["e2e-nivel2"].Exclusivo || s.Tipos["e2e-nivel1"].Exclusivo || s.Tipos["e2e-nivel1"].Dims["vram_mb"] != 5000 {
		t.Fatalf("tipos = %+v", s.Tipos)
	}
	if s.MedidorIntervalo != 15*time.Second {
		t.Fatalf("medidor = %v", s.MedidorIntervalo)
	}
	if s.PrazoMax != 30*time.Minute || s.PrazoMaxPorPrioridade["versao"] != 120*time.Minute {
		t.Fatalf("prazos = %v %v", s.PrazoMax, s.PrazoMaxPorPrioridade)
	}
	if len(s.Prioridades) != 5 || s.SemUso == nil || s.SemUso.Dimensao != "uso_pct" || s.SemUso.Apos != 5*time.Minute {
		t.Fatalf("prioridades/sem_uso = %v %+v", s.Prioridades, s.SemUso)
	}
}

func TestSpecSemMedidor(t *testing.T) {
	c, err := ParseProjectConfig([]byte("[recursos.r]\ndimensoes = { n = 4 }\nprioridades = [\"a\"]\n"))
	if err != nil {
		t.Fatal(err)
	}
	s, err := c.Recursos["r"].Spec("r")
	if err != nil {
		t.Fatal(err)
	}
	if s.MedidorIntervalo != 0 || s.SemUso != nil || len(s.Tipos) != 0 {
		t.Fatalf("spec = %+v", s)
	}
}

func TestParseConfigPadroes(t *testing.T) {
	c, err := ParseProjectConfig([]byte("[projeto]\nid = \"p\"\nnome = \"P\"\n"))
	if err != nil {
		t.Fatal(err)
	}
	if c.Agentes.SumidoApos.Duration != 30*time.Minute {
		t.Fatalf("sumido_apos = %v", c.Agentes.SumidoApos)
	}
	if len(c.Recursos) != 0 {
		t.Fatalf("recursos = %v", c.Recursos)
	}
}

func TestParseConfigPadroesDoRecurso(t *testing.T) {
	c, err := ParseProjectConfig([]byte(`
[recursos.gpu]
dimensoes   = { vram_mb = 8192 }
medidor     = { comando = "medir", mapa = ["vram_mb"] }
prioridades = ["alta"]
sem_uso     = { dimensao = "vram_mb", limite = 5 }
`))
	if err != nil {
		t.Fatal(err)
	}
	g := c.Recursos["gpu"]
	if g.PrazoMax.Duration != 30*time.Minute || g.Medidor.Intervalo.Duration != 15*time.Second || g.SemUso.Apos.Duration != 5*time.Minute {
		t.Fatalf("padrões = %v %v %v", g.PrazoMax, g.Medidor.Intervalo, g.SemUso.Apos)
	}
}

func TestParseConfigInvalida(t *testing.T) {
	base := "[recursos.gpu]\ndimensoes = { vram_mb = 8192, uso_pct = 100 }\nreservado = { vram_mb = 2200 }\nprioridades = [\"a\", \"b\"]\n"
	casos := []struct {
		nome, texto string
		quer        []string // trechos que a mensagem precisa ter
	}{
		{"reservado desconhecido", "[recursos.gpu]\ndimensoes = { vram_mb = 8192 }\nreservado = { ram_mb = 10 }\nprioridades = [\"a\"]\n",
			[]string{"gpu", "reservado", "ram_mb"}},
		{"teto acima de 1", base + "teto_medido = { vram_mb = 1.5 }\n", []string{"gpu", "teto_medido"}},
		{"prioridade repetida", "[recursos.gpu]\ndimensoes = { vram_mb = 8192 }\nprioridades = [\"a\", \"b\", \"a\"]\n",
			[]string{"gpu", "prioridades", `"a"`}},
		{"tipo maior que o livre", base + "[recursos.gpu.tipos]\ngrande = { vram_mb = 7000 }\n",
			[]string{"gpu", "tipos", "grande", "vram_mb"}},
		{"toml quebrado", "[projeto]\nid = \"p\"\nnome = \n", []string{"line"}},
		{"dimensoes vazio", "[recursos.gpu]\nprioridades = [\"a\"]\n", []string{"gpu", "dimensoes"}},
		{"dimensao zero", "[recursos.gpu]\ndimensoes = { vram_mb = 0 }\nprioridades = [\"a\"]\n", []string{"gpu", "dimensoes", "vram_mb"}},
		{"sem prioridades", "[recursos.gpu]\ndimensoes = { vram_mb = 1 }\n", []string{"gpu", "prioridades"}},
		{"prazo de prioridade desconhecida", base + "prazo_max_por_prioridade = { z = \"1h\" }\n", []string{"gpu", "prazo_max_por_prioridade", "z"}},
		{"mapa do medidor desconhecido", base + "medidor = { comando = \"m\", mapa = [\"x\"] }\n", []string{"gpu", "medidor", "x"}},
		{"intervalo do medidor curto", base + "medidor = { comando = \"m\", mapa = [\"vram_mb\"], intervalo = \"500ms\" }\n", []string{"gpu", "medidor", "intervalo", "1s"}},
		{"sem_uso desconhecido", base + "sem_uso = { dimensao = \"x\", limite = 5 }\n", []string{"gpu", "sem_uso", "x"}},
		{"exclusivo não booleano", base + "[recursos.gpu.tipos]\nt = { exclusivo = \"sim\" }\n", []string{"gpu", "tipos", "exclusivo"}},
		{"tipo com dimensao desconhecida", base + "[recursos.gpu.tipos]\nt = { ram = 1 }\n", []string{"gpu", "tipos", "ram"}},
		{"duracao invalida", "[agentes]\nsumido_apos = \"logo\"\n", []string{"line", "sumido_apos"}},
		{"dimensao nan", "[recursos.gpu]\ndimensoes = { vram_mb = nan }\nprioridades = [\"a\"]\n", []string{"gpu", "dimensoes", "vram_mb"}},
		{"dimensao infinita", "[recursos.gpu]\ndimensoes = { vram_mb = inf }\nprioridades = [\"a\"]\n", []string{"gpu", "dimensoes", "vram_mb"}},
		{"teto nan", base + "teto_medido = { vram_mb = nan }\n", []string{"gpu", "teto_medido"}},
		{"margem nan", base + "margem = { vram_mb = nan }\n", []string{"gpu", "margem"}},
	}
	for _, c := range casos {
		t.Run(c.nome, func(t *testing.T) {
			_, err := ParseProjectConfig([]byte(c.texto))
			if err == nil {
				t.Fatal("aceitou configuração inválida")
			}
			for _, q := range c.quer {
				if !strings.Contains(err.Error(), q) {
					t.Errorf("erro %q não contém %q", err, q)
				}
			}
		})
	}
}

func TestParseProcessoPadroes(t *testing.T) {
	c, err := ParseProjectConfig([]byte("[projeto]\nid = \"p\"\n"))
	if err != nil {
		t.Fatal(err)
	}
	s := c.Sprint
	if s.Escopo.Duration != 75*time.Minute || s.Congelamento.Duration != 60*time.Minute ||
		s.Corte.Duration != 30*time.Minute || s.Retro.Duration != 20*time.Minute {
		t.Fatalf("sprint = %+v", s)
	}
	if want := []string{"titulo", "area", "prioridade", "criterio_de_pronto"}; !slices.Equal(c.Itens.Obrigatorios, want) {
		t.Fatalf("obrigatorios = %v", c.Itens.Obrigatorios)
	}
	if c.Processo.OrfasApos.Duration != 12*time.Hour || c.Processo.LimiteParado["concluida"].Duration != 30*time.Minute ||
		c.Processo.LimiteParado["em_andamento"].Duration != 3*time.Hour {
		t.Fatalf("processo = %+v", c.Processo)
	}
}

func TestParseProcessoCompleto(t *testing.T) {
	texto := `[papeis]
nomes = { EXECUTOR = "FIXER" }
desligados = ["VALIDADOR"]
senior = ["FIXER_SENIOR"]

[itens.arquivos]
ref = "develop"
padrao = "T-*.md"
pastas = { proposta = "backlog/proposed", pronta = "backlog/todo", integrada = "backlog/done" }
mapa = { arquivos = "arquivos_provaveis" }
recurso_se = { usa_gpu = "gpu" }

[sprint.acordar]
corte = ["INTEGRADOR", "GERENTE"]
`
	c, err := ParseProjectConfig([]byte(texto))
	if err != nil {
		t.Fatal(err)
	}
	if c.Papeis.Nomes["EXECUTOR"] != "FIXER" || c.Itens.Arquivos == nil || c.Itens.Arquivos.Pastas["pronta"] != "backlog/todo" ||
		c.Itens.Arquivos.RecursoSe["usa_gpu"] != "gpu" || len(c.Sprint.Acordar["corte"]) != 2 {
		t.Fatalf("config = %+v", c)
	}
}

func TestParseProcessoInvalido(t *testing.T) {
	casos := []struct{ nome, texto, quer string }{
		{"desligar gerente", "[papeis]\ndesligados = [\"GERENTE\"]\n", "GERENTE"},
		{"papel desconhecido", "[papeis]\nnomes = { COZINHEIRO = \"X\" }\n", "COZINHEIRO"},
		{"nome repetido", "[papeis]\nnomes = { EXECUTOR = \"R\", REVISOR = \"R\" }\n", "\"R\""},
		{"offsets fora de ordem", "[sprint]\ncorte = \"90m\"\n", "sprint"},
		{"pasta com estado errado", "[itens.arquivos]\npastas = { em_andamento = \"x\" }\n", "em_andamento"},
		{"acordar papel desconhecido", "[sprint.acordar]\ncorte = [\"X\"]\n", "\"X\""},
		{"acordar cerimonia desconhecida", "[sprint.acordar]\nalmoco = [\"GERENTE\"]\n", "almoco"},
	}
	for _, c := range casos {
		t.Run(c.nome, func(t *testing.T) {
			_, err := ParseProjectConfig([]byte(c.texto))
			if err == nil || !strings.Contains(err.Error(), c.quer) {
				t.Fatalf("err = %v, quer conter %q", err, c.quer)
			}
		})
	}
}

func TestParseBriefELeitura(t *testing.T) {
	texto := "[brief]\nmax_tokens = 6000\nfatos = { EXECUTOR = [\"e2e.base*\"], TODOS = [\"gpu.zero\"] }\n\n" +
		"[[leitura]]\ncaminho = \"docs/ARQ.md\"\nresumo = \"arquitetura\"\nquando = \"antes de mexer no backend\"\npapeis = [\"EXECUTOR\"]\n"
	c, err := ParseProjectConfig([]byte(texto))
	if err != nil {
		t.Fatal(err)
	}
	if c.Brief.MaxTokens != 6000 || c.Brief.Fatos["EXECUTOR"][0] != "e2e.base*" || len(c.Leitura) != 1 || c.Leitura[0].Quando == "" {
		t.Fatalf("brief = %+v leitura = %+v", c.Brief, c.Leitura)
	}
	padrao, _ := ParseProjectConfig([]byte("[projeto]\nid = \"p\"\n"))
	if padrao.Brief.MaxTokens != 8000 {
		t.Fatalf("padrão = %d", padrao.Brief.MaxTokens)
	}
	for _, ruim := range []string{"[brief]\nfatos = { FIXER = [\"x\"] }\n", "[[leitura]]\nresumo = \"x\"\n", "[brief]\nmax_tokens = 10\n"} {
		if _, err := ParseProjectConfig([]byte(ruim)); err == nil {
			t.Errorf("aceitou %q", ruim)
		}
	}
}
