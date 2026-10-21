package config

import (
	"strings"
	"testing"
	"time"
)

const comExtensoes = `
[projeto]
id = "lyriccut"

[[extensoes]]
nome    = "exemplos"
comando = "python -I .central/ext/exemplos.py"
timeout = "10m"
cli     = ["listar", "importar", "status"]
aba     = { titulo = "Exemplos", visao = "galeria", acao = "listar" }
entrada = { pasta = "artifacts/entrada", ao_chegar = "importar" }

[[extensoes]]
nome    = "e2e"
comando = "python .central/ext/e2e.py"
cli     = ["registrar", "achar"]

[acordar]
exemplo-novo = ["PESQUISADOR", "planejador"]
`

func TestExtensoesValidas(t *testing.T) {
	c, err := ParseProjectConfig([]byte(comExtensoes))
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Extensoes) != 2 {
		t.Fatalf("extensoes = %+v", c.Extensoes)
	}
	ex := c.Extensoes[0]
	if ex.Nome != "exemplos" || ex.Timeout.Duration != 10*time.Minute || ex.Aba == nil || ex.Aba.Visao != "galeria" ||
		ex.Entrada == nil || ex.Entrada.Pasta != "artifacts/entrada" || ex.Entrada.AoChegar != "importar" {
		t.Fatalf("exemplos = %+v", ex)
	}
	if e2e := c.Extensoes[1]; e2e.Timeout.Duration != PadraoExtTimeout || e2e.Aba != nil || e2e.Entrada != nil {
		t.Fatalf("e2e = %+v", e2e)
	}
	if got := c.Acordar["exemplo-novo"]; len(got) != 2 || got[1] != "PLANEJADOR" {
		t.Fatalf("acordar = %v (papéis em maiúsculas)", c.Acordar)
	}
	acoes := ex.Acoes()
	for _, a := range []string{"listar", "importar", "status"} {
		if !acoes[a] {
			t.Fatalf("acoes = %v", acoes)
		}
	}
	if ext, ok := c.Extensao("e2e"); !ok || ext.Nome != "e2e" {
		t.Fatal("Extensao(e2e) não achou")
	}
}

func TestExtensaoInvalida(t *testing.T) {
	base := "[projeto]\nid = \"p\"\n\n[[extensoes]]\n"
	casos := []struct{ nome, corpo, quer string }{
		{"sem nome", `comando = "x"`, "nome"},
		{"nome ruim", "nome = \"Ex mp\"\ncomando = \"x\"", "nome"},
		{"nome reservado", "nome = \"item\"\ncomando = \"x\"", "reservado"},
		{"sem comando", `nome = "a"`, "comando"},
		{"timeout curto", "nome = \"a\"\ncomando = \"x\"\ntimeout = \"10ms\"", "timeout"},
		{"timeout longo", "nome = \"a\"\ncomando = \"x\"\ntimeout = \"2h\"", "timeout"},
		{"visao ruim", "nome = \"a\"\ncomando = \"x\"\naba = { titulo = \"A\", visao = \"pizza\", acao = \"l\" }", "visao"},
		{"aba sem acao", "nome = \"a\"\ncomando = \"x\"\naba = { titulo = \"A\", visao = \"tabela\" }", "acao"},
		{"entrada sem ao_chegar", "nome = \"a\"\ncomando = \"x\"\nentrada = { pasta = \"in\" }", "ao_chegar"},
		{"entrada absoluta", "nome = \"a\"\ncomando = \"x\"\nentrada = { pasta = \"/tmp/in\", ao_chegar = \"i\" }", "pasta"},
		{"entrada com ..", "nome = \"a\"\ncomando = \"x\"\nentrada = { pasta = \"../in\", ao_chegar = \"i\" }", "pasta"},
		{"acao ruim", "nome = \"a\"\ncomando = \"x\"\ncli = [\"Bad Acao\"]", "acao"},
		{"repetida", "nome = \"a\"\ncomando = \"x\"\n\n[[extensoes]]\nnome = \"a\"\ncomando = \"y\"", "repetida"},
	}
	for _, c := range casos {
		_, err := ParseProjectConfig([]byte(base + c.corpo))
		if err == nil || !strings.Contains(err.Error(), c.quer) {
			t.Errorf("%s: err = %v, quer %q", c.nome, err, c.quer)
		}
	}
}

func TestAcordarInvalido(t *testing.T) {
	for _, corpo := range []string{"[acordar]\n\"Tipo Ruim\" = [\"X\"]", "[acordar]\nnovo = []"} {
		if _, err := ParseProjectConfig([]byte("[projeto]\nid = \"p\"\n" + corpo)); err == nil || !strings.Contains(err.Error(), "acordar") {
			t.Errorf("%q: err = %v", corpo, err)
		}
	}
}

func TestNomesDoProjetoNaLeituraEnosFatos(t *testing.T) {
	c, err := ParseProjectConfig([]byte(`[projeto]
id = "p"

[papeis]
nomes = { EXECUTOR = "FIXER" }

[brief.fatos]
FIXER = ["e2e.base*"]

[sprint.acordar]
corte = ["FIXER", "INTEGRADOR"]

[[leitura]]
caminho = "docs/x.md"
papeis  = ["FIXER", "REVISOR"]
`))
	if err != nil {
		t.Fatal(err)
	}
	if got := c.Leitura[0].Papeis; got[0] != "EXECUTOR" || got[1] != "REVISOR" {
		t.Fatalf("leitura.papeis = %v, quer os canônicos", got)
	}
	if _, ok := c.Brief.Fatos["EXECUTOR"]; !ok || len(c.Brief.Fatos) != 1 {
		t.Fatalf("brief.fatos = %v, quer a chave canônica", c.Brief.Fatos)
	}
	if got := c.Sprint.Acordar["corte"]; got[0] != "EXECUTOR" {
		t.Fatalf("sprint.acordar = %v", got)
	}
}
