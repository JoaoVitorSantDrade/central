package config

import (
	"errors"
	"fmt"
	"maps"
	"math"
	"slices"
	"strings"
	"time"

	"github.com/JoaoVitorSantDrade/central/internal/core"
	"github.com/pelletier/go-toml/v2"
)

// Padrões do central.toml (Global Constraints do M1a).
const (
	PadraoSumidoApos       = 30 * time.Minute
	PadraoMedidorIntervalo = 15 * time.Second
	PadraoPrazoMax         = 30 * time.Minute
	PadraoSemUsoApos       = 5 * time.Minute
	// MinMedidorIntervalo é o menor intervalo do medidor: cada leitura roda um comando.
	MinMedidorIntervalo = time.Second
)

// Duration lê durações escritas como texto ("15s", "30m") no TOML.
type Duration struct{ time.Duration }

// UnmarshalText aceita o formato de time.ParseDuration.
func (d *Duration) UnmarshalText(b []byte) error {
	v, err := time.ParseDuration(string(b))
	if err != nil {
		return fmt.Errorf("duração inválida %q (ex.: 15s, 30m)", b)
	}
	d.Duration = v
	return nil
}

// MarshalText escreve a duração no mesmo formato (ex.: "30m0s").
func (d Duration) MarshalText() ([]byte, error) { return []byte(d.String()), nil }

// ProjectConfig é o central.toml inteiro, já com os padrões aplicados.
type ProjectConfig struct {
	Projeto  ProjetoSec             `toml:"projeto" json:"projeto"`
	Agentes  AgentesSec             `toml:"agentes" json:"agentes"`
	Recursos map[string]ResourceSec `toml:"recursos" json:"recursos"`
	Papeis   PapeisSec              `toml:"papeis" json:"papeis"`
	Itens    ItensSec               `toml:"itens" json:"itens"`
	Sprint   SprintSec              `toml:"sprint" json:"sprint"`
	Processo ProcessoSec            `toml:"processo" json:"processo"`
	Brief    BriefSec               `toml:"brief" json:"brief"`
	Leitura  []LeituraSec           `toml:"leitura" json:"leitura"`
	// Extensoes são as extensões ext/v1 (spec §9); Acordar diz que papéis cada
	// tipo de evento de extensão acorda.
	Extensoes []ExtensaoSec       `toml:"extensoes" json:"extensoes"`
	Acordar   map[string][]string `toml:"acordar" json:"acordar"`
	// Execucao liga o executor de vagas; nil quando a seção não existe.
	Execucao *ExecucaoSec `toml:"execucao" json:"execucao,omitempty"`
}

// ProjetoSec identifica o projeto.
type ProjetoSec struct {
	ID   string `toml:"id" json:"id"`
	Nome string `toml:"nome" json:"nome"`
}

// AgentesSec configura o acompanhamento das vagas.
type AgentesSec struct {
	SumidoApos Duration `toml:"sumido_apos" json:"sumido_apos"`
}

// ResourceSec é um recurso com dimensões (spec §5.3).
type ResourceSec struct {
	Dimensoes             map[string]float64        `toml:"dimensoes" json:"dimensoes"`
	Reservado             map[string]float64        `toml:"reservado" json:"reservado"`
	Margem                map[string]float64        `toml:"margem" json:"margem"`
	TetoMedido            map[string]float64        `toml:"teto_medido" json:"teto_medido"`
	Medidor               *MeterSec                 `toml:"medidor" json:"medidor"`
	PrazoMax              Duration                  `toml:"prazo_max" json:"prazo_max"`
	PrazoMaxPorPrioridade map[string]Duration       `toml:"prazo_max_por_prioridade" json:"prazo_max_por_prioridade"`
	Prioridades           []string                  `toml:"prioridades" json:"prioridades"`
	Tipos                 map[string]map[string]any `toml:"tipos" json:"tipos"`
	SemUso                *SemUsoSec                `toml:"sem_uso" json:"sem_uso"`
}

// MeterSec é o comando que mede o recurso; mapa diz a dimensão de cada coluna.
type MeterSec struct {
	Comando   string   `toml:"comando" json:"comando"`
	Mapa      []string `toml:"mapa" json:"mapa"`
	Intervalo Duration `toml:"intervalo" json:"intervalo"`
}

// SemUsoSec avisa quando uma concessão não usa o que pediu.
type SemUsoSec struct {
	Dimensao string   `toml:"dimensao" json:"dimensao"`
	Limite   float64  `toml:"limite" json:"limite"`
	Apos     Duration `toml:"apos" json:"apos"`
}

