package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/JoaoVitorSantDrade/central/internal/config"
	"github.com/JoaoVitorSantDrade/central/internal/core"
)

// Limites da pasta de entrada.
const (
	PadraoIntervaloEntrada = 2 * time.Second
	MaxUpload              = 8 << 30
	// PrefixoParcial marca o arquivo que o upload ainda está gravando.
	PrefixoParcial = ".central-parcial-"
)

// Pendente é uma chamada da pasta de entrada que pediu campos ao humano.
type Pendente struct {
	ID         int64           `json:"id"`
	Acao       string          `json:"acao"`
	Args       map[string]any  `json:"args"`
	Formulario json.RawMessage `json:"formulario"`
	Desde      time.Time       `json:"desde"`
	// Enviando marca a resposta do humano em andamento (outra dá conflito).
	Enviando bool `json:"enviando"`
}

// EntradaEstado é o que a aba mostra da pasta de entrada: as pendências e o
// último erro de uma entrega (vazio quando a última deu certo).
type EntradaEstado struct {
	Pasta     string     `json:"pasta"`
	Pendentes []Pendente `json:"pendentes"`
	Erro      string     `json:"erro,omitempty"`
	ErroEm    *time.Time `json:"erro_em,omitempty"`
}

type visto struct {
	tamanho int64
	mod     time.Time
}

func (v visto) igual(o visto) bool { return v.tamanho == o.tamanho && v.mod.Equal(o.mod) }

// raizExiste diz se a raiz registrada do projeto ainda é uma pasta.
func raizExiste(raiz string) bool {
	info, err := os.Stat(raiz)
	return err == nil && info.IsDir()
}

// entradaExt é o estado em memória da pasta de entrada de uma extensão.
type entradaExt struct {
	candidatos map[string]visto // como estava na última leitura
	entregues  map[string]visto // já entregues (não entregam de novo)
	ocupada    bool
	pendentes  []Pendente
	erro       string
	erroEm     time.Time
}

type entradas struct {
	mu  sync.Mutex
	seq int64
	m   map[string]*entradaExt // projeto/extensão
}

func (en *entradas) get(chave string) *entradaExt {
	if en.m == nil {
		en.m = map[string]*entradaExt{}
	}
	e, ok := en.m[chave]
	if !ok {
		e = &entradaExt{candidatos: map[string]visto{}, entregues: map[string]visto{}}
		en.m[chave] = e
	}
	return e
}

