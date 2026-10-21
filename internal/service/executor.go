package service

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/JoaoVitorSantDrade/central/internal/config"
	"github.com/JoaoVitorSantDrade/central/internal/core"
	"github.com/JoaoVitorSantDrade/central/internal/executor"
	"github.com/JoaoVitorSantDrade/central/internal/process"
)

// rodarTurno é a costura dos testes: roda um turno do Claude Code.
var rodarTurno = executor.Rodar

// Esperas antes de uma nova tentativa, pela falha seguida (spec do executor §3.3).
var esperasFalha = []time.Duration{time.Minute, 5 * time.Minute, 15 * time.Minute}

const (
	// MaxFalhasSeguidas suspende a vaga.
	MaxFalhasSeguidas = 3
	// PausaSemReset é a pausa do plano quando o limite não diz a hora do reset.
	PausaSemReset = 30 * time.Minute
	// semPIDApos é quanto um turno de outro daemon pode ficar sem pid antes de
	// ser dado como perdido (o pid é gravado logo depois de o processo começar).
	semPIDApos = time.Minute
)

// Falhas que só a recuperação dá a um turno.
const (
	FalhaDaemonReiniciou = "daemon_reiniciou"
	FalhaProcessoSumiu   = "processo_sumiu"
)

// Textos dos turnos: só modelos fixos mais dados da Central (spec do executor §4).
const (
	textoPrimeiro   = "Você é a vaga %s (papel %s%s) do projeto %s. Rode central entrar --vaga %s --papel %s%s e depois central brief --md, e siga o seu cartão.%s "
	textoFimDeTurno = `Ao terminar uma unidade de trabalho, termine o turno: a Central acorda você quando houver algo. Para esperar algo sem mensagem, use central despertar --em <duração> "<motivo>".`
	textoAcordou    = "Você acordou: %s (%d mensagens). Comece por central caixa."
	textoFalhou     = "O turno anterior terminou por %s; confira onde parou. "
)

// execEstado guarda os turnos que este processo está rodando.
type execEstado struct {
	mu      sync.Mutex
	emCurso map[string]bool // projeto/vaga
	wg      sync.WaitGroup
}

func chaveTurno(projeto, vaga string) string { return projeto + "/" + vaga }

func (e *execEstado) rodando(chave string) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.emCurso[chave]
}

func (e *execEstado) marcar(chave string, v bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.emCurso == nil {
		e.emCurso = map[string]bool{}
	}
	if v {
		e.emCurso[chave] = true
	} else {
		delete(e.emCurso, chave)
	}
}

// esperarTurnos espera os turnos deste processo terminarem (testes e desligamento).
func (s *Service) esperarTurnos() { s.exec.wg.Wait() }

// textoTurno monta o texto de um turno a partir dos modelos fixos.
func textoTurno(projeto string, v core.VagaExec, motivo string, n int, primeiro bool, falhaAnterior string) string {
	var b strings.Builder
	if falhaAnterior != "" {
		fmt.Fprintf(&b, textoFalhou, falhaAnterior)
	}
	if !primeiro {
		fmt.Fprintf(&b, textoAcordou, motivo, n)
		return b.String()
	}
	area, areaArg, nota := "", "", ""
	if v.Area != "" {
		area, areaArg = ", área "+v.Area, " --area "+v.Area
	}
	if v.Nota != "" {
		nota = " Nota do Gerente: " + v.Nota
	}
	fmt.Fprintf(&b, textoPrimeiro, v.Vaga, v.Papel, area, projeto, v.Vaga, v.Papel, areaArg, nota)
	b.WriteString(textoFimDeTurno)
	return b.String()
}

// logTurno é o arquivo do stream do turno n da vaga.
func (s *Service) logTurno(projeto, vaga string, n int) string {
	return filepath.Join(s.dataDir, "projetos", projeto, "turnos", vaga, strconv.Itoa(n)+".jsonl")
}

// RunExecutor conduz as vagas executadas de todos os projetos abertos até ctx
// terminar: um passo a cada intervalo ou logo depois de uma mudança num projeto.
// Os turnos em curso não param com ctx: o Claude Code segue sozinho, e a
// recuperação do próximo daemon o reconhece pelo pid.
func (s *Service) RunExecutor(ctx context.Context, intervalo time.Duration) {
	t := time.NewTicker(intervalo)
	defer t.Stop()
	for {
		s.passoExecutor(ctx, s.now())
		mudou := make(chan struct{}, 1)
		parar := make(chan struct{})
		for _, p := range s.projetosAbertos() {
			go func(ch <-chan struct{}) {
				select {
				case <-ch:
					select {
					case mudou <- struct{}{}:
					default:
					}
				case <-parar:
				}
			}(p.core.Changed())
		}
		select {
		case <-ctx.Done():
			close(parar)
			return
		case <-t.C:
		case <-mudou:
		}
		close(parar)
	}
}

func (s *Service) projetosAbertos() map[string]*project {
	s.mu.Lock()
	defer s.mu.Unlock()
	ps := make(map[string]*project, len(s.projects))
	for id, p := range s.projects {
		ps[id] = p
	}
	return ps
}

