// Package service reúne as operações públicas da Central. Todas as interfaces
// (HTTP, CLI, MCP, painel) chamam este pacote e nenhum outro (spec §4, regra 1).
package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/JoaoVitorSantDrade/central/internal/config"
	"github.com/JoaoVitorSantDrade/central/internal/core"
	"github.com/JoaoVitorSantDrade/central/internal/process"
	"github.com/JoaoVitorSantDrade/central/internal/store"
)

// Motivos de retorno de Wait.
const (
	MotivoMensagem = "mensagem"
	MotivoTempo    = "tempo"
)

// WaitResult diz por que um Wait terminou e o que fazer em seguida.
// Motivo vem da primeira não lida: se ela é da CENTRAL, a parte do tipo antes
// do ponto (recurso.concedida → recurso); senão, mensagem.
type WaitResult struct {
	Motivo    string        `json:"motivo"`
	Novas     int           `json:"novas"`
	Primeira  *core.Message `json:"primeira,omitempty"`
	Sugestao  string        `json:"sugestao"`
	EsperouMs int64         `json:"esperou_ms"`
}

// motivo deriva o Motivo de Wait a partir da primeira mensagem não lida.
func motivo(m core.Message) string {
	if m.De != core.Sistema {
		return MotivoMensagem
	}
	antes, _, _ := strings.Cut(m.Tipo, ".")
	return antes
}

// PadraoRenovarEspera é de quanto em quanto tempo um Wait bloqueado renova o
// batimento da vaga, para um esperar longo nunca parecer sumido.
const PadraoRenovarEspera = 60 * time.Second

type project struct {
	db   *sql.DB
	core *core.Core
	raiz string // raiz registrada (a do primeiro registro); os medidores rodam nela

	// cfgMu também serializa os Enter com configuração, para a vigente em
	// memória seguir a ordem em que as versões foram gravadas.
	cfgMu sync.Mutex
	cfg   config.ProjectConfig // vigente

	// eng é o processo do projeto, refeito a cada configuração nova.
	eng atomic.Pointer[process.Engine]
}

// engine devolve o processo vigente do projeto.
func (p *project) engine() *process.Engine { return p.eng.Load() }

// Service guarda o registro da máquina e os bancos abertos dos projetos.
type Service struct {
	dataDir  string
	now      func() time.Time
	registry *sql.DB

	mu       sync.Mutex
	projects map[string]*project

	// waitTouchEvery é o intervalo de renovação do batimento dentro de Wait.
	waitTouchEvery time.Duration
	// logf recebe os erros do RunTicker e dos medidores (nil: descarta).
	logf func(format string, args ...any)
	// meterWake acorda o RunMeters quando um projeto abre ou a configuração muda.
	meterWake chan struct{}
	// adapters guarda o último head sincronizado de cada projeto.
	adapters adapterHeads
	// extLocks serializa as chamadas de cada extensão de cada projeto.
	extLocks extLocks
	// entradas guarda o estado das pastas de entrada das extensões.
	entradas entradas

	// exec guarda os turnos do executor de vagas que este processo roda.
	exec execEstado

	logMu   sync.Mutex
	lastLog map[string]string
}

// esquecerLog volta a chave do logOnce ao estado sem erro, sem registrar nada:
// o mesmo erro, se voltar, aparece de novo.
func (s *Service) esquecerLog(chave string) {
	s.logMu.Lock()
	defer s.logMu.Unlock()
	delete(s.lastLog, chave)
}

// logOnce registra a mensagem só quando ela muda para a chave (sem inundar o log).
func (s *Service) logOnce(chave, format string, args ...any) {
	if s.logf == nil {
		return
	}
	msg := fmt.Sprintf(format, args...)
	s.logMu.Lock()
	defer s.logMu.Unlock()
	if s.lastLog == nil {
		s.lastLog = map[string]string{}
	}
	if s.lastLog[chave] == msg {
		return
	}
	s.lastLog[chave] = msg
	s.logf("%s", msg)
}