// RunEntradas olha as pastas de entrada dos projetos abertos até ctx acabar.
func (s *Service) RunEntradas(ctx context.Context, intervalo time.Duration) {
	if intervalo <= 0 {
		intervalo = PadraoIntervaloEntrada
	}
	var wg sync.WaitGroup
	defer wg.Wait()
	t := time.NewTicker(intervalo)
	defer t.Stop()
	for {
		s.mu.Lock()
		ps := make(map[string]*project, len(s.projects))
		for id, p := range s.projects {
			ps[id] = p
		}
		s.mu.Unlock()
		for id, p := range ps {
			for _, e := range p.config().Extensoes {
				if e.Entrada != nil {
					s.olharEntrada(ctx, &wg, id, p, e)
				}
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// olharEntrada lê a pasta uma vez e entrega os arquivos estáveis ainda não
// entregues, numa chamada só, em segundo plano.
func (s *Service) olharEntrada(ctx context.Context, wg *sync.WaitGroup, id string, p *project, e config.ExtensaoSec) {
	dir := filepath.Join(p.raiz, filepath.FromSlash(e.Entrada.Pasta))
	ds, err := os.ReadDir(dir)
	if err != nil {
		// Cria a pasta só dentro de uma raiz que existe: um projeto apagado não volta.
		if errors.Is(err, os.ErrNotExist) && raizExiste(p.raiz) {
			os.MkdirAll(dir, 0o755)
		}
		return
	}
	atuais := map[string]visto{}
	for _, d := range ds {
		if strings.HasPrefix(d.Name(), ".") || !d.Type().IsRegular() {
			continue
		}
		info, err := d.Info()
		if err != nil {
			continue
		}
		atuais[d.Name()] = visto{info.Size(), info.ModTime()}
	}
	s.entradas.mu.Lock()
	st := s.entradas.get(id + "/" + e.Nome)
	var prontos []string
	for nome, v := range atuais {
		if st.candidatos[nome].igual(v) && !st.entregues[nome].igual(v) {
			prontos = append(prontos, nome)
		}
	}
	// Quem saiu da pasta sai da memória.
	for nome := range st.entregues {
		if _, ok := atuais[nome]; !ok {
			delete(st.entregues, nome)
		}
	}
	st.candidatos = atuais
	if len(prontos) == 0 || st.ocupada {
		s.entradas.mu.Unlock()
		return
	}
	for _, nome := range prontos {
		st.entregues[nome] = atuais[nome]
	}
	st.ocupada = true
	s.entradas.mu.Unlock()
	sort.Strings(prontos)
	arquivos := make([]string, len(prontos))
	for i, nome := range prontos {
		arquivos[i] = path.Join(e.Entrada.Pasta, nome)
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		args := map[string]any{"arquivos": arquivos}
		res, err := s.chamarExtensao(ctx, p, id, core.Sistema, nil, e.Nome, e.Entrada.AoChegar, args)
		s.entradas.mu.Lock()
		defer s.entradas.mu.Unlock()
		st.ocupada = false
		s.registrarEntrega(st, e.Entrada.AoChegar, args, res, err)
		if err != nil && s.logf != nil && ctx.Err() == nil {
			s.logf("entrada %s/%s: %v", id, e.Nome, err)
		}
	}()
}

// registrarEntrega guarda o resultado de uma chamada vinda da entrada (com
// s.entradas.mu travado): formulário vira pendência, erro fica para a aba.
func (s *Service) registrarEntrega(st *entradaExt, acao string, args map[string]any, res ExtResultado, err error) {
	if err != nil {
		st.erro, st.erroEm = err.Error(), time.Now()
		return
	}
	st.erro = ""
	if len(res.Formulario) > 0 {
		s.entradas.seq++
		st.pendentes = append(st.pendentes, Pendente{ID: s.entradas.seq, Acao: acao, Args: args, Formulario: res.Formulario, Desde: time.Now()})
	}
}

// entradaDe devolve o projeto e a extensão com pasta de entrada.
func (s *Service) entradaDe(ctx context.Context, projetoID, nome string) (*project, config.ExtensaoSec, error) {
	p, err := s.project(ctx, projetoID)
	if err != nil {
		return nil, config.ExtensaoSec{}, err
	}
	e, ok := p.config().Extensao(nome)
	if !ok || e.Entrada == nil {
		return nil, config.ExtensaoSec{}, fmt.Errorf("extensão %q sem pasta de entrada no central.toml: %w", nome, core.ErrNotFound)
	}
	return p, e, nil
}

// Pendentes devolve as pendências e o último erro da pasta de entrada.
func (s *Service) Pendentes(ctx context.Context, projetoID, nome string) (EntradaEstado, error) {
	_, e, err := s.entradaDe(ctx, projetoID, nome)
	if err != nil {
		return EntradaEstado{}, err
	}
	s.entradas.mu.Lock()
	defer s.entradas.mu.Unlock()
	st := s.entradas.get(projetoID + "/" + nome)
	out := EntradaEstado{Pasta: e.Entrada.Pasta, Pendentes: slices.Clone(st.pendentes), Erro: st.erro}
	if out.Pendentes == nil {
		out.Pendentes = []Pendente{}
	}
	if st.erro != "" {
		em := st.erroEm
		out.ErroEm = &em
	}
	return out, nil
}

// ResponderFormulario chama de novo a ação da pendência com os args originais
// mais os campos do humano. Se a resposta pedir de novo, vira outra pendência.
func (s *Service) ResponderFormulario(ctx context.Context, projetoID, nome string, id int64, campos map[string]any) (ExtResultado, error) {
	p, _, err := s.entradaDe(ctx, projetoID, nome)
	if err != nil {
		return ExtResultado{}, err
	}
	chave := projetoID + "/" + nome
	s.entradas.mu.Lock()
	st := s.entradas.get(chave)
	i := slices.IndexFunc(st.pendentes, func(pd Pendente) bool { return pd.ID == id })
	if i < 0 {
		s.entradas.mu.Unlock()
		return ExtResultado{}, fmt.Errorf("pendência %d de %s: %w", id, nome, core.ErrNotFound)
	}
	if st.pendentes[i].Enviando {
		s.entradas.mu.Unlock()
		return ExtResultado{}, fmt.Errorf("%w: a pendência %d já está sendo respondida", core.ErrConflict, id)
	}
	st.pendentes[i].Enviando = true
	pd := st.pendentes[i]
	s.entradas.mu.Unlock()
	args := map[string]any{}
	for k, v := range pd.Args {
		args[k] = v
	}
	for k, v := range campos {
		args[k] = v
	}
	res, err := s.chamarExtensao(ctx, p, projetoID, core.Humano, nil, nome, pd.Acao, args)
	if err != nil {
		// A pendência fica para o humano tentar de novo.
		s.entradas.mu.Lock()
		if j := slices.IndexFunc(st.pendentes, func(x Pendente) bool { return x.ID == id }); j >= 0 {
			st.pendentes[j].Enviando = false
		}
		s.entradas.mu.Unlock()
		return ExtResultado{}, err
	}
	s.entradas.mu.Lock()
	defer s.entradas.mu.Unlock()
	st.pendentes = slices.DeleteFunc(st.pendentes, func(x Pendente) bool { return x.ID == id })
	s.registrarEntrega(st, pd.Acao, args, res, nil)
	return res, nil
}

// nomeEntradaValido recusa caminhos, nomes ocultos, dispositivos do Windows e
// caracteres de controle: o upload grava só um arquivo solto na pasta.
func nomeEntradaValido(nome string) bool {
	if nome == "" || len(nome) > 200 || strings.HasPrefix(nome, ".") || strings.ContainsAny(nome, `/\:*?"<>|`) {
		return false
	}
	for _, r := range nome {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	base := strings.ToUpper(strings.TrimRight(strings.SplitN(nome, ".", 2)[0], " "))
	switch base {
	case "CON", "PRN", "AUX", "NUL":
		return false
	}
	if len(base) == 4 && (strings.HasPrefix(base, "COM") || strings.HasPrefix(base, "LPT")) && base[3] >= '0' && base[3] <= '9' {
		return false
	}
	return !strings.HasSuffix(nome, " ") && !strings.HasSuffix(nome, ".")
}

// SalvarEntrada grava o arquivo na pasta de entrada da extensão: primeiro como
// parcial (o vigia ignora), depois renomeia. Um nome que já existe dá conflito.
func (s *Service) SalvarEntrada(ctx context.Context, projetoID, nome, arquivo string, r io.Reader) error {
	if !nomeEntradaValido(arquivo) {
		return fmt.Errorf("%w: nome de arquivo %q (só o nome, sem pasta, sem começar por ponto)", core.ErrInvalid, arquivo)
	}
	p, e, err := s.entradaDe(ctx, projetoID, nome)
	if err != nil {
		return err
	}
	if !raizExiste(p.raiz) {
		return fmt.Errorf("a raiz %s do projeto não existe mais: %w", p.raiz, core.ErrNotFound)
	}
	dir := filepath.Join(p.raiz, filepath.FromSlash(e.Entrada.Pasta))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	final := filepath.Join(dir, arquivo)
	if _, err := os.Stat(final); err == nil {
		return fmt.Errorf("%w: %s já está na pasta de entrada", core.ErrConflict, arquivo)
	}
	parcial := filepath.Join(dir, PrefixoParcial+arquivo)
	f, err := os.OpenFile(parcial, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return fmt.Errorf("%w: %s já está sendo enviado", core.ErrConflict, arquivo)
		}
		return err
	}
	n, err := io.Copy(f, io.LimitReader(r, MaxUpload+1))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil && n > MaxUpload {
		err = fmt.Errorf("%w: arquivo maior que %d GiB", core.ErrInvalid, MaxUpload>>30)
	}
	if err != nil {
		os.Remove(parcial)
		return err
	}
	if _, err := os.Stat(final); err == nil {
		os.Remove(parcial)
		return fmt.Errorf("%w: %s já está na pasta de entrada", core.ErrConflict, arquivo)
	}
	if err := os.Rename(parcial, final); err != nil {
		os.Remove(parcial)
		return err
	}
	return nil
}
