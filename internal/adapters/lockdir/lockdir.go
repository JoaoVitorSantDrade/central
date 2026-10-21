// Package lockdir lê e escreve o diretório de estado do time no formato do
// manual do LyricCut (.git/lyriccut-agents: canal/, equipe/, T-XXX/owner.json,
// e2e/, ids/, sprint.json). É só o formato dos arquivos; quem mapeia para a
// Central é o serviço (spec §13).
package lockdir

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Mensagem é um arquivo de canal/.
type Mensagem struct {
	ID              string `json:"id"`
	De              string `json:"de"`
	Para            string `json:"para"`
	Assunto         string `json:"assunto"`
	Ref             string `json:"ref"`
	Corpo           string `json:"corpo"`
	PrecisaResposta bool   `json:"precisa_resposta"`
	Responde        string `json:"responde"`
}

// Ponto é um arquivo de equipe/ (o "ponto" de uma vaga).
type Ponto struct {
	ID         string `json:"id"`
	Vaga       string `json:"vaga"`
	Papel      string `json:"papel"`
	Estado     string `json:"estado"`
	Tarefa     string `json:"tarefa"`
	Branch     string `json:"branch"`
	Atualizado string `json:"atualizado"`
	Nota       string `json:"nota"`
}

// Revisao é o veredito gravado numa reserva.
type Revisao struct {
	Veredito string `json:"veredito"`
	Ponta    string `json:"ponta"`
	Revisor  string `json:"revisor"`
}

// Reserva é um T-XXX/owner.json ou M-XXX/owner.json.
type Reserva struct {
	Tarefa      string   `json:"tarefa"`
	Agente      string   `json:"agente"`
	Branch      string   `json:"branch"`
	Worktree    string   `json:"worktree"`
	Estado      string   `json:"estado"`
	Inicio      string   `json:"inicio"`
	Atualizado  string   `json:"atualizado"`
	CommitFinal string   `json:"commit_final"`
	Marco       Flexivel `json:"marco"`
	Revisao     *Revisao `json:"revisao"`
	Nota        string   `json:"nota"`
}

// Flexivel aceita número ou texto (o manual grava "3" e 3).
type Flexivel string

// UnmarshalJSON lê número, texto ou null.
func (f *Flexivel) UnmarshalJSON(b []byte) error {
	b = bytes.TrimSpace(b)
	if string(b) == "null" {
		*f = ""
		return nil
	}
	var s string
	if err := json.Unmarshal(b, &s); err == nil {
		*f = Flexivel(s)
		return nil
	}
	*f = Flexivel(string(b))
	return nil
}

// Int devolve o valor como inteiro (0 se não for número).
func (f Flexivel) Int() int {
	n, _ := strconv.Atoi(strings.TrimSpace(string(f)))
	return n
}

// Vaga é uma linha da equipe do sprint.json.
type Vaga struct {
	Vaga  string `json:"vaga"`
	Papel string `json:"papel"`
	ID    string `json:"id"`
}

// Sprint é o sprint.json.
type Sprint struct {
	Sprint       string `json:"sprint"`
	Inicio       string `json:"inicio"`
	Checkin      string `json:"checkin"`
	EscopoVersao string `json:"escopo_versao"`
	Congelamento string `json:"congelamento"`
	Corte        string `json:"corte"`
	Retro        string `json:"retro"`
	Fim          string `json:"fim"`
	Equipe       []Vaga `json:"equipe"`
}

// Snapshot é o diretório inteiro lido.
type Snapshot struct {
	Mensagens []Mensagem
	Pontos    []Ponto
	Reservas  []Reserva
	// E2E guarda cada registro de e2e (o nome do arquivo sem .json → conteúdo).
	E2E    map[string]json.RawMessage
	IDs    []string
	Sprint *Sprint
	// Erros lista os arquivos que não puderam ser lidos (o resto segue).
	Erros []string
}

// LayoutLocal é o formato dos horários do manual (hora local, sem fuso).
const LayoutLocal = "2006-01-02T15:04"