// passoExecutor faz um passo do escalonador em cada projeto com [execucao].
func (s *Service) passoExecutor(ctx context.Context, agora time.Time) {
	for id, p := range s.projetosAbertos() {
		cfg := p.config()
		if cfg.Execucao == nil {
			continue
		}
		if err := s.passoProjeto(ctx, id, p, cfg, agora); err != nil {
			s.logOnce("executor:"+id, "executor de vagas (%s): %v", id, err)
		} else {
			s.esquecerLog("executor:" + id)
		}
	}
}

func (s *Service) passoProjeto(ctx context.Context, id string, p *project, cfg config.ProjectConfig, agora time.Time) error {
	e := *cfg.Execucao
	vs, err := p.core.VagasExec(ctx)
	if err != nil {
		return err
	}
	// 1. Recuperação: turnos de outro daemon cujo processo já acabou.
	for _, v := range vs {
		if v.TurnoDesde == nil || s.exec.rodando(chaveTurno(id, v.Vaga)) {
			continue
		}
		if v.PID > 0 && processoVivo(v.PID) {
			continue
		}
		if v.PID == 0 && agora.Sub(*v.TurnoDesde) < semPIDApos {
			continue
		}
		r, ok := executor.LerLog(s.logTurno(id, v.Vaga, v.NTurnos))
		if !ok {
			r.Falha = FalhaDaemonReiniciou
			if v.PID == 0 {
				r.Falha = FalhaProcessoSumiu
			}
		}
		if err := s.fimDeTurno(ctx, p, e, v, r, agora.Sub(*v.TurnoDesde), agora); err != nil {
			return err
		}
	}
	// 2. Pausa do plano e teto de custo.
	ate, err := p.core.PlanoPausadoAte(ctx)
	if err != nil {
		return err
	}
	if !ate.IsZero() && !agora.Before(ate) {
		if err := p.core.RetomarPlano(ctx); err != nil {
			return err
		}
		ate = time.Time{}
	}
	if ate.IsZero() {
		if pausou, err := s.conferirTetoDeCusto(ctx, p, e, agora); err != nil || pausou {
			return err
		}
	} else {
		return nil
	}
	// 3. Escolha dos turnos.
	if vs, err = p.core.VagasExec(ctx); err != nil {
		return err
	}
	emCurso := 0
	for _, v := range vs {
		if v.TurnoDesde != nil {
			emCurso++
		}
	}
	despertares, err := p.core.DespertaresVencidos(ctx, agora)
	if err != nil {
		return err
	}
	roles := p.engine().Roles()
	type candidata struct {
		v       core.VagaExec
		motivo  string
		n       int
		gerente bool
		desde   time.Time
	}
	var cs []candidata
	for _, v := range vs {
		if v.Estado != core.VagaAberta || v.TurnoDesde != nil || s.exec.rodando(chaveTurno(id, v.Vaga)) {
			continue
		}
		if v.ProximoEm != nil && agora.Before(*v.ProximoEm) {
			continue
		}
		c := candidata{v: v, desde: *v.AbertaEm}
		if v.ProximoEm != nil {
			c.desde = *v.ProximoEm
		}
		can, _ := roles.Canonical(v.Papel)
		c.gerente = can == process.Gerente
		m, n, err := p.core.FirstUnread(ctx, v.Vaga)
		if err != nil && !errors.Is(err, core.ErrNotFound) {
			return err
		}
		if n > 0 && v.UltimoTurno != nil {
			// Só mensagem nova desde o último turno acorda: uma que o turno
			// deixou sem ler (ex.: central fora do PATH) não gera turnos sem fim.
			novas, err := p.core.NaoLidasDesde(ctx, v.Vaga, *v.UltimoTurno)
			if err != nil {
				return err
			}
			if novas == 0 {
				n = 0
			}
		}
		switch {
		case v.NTurnos == 0:
			c.motivo = "primeiro turno"
		case n > 0:
			c.motivo, c.n = "mensagem de "+m.De, n
		case despertares[v.Vaga] != "":
			c.motivo = "despertar: " + despertares[v.Vaga]
		case v.FalhaAnterior != "" && v.FalhaAnterior != core.FalhaLimitePlano:
			c.motivo = "nova tentativa"
		case v.FalhaAnterior == core.FalhaLimitePlano:
			c.motivo = "fim da pausa do plano"
		default:
			continue
		}
		cs = append(cs, c)
	}
	sort.SliceStable(cs, func(i, j int) bool {
		if cs[i].gerente != cs[j].gerente {
			return cs[i].gerente
		}
		return cs[i].desde.Before(cs[j].desde)
	})
	for _, c := range cs {
		if emCurso >= e.MaxSimultaneos {
			break
		}
		if err := s.iniciarTurno(ctx, id, p, e, c.v, c.motivo, c.n, agora); err != nil {
			return err
		}
		emCurso++
	}
	return nil
}

