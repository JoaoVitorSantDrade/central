// Package files lê itens de trabalho de arquivos markdown com frontmatter YAML,
// numa pasta de trabalho ou numa branch git (spec §6.5). Nunca escreve nos
// arquivos: só produz o que o processo sincroniza.
package files

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"

	"github.com/JoaoVitorSantDrade/central/internal/process"
)

// Source diz de onde ler os itens.
type Source struct {
	// Dir é a pasta do repositório (ou da árvore de trabalho).
	Dir string
	// Ref é a branch ou o commit; vazio lê a pasta de trabalho.
	Ref string
	// Padrao filtra o nome do arquivo (ex.: "T-*.md").
	Padrao string
	// Pastas liga cada estado (proposta, pronta, integrada) a uma pasta relativa.
	Pastas map[string]string
	// Mapa dá apelidos a campos canônicos (ex.: arquivos = "arquivos_provaveis").
	Mapa map[string]string
	// RecursoSe liga um campo booleano a um recurso (ex.: usa_gpu = "gpu").
	RecursoSe map[string]string
}

// gitTimeout limita cada chamada ao git.
const gitTimeout = 30 * time.Second

// ParseFrontmatter lê o bloco YAML entre as duas primeiras linhas "---".
func ParseFrontmatter(b []byte) (map[string]any, error) {
	b = bytes.TrimPrefix(b, []byte("\xef\xbb\xbf"))
	text := strings.ReplaceAll(string(b), "\r\n", "\n")
	if !strings.HasPrefix(text, "---\n") {
		return nil, errors.New("sem frontmatter (o arquivo precisa começar com ---)")
	}
	rest := text[4:]
	end := strings.Index(rest, "\n---")
	if end < 0 {
		return nil, errors.New("frontmatter sem o --- de fechamento")
	}
	m := map[string]any{}
	if err := yaml.Unmarshal([]byte(rest[:end]), &m); err != nil {
		return nil, fmt.Errorf("YAML inválido: %w", err)
	}
	return m, nil
}

type arquivo struct {
	estado, caminho string
	conteudo        []byte
}

// Read lê todos os arquivos das pastas e devolve o "head" (o que muda quando
// algum arquivo muda), os itens achados e os arquivos inválidos.
func Read(ctx context.Context, s Source) (string, []process.Found, []process.Invalid, error) {
	if s.Padrao == "" {
		s.Padrao = "*.md"
	}
	var arqs []arquivo
	var head string
	var err error
	if s.Ref == "" {
		arqs, head, err = lerPasta(s)
	} else {
		arqs, head, err = lerGit(ctx, s)
	}
	if err != nil {
		return "", nil, nil, err
	}
	var achados []process.Found
	var invalidos []process.Invalid
	for _, a := range arqs {
		sum := sha256.Sum256(a.conteudo)
		hash := hex.EncodeToString(sum[:8])
		f, err := s.parse(a)
		if err != nil {
			invalidos = append(invalidos, process.Invalid{Caminho: a.caminho, Hash: hash, Erro: err.Error()})
			continue
		}
		f.Hash = hash
		achados = append(achados, f)
	}
	return head, achados, invalidos, nil
}

func estadosOrdenados(p map[string]string) []string {
	es := make([]string, 0, len(p))
	for e := range p {
		es = append(es, e)
	}
	sort.Strings(es)
	return es
}

func lerPasta(s Source) ([]arquivo, string, error) {
	var arqs []arquivo
	h := sha256.New()
	for _, estado := range estadosOrdenados(s.Pastas) {
		dir := filepath.Join(s.Dir, filepath.FromSlash(s.Pastas[estado]))
		ents, err := os.ReadDir(dir)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, "", err
		}
		for _, ent := range ents {
			if ent.IsDir() {
				continue
			}
			if ok, _ := path.Match(s.Padrao, ent.Name()); !ok {
				continue
			}
			b, err := os.ReadFile(filepath.Join(dir, ent.Name()))
			if err != nil {
				return nil, "", err
			}
			caminho := path.Join(s.Pastas[estado], ent.Name())
			fmt.Fprintf(h, "%s\x00%d\x00", caminho, len(b))
			h.Write(b)
			arqs = append(arqs, arquivo{estado: estado, caminho: caminho, conteudo: b})
		}
	}
	return arqs, hex.EncodeToString(h.Sum(nil)), nil
}

func git(ctx context.Context, dir string, stdin io.Reader, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, gitTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	cmd.Stdin = stdin
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git %s: %v: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return out, nil
}

