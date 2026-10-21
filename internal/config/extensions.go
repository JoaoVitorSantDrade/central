package config

import (
	"errors"
	"fmt"
	"maps"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"
)

// Limites do prazo de uma chamada de extensão (spec §9).
const (
	PadraoExtTimeout = 60 * time.Second
	MinExtTimeout    = time.Second
	MaxExtTimeout    = 60 * time.Minute
)

// VisoesExt são as visões de aba que o painel sabe desenhar.
var VisoesExt = []string{"tabela", "galeria", "serie", "formulario"}

// ComandosReservados são os comandos da própria CLI: nenhuma extensão pode
// ter um desses nomes (o teste da CLI confere que a lista está completa).
var ComandosReservados = []string{
	"versao", "daemon", "doctor", "painel", "parar", "instalar", "desinstalar", "atualizar", "init", "mcp", "rodar",
	"entrar", "enviar", "responder", "pendentes", "caixa", "esperar", "status", "sair", "agentes",
	"trava", "soltar", "travas", "fato", "fatos", "id", "recurso",
	"item", "proxima", "reservar", "revisar", "reabrir", "concluir", "aprovar", "integrar",
	"bloquear", "rejeitar", "validar", "sprint", "decisao", "saude", "orfas", "metricas",
	"importar-locks", "exportar-locks", "brief", "regra",
}

var (
	nomeExtensao = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)
	nomeAcao     = regexp.MustCompile(`^[a-z][a-z0-9_-]*$`)
	// TipoEventoExt é o formato do tipo de um evento de extensão (sem o prefixo ext.).
	TipoEventoExt = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]*$`)
)

// ExtensaoSec é uma extensão ext/v1 declarada no central.toml.
type ExtensaoSec struct {
	Nome    string      `toml:"nome" json:"nome"`
	Comando string      `toml:"comando" json:"comando"`
	Timeout Duration    `toml:"timeout" json:"timeout"`
	CLI     []string    `toml:"cli" json:"cli"`
	Aba     *AbaSec     `toml:"aba" json:"aba,omitempty"`
	Entrada *EntradaSec `toml:"entrada" json:"entrada,omitempty"`
}

// AbaSec é a aba da extensão no painel: a visão e a ação que a preenche.
type AbaSec struct {
	Titulo string `toml:"titulo" json:"titulo"`
	Visao  string `toml:"visao" json:"visao"`
	Acao   string `toml:"acao" json:"acao"`
}

// EntradaSec é a pasta de entrada: arquivos soltos nela chamam ao_chegar.
type EntradaSec struct {
	Pasta    string `toml:"pasta" json:"pasta"`
	AoChegar string `toml:"ao_chegar" json:"ao_chegar"`
}

// Acoes são as ações chamáveis da extensão: as do cli, a da aba e a da entrada.
func (e ExtensaoSec) Acoes() map[string]bool {
	out := map[string]bool{}
	for _, a := range e.CLI {
		out[a] = true
	}
	if e.Aba != nil {
		out[e.Aba.Acao] = true
	}
	if e.Entrada != nil {
		out[e.Entrada.AoChegar] = true
	}
	return out
}

// Extensao devolve a extensão pelo nome.
func (c ProjectConfig) Extensao(nome string) (ExtensaoSec, bool) {
	for _, e := range c.Extensoes {
		if e.Nome == nome {
			return e, true
		}
	}
	return ExtensaoSec{}, false
}

func validarAcao(campo, a string) error {
	if !nomeAcao.MatchString(a) {
		return fmt.Errorf("%s: acao %q (use letras minúsculas, dígitos, _ e -)", campo, a)
	}
	return nil
}

// normalizeExtensoes aplica os padrões e valida [[extensoes]] e [acordar].
func (c *ProjectConfig) normalizeExtensoes() error {
	vistas := map[string]bool{}
	for i := range c.Extensoes {
		e := &c.Extensoes[i]
		campo := fmt.Sprintf("extensoes[%d]", i)
		if !nomeExtensao.MatchString(e.Nome) {
			return fmt.Errorf("%s: nome %q (use letras minúsculas, dígitos e hífen, começando por letra)", campo, e.Nome)
		}
		campo = "extensoes." + e.Nome
		if slices.Contains(ComandosReservados, e.Nome) {
			return fmt.Errorf("%s: nome reservado (é um comando da central)", campo)
		}
		if vistas[e.Nome] {
			return fmt.Errorf("%s: extensão repetida", campo)
		}
		vistas[e.Nome] = true
		if strings.TrimSpace(e.Comando) == "" {
			return fmt.Errorf("%s: comando é obrigatório", campo)
		}
		if e.Timeout.Duration == 0 {
			e.Timeout.Duration = PadraoExtTimeout
		}
		if e.Timeout.Duration < MinExtTimeout || e.Timeout.Duration > MaxExtTimeout {
			return fmt.Errorf("%s: timeout precisa ficar entre %v e %v", campo, MinExtTimeout, MaxExtTimeout)
		}
		for _, a := range e.CLI {
			if err := validarAcao(campo+".cli", a); err != nil {
				return err
			}
		}
		if e.Aba != nil {
			if !slices.Contains(VisoesExt, e.Aba.Visao) {
				return fmt.Errorf("%s.aba: visao %q (use %v)", campo, e.Aba.Visao, VisoesExt)
			}
			if e.Aba.Acao == "" {
				return fmt.Errorf("%s.aba: acao é obrigatória", campo)
			}
			if err := validarAcao(campo+".aba", e.Aba.Acao); err != nil {
				return err
			}
			if e.Aba.Titulo == "" {
				e.Aba.Titulo = e.Nome
			}
		}
		if e.Entrada != nil {
			if e.Entrada.AoChegar == "" {
				return fmt.Errorf("%s.entrada: ao_chegar é obrigatório", campo)
			}
			if err := validarAcao(campo+".entrada", e.Entrada.AoChegar); err != nil {
				return err
			}
			p := filepath.ToSlash(e.Entrada.Pasta)
			if p == "" || path.IsAbs(p) || filepath.IsAbs(e.Entrada.Pasta) || filepath.VolumeName(e.Entrada.Pasta) != "" ||
				slices.Contains(strings.Split(p, "/"), "..") {
				return fmt.Errorf("%s.entrada: pasta %q precisa ser relativa à raiz do projeto, sem ..", campo, e.Entrada.Pasta)
			}
			e.Entrada.Pasta = path.Clean(p)
		}
	}
	for _, tipo := range slices.Sorted(maps.Keys(c.Acordar)) {
		if !TipoEventoExt.MatchString(tipo) {
			return fmt.Errorf("acordar: tipo %q (use letras minúsculas, dígitos, . _ e -)", tipo)
		}
		papeis := c.Acordar[tipo]
		if len(papeis) == 0 {
			return fmt.Errorf("acordar.%s: diga ao menos um papel", tipo)
		}
		for i, p := range papeis {
			p = strings.ToUpper(strings.TrimSpace(p))
			if p == "" {
				return errors.New("acordar." + tipo + ": papel vazio")
			}
			papeis[i] = p
		}
	}
	return nil
}