// ParseProjectConfig lê o central.toml, aplica os padrões e valida.
func ParseProjectConfig(data []byte) (ProjectConfig, error) {
	var c ProjectConfig
	if err := toml.Unmarshal(data, &c); err != nil {
		return ProjectConfig{}, tomlError(err)
	}
	if c.Agentes.SumidoApos.Duration == 0 {
		c.Agentes.SumidoApos.Duration = PadraoSumidoApos
	}
	if c.Agentes.SumidoApos.Duration < 0 {
		return ProjectConfig{}, errors.New("agentes.sumido_apos: precisa ser positivo")
	}
	for _, nome := range slices.Sorted(maps.Keys(c.Recursos)) {
		r := c.Recursos[nome]
		if err := r.normalize(); err != nil {
			return ProjectConfig{}, fmt.Errorf("recursos.%s.%w", nome, err)
		}
		c.Recursos[nome] = r
	}
	if err := c.normalizeProcesso(); err != nil {
		return ProjectConfig{}, err
	}
	if err := c.normalizeExecucao(); err != nil {
		return ProjectConfig{}, err
	}
	if err := c.normalizeExtensoes(); err != nil {
		return ProjectConfig{}, err
	}
	return c, nil
}

// tomlError põe a linha e a coluna na mensagem de um erro do TOML.
func tomlError(err error) error {
	var de *toml.DecodeError
	if !errors.As(err, &de) {
		return fmt.Errorf("TOML inválido: %w", err)
	}
	row, col := de.Position()
	msg := strings.TrimPrefix(de.Error(), "toml: ")
	if k := de.Key(); len(k) > 0 {
		msg = strings.Join(k, ".") + ": " + msg
	}
	return fmt.Errorf("TOML inválido (line %d, column %d): %s", row, col, msg)
}

// fieldError é um erro de um campo do recurso; o chamador acrescenta "recursos.<nome>.".
func fieldError(campo, format string, args ...any) error {
	return fmt.Errorf("%s: %s", campo, fmt.Sprintf(format, args...))
}

// finite recusa NaN e ±Inf, que o TOML aceita e que passariam por qualquer comparação.
func finite(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) }

// normalize aplica os padrões do recurso e valida os campos (spec §5.3).
func (r *ResourceSec) normalize() error {
	if len(r.Dimensoes) == 0 {
		return fieldError("dimensoes", "declare ao menos uma dimensão")
	}
	for _, d := range slices.Sorted(maps.Keys(r.Dimensoes)) {
		if !finite(r.Dimensoes[d]) || r.Dimensoes[d] <= 0 {
			return fieldError("dimensoes", "%s precisa ser um número finito maior que zero", d)
		}
	}
	known := func(campo string, dims map[string]float64, check func(dim string, v float64) error) error {
		for _, d := range slices.Sorted(maps.Keys(dims)) {
			if _, ok := r.Dimensoes[d]; !ok {
				return fieldError(campo, "dimensão desconhecida %q (declare-a em dimensoes)", d)
			}
			if !finite(dims[d]) {
				return fieldError(campo, "%s precisa ser um número finito", d)
			}
			if err := check(d, dims[d]); err != nil {
				return err
			}
		}
		return nil
	}
	if err := known("reservado", r.Reservado, func(d string, v float64) error {
		if v < 0 || v >= r.Dimensoes[d] {
			return fieldError("reservado", "%s precisa estar entre 0 e a dimensão (%g)", d, r.Dimensoes[d])
		}
		return nil
	}); err != nil {
		return err
	}
	if err := known("margem", r.Margem, func(d string, v float64) error {
		if v < 0 {
			return fieldError("margem", "%s não pode ser negativa", d)
		}
		return nil
	}); err != nil {
		return err
	}
	if err := known("teto_medido", r.TetoMedido, func(d string, v float64) error {
		if v <= 0 || v > 1 {
			return fieldError("teto_medido", "%s é uma fração da capacidade: precisa estar em (0, 1]", d)
		}
		return nil
	}); err != nil {
		return err
	}
	if m := r.Medidor; m != nil {
		if strings.TrimSpace(m.Comando) == "" {
			return fieldError("medidor", "comando é obrigatório")
		}
		if len(m.Mapa) == 0 {
			return fieldError("medidor", "mapa precisa dizer a dimensão de cada coluna")
		}
		for _, d := range m.Mapa {
			if _, ok := r.Dimensoes[d]; !ok {
				return fieldError("medidor", "mapa cita a dimensão desconhecida %q", d)
			}
		}
		if m.Intervalo.Duration < 0 {
			return fieldError("medidor", "intervalo precisa ser positivo")
		}
		if m.Intervalo.Duration == 0 {
			m.Intervalo.Duration = PadraoMedidorIntervalo
		}
		if m.Intervalo.Duration < MinMedidorIntervalo {
			return fieldError("medidor", "intervalo precisa ser de pelo menos %v (cada leitura roda um comando)", MinMedidorIntervalo)
		}
	}
	if r.PrazoMax.Duration < 0 {
		return fieldError("prazo_max", "precisa ser positivo")
	}
	if r.PrazoMax.Duration == 0 {
		r.PrazoMax.Duration = PadraoPrazoMax
	}
	if len(r.Prioridades) == 0 {
		return fieldError("prioridades", "declare ao menos uma prioridade, da maior para a menor")
	}
	seen := map[string]bool{}
	for _, p := range r.Prioridades {
		if p == "" {
			return fieldError("prioridades", "prioridade vazia")
		}
		if seen[p] {
			return fieldError("prioridades", "%q aparece mais de uma vez", p)
		}
		seen[p] = true
	}
	for _, p := range slices.Sorted(maps.Keys(r.PrazoMaxPorPrioridade)) {
		if !seen[p] {
			return fieldError("prazo_max_por_prioridade", "prioridade desconhecida %q (declare-a em prioridades)", p)
		}
		if r.PrazoMaxPorPrioridade[p].Duration <= 0 {
			return fieldError("prazo_max_por_prioridade", "%s precisa ser positivo", p)
		}
	}
	for _, tipo := range slices.Sorted(maps.Keys(r.Tipos)) {
		if err := r.checkTipo(tipo); err != nil {
			return err
		}
	}
	if s := r.SemUso; s != nil {
		if _, ok := r.Dimensoes[s.Dimensao]; !ok {
			return fieldError("sem_uso", "dimensão desconhecida %q", s.Dimensao)
		}
		if s.Limite < 0 {
			return fieldError("sem_uso", "limite não pode ser negativo")
		}
		if s.Apos.Duration < 0 {
			return fieldError("sem_uso", "apos precisa ser positivo")
		}
		if s.Apos.Duration == 0 {
			s.Apos.Duration = PadraoSemUsoApos
		}
	}
	return nil
}