func lerGit(ctx context.Context, s Source) ([]arquivo, string, error) {
	out, err := git(ctx, s.Dir, nil, "rev-parse", "--verify", s.Ref+"^{commit}")
	if err != nil {
		return nil, "", err
	}
	head := strings.TrimSpace(string(out))
	estadoDe := map[string]string{}
	var pastas []string
	for _, estado := range estadosOrdenados(s.Pastas) {
		p := strings.Trim(s.Pastas[estado], "/")
		estadoDe[p] = estado
		pastas = append(pastas, p)
	}
	if len(pastas) == 0 {
		return nil, head, nil
	}
	out, err = git(ctx, s.Dir, nil, append([]string{"ls-tree", "-r", "--name-only", head, "--"}, pastas...)...)
	if err != nil {
		return nil, "", err
	}
	var caminhos []string
	for _, linha := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		linha = strings.TrimSpace(linha)
		if linha == "" {
			continue
		}
		if ok, _ := path.Match(s.Padrao, path.Base(linha)); ok {
			caminhos = append(caminhos, linha)
		}
	}
	if len(caminhos) == 0 {
		return nil, head, nil
	}
	var req bytes.Buffer
	for _, c := range caminhos {
		fmt.Fprintf(&req, "%s:%s\n", head, c)
	}
	out, err = git(ctx, s.Dir, &req, "cat-file", "--batch")
	if err != nil {
		return nil, "", err
	}
	r := bufio.NewReader(bytes.NewReader(out))
	var arqs []arquivo
	for _, c := range caminhos {
		cab, err := r.ReadString('\n')
		if err != nil {
			return nil, "", fmt.Errorf("git cat-file: resposta curta para %s", c)
		}
		campos := strings.Fields(cab)
		if len(campos) != 3 {
			return nil, "", fmt.Errorf("git cat-file: %s: %s", c, strings.TrimSpace(cab))
		}
		n, err := strconv.Atoi(campos[2])
		if err != nil {
			return nil, "", fmt.Errorf("git cat-file: tamanho inválido para %s", c)
		}
		b := make([]byte, n)
		if _, err := io.ReadFull(r, b); err != nil {
			return nil, "", err
		}
		r.ReadByte() // o \n depois do conteúdo
		arqs = append(arqs, arquivo{estado: estadoDe[path.Dir(c)], caminho: c, conteudo: b})
	}
	return arqs, head, nil
}

// campo lê um campo canônico, pelo apelido se houver.
func (s Source) campo(m map[string]any, nome string, usados map[string]bool) (any, bool) {
	chave := nome
	if alias, ok := s.Mapa[nome]; ok && alias != "" {
		chave = alias
	}
	v, ok := m[chave]
	if ok {
		usados[chave] = true
	}
	return v, ok
}

func texto(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return strings.TrimSpace(x)
	default:
		return strings.TrimSpace(fmt.Sprint(x))
	}
}

func booleano(v any) bool {
	switch x := v.(type) {
	case bool:
		return x
	case string:
		switch strings.ToLower(strings.TrimSpace(x)) {
		case "true", "sim", "s", "yes", "1":
			return true
		}
	}
	return false
}

func listaDe(v any) []string {
	switch x := v.(type) {
	case []any:
		out := []string{}
		for _, e := range x {
			if t := texto(e); t != "" {
				out = append(out, t)
			}
		}
		return out
	case string:
		out := []string{}
		for _, p := range strings.Split(x, ",") {
			if p = strings.TrimSpace(p); p != "" {
				out = append(out, p)
			}
		}
		return out
	}
	return []string{}
}

// jsonavel converte valores do YAML para tipos que viram JSON (mapas com chave texto).
func jsonavel(v any) any {
	switch x := v.(type) {
	case map[string]any:
		out := map[string]any{}
		for k, e := range x {
			out[k] = jsonavel(e)
		}
		return out
	case map[any]any:
		out := map[string]any{}
		for k, e := range x {
			out[fmt.Sprint(k)] = jsonavel(e)
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = jsonavel(e)
		}
		return out
	case time.Time:
		return x.Format(time.RFC3339)
	}
	return v
}

func (s Source) parse(a arquivo) (process.Found, error) {
	m, err := ParseFrontmatter(a.conteudo)
	if err != nil {
		return process.Found{}, err
	}
	usados := map[string]bool{}
	get := func(nome string) any { v, _ := s.campo(m, nome, usados); return v }
	id := texto(get("id"))
	if id == "" {
		return process.Found{}, errors.New("frontmatter sem id")
	}
	in := process.ItemInput{
		ID: id, Titulo: texto(get("titulo")), Area: texto(get("area")), Prioridade: texto(get("prioridade")),
		Categoria: texto(get("tipo")), Sprint: texto(get("sprint")), Arquivos: listaDe(get("arquivos")),
		Recurso: texto(get("recurso")), Folga: booleano(get("folga")), Bloqueante: booleano(get("bloqueante")),
		Visivel: booleano(get("visivel_ao_usuario")),
	}
	if in.Titulo == "" {
		in.Titulo = id
	}
	for campo, recurso := range s.RecursoSe {
		if v, ok := m[campo]; ok {
			usados[campo] = true
			if in.Recurso == "" && booleano(v) {
				in.Recurso = recurso
			}
		}
	}
	bloqueio := texto(get("bloqueio"))
	campos := map[string]any{}
	for k, v := range m {
		if !usados[k] {
			campos[k] = jsonavel(v)
		}
	}
	in.Campos = campos
	return process.Found{Estado: a.estado, Caminho: a.caminho, In: in, Bloqueio: bloqueio}, nil
}
