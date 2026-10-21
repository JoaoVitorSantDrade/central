package config

import (
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/JoaoVitorSantDrade/central/internal/process"
)

// Padrões do processo (Global Constraints do M1b).
const (
	PadraoEscopo       = 75 * time.Minute
	PadraoCongelamento = 60 * time.Minute
	PadraoCorte        = 30 * time.Minute
	PadraoRetro        = 20 * time.Minute
	PadraoOrfasApos    = 12 * time.Hour
	// PadraoAdaptadorIntervalo é o intervalo da leitura do backlog em arquivos (spec §6.5).
	PadraoAdaptadorIntervalo = 30 * time.Second
)

// PadraoObrigatorios são os campos que um item precisa ter para ficar pronto.
var PadraoObrigatorios = []string{"titulo", "area", "prioridade", "criterio_de_pronto"}

// PadraoLimiteParado é quanto um item pode ficar em cada estado antes de virar alerta.
var PadraoLimiteParado = map[string]time.Duration{
	"concluida":    30 * time.Minute,
	"aprovada":     30 * time.Minute,
	"em_andamento": 3 * time.Hour,
}

// PapeisSec renomeia, desliga e marca vagas sênior (spec §6.1).
type PapeisSec struct {
	Nomes      map[string]string `toml:"nomes" json:"nomes"`
	Desligados []string          `toml:"desligados" json:"desligados"`
	Senior     []string          `toml:"senior" json:"senior"`
}

// ItensSec configura os itens de trabalho.
type ItensSec struct {
	Obrigatorios []string     `toml:"obrigatorios" json:"obrigatorios"`
	Arquivos     *ArquivosSec `toml:"arquivos" json:"arquivos"`
}

// ArquivosSec é o adaptador de itens em arquivos (spec §6.5).
type ArquivosSec struct {
	Ref       string            `toml:"ref" json:"ref"`
	Repo      string            `toml:"repo" json:"repo"`
	Padrao    string            `toml:"padrao" json:"padrao"`
	Pastas    map[string]string `toml:"pastas" json:"pastas"`
	Mapa      map[string]string `toml:"mapa" json:"mapa"`
	RecursoSe map[string]string `toml:"recurso_se" json:"recurso_se"`
	// Intervalo entre as leituras da fonte (padrão 30s, mínimo 1s).
	Intervalo Duration `toml:"intervalo" json:"intervalo"`
}

// SprintSec são os deslocamentos das cerimônias antes do fim e quem cada uma acorda.
type SprintSec struct {
	Escopo       Duration            `toml:"escopo" json:"escopo"`
	Congelamento Duration            `toml:"congelamento" json:"congelamento"`
	Corte        Duration            `toml:"corte" json:"corte"`
	Retro        Duration            `toml:"retro" json:"retro"`
	Acordar      map[string][]string `toml:"acordar" json:"acordar"`
}

// PadraoBriefMaxTokens é o teto padrão do brief (spec §7).
const PadraoBriefMaxTokens = 8000

// BriefSec configura o brief: o teto e, por papel canônico, os padrões das
// chaves de fato relevantes ("*" no fim casa prefixo).
type BriefSec struct {
	MaxTokens int                 `toml:"max_tokens" json:"max_tokens"`
	Fatos     map[string][]string `toml:"fatos" json:"fatos"`
}

// LeituraSec é uma linha do mapa de leitura (spec §7): o brief mostra o
// resumo e quando ler; o agente lê o arquivo só se precisar.
type LeituraSec struct {
	Caminho string   `toml:"caminho" json:"caminho"`
	Resumo  string   `toml:"resumo" json:"resumo"`
	Quando  string   `toml:"quando" json:"quando"`
	Papeis  []string `toml:"papeis" json:"papeis"`
}

// ProcessoSec são os limites usados por saude e orfas.
type ProcessoSec struct {
	LimiteParado map[string]Duration `toml:"limite_parado" json:"limite_parado"`
	OrfasApos    Duration            `toml:"orfas_apos" json:"orfas_apos"`
}

func defaultDur(d *Duration, v time.Duration) {
	if d.Duration == 0 {
		d.Duration = v
	}
}