// conferirTetoDeCusto pausa o plano até 00:00 local quando o custo de hoje
// passou do teto configurado.
func (s *Service) conferirTetoDeCusto(ctx context.Context, p *project, e config.ExecucaoSec, agora time.Time) (bool, error) {
	if e.CustoDiaUSD <= 0 {
		return false, nil
	}
	hoje, err := p.core.CustoHoje(ctx)
	if err != nil || hoje < e.CustoDiaUSD {
		return false, err
	}
	l := agora.Local()
	meiaNoite := time.Date(l.Year(), l.Month(), l.Day()+1, 0, 0, 0, 0, time.Local)
	return true, p.core.PausarPlano(ctx, meiaNoite, fmt.Sprintf("teto de custo diário (US$ %.2f de US$ %.2f)", hoje, e.CustoDiaUSD))
}

// iniciarTurno reserva o turno no núcleo e o roda numa goroutine.
func (s *Service) iniciarTurno(ctx context.Context, id string, p *project, e config.ExecucaoSec, v core.VagaExec, motivo string, nMsgs int, agora time.Time) error {
	n, err := p.core.IniciarTurno(ctx, v.Vaga, motivo, 0)
	if errors.Is(err, core.ErrConflict) {
		return nil
	}
	if err != nil {
		return err
	}
	primeiro := v.Pasta == ""
	pasta := v.Pasta
	if primeiro {
		pasta = p.raiz
	}
	prazo := e.PrazoTurno.Duration
	if can, ok := p.engine().Roles().Canonical(v.Papel); ok {
		if c, ok := e.Cargo(can); ok {
			prazo = c.PrazoTurno.Duration
		}
	}
	t := executor.Turno{
		Claude: e.Claude, Pasta: pasta, Sessao: v.Sessao, Worktree: v.Vaga, Modelo: v.Modelo, Esforco: v.Esforco,
		Texto: textoTurno(id, v, motivo, nMsgs, primeiro, v.FalhaAnterior), Log: s.logTurno(id, v.Vaga, n),
		Primeiro: primeiro, Prazo: prazo,
		Ambiente: []string{"CENTRAL_HOME=" + s.dataDir, "CENTRAL_ADDR=" + config.Addr(), "CENTRAL_PROJETO=" + id, "CENTRAL_VAGA=" + v.Vaga},
	}
	chave := chaveTurno(id, v.Vaga)
	s.exec.marcar(chave, true)
	s.exec.wg.Add(1)
	go func() {
		defer s.exec.wg.Done()
		defer s.exec.marcar(chave, false)
		// O turno não depende do ctx do daemon: ele segue se o daemon parar.
		tctx := context.WithoutCancel(ctx)
		ini := time.Now()
		r, err := rodarTurno(tctx, t, func(pid int) {
			if err := p.core.RegistrarPID(tctx, v.Vaga, pid); err != nil {
				s.logOnce("executor:pid:"+chave, "executor de vagas (%s): gravar o pid de %s: %v", id, v.Vaga, err)
			}
		})
		if err != nil {
			r = executor.Resultado{Falha: executor.FalhaErro, Stderr: err.Error()}
		}
		if err := s.fimDeTurno(tctx, p, e, v, r, time.Since(ini), s.now()); err != nil {
			s.logOnce("executor:fim:"+chave, "executor de vagas (%s): fim do turno de %s: %v", id, v.Vaga, err)
		}
	}()
	return nil
}

// fimDeTurno aplica a regra das falhas e grava o fim do turno.
func (s *Service) fimDeTurno(ctx context.Context, p *project, e config.ExecucaoSec, v core.VagaExec, r executor.Resultado, dur time.Duration, agora time.Time) error {
	atual, err := p.core.VagaExec(ctx, v.Vaga)
	if err != nil {
		return err
	}
	fim := core.TurnoFim{Falha: r.Falha, Texto: r.Texto, Stderr: r.Stderr, Sessao: r.Sessao, Pasta: r.Pasta,
		CustoUSD: r.CustoUSD, TokensEntrada: r.TokensEntrada, TokensSaida: r.TokensSaida, Codigo: r.Codigo,
		Negacoes: r.Negacoes, Dur: dur}
	switch r.Falha {
	case executor.FalhaOK:
	case executor.FalhaLimitePlano:
		ate := agora.Add(PausaSemReset)
		if r.ResetEm != nil && r.ResetEm.After(agora) {
			ate = *r.ResetEm
		}
		if err := p.core.PausarPlano(ctx, ate, "limite de uso do plano"); err != nil {
			return err
		}
	default:
		falhas := atual.Falhas + 1
		i := min(falhas, len(esperasFalha)) - 1
		prox := agora.Add(esperasFalha[i])
		fim.ProximoEm = &prox
		fim.Suspender = falhas >= MaxFalhasSeguidas
	}
	if _, err := p.core.TerminarTurno(ctx, v.Vaga, fim); err != nil {
		return err
	}
	if r.CustoUSD > 0 {
		if pausado, err := p.core.PlanoPausadoAte(ctx); err == nil && pausado.IsZero() {
			_, err = s.conferirTetoDeCusto(ctx, p, e, agora)
			return err
		}
	}
	return nil
}
