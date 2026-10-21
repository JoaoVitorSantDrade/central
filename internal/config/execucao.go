package config

import (
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/JoaoVitorSantDrade/central/internal/process"
)

// Padrões do executor de vagas (spec do executor §3.4).
const (
	PadraoClaude         = "claude"
	PadraoMaxSimultaneos = 4
	PadraoPrazoTurno     = 2 * time.Hour
	MinPrazoTurno        = time.Minute
	MaxPrazoTurno        = 12 * time.Hour
)

// Esforcos são os valores aceitos em esforco (os do --effort do Claude Code).
var Esforcos = []string{"low", "medium", "high", "xhigh", "max"}

// ExecucaoSec é a seção [execucao]: a Central abre e conduz as vagas como
// sessões do Claude Code CLI. Sem ela, o projeto funciona como antes.
type ExecucaoSec struct {
	Claude         string               `toml:"claude" json:"claude"`
	MaxSimultaneos int                  `toml:"max_simultaneos" json:"max_simultaneos"`
	PrazoTurno     Duration             `toml:"prazo_turno" json:"prazo_turno"`
	CustoDiaUSD    float64              `toml:"custo_dia_usd" json:"custo_dia_usd"`
	Cargos         map[string]CargoExec `toml:"cargos" json:"cargos"`
}

// CargoExec é o teto e o modelo de um cargo; as chaves de Cargos ficam no canônico.
type CargoExec struct {
	Max        int      `toml:"max" json:"max"`
	Modelo     string   `toml:"modelo" json:"modelo"`
	Esforco    string   `toml:"esforco" json:"esforco"`
	PrazoTurno Duration `toml:"prazo_turno" json:"prazo_turno"`
}

// Cargo devolve o cargo canônico com o prazo próprio ou o herdado da seção.
// Um cargo sem entrada não pode ser aberto pelo executor.
func (e ExecucaoSec) Cargo(canonico string) (CargoExec, bool) {
	c, ok := e.Cargos[canonico]
	if !ok {
		return CargoExec{}, false
	}
	if c.PrazoTurno.Duration == 0 {
		c.PrazoTurno = e.PrazoTurno
	}
	return c, true
}

// canonicoDoPapel traduz o nome de um papel no projeto (ex.: FIXER) ou o
// canônico para o canônico, como o normalizeProcesso faz.
func (c *ProjectConfig) canonicoDoPapel(p string) (string, bool) {
	p = strings.ToUpper(strings.TrimSpace(p))
	for _, can := range process.Canonicos {
		nome := can
		if v := c.Papeis.Nomes[can]; v != "" {
			nome = v
		}
		if p == nome {
			return can, true
		}
	}
	if process.IsCanonical(p) {
		return p, true
	}
	return "", false
}

func prazoTurnoValido(campo string, d time.Duration) error {
	if d < MinPrazoTurno || d > MaxPrazoTurno {
		return fmt.Errorf("%s: precisa estar entre %v e %v (agora %v)", campo, MinPrazoTurno, MaxPrazoTurno, d)
	}
	return nil
}

// normalizeExecucao aplica os padrões de [execucao] e valida; roda depois de
// normalizeProcesso porque usa os nomes dos papéis.
func (c *ProjectConfig) normalizeExecucao() error {
	e := c.Execucao
	if e == nil {
		return nil
	}
	if strings.TrimSpace(e.Claude) == "" {
		e.Claude = PadraoClaude
	}
	if e.MaxSimultaneos == 0 {
		e.MaxSimultaneos = PadraoMaxSimultaneos
	}
	if e.MaxSimultaneos < 1 {
		return fmt.Errorf("execucao.max_simultaneos: precisa ser pelo menos 1 (agora %d)", e.MaxSimultaneos)
	}
	defaultDur(&e.PrazoTurno, PadraoPrazoTurno)
	if err := prazoTurnoValido("execucao.prazo_turno", e.PrazoTurno.Duration); err != nil {
		return err
	}
	if !finite(e.CustoDiaUSD) || e.CustoDiaUSD < 0 {
		return fmt.Errorf("execucao.custo_dia_usd: precisa ser 0 (sem teto) ou positivo")
	}
	cargos := map[string]CargoExec{}
	for _, nome := range slices.Sorted(maps.Keys(e.Cargos)) {
		campo := "execucao.cargos." + nome
		can, ok := c.canonicoDoPapel(nome)
		if !ok || can == process.Humano {
			return fmt.Errorf("%s: papel desconhecido (use o nome do projeto ou o canônico de %v)", campo, process.Canonicos[:len(process.Canonicos)-1])
		}
		if _, dup := cargos[can]; dup {
			return fmt.Errorf("%s: o cargo %s aparece mais de uma vez", campo, can)
		}
		x := e.Cargos[nome]
		if x.Max < 1 {
			return fmt.Errorf("%s.max: precisa ser pelo menos 1 (agora %d)", campo, x.Max)
		}
		if strings.TrimSpace(x.Modelo) == "" {
			return fmt.Errorf("%s.modelo: obrigatório (ex.: sonnet, opus)", campo)
		}
		if !slices.Contains(Esforcos, x.Esforco) {
			return fmt.Errorf("%s.esforco: %q inválido (use %v)", campo, x.Esforco, Esforcos)
		}
		if x.PrazoTurno.Duration != 0 {
			if err := prazoTurnoValido(campo+".prazo_turno", x.PrazoTurno.Duration); err != nil {
				return err
			}
		}
		cargos[can] = x
	}
	e.Cargos = cargos
	return nil
}
