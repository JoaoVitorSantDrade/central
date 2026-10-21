package files

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

var ctx = context.Background()

func TestParseFrontmatterDeTarefa(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("testdata", "T-094.md"))
	if err != nil {
		t.Fatal(err)
	}
	m, err := ParseFrontmatter(b)
	if err != nil {
		t.Fatal(err)
	}
	if m["id"] != "T-094" || m["prioridade"] != "P0" {
		t.Fatalf("m = %v", m)
	}
	if a, ok := m["arquivos_provaveis"].([]any); !ok || len(a) != 2 {
		t.Fatalf("lista = %#v", m["arquivos_provaveis"])
	}
	if amb, ok := m["ambiente"].(map[string]any); !ok || amb["branch"] != "develop" {
		t.Fatalf("mapa aninhado = %#v", m["ambiente"])
	}
	if c, _ := m["criterio_de_pronto"].(string); c == "" {
		t.Fatal("texto dobrado (>) não lido")
	}
}

func TestParseFrontmatterInvalido(t *testing.T) {
	for _, s := range []string{"sem frontmatter", "---\nid: x\n", "---\nid: [quebrado\n---\n"} {
		if _, err := ParseFrontmatter([]byte(s)); err == nil {
			t.Errorf("%q aceito", s)
		}
	}
}

func write(t *testing.T, path, s string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(s), 0o644); err != nil {
		t.Fatal(err)
	}
}

func lyricSource(dir, ref string) Source {
	return Source{Dir: dir, Ref: ref, Padrao: "T-*.md",
		Pastas:    map[string]string{"proposta": "backlog/proposed", "pronta": "backlog/todo", "integrada": "backlog/done"},
		Mapa:      map[string]string{"arquivos": "arquivos_provaveis"},
		RecursoSe: map[string]string{"usa_gpu": "gpu"}}
}

func TestReadPastaDeTrabalho(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "backlog/todo/T-001-a.md"), "---\nid: T-001\ntitulo: a\nprioridade: P1\narquivos_provaveis: [x.go]\nusa_gpu: true\ncriterio_de_pronto: passa\n---\ncorpo\n")
	write(t, filepath.Join(dir, "backlog/done/T-002-b.md"), "---\nid: T-002\ntitulo: b\nbloqueio: não reproduz\n---\n")
	write(t, filepath.Join(dir, "backlog/todo/README.md"), "não é tarefa")
	write(t, filepath.Join(dir, "backlog/todo/T-003-ruim.md"), "sem frontmatter")
	head, achados, invalidos, err := Read(ctx, lyricSource(dir, ""))
	if err != nil {
		t.Fatal(err)
	}
	if head == "" || len(achados) != 2 || len(invalidos) != 1 || invalidos[0].Caminho != "backlog/todo/T-003-ruim.md" {
		t.Fatalf("head %q achados %+v invalidos %+v", head, achados, invalidos)
	}
	por := map[string]int{}
	for i, f := range achados {
		por[f.In.ID] = i
	}
	a := achados[por["T-001"]]
	if a.Estado != "pronta" || a.In.Recurso != "gpu" || len(a.In.Arquivos) != 1 || a.In.Campos["criterio_de_pronto"] != "passa" {
		t.Fatalf("T-001 = %+v", a)
	}
	if _, sobrou := a.In.Campos["arquivos_provaveis"]; sobrou {
		t.Fatal("campo mapeado ficou também em campos")
	}
	if b := achados[por["T-002"]]; b.Estado != "integrada" || b.Bloqueio != "não reproduz" {
		t.Fatalf("T-002 = %+v", b)
	}
	// head muda quando um arquivo muda
	write(t, filepath.Join(dir, "backlog/todo/T-001-a.md"), "---\nid: T-001\ntitulo: a2\n---\n")
	head2, _, _, _ := Read(ctx, lyricSource(dir, ""))
	if head2 == head {
		t.Fatal("head não mudou")
	}
}

func gitCmd(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func TestReadGit(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git indisponível")
	}
	dir := t.TempDir()
	gitCmd(t, dir, "init", "-q", "-b", "main")
	write(t, filepath.Join(dir, "backlog/todo/T-001-a.md"), "---\nid: T-001\ntitulo: na main\n---\n")
	gitCmd(t, dir, "add", ".")
	gitCmd(t, dir, "commit", "-q", "-m", "a")
	gitCmd(t, dir, "switch", "-q", "-c", "develop")
	write(t, filepath.Join(dir, "backlog/todo/T-002-b.md"), "---\nid: T-002\ntitulo: só na develop\n---\n")
	gitCmd(t, dir, "add", ".")
	gitCmd(t, dir, "commit", "-q", "-m", "b")
	gitCmd(t, dir, "switch", "-q", "main")
	// uma mudança não commitada na árvore de trabalho não conta
	write(t, filepath.Join(dir, "backlog/todo/T-003-c.md"), "---\nid: T-003\ntitulo: solta\n---\n")
	head, achados, _, err := Read(ctx, lyricSource(dir, "develop"))
	if err != nil {
		t.Fatal(err)
	}
	ids := map[string]bool{}
	for _, f := range achados {
		ids[f.In.ID] = true
	}
	if len(head) < 40 || len(achados) != 2 || !ids["T-001"] || !ids["T-002"] {
		t.Fatalf("head %q achados %+v", head, achados)
	}
	if _, _, _, err := Read(ctx, lyricSource(dir, "nao-existe")); err == nil {
		t.Fatal("branch inexistente aceita")
	}
}
