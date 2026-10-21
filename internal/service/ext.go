package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/JoaoVitorSantDrade/central/internal/config"
	"github.com/JoaoVitorSantDrade/central/internal/core"
	"github.com/JoaoVitorSantDrade/central/internal/ext"
)

// ErrExtensao marca a falha de uma extensão (prazo, saída, resposta ou ok=false):
// é erro da chamada, nunca do daemon.
var ErrExtensao = errors.New("a extensão falhou")

// ExtInfo é o que o painel e a CLI sabem de uma extensão declarada.
type ExtInfo struct {
	Nome    string             `json:"nome"`
	Acoes   []string           `json:"acoes"`
	CLI     []string           `json:"cli"`
	Aba     *config.AbaSec     `json:"aba,omitempty"`
	Entrada *config.EntradaSec `json:"entrada,omitempty"`
}

// ExtResultado é a resposta de uma chamada bem-sucedida.
type ExtResultado struct {
	Dados      json.RawMessage `json:"dados"`
	Formulario json.RawMessage `json:"formulario,omitempty"`
	Eventos    []ext.Evento    `json:"eventos"`
	MS         int64           `json:"ms"`
}

// extLocks serializa as chamadas de uma extensão de um projeto.
type extLocks struct {
	mu sync.Mutex
	m  map[string]*sync.Mutex
}

func (l *extLocks) get(chave string) *sync.Mutex {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.m == nil {
		l.m = map[string]*sync.Mutex{}
	}
	m, ok := l.m[chave]
	if !ok {
		m = &sync.Mutex{}
		l.m[chave] = m
	}
	return m
}

func (p *project) config() config.ProjectConfig {
	p.cfgMu.Lock()
	defer p.cfgMu.Unlock()
	return p.cfg
}

// Extensoes lista as extensões declaradas no central.toml vigente do projeto.
func (s *Service) Extensoes(ctx context.Context, projetoID string) ([]ExtInfo, error) {
	p, err := s.project(ctx, projetoID)
	if err != nil {
		return nil, err
	}
	out := []ExtInfo{}
	for _, e := range p.config().Extensoes {
		acoes := []string{}
		for a := range e.Acoes() {
			acoes = append(acoes, a)
		}
		sort.Strings(acoes)
		cli := slices.Clone(e.CLI)
		if cli == nil {
			cli = []string{}
		}
		out = append(out, ExtInfo{Nome: e.Nome, Acoes: acoes, CLI: cli, Aba: e.Aba, Entrada: e.Entrada})
	}
	return out, nil
}

// Extensao chama a ação da extensão como a vaga da pasta, ou como o HUMANO
// com pasta vazia. Só as ações declaradas (cli, aba, entrada) são chamáveis.
func (s *Service) Extensao(ctx context.Context, projetoID, pasta, nome, acao string, args map[string]any) (ExtResultado, error) {
	p, err := s.project(ctx, projetoID)
	if err != nil {
		return ExtResultado{}, err
	}
	quem := core.Humano
	var agente *ext.Agente
	if pasta != "" {
		_, a, err := s.agent(ctx, projetoID, pasta)
		if err != nil {
			return ExtResultado{}, err
		}
		quem, agente = a.Vaga, &ext.Agente{Vaga: a.Vaga, Papel: a.Papel}
	}
	return s.chamarExtensao(ctx, p, projetoID, quem, agente, nome, acao, args)
}