// New abre o registro em dataDir/registro.db. Guarda dataDir absoluto.
func New(dataDir string, now func() time.Time) (*Service, error) {
	if now == nil {
		now = time.Now
	}
	dataDir, err := filepath.Abs(dataDir)
	if err != nil {
		return nil, err
	}
	reg, err := store.Open(filepath.Join(dataDir, "registro.db"), registryMigrations, store.Options{BackupDir: filepath.Join(dataDir, BackupDir)})
	if err != nil {
		return nil, err
	}
	return &Service{dataDir: dataDir, now: now, registry: reg, projects: map[string]*project{},
		waitTouchEvery: PadraoRenovarEspera, meterWake: make(chan struct{}, 1)}, nil
}

// Close fecha todos os bancos.
func (s *Service) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	var errs []error
	for id, p := range s.projects {
		errs = append(errs, p.db.Close())
		delete(s.projects, id)
	}
	errs = append(errs, s.registry.Close())
	return errors.Join(errs...)
}

// BackupDir é a pasta das cópias dos bancos, dentro da pasta de dados.
const BackupDir = "backups"

func (s *Service) backupDir() string { return filepath.Join(s.dataDir, BackupDir) }

// BackupAll copia o registro (backups/registro-*.db) e os projetos abertos
// (backups/projetos/<id>-*.db), guardando as store.KeepBackups mais novas de
// cada banco. Um banco que falha não impede os outros.
func (s *Service) BackupAll(ctx context.Context) error {
	type alvo struct {
		db        *sql.DB
		dir, base string
	}
	s.mu.Lock()
	alvos := []alvo{{s.registry, s.backupDir(), "registro"}}
	for id, p := range s.projects {
		alvos = append(alvos, alvo{p.db, filepath.Join(s.backupDir(), "projetos"), id})
	}
	s.mu.Unlock()
	var errs []error
	for _, a := range alvos {
		if err := ctx.Err(); err != nil {
			return err
		}
		if _, err := store.Backup(a.db, a.dir, a.base, store.KeepBackups); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// DataDir devolve a pasta de dados (absoluta) deste serviço.
func (s *Service) DataDir() string { return s.dataDir }

func (s *Service) stamp() string { return s.now().UTC().Format(core.TimeLayout) }

func (s *Service) openProject(ctx context.Context, id string) (*project, error) {
	if p, ok := s.projects[id]; ok {
		return p, nil
	}
	var raiz string
	if err := s.registry.QueryRowContext(ctx, `SELECT raiz FROM projetos WHERE id = ?`, id).Scan(&raiz); err != nil {
		return nil, err
	}
	db, err := store.Open(filepath.Join(s.dataDir, "projetos", id+".db"), core.ProjectMigrations, store.Options{BackupDir: filepath.Join(s.backupDir(), "projetos")})
	if err != nil {
		return nil, err
	}
	c := core.New(db, s.now)
	cfg, err := loadConfig(ctx, c)
	if err != nil {
		db.Close()
		return nil, err
	}
	p := &project{db: db, core: c, raiz: raiz}
	p.setConfig(cfg)
	// O processo entra no relógio do núcleo por um gancho do Tick.
	c.AddTickHook(func(ctx context.Context) error { return p.engine().TickSprint(ctx) })
	s.projects[id] = p
	s.wakeMeters()
	return p, nil
}

func invalidID(id string) error {
	return fmt.Errorf("%w: id de projeto %q (use letras minúsculas, dígitos e hífen)", core.ErrInvalid, id)
}

// project devolve um projeto já registrado, ou core.ErrNotFound.
func (s *Service) project(ctx context.Context, id string) (*project, error) {
	if !core.ValidProjectID(id) {
		return nil, invalidID(id)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if p, ok := s.projects[id]; ok {
		return p, nil
	}
	var n int
	if err := s.registry.QueryRowContext(ctx, `SELECT count(*) FROM projetos WHERE id = ?`, id).Scan(&n); err != nil {
		return nil, err
	}
	if n == 0 {
		return nil, fmt.Errorf("projeto %q: %w", id, core.ErrNotFound)
	}
	return s.openProject(ctx, id)
}

// EnterInput são os dados do entrar. Config é o texto do central.toml;
// vazio mantém a configuração vigente.
type EnterInput struct {
	ProjetoID, Nome, Raiz, Pasta, Vaga, Papel, Config string
	// Area é a área preferida da vaga (opcional; usada pelo proxima).
	Area string
}

// Enter registra o projeto (se preciso), liga a vaga à pasta e grava a
// configuração enviada (só quando o texto muda). Com a configuração inválida
// nada muda: a vaga não entra e a anterior continua.
func (s *Service) Enter(ctx context.Context, in EnterInput) (core.Agent, error) {
	projetoID, nome, raiz, pasta, vaga, papel := in.ProjetoID, in.Nome, in.Raiz, in.Pasta, in.Vaga, in.Papel
	if !core.ValidProjectID(projetoID) {
		return core.Agent{}, invalidID(projetoID)
	}
	var cfg config.ProjectConfig
	if in.Config != "" {
		var err error
		if cfg, err = parseConfig(in.Config); err != nil {
			return core.Agent{}, err
		}
	}
	// A raiz fica a do primeiro registro: outra pasta do mesmo projeto não a troca.
	s.mu.Lock()
	_, err := s.registry.ExecContext(ctx, `INSERT INTO projetos (id, nome, raiz, ultimo_acesso) VALUES (?, ?, ?, ?)
		ON CONFLICT (id) DO UPDATE SET nome = excluded.nome, ultimo_acesso = excluded.ultimo_acesso`,
		projetoID, nome, raiz, s.stamp())
	var p *project
	if err == nil {
		p, err = s.openProject(ctx, projetoID)
	}
	s.mu.Unlock()
	if err != nil {
		return core.Agent{}, err
	}
	if in.Config == "" {
		a, _, err := p.core.EnterWith(ctx, core.EnterArgs{Vaga: vaga, Papel: papel, Pasta: pasta, Area: in.Area})
		return a, err
	}
	// A vaga e a nova versão da configuração entram na mesma transação.
	p.cfgMu.Lock()
	defer p.cfgMu.Unlock()
	a, _, err := p.core.EnterWith(ctx, core.EnterArgs{Vaga: vaga, Papel: papel, Pasta: pasta, Area: in.Area,
		Config: in.Config, Hash: configHash(in.Config)})
	if err != nil {
		return core.Agent{}, err
	}
	p.setConfig(cfg)
	s.wakeMeters()
	return a, nil
}

func (s *Service) agent(ctx context.Context, projetoID, pasta string) (*project, core.Agent, error) {
	p, err := s.project(ctx, projetoID)
	if err != nil {
		return nil, core.Agent{}, err
	}
	a, err := p.core.AgentByPasta(ctx, pasta)
	if err != nil {
		return nil, core.Agent{}, fmt.Errorf("pasta %q não entrou no projeto %q: %w", pasta, projetoID, err)
	}
	// Todo comando do agente renova o batimento (spec §5).
	if err := p.core.Touch(ctx, a.Vaga); err != nil {
		return nil, core.Agent{}, err
	}
	return p, a, nil
}

// Status grava o estado declarado e o status livre da vaga ligada à pasta.
func (s *Service) Status(ctx context.Context, projetoID, pasta, estado, status string) (core.Agent, error) {
	p, a, err := s.agent(ctx, projetoID, pasta)
	if err != nil {
		return core.Agent{}, err
	}
	return p.core.SetStatus(ctx, a.Vaga, estado, status)
}

// Leave encerra a vaga ligada à pasta.
func (s *Service) Leave(ctx context.Context, projetoID, pasta string) (core.Agent, error) {
	p, a, err := s.agent(ctx, projetoID, pasta)
	if err != nil {
		return core.Agent{}, err
	}
	return p.core.Leave(ctx, a.Vaga)
}

// Agents lista as vagas do projeto. Com pasta de uma vaga, renova o batimento
// dela; uma pasta que não entrou só lista.
func (s *Service) Agents(ctx context.Context, projetoID, pasta string) ([]core.Agent, error) {
	p, err := s.reader(ctx, projetoID, pasta)
	if err != nil {
		return nil, err
	}
	return p.core.Agents(ctx)
}

// reader abre o projeto para uma leitura. Com pasta de uma vaga, renova o
// batimento dela; uma pasta que não entrou só lê.
func (s *Service) reader(ctx context.Context, projetoID, pasta string) (*project, error) {
	p, err := s.project(ctx, projetoID)
	if err != nil {
		return nil, err
	}
	if pasta != "" {
		a, err := p.core.AgentByPasta(ctx, pasta)
		switch {
		case err == nil:
			if err := p.core.Touch(ctx, a.Vaga); err != nil {
				return nil, err
			}
		case !errors.Is(err, core.ErrNotFound):
			return nil, err
		}
	}
	return p, nil
}

// Send envia como a vaga ligada à pasta (in.De é ignorado).
func (s *Service) Send(ctx context.Context, projetoID, pasta string, in core.SendInput) (core.Message, error) {
	p, a, err := s.agent(ctx, projetoID, pasta)
	if err != nil {
		return core.Message{}, err
	}
	in.De = a.Vaga
	return p.core.Send(ctx, in)
}

// Reply responde, como a vaga ligada à pasta, à mensagem seq.
func (s *Service) Reply(ctx context.Context, projetoID, pasta string, seq int64, corpo string) (core.Message, error) {
	p, a, err := s.agent(ctx, projetoID, pasta)
	if err != nil {
		return core.Message{}, err
	}
	return p.core.Reply(ctx, a.Vaga, seq, corpo)
}

// Pending devolve as pendências de resposta da vaga ligada à pasta.
func (s *Service) Pending(ctx context.Context, projetoID, pasta string) (core.Pendencias, error) {
	p, a, err := s.agent(ctx, projetoID, pasta)
	if err != nil {
		return core.Pendencias{}, err
	}
	return p.core.Pending(ctx, a.Vaga)
}

// Inbox lê a caixa da vaga ligada à pasta.
func (s *Service) Inbox(ctx context.Context, projetoID, pasta string) ([]core.Message, error) {
	p, a, err := s.agent(ctx, projetoID, pasta)
	if err != nil {
		return nil, err
	}
	return p.core.Inbox(ctx, a.Vaga)
}

// Wait bloqueia até haver mensagem não lida para a vaga, o prazo vencer ou ctx terminar.
// Não avança o cursor. Renova o batimento no início, a cada waitTouchEvery
// enquanto espera e no retorno.
func (s *Service) Wait(ctx context.Context, projetoID, pasta string, ate time.Duration) (res WaitResult, err error) {
	p, a, err := s.agent(ctx, projetoID, pasta)
	if err != nil {
		return WaitResult{}, err
	}
	defer func() {
		// Mesmo com ctx cancelado: a espera terminou agora e o agente estava vivo.
		if terr := p.core.Touch(context.WithoutCancel(ctx), a.Vaga); terr != nil && err == nil {
			res, err = WaitResult{}, terr
		}
	}()
	start := time.Now()
	timer := time.NewTimer(ate)
	defer timer.Stop()
	renew := time.NewTicker(s.waitTouchEvery)
	defer renew.Stop()
	for {
		// O canal antes da contagem: uma mensagem que chegue entre as duas ainda acorda.
		ch := p.core.Changed()
		m, n, err := p.core.FirstUnread(ctx, a.Vaga)
		if err != nil {
			return WaitResult{}, err
		}
		if n > 0 {
			return WaitResult{Motivo: motivo(m), Novas: n, Primeira: &m, Sugestao: "central caixa", EsperouMs: time.Since(start).Milliseconds()}, nil
		}
		select {
		case <-ch:
		case <-renew.C:
			if err := p.core.Touch(ctx, a.Vaga); err != nil {
				return WaitResult{}, err
			}
		case <-timer.C:
			return WaitResult{Motivo: MotivoTempo, EsperouMs: time.Since(start).Milliseconds()}, nil
		case <-ctx.Done():
			return WaitResult{}, ctx.Err()
		}
	}
}

// Lock toma a trava nome para a vaga ligada à pasta.
func (s *Service) Lock(ctx context.Context, projetoID, pasta, nome, motivo string) (core.Lock, error) {
	p, a, err := s.agent(ctx, projetoID, pasta)
	if err != nil {
		return core.Lock{}, err
	}
	return p.core.Lock(ctx, a.Vaga, nome, motivo)
}

// Unlock solta a trava nome da vaga ligada à pasta.
func (s *Service) Unlock(ctx context.Context, projetoID, pasta, nome string) error {
	p, a, err := s.agent(ctx, projetoID, pasta)
	if err != nil {
		return err
	}
	return p.core.Unlock(ctx, a.Vaga, nome)
}

// Locks lista as travas do projeto. Com pasta de uma vaga, renova o batimento
// dela; uma pasta que não entrou só lista.
func (s *Service) Locks(ctx context.Context, projetoID, pasta string) ([]core.Lock, error) {
	p, err := s.reader(ctx, projetoID, pasta)
	if err != nil {
		return nil, err
	}
	return p.core.Locks(ctx)
}

// RequestGrant pede uma concessão do recurso para a vaga ligada à pasta (r.Vaga é ignorado).
func (s *Service) RequestGrant(ctx context.Context, projetoID, pasta string, r core.GrantRequest) (core.Grant, error) {
	p, a, err := s.agent(ctx, projetoID, pasta)
	if err != nil {
		return core.Grant{}, err
	}
	r.Vaga = a.Vaga
	return p.core.RequestGrant(ctx, r)
}

// ReleaseGrant solta o pedido ou a concessão da vaga ligada à pasta no recurso.
func (s *Service) ReleaseGrant(ctx context.Context, projetoID, pasta, recurso string) (core.Grant, error) {
	p, a, err := s.agent(ctx, projetoID, pasta)
	if err != nil {
		return core.Grant{}, err
	}
	return p.core.ReleaseGrant(ctx, a.Vaga, recurso)
}

// ReleaseGrantByID solta o pedido ou a concessão id, da vaga ligada à pasta,
// no recurso. De outra vaga, de outro recurso ou já encerrado: core.ErrNotFound.
func (s *Service) ReleaseGrantByID(ctx context.Context, projetoID, pasta, recurso string, id int64) (core.Grant, error) {
	p, a, err := s.agent(ctx, projetoID, pasta)
	if err != nil {
		return core.Grant{}, err
	}
	g, err := p.core.GrantByID(ctx, id)
	if err != nil {
		return core.Grant{}, err
	}
	if g.Recurso != recurso {
		return core.Grant{}, fmt.Errorf("pedido %d de %s em %s: %w", id, a.Vaga, recurso, core.ErrNotFound)
	}
	return p.core.ReleaseGrantByID(ctx, a.Vaga, id)
}

// ResourceStatus devolve o estado do recurso. Com pasta de uma vaga, renova o
// batimento dela; uma pasta que não entrou só lê.
func (s *Service) ResourceStatus(ctx context.Context, projetoID, pasta, nome string) (core.ResourceView, error) {
	p, err := s.reader(ctx, projetoID, pasta)
	if err != nil {
		return core.ResourceView{}, err
	}
	return p.core.ResourceStatus(ctx, nome)
}

// Motivos de retorno de WaitGrant (além de MotivoTempo).
const (
	MotivoConcedida = "concedida"
	MotivoEncerrada = "encerrada"
)

// GrantWaitResult diz como terminou a espera por uma concessão.
type GrantWaitResult struct {
	Motivo    string     `json:"motivo"`
	Concessao core.Grant `json:"concessao"`
	EsperouMs int64      `json:"esperou_ms"`
}

// WaitGrant bloqueia até o pedido id (da vaga ligada à pasta, no recurso)
// sair da fila, o prazo vencer ou ctx terminar. Ativo: motivo concedida;
// encerrado sem ter sido ativado agora (solto ou expirado): encerrada; ainda
// na fila no prazo: tempo. Renova o batimento como Wait.
func (s *Service) WaitGrant(ctx context.Context, projetoID, pasta, recurso string, id int64, ate time.Duration) (res GrantWaitResult, err error) {
	p, a, err := s.agent(ctx, projetoID, pasta)
	if err != nil {
		return GrantWaitResult{}, err
	}
	defer func() {
		if terr := p.core.Touch(context.WithoutCancel(ctx), a.Vaga); terr != nil && err == nil {
			res, err = GrantWaitResult{}, terr
		}
	}()
	start := time.Now()
	timer := time.NewTimer(ate)
	defer timer.Stop()
	renew := time.NewTicker(s.waitTouchEvery)
	defer renew.Stop()
	for {
		ch := p.core.Changed()
		g, err := p.core.GrantByID(ctx, id)
		if err != nil {
			return GrantWaitResult{}, err
		}
		if g.Vaga != a.Vaga || g.Recurso != recurso {
			return GrantWaitResult{}, fmt.Errorf("pedido %d de %s em %s: %w", id, a.Vaga, recurso, core.ErrNotFound)
		}
		switch g.Estado {
		case core.GrantAtiva:
			return GrantWaitResult{Motivo: MotivoConcedida, Concessao: g, EsperouMs: time.Since(start).Milliseconds()}, nil
		case core.GrantEncerrada:
			return GrantWaitResult{Motivo: MotivoEncerrada, Concessao: g, EsperouMs: time.Since(start).Milliseconds()}, nil
		}
		select {
		case <-ch:
		case <-renew.C:
			if err := p.core.Touch(ctx, a.Vaga); err != nil {
				return GrantWaitResult{}, err
			}
		case <-timer.C:
			return GrantWaitResult{Motivo: MotivoTempo, Concessao: g, EsperouMs: time.Since(start).Milliseconds()}, nil
		case <-ctx.Done():
			return GrantWaitResult{}, ctx.Err()
		}
	}
}

// SetFact grava um fato como a vaga ligada à pasta.
func (s *Service) SetFact(ctx context.Context, projetoID, pasta, chave string, valor json.RawMessage, motivo string) (core.Fact, error) {
	p, a, err := s.agent(ctx, projetoID, pasta)
	if err != nil {
		return core.Fact{}, err
	}
	return p.core.SetFact(ctx, a.Vaga, chave, valor, motivo)
}

// GetFact devolve o valor vigente da chave. Com pasta de uma vaga, renova o batimento.
func (s *Service) GetFact(ctx context.Context, projetoID, pasta, chave string) (core.Fact, error) {
	p, err := s.reader(ctx, projetoID, pasta)
	if err != nil {
		return core.Fact{}, err
	}
	return p.core.GetFact(ctx, chave)
}

// FactHistory lista os valores da chave, do mais novo ao mais velho.
func (s *Service) FactHistory(ctx context.Context, projetoID, pasta, chave string) ([]core.Fact, error) {
	p, err := s.reader(ctx, projetoID, pasta)
	if err != nil {
		return nil, err
	}
	return p.core.FactHistory(ctx, chave)
}

// Facts lista o fato vigente de cada chave.
func (s *Service) Facts(ctx context.Context, projetoID, pasta string) ([]core.Fact, error) {
	p, err := s.reader(ctx, projetoID, pasta)
	if err != nil {
		return nil, err
	}
	return p.core.Facts(ctx)
}

// NextID entrega o próximo id do prefixo à vaga ligada à pasta.
func (s *Service) NextID(ctx context.Context, projetoID, pasta, prefixo string, minimo int) (string, error) {
	p, a, err := s.agent(ctx, projetoID, pasta)
	if err != nil {
		return "", err
	}
	return p.core.NextID(ctx, a.Vaga, prefixo, minimo)
}
