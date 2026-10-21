package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/JoaoVitorSantDrade/central/internal/config"
	"github.com/JoaoVitorSantDrade/central/internal/core"
	"github.com/JoaoVitorSantDrade/central/internal/process"
	"github.com/JoaoVitorSantDrade/central/internal/shell"
)

// VagaGerente é a vaga que a abertura da sprint abre num projeto com [execucao].
const VagaGerente = "GERENTE1"

// Limites do despertar e da conferência do Claude Code.
const (
	MinDespertar      = time.Minute
	MaxDespertar      = 24 * time.Hour
	claudeCacheOK     = 5 * time.Minute
	claudeAuthTimeout = 30 * time.Second
)

// VagaAbrir são os dados de abrir uma vaga executada.
type VagaAbrir struct {
	Vaga  string `json:"vaga"`
	Papel string `json:"papel"`
	Area  string `json:"area"`
	Nota  string `json:"nota"`
}

// VagasInfo é a lista das vagas executadas com o estado do executor.
type VagasInfo struct {
	Vagas           []core.VagaExec `json:"vagas"`
	PlanoPausadoAte *time.Time      `json:"plano_pausado_ate"`
	PlanoMotivo     string          `json:"plano_motivo,omitempty"`
	CustoHoje       float64         `json:"custo_hoje"`
	Execucao        bool            `json:"execucao"`
}

// claudeCache guarda, por comando, a hora da última conferência que passou.
type claudeCache struct {
	mu sync.Mutex
	ok map[string]time.Time
}

// execucaoDe devolve o [execucao] do projeto ou ErrInvalid sem ele.
func execucaoDe(p *project) (config.ExecucaoSec, error) {
	cfg := p.config()
	if cfg.Execucao == nil {
		return config.ExecucaoSec{}, fmt.Errorf("%w: o projeto não tem [execucao] no central.toml", core.ErrInvalid)
	}
	return *cfg.Execucao, nil
}

func cargosDe(e config.ExecucaoSec) map[string]process.Cargo {
	out := map[string]process.Cargo{}
	for can, c := range e.Cargos {
		out[can] = process.Cargo{Max: c.Max, Modelo: c.Modelo, Esforco: c.Esforco}
	}
	return out
}

// ClaudeDisponivel confere que o Claude Code do [execucao] roda e tem login
// (`<claude> auth status` sai com 0). Um resultado bom vale por 5 min.
func (s *Service) ClaudeDisponivel(ctx context.Context, projetoID string) error {
	p, err := s.project(ctx, projetoID)
	if err != nil {
		return err
	}
	e, err := execucaoDe(p)
	if err != nil {
		return err
	}
	return s.conferirClaude(ctx, e.Claude, MeterDir(p.raiz, s.dataDir))
}

func (s *Service) conferirClaude(ctx context.Context, claude, dir string) error {
	s.claude.mu.Lock()
	quando, ok := s.claude.ok[claude]
	s.claude.mu.Unlock()
	if ok && s.now().Sub(quando) < claudeCacheOK {
		return nil
	}
	if err := RodarClaudeAuth(ctx, claude, dir); err != nil {
		return err
	}
	s.claude.mu.Lock()
	if s.claude.ok == nil {
		s.claude.ok = map[string]time.Time{}
	}
	s.claude.ok[claude] = s.now()
	s.claude.mu.Unlock()
	return nil
}

// RodarClaudeAuth roda `<claude> auth status` e explica a falha. O doctor usa o mesmo caminho.
func RodarClaudeAuth(ctx context.Context, claude, dir string) error {
	ctx, cancel := context.WithTimeout(ctx, claudeAuthTimeout)
	defer cancel()
	cmd := shell.Exec(ctx, claude, "auth", "status")
	cmd.Dir = dir
	cmd.WaitDelay = time.Second
	out, err := cmd.CombinedOutput()
	if err == nil {
		return nil
	}
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return fmt.Errorf("%q auth status passou de %v", claude, claudeAuthTimeout)
	}
	var ee interface{ ExitCode() int }
	if errors.As(err, &ee) {
		msg := strings.TrimSpace(string(out))
		if len(msg) > 300 {
			msg = msg[:300]
		}
		return fmt.Errorf("%q auth status saiu com %d (faça login com claude auth login): %s", claude, ee.ExitCode(), msg)
	}
	return fmt.Errorf("não consegui rodar %q (instale o Claude Code CLI ou ajuste [execucao] claude): %v", claude, err)
}

// quemAge devolve a vaga ligada à pasta ou, sem pasta (ou numa pasta que não
// entrou, como a raiz do projeto na máquina do humano), o humano.
func (s *Service) quemAge(ctx context.Context, p *project, pasta string) (string, error) {
	if pasta == "" {
		return core.Humano, nil
	}
	a, err := p.core.AgentByPasta(ctx, pasta)
	if errors.Is(err, core.ErrNotFound) {
		return core.Humano, nil
	}
	if err != nil {
		return "", err
	}
	if err := p.core.Touch(ctx, a.Vaga); err != nil {
		return "", err
	}
	return a.Vaga, nil
}