func (s *Service) chamarExtensao(ctx context.Context, p *project, projetoID, quem string, agente *ext.Agente, nome, acao string, args map[string]any) (ExtResultado, error) {
	cfg := p.config()
	e, ok := cfg.Extensao(nome)
	if !ok {
		return ExtResultado{}, fmt.Errorf("extensão %q não está no central.toml: %w", nome, core.ErrNotFound)
	}
	if !e.Acoes()[acao] {
		return ExtResultado{}, fmt.Errorf("%w: a extensão %s não declara a ação %q (declaradas: cli, aba.acao, entrada.ao_chegar)", core.ErrInvalid, nome, acao)
	}
	nomeProj, err := s.nomeProjeto(ctx, projetoID)
	if err != nil {
		return ExtResultado{}, err
	}
	lock := s.extLocks.get(projetoID + "/" + nome)
	lock.Lock()
	defer lock.Unlock()
	inicio := time.Now()
	r, runErr := ext.Rodar(ctx, ext.Execucao{
		Comando: e.Comando, Pasta: p.raiz, Timeout: e.Timeout.Duration,
		Ambiente: []string{"CENTRAL_PROJETO=" + projetoID, "CENTRAL_ADDR=" + config.Addr()},
	}, ext.Pedido{Acao: acao, Args: args, Projeto: ext.Projeto{ID: projetoID, Nome: nomeProj, Raiz: p.raiz}, Agente: agente})
	ms := time.Since(inicio).Milliseconds()
	if runErr == nil && !r.OK {
		msg := "sem mensagem"
		if r.Erro != nil && r.Erro.Mensagem != "" {
			msg = r.Erro.Mensagem
		}
		runErr = errors.New(msg)
	}
	chamada := map[string]any{"ok": runErr == nil, "ms": ms}
	if runErr != nil {
		chamada["erro"] = runErr.Error()
		// O registro da falha não pode esconder a falha.
		_ = p.core.ExtEvents(context.WithoutCancel(ctx), quem, nome, acao, nil, nil, chamada)
		return ExtResultado{}, fmt.Errorf("%w: %s %s: %v", ErrExtensao, nome, acao, runErr)
	}
	evs := make([]core.ExtEvent, 0, len(r.Eventos))
	for _, ev := range r.Eventos {
		corpo := fmt.Sprintf("%s: %s %s", nome, ev.Tipo, ev.Ref)
		if res, ok := ev.Dados["resumo"].(string); ok && res != "" {
			corpo = res
		}
		evs = append(evs, core.ExtEvent{Tipo: ev.Tipo, Ref: ev.Ref, Dados: ev.Dados, Para: cfg.Acordar[ev.Tipo], Corpo: corpo})
	}
	fatos := make([]core.ExtFact, 0, len(r.Fatos))
	for _, f := range r.Fatos {
		fatos = append(fatos, core.ExtFact{Chave: f.Chave, Valor: f.Valor, Motivo: f.Motivo})
	}
	if err := p.core.ExtEvents(ctx, quem, nome, acao, evs, fatos, chamada); err != nil {
		if errors.Is(err, core.ErrInvalid) {
			// Evento ou fato fora do formato: a resposta não vale, e a chamada fica registrada como falha.
			_ = p.core.ExtEvents(context.WithoutCancel(ctx), quem, nome, acao, nil, nil, map[string]any{"ok": false, "ms": ms, "erro": err.Error()})
			return ExtResultado{}, fmt.Errorf("%w: %s %s: %v", ErrExtensao, nome, acao, err)
		}
		return ExtResultado{}, err
	}
	out := ExtResultado{Dados: r.Dados, Eventos: r.Eventos, MS: ms}
	if r.TemFormulario() {
		out.Formulario = r.Formulario
	}
	if out.Dados == nil {
		out.Dados = json.RawMessage("null")
	}
	if out.Eventos == nil {
		out.Eventos = []ext.Evento{}
	}
	return out, nil
}

// nomeProjeto lê o nome registrado do projeto.
func (s *Service) nomeProjeto(ctx context.Context, id string) (string, error) {
	var nome string
	err := s.registry.QueryRowContext(ctx, `SELECT nome FROM projetos WHERE id = ?`, id).Scan(&nome)
	return nome, err
}

// TiposArquivo são as extensões de arquivo que o painel pode mostrar (sem SVG:
// servido da mesma origem do painel, ele rodaria script).
var TiposArquivo = map[string]string{
	".png": "image/png", ".jpg": "image/jpeg", ".jpeg": "image/jpeg", ".gif": "image/gif", ".webp": "image/webp",
	".mp4": "video/mp4", ".webm": "video/webm",
}

// Arquivo resolve um caminho relativo à raiz do projeto para o painel mostrar:
// recusa caminho absoluto, .., link para fora da raiz e tipo fora de TiposArquivo.
func (s *Service) Arquivo(ctx context.Context, projetoID, caminho string) (string, string, error) {
	p, err := s.project(ctx, projetoID)
	if err != nil {
		return "", "", err
	}
	limpo := filepath.ToSlash(caminho)
	if limpo == "" || path.IsAbs(limpo) || filepath.IsAbs(caminho) || filepath.VolumeName(caminho) != "" ||
		slices.Contains(strings.Split(limpo, "/"), "..") {
		return "", "", fmt.Errorf("%w: caminho %q precisa ser relativo à raiz, sem ..", core.ErrInvalid, caminho)
	}
	tipo, ok := TiposArquivo[strings.ToLower(path.Ext(limpo))]
	if !ok {
		return "", "", fmt.Errorf("%w: o painel só mostra imagens e vídeos (%s)", core.ErrInvalid, path.Ext(limpo))
	}
	raiz, err := filepath.EvalSymlinks(p.raiz)
	if err != nil {
		return "", "", fmt.Errorf("raiz do projeto: %w", core.ErrNotFound)
	}
	real, err := filepath.EvalSymlinks(filepath.Join(raiz, filepath.FromSlash(limpo)))
	if err != nil {
		return "", "", fmt.Errorf("%s: %w", caminho, core.ErrNotFound)
	}
	if rel, err := filepath.Rel(raiz, real); err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", "", fmt.Errorf("%w: %s aponta para fora da raiz", core.ErrInvalid, caminho)
	}
	return real, tipo, nil
}