// checkTipo valida um tipo: exclusivo é booleano; as outras chaves são
// dimensões com valor numérico que cabe na capacidade livre (dimensão − reservado).
func (r *ResourceSec) checkTipo(tipo string) error {
	campo := "tipos." + tipo
	vals := r.Tipos[tipo]
	for _, k := range slices.Sorted(maps.Keys(vals)) {
		v := vals[k]
		if k == "exclusivo" {
			if _, ok := v.(bool); !ok {
				return fieldError(campo, "exclusivo precisa ser true ou false")
			}
			continue
		}
		dim, ok := r.Dimensoes[k]
		if !ok {
			return fieldError(campo, "dimensão desconhecida %q", k)
		}
		var n float64
		switch x := v.(type) {
		case int64:
			n = float64(x)
		case float64:
			n = x
		default:
			return fieldError(campo, "%s precisa ser um número", k)
		}
		if livre := dim - r.Reservado[k]; !finite(n) || n < 0 || n > livre {
			return fieldError(campo, "%s = %g não cabe na capacidade livre (%g)", k, n, livre)
		}
	}
	return nil
}

// Spec converte o recurso já validado no formato que o núcleo usa. Sem medidor,
// MedidorIntervalo fica zero.
func (r ResourceSec) Spec(nome string) (core.ResourceSpec, error) {
	s := core.ResourceSpec{
		Nome:        nome,
		Dimensoes:   core.Dims(maps.Clone(r.Dimensoes)),
		Reservado:   core.Dims(maps.Clone(r.Reservado)),
		Margem:      core.Dims(maps.Clone(r.Margem)),
		TetoMedido:  core.Dims(maps.Clone(r.TetoMedido)),
		Prioridades: slices.Clone(r.Prioridades),
		PrazoMax:    r.PrazoMax.Duration,
		Tipos:       map[string]core.TipoSpec{},
	}
	if r.PrazoMaxPorPrioridade != nil {
		s.PrazoMaxPorPrioridade = map[string]time.Duration{}
		for p, d := range r.PrazoMaxPorPrioridade {
			s.PrazoMaxPorPrioridade[p] = d.Duration
		}
	}
	for tipo, vals := range r.Tipos {
		t := core.TipoSpec{Dims: core.Dims{}}
		for k, v := range vals {
			switch x := v.(type) {
			case bool:
				if k != "exclusivo" {
					return core.ResourceSpec{}, fmt.Errorf("recursos.%s.tipos.%s: %s precisa ser um número", nome, tipo, k)
				}
				t.Exclusivo = x
			case int64:
				t.Dims[k] = float64(x)
			case float64:
				t.Dims[k] = x
			default:
				return core.ResourceSpec{}, fmt.Errorf("recursos.%s.tipos.%s: valor inválido em %s", nome, tipo, k)
			}
		}
		s.Tipos[tipo] = t
	}
	if r.SemUso != nil {
		s.SemUso = &core.SemUsoSpec{Dimensao: r.SemUso.Dimensao, Limite: r.SemUso.Limite, Apos: r.SemUso.Apos.Duration}
	}
	if r.Medidor != nil {
		s.MedidorIntervalo = r.Medidor.Intervalo.Duration
	}
	return s, nil
}