// AbrirVaga abre (ou reabre) uma vaga executada, como o GERENTE da pasta ou o humano.
func (s *Service) AbrirVaga(ctx context.Context, projetoID, pasta string, in VagaAbrir) (core.VagaExec, error) {
	p, err := s.project(ctx, projetoID)
	if err != nil {
		return core.VagaExec{}, err
	}
	e, err := execucaoDe(p)
	if err != nil {
		return core.VagaExec{}, err
	}
	quem, err := s.quemAge(ctx, p, pasta)
	if err != nil {
		return core.VagaExec{}, err
	}
	return s.abrirVaga(ctx, p, e, quem, in)
}

func (s *Service) abrirVaga(ctx context.Context, p *project, e config.ExecucaoSec, quem string, in VagaAbrir) (core.VagaExec, error) {
	claudeOK := s.conferirClaude(ctx, e.Claude, MeterDir(p.raiz, s.dataDir))
	return p.engine().AbrirVaga(ctx, quem, process.VagaInput{Vaga: in.Vaga, Papel: in.Papel, Area: in.Area, Nota: in.Nota},
		cargosDe(e), claudeOK)
}

// FecharVaga fecha uma vaga executada, como o GERENTE da pasta ou o humano.
func (s *Service) FecharVaga(ctx context.Context, projetoID, pasta, vaga string) (core.VagaExec, error) {
	p, err := s.project(ctx, projetoID)
	if err != nil {
		return core.VagaExec{}, err
	}
	quem, err := s.quemAge(ctx, p, pasta)
	if err != nil {
		return core.VagaExec{}, err
	}
	return p.engine().FecharVaga(ctx, quem, vaga)
}

// Vagas lista as vagas executadas, a pausa do plano e o custo de hoje.
func (s *Service) Vagas(ctx context.Context, projetoID, pasta string) (VagasInfo, error) {
	p, err := s.reader(ctx, projetoID, pasta)
	if err != nil {
		return VagasInfo{}, err
	}
	info := VagasInfo{Execucao: p.config().Execucao != nil}
	if info.Vagas, err = p.core.VagasExec(ctx); err != nil {
		return VagasInfo{}, err
	}
	ate, motivo, err := p.core.PlanoPausado(ctx)
	if err != nil {
		return VagasInfo{}, err
	}
	if !ate.IsZero() && p.core.Now().Before(ate) {
		info.PlanoPausadoAte, info.PlanoMotivo = &ate, motivo
	}
	info.CustoHoje, err = p.core.CustoHoje(ctx)
	return info, err
}

// Despertar pede um turno da vaga executada da pasta daqui a em (1 min a 24 h).
func (s *Service) Despertar(ctx context.Context, projetoID, pasta string, em time.Duration, motivo string) error {
	if em < MinDespertar || em > MaxDespertar {
		return fmt.Errorf("%w: --em precisa estar entre %v e %v", core.ErrInvalid, MinDespertar, MaxDespertar)
	}
	p, a, err := s.agent(ctx, projetoID, pasta)
	if err != nil {
		return err
	}
	return p.core.Despertar(ctx, a.Vaga, p.core.Now().Add(em), motivo)
}

// abrirGerenteDaSprint abre a GERENTE1 quando o projeto tem [execucao] com o
// cargo GERENTE e ela não está aberta. Um erro vira aviso: a sprint abre igual.
func (s *Service) abrirGerenteDaSprint(ctx context.Context, p *project, quem string) []string {
	cfg := p.config()
	if cfg.Execucao == nil {
		return nil
	}
	if _, ok := cfg.Execucao.Cargo(process.Gerente); !ok {
		return nil
	}
	if v, err := p.core.VagaExec(ctx, VagaGerente); err == nil && v.Estado == core.VagaAberta {
		return nil
	}
	in := VagaAbrir{Vaga: VagaGerente, Papel: process.Gerente}
	if _, err := s.abrirVaga(ctx, p, *cfg.Execucao, quem, in); err != nil {
		return []string{fmt.Sprintf("a vaga %s não abriu: %v", VagaGerente, err)}
	}
	return nil
}

// AbrirNoApp abre a sessão da vaga no app do Claude Code
// (`<claude> --desktop --resume <sessão>`, na pasta da vaga), sem esperar.
// Recusa enquanto um turno roda, para os dois não escreverem na mesma sessão.
func (s *Service) AbrirNoApp(ctx context.Context, projetoID, vaga string) error {
	p, err := s.project(ctx, projetoID)
	if err != nil {
		return err
	}
	e, err := execucaoDe(p)
	if err != nil {
		return err
	}
	v, err := p.core.VagaExec(ctx, vaga)
	if err != nil {
		return fmt.Errorf("vaga %s: %w", vaga, err)
	}
	if v.Pasta == "" {
		return fmt.Errorf("%w: a vaga %s ainda não teve um turno (a sessão não existe)", core.ErrConflict, vaga)
	}
	if v.TurnoDesde != nil {
		return fmt.Errorf("%w: a vaga %s está num turno; espere ele terminar ou feche a vaga", core.ErrConflict, vaga)
	}
	cmd := shell.Exec(context.Background(), e.Claude, "--desktop", "--resume", v.Sessao)
	cmd.Dir = v.Pasta
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("%w: não consegui rodar %q: %v", core.ErrInvalid, e.Claude, err)
	}
	go cmd.Wait()
	return nil
}
