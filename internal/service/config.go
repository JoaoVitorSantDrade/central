package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/JoaoVitorSantDrade/central/internal/config"
	"github.com/JoaoVitorSantDrade/central/internal/core"
	"github.com/JoaoVitorSantDrade/central/internal/process"
)

// parseConfig valida o texto do central.toml enviado pelo entrar.
func parseConfig(texto string) (config.ProjectConfig, error) {
	c, err := config.ParseProjectConfig([]byte(texto))
	if err != nil {
		return config.ProjectConfig{}, fmt.Errorf("%w: central.toml: %v", core.ErrInvalid, err)
	}
	return c, nil
}

// loadConfig interpreta a configuração vigente gravada no projeto.
// Sem nenhuma versão, ou se o texto gravado não passar mais na validação,
// vale a configuração padrão: o próximo entrar com um arquivo válido a troca.
func loadConfig(ctx context.Context, c *core.Core) (config.ProjectConfig, error) {
	texto, _, err := c.CurrentConfig(ctx)
	if err != nil {
		return config.ProjectConfig{}, err
	}
	if cfg, err := config.ParseProjectConfig([]byte(texto)); err == nil {
		return cfg, nil
	}
	return config.ParseProjectConfig(nil)
}

// setConfig troca a configuração vigente em memória e a aplica ao núcleo.
// Quem chama segura cfgMu (ou ainda é o único dono de p).
func (p *project) setConfig(cfg config.ProjectConfig) {
	p.cfg = cfg
	recursos := map[string]core.ResourceSpec{}
	for nome, r := range cfg.Recursos {
		// cfg já passou pela validação, então Spec não falha; um recurso que
		// falhasse ficaria de fora.
		if spec, err := r.Spec(nome); err == nil {
			recursos[nome] = spec
		}
	}
	p.core.SetSettings(core.Settings{SumidoApos: cfg.Agentes.SumidoApos.Duration, Resources: recursos})
	p.eng.Store(process.NewEngine(p.core, rolesOf(cfg), processConfig(cfg)))
}

// rolesOf monta os papéis do projeto a partir do [papeis].
func rolesOf(cfg config.ProjectConfig) process.Roles {
	return process.NewRoles(cfg.Papeis.Nomes, cfg.Papeis.Desligados, cfg.Papeis.Senior)
}

// processConfig converte as seções do processo do central.toml.
func processConfig(cfg config.ProjectConfig) process.Config {
	limites := map[string]time.Duration{}
	for k, v := range cfg.Processo.LimiteParado {
		limites[k] = v.Duration
	}
	return process.Config{
		Obrigatorios: cfg.Itens.Obrigatorios,
		Sprint: process.SprintCfg{Escopo: cfg.Sprint.Escopo.Duration, Congelamento: cfg.Sprint.Congelamento.Duration,
			Corte: cfg.Sprint.Corte.Duration, Retro: cfg.Sprint.Retro.Duration, Acordar: cfg.Sprint.Acordar},
		LimiteParado: limites,
		OrfasApos:    cfg.Processo.OrfasApos.Duration,
	}
}

func configHash(texto string) string {
	sum := sha256.Sum256([]byte(texto))
	return hex.EncodeToString(sum[:])
}

// ProjectConfig devolve a configuração vigente do projeto.
func (s *Service) ProjectConfig(ctx context.Context, projetoID string) (config.ProjectConfig, error) {
	p, err := s.project(ctx, projetoID)
	if err != nil {
		return config.ProjectConfig{}, err
	}
	p.cfgMu.Lock()
	defer p.cfgMu.Unlock()
	return p.cfg, nil
}