// normalizeProcesso aplica os padrões do processo e valida.
func (c *ProjectConfig) normalizeProcesso() error {
	p := &c.Papeis
	usados := map[string]string{} // nome no projeto → canônico
	for _, can := range slices.Sorted(maps.Keys(p.Nomes)) {
		if !process.IsCanonical(can) {
			return fmt.Errorf("papeis.nomes: papel desconhecido %s (use %v)", can, process.Canonicos)
		}
	}
	for _, can := range process.Canonicos {
		nome := can
		if v := p.Nomes[can]; v != "" {
			nome = v
		}
		if outro, ok := usados[nome]; ok {
			return fmt.Errorf("papeis.nomes: o nome %q é usado por %s e %s", nome, outro, can)
		}
		usados[nome] = can
	}
	for _, d := range p.Desligados {
		if !process.IsCanonical(d) {
			return fmt.Errorf("papeis.desligados: papel desconhecido %s", d)
		}
		if slices.Contains(process.Obrigatorios, d) {
			return fmt.Errorf("papeis.desligados: %s não pode ser desligado", d)
		}
	}

	if len(c.Itens.Obrigatorios) == 0 {
		c.Itens.Obrigatorios = slices.Clone(PadraoObrigatorios)
	}
	if a := c.Itens.Arquivos; a != nil {
		if a.Padrao == "" {
			a.Padrao = "*.md"
		}
		defaultDur(&a.Intervalo, PadraoAdaptadorIntervalo)
		if a.Intervalo.Duration < time.Second {
			return fmt.Errorf("itens.arquivos.intervalo: precisa ser de pelo menos 1s (agora %v)", a.Intervalo)
		}
		for _, estado := range slices.Sorted(maps.Keys(a.Pastas)) {
			if !slices.Contains(process.EstadosDeFonte, estado) {
				return fmt.Errorf("itens.arquivos.pastas: estado %s não vem de pasta (use %v)", estado, process.EstadosDeFonte)
			}
		}
	}

	s := &c.Sprint
	defaultDur(&s.Escopo, PadraoEscopo)
	defaultDur(&s.Congelamento, PadraoCongelamento)
	defaultDur(&s.Corte, PadraoCorte)
	defaultDur(&s.Retro, PadraoRetro)
	if !(s.Escopo.Duration > s.Congelamento.Duration && s.Congelamento.Duration > s.Corte.Duration &&
		s.Corte.Duration > s.Retro.Duration && s.Retro.Duration > 0) {
		return fmt.Errorf("sprint: os deslocamentos antes do fim precisam seguir escopo > congelamento > corte > retro > 0 (agora %v, %v, %v, %v)",
			s.Escopo, s.Congelamento, s.Corte, s.Retro)
	}
	// canonico aceita o nome do papel no projeto (ex.: FIXER) ou o canônico, e TODOS.
	canonico := func(p string) (string, bool) {
		p = strings.ToUpper(strings.TrimSpace(p))
		if p == "TODOS" || process.IsCanonical(p) {
			return p, true
		}
		can, ok := usados[p]
		return can, ok
	}
	for _, cer := range slices.Sorted(maps.Keys(s.Acordar)) {
		if !slices.Contains(process.Cerimonias, cer) {
			return fmt.Errorf("sprint.acordar: cerimônia desconhecida %s (use %v)", cer, process.Cerimonias)
		}
		for i, papel := range s.Acordar[cer] {
			can, ok := canonico(papel)
			if !ok {
				return fmt.Errorf("sprint.acordar.%s: papel desconhecido %q", cer, papel)
			}
			s.Acordar[cer][i] = can
		}
	}

	if c.Brief.MaxTokens == 0 {
		c.Brief.MaxTokens = PadraoBriefMaxTokens
	}
	if c.Brief.MaxTokens < 500 {
		return fmt.Errorf("brief.max_tokens: precisa ser de pelo menos 500 (agora %d)", c.Brief.MaxTokens)
	}
	if len(c.Brief.Fatos) > 0 {
		fatos := map[string][]string{}
		for _, papel := range slices.Sorted(maps.Keys(c.Brief.Fatos)) {
			can, ok := canonico(papel)
			if !ok {
				return fmt.Errorf("brief.fatos: papel desconhecido %s (use o nome do projeto, o canônico ou TODOS)", papel)
			}
			fatos[can] = append(fatos[can], c.Brief.Fatos[papel]...)
		}
		c.Brief.Fatos = fatos
	}
	for i, l := range c.Leitura {
		if strings.TrimSpace(l.Caminho) == "" {
			return fmt.Errorf("leitura[%d]: caminho vazio", i)
		}
		for j, p := range l.Papeis {
			can, ok := canonico(p)
			if !ok {
				return fmt.Errorf("leitura[%d] (%s): papel desconhecido %s", i, l.Caminho, p)
			}
			c.Leitura[i].Papeis[j] = can
		}
	}

	pr := &c.Processo
	defaultDur(&pr.OrfasApos, PadraoOrfasApos)
	if pr.LimiteParado == nil {
		pr.LimiteParado = map[string]Duration{}
	}
	for estado, v := range PadraoLimiteParado {
		if _, ok := pr.LimiteParado[estado]; !ok {
			pr.LimiteParado[estado] = Duration{v}
		}
	}
	return nil
}
