package cli_test

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/JoaoVitorSantDrade/central/internal/config"
)

func gitRun(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func tarefa(id, prioridade string) string {
	return fmt.Sprintf("---\nid: %s\ntitulo: tarefa %s\narea: core\nprioridade: %s\ncriterio_de_pronto: teste passa\narquivos_provaveis: [internal/%s.go]\n---\ncorpo\n", id, id, prioridade, strings.ToLower(id))
}

// esperarAte repete f até ela devolver true ou o prazo acabar.
func esperarAte(t *testing.T, prazo time.Duration, o string, f func() bool) {
	t.Helper()
	fim := time.Now().Add(prazo)
	for time.Now().Before(fim) {
		if f() {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("esperando %s: não aconteceu em %v", o, prazo)
}

func TestCenarioSprint(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git indisponível")
	}
	repo := filepath.Join(t.TempDir(), "repo")
	if err := os.MkdirAll(filepath.Join(repo, "backlog", "todo"), 0o755); err != nil {
		t.Fatal(err)
	}
	gitRun(t, repo, "init", "-q", "-b", "develop")
	os.MkdirAll(filepath.Join(repo, "backlog", "done"), 0o755)
	os.WriteFile(filepath.Join(repo, "backlog", "todo", "T-001-a.md"), []byte(tarefa("T-001", "P0")), 0o644)
	os.WriteFile(filepath.Join(repo, "backlog", "todo", "T-002-b.md"), []byte(tarefa("T-002", "P2")), 0o644)
	gitRun(t, repo, "add", ".")
	gitRun(t, repo, "commit", "-q", "-m", "backlog")

	cfg := fmt.Sprintf(`
[papeis]
nomes = { EXECUTOR = "FIXER" }
desligados = ["VALIDADOR"]

[itens.arquivos]
ref = "develop"
repo = %q
padrao = "T-*.md"
intervalo = "1s"
pastas = { pronta = "backlog/todo", integrada = "backlog/done" }
mapa = { arquivos = "arquivos_provaveis" }
`, repo)

	e := newEnv(t)
	dirs := map[string]string{}
	for _, v := range []struct{ vaga, papel string }{{"G", "GERENTE"}, {"P", "PLANEJADOR"}, {"F1", "FIXER"}, {"R", "REVISOR"}, {"I", "INTEGRADOR"}} {
		d := e.project(v.vaga)
		f, err := os.OpenFile(filepath.Join(d, config.FileName), os.O_APPEND|os.O_WRONLY, 0)
		if err != nil {
			t.Fatal(err)
		}
		f.WriteString(cfg)
		f.Close()
		dirs[v.vaga] = d
		e.ok(d, "entrar", "--vaga", v.vaga, "--papel", v.papel)
	}

	// 1. o adaptador importa as duas tarefas da develop
	esperarAte(t, 15*time.Second, "o adaptador importar o backlog", func() bool {
		l := e.ok(dirs["P"], "item", "listar", "--estado", "pronta")
		return len(l["itens"].([]any)) == 2
	})

	// 2. o GERENTE abre a sprint; todos recebem a abertura
	e.ok(dirs["G"], "sprint", "abrir", "--expediente", expedienteDeAgora())
	if w := e.ok(dirs["F1"], "esperar", "--ate", "15s"); w["motivo"] != "cerimonia" {
		t.Fatalf("FIXER esperar = %v", w)
	}
	// todos leem a abertura antes do trabalho começar (a caixa marca tudo como lido)
	esperarAte(t, 10*time.Second, "a abertura chegar a todos", func() bool {
		for _, v := range []string{"R", "I"} {
			if w := e.ok(dirs[v], "esperar", "--ate", "1s"); w["motivo"] != "cerimonia" {
				return false
			}
		}
		return true
	})
	for _, v := range []string{"F1", "R", "I"} {
		e.ok(dirs[v], "caixa")
	}

	// 3. o FIXER pega a P0 pela proxima, reserva e conclui
	m := e.ok(dirs["F1"], "proxima")
	if it, _ := m["item"].(map[string]any); it == nil || it["id"] != "T-001" {
		t.Fatalf("proxima = %v", m)
	}
	e.ok(dirs["F1"], "reservar", "T-001")
	e.ok(dirs["F1"], "concluir", "T-001", "--ponta", "abc123")

	// 4. o REVISOR acorda pela conclusão, revisa e aprova
	if w := e.ok(dirs["R"], "esperar", "--ate", "10s"); w["motivo"] != "item" {
		t.Fatalf("REVISOR esperar = %v", w)
	}
	e.ok(dirs["R"], "revisar", "T-001")
	e.ok(dirs["R"], "aprovar", "T-001", "--ponta", "abc123")

	// 5. o INTEGRADOR acorda e integra
	if w := e.ok(dirs["I"], "esperar", "--ate", "10s"); w["motivo"] != "item" {
		t.Fatalf("INTEGRADOR esperar = %v", w)
	}
	e.ok(dirs["I"], "integrar", "T-001", "--commit", "c0ffee")

	// 6. o arquivo vai para done na develop: o item segue integrado, com a fonte nova
	gitRun(t, repo, "mv", "backlog/todo/T-001-a.md", "backlog/done/T-001-a.md")
	gitRun(t, repo, "commit", "-q", "-m", "done")
	esperarAte(t, 15*time.Second, "o adaptador ver o done", func() bool {
		it := e.ok(dirs["P"], "item", "ver", "T-001")["item"].(map[string]any)
		return it["estado"] == "integrada" && strings.Contains(it["fonte"].(string), "backlog/done")
	})

	// 7. métricas com a entrega e as latências
	mt := e.ok(dirs["G"], "metricas")["metricas"].(map[string]any)
	lat := mt["latencias"].(map[string]any)["concluida_integrada"].(map[string]any)
	if mt["integradas"].(float64) != 1 || lat["n"].(float64) != 1 {
		t.Fatalf("metricas = %v", mt)
	}

	// 8. saúde sem alertas
	if s := e.ok(dirs["G"], "saude")["saude"].(map[string]any); len(s["alertas"].([]any)) != 0 {
		t.Fatalf("saude = %v", s)
	}
}