// ParseLocal lê um horário do manual na hora local (aceita também com segundos ou RFC 3339).
func ParseLocal(s string) (time.Time, bool) {
	s = strings.TrimSpace(s)
	for _, l := range []string{LayoutLocal, "2006-01-02T15:04:05", time.RFC3339} {
		if t, err := time.ParseInLocation(l, s, time.Local); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

// HoraDoID lê o horário de um id de mensagem AAAAMMDDTHHMMSS-… (hora local).
func HoraDoID(id string) (time.Time, bool) {
	if len(id) < 15 {
		return time.Time{}, false
	}
	t, err := time.ParseInLocation("20060102T150405", id[:15], time.Local)
	return t, err == nil
}

func lerJSON(path string, v any) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	b = bytes.TrimPrefix(b, []byte("\xef\xbb\xbf"))
	return json.Unmarshal(b, v)
}

var reItem = regexp.MustCompile(`^[TM]-\d+$`)

// Load lê o diretório. Arquivos quebrados vão para Snapshot.Erros.
func Load(dir string) (Snapshot, error) {
	st, err := os.Stat(dir)
	if err != nil {
		return Snapshot{}, err
	}
	if !st.IsDir() {
		return Snapshot{}, fmt.Errorf("%s não é uma pasta", dir)
	}
	s := Snapshot{E2E: map[string]json.RawMessage{}}
	erro := func(path string, err error) {
		s.Erros = append(s.Erros, fmt.Sprintf("%s: %v", filepath.Base(path), err))
	}

	canal, _ := filepath.Glob(filepath.Join(dir, "canal", "*.json"))
	sort.Strings(canal)
	for _, p := range canal {
		var m Mensagem
		if err := lerJSON(p, &m); err != nil {
			erro(p, err)
			continue
		}
		if m.ID == "" {
			m.ID = strings.TrimSuffix(filepath.Base(p), ".json")
		}
		s.Mensagens = append(s.Mensagens, m)
	}
	equipe, _ := filepath.Glob(filepath.Join(dir, "equipe", "*.json"))
	sort.Strings(equipe)
	for _, p := range equipe {
		var pt Ponto
		if err := lerJSON(p, &pt); err != nil {
			erro(p, err)
			continue
		}
		if pt.ID == "" {
			pt.ID = strings.TrimSuffix(filepath.Base(p), ".json")
		}
		s.Pontos = append(s.Pontos, pt)
	}
	ents, _ := os.ReadDir(dir)
	for _, e := range ents {
		if !e.IsDir() || !reItem.MatchString(e.Name()) {
			continue
		}
		p := filepath.Join(dir, e.Name(), "owner.json")
		var r Reserva
		if err := lerJSON(p, &r); err != nil {
			if !errors.Is(err, os.ErrNotExist) {
				erro(p, err)
			}
			continue
		}
		if r.Tarefa == "" {
			r.Tarefa = e.Name()
		}
		s.Reservas = append(s.Reservas, r)
	}
	e2es, _ := filepath.Glob(filepath.Join(dir, "e2e", "*.json"))
	sort.Strings(e2es)
	for _, p := range e2es {
		var raw json.RawMessage
		if err := lerJSON(p, &raw); err != nil {
			erro(p, err)
			continue
		}
		s.E2E[strings.TrimSuffix(filepath.Base(p), ".json")] = raw
	}
	ids, _ := os.ReadDir(filepath.Join(dir, "ids"))
	for _, e := range ids {
		s.IDs = append(s.IDs, e.Name())
	}
	sort.Strings(s.IDs)
	var sp Sprint
	if err := lerJSON(filepath.Join(dir, "sprint.json"), &sp); err == nil {
		s.Sprint = &sp
	} else if !errors.Is(err, os.ErrNotExist) {
		erro(filepath.Join(dir, "sprint.json"), err)
	}
	return s, nil
}

func escreverJSON(path string, v any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(b, '\n'), 0o644)
}

var reNome = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

// Export escreve o snapshot em dir no formato do manual. Não apaga nada que já esteja lá.
func Export(dir string, s Snapshot) error {
	for _, m := range s.Mensagens {
		nome := reNome.ReplaceAllString(m.ID, "_") + ".json"
		if err := escreverJSON(filepath.Join(dir, "canal", nome), m); err != nil {
			return err
		}
	}
	for _, p := range s.Pontos {
		if err := escreverJSON(filepath.Join(dir, "equipe", reNome.ReplaceAllString(p.ID, "_")+".json"), p); err != nil {
			return err
		}
	}
	for _, r := range s.Reservas {
		if err := escreverJSON(filepath.Join(dir, reNome.ReplaceAllString(r.Tarefa, "_"), "owner.json"), r); err != nil {
			return err
		}
	}
	for nome, raw := range s.E2E {
		var v any
		if err := json.Unmarshal(raw, &v); err != nil {
			return fmt.Errorf("e2e %s: %w", nome, err)
		}
		if err := escreverJSON(filepath.Join(dir, "e2e", reNome.ReplaceAllString(nome, "_")+".json"), v); err != nil {
			return err
		}
	}
	for _, id := range s.IDs {
		if err := os.MkdirAll(filepath.Join(dir, "ids", reNome.ReplaceAllString(id, "_")), 0o755); err != nil {
			return err
		}
	}
	if s.Sprint != nil {
		return escreverJSON(filepath.Join(dir, "sprint.json"), s.Sprint)
	}
	return nil
}
