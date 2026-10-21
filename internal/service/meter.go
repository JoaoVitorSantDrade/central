package service

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/JoaoVitorSantDrade/central/internal/core"
	"github.com/JoaoVitorSantDrade/central/internal/shell"
)

const (
	// MeterTimeout é quanto um comando de medidor pode demorar.
	MeterTimeout = 10 * time.Second
	// meterWaitDelay é quanto a espera pelo comando ainda aguarda a saída
	// depois de matá-lo, para um neto que herdou a saída não prender o medidor.
	meterWaitDelay = time.Second
)

// ParseMeterOutput lê a saída de um medidor: a primeira linha não vazia, com
// um número por coluna separado por vírgula, na ordem de mapa.
func ParseMeterOutput(out string, mapa []string) (core.Dims, error) {
	var linha string
	for l := range strings.Lines(out) {
		if l = strings.TrimSpace(l); l != "" {
			linha = l
			break
		}
	}
	if linha == "" {
		return nil, errors.New("saída vazia")
	}
	cols := strings.Split(linha, ",")
	if len(cols) != len(mapa) {
		return nil, fmt.Errorf("a linha %q tem %d colunas e o mapa %d (%s)", linha, len(cols), len(mapa), strings.Join(mapa, ", "))
	}
	d := core.Dims{}
	for i, col := range cols {
		v, err := strconv.ParseFloat(strings.TrimSpace(col), 64)
		if err != nil || math.IsNaN(v) || math.IsInf(v, 0) {
			return nil, fmt.Errorf("a coluna %d (%s) da linha %q não é um número", i+1, mapa[i], linha)
		}
		d[mapa[i]] = v
	}
	return d, nil
}

// MeterDir é a pasta onde um medidor roda: a raiz registrada do projeto, se
// ela existe; senão, a pasta de dados.
func MeterDir(raiz, dataDir string) string {
	if raiz != "" {
		if st, err := os.Stat(raiz); err == nil && st.IsDir() {
			return raiz
		}
	}
	return dataDir
}

// RunMeter roda o comando do medidor no shell do sistema (cmd /C no Windows,
// sh -c nos outros), na pasta dir (veja MeterDir), e lê a saída com
// ParseMeterOutput. Passado o timeout, mata a árvore do comando e devolve erro.
func RunMeter(ctx context.Context, dir, comando string, mapa []string, timeout time.Duration) (core.Dims, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := shell.Command(ctx, comando)
	cmd.Dir = dir
	cmd.WaitDelay = meterWaitDelay
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return nil, fmt.Errorf("o medidor passou do tempo de %v", timeout)
	}
	if err != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return nil, fmt.Errorf("%w: %s", err, msg)
		}
		return nil, err
	}
	return ParseMeterOutput(stdout.String(), mapa)
}

// meter é um medidor da configuração vigente de um projeto aberto.
type meter struct {
	core      *core.Core
	raiz      string // raiz registrada do projeto
	recurso   string
	comando   string
	mapa      []string
	intervalo time.Duration
}

// same diz se m e o medem o mesmo recurso do mesmo jeito.
func (m meter) same(o meter) bool {
	return m.core == o.core && m.raiz == o.raiz && m.comando == o.comando && m.intervalo == o.intervalo && slices.Equal(m.mapa, o.mapa)
}

type meterKey struct{ projeto, recurso string }

// wantedMeters lista os medidores da configuração vigente dos projetos abertos.
func (s *Service) wantedMeters() map[meterKey]meter {
	s.mu.Lock()
	ps := make(map[string]*project, len(s.projects))
	for id, p := range s.projects {
		ps[id] = p
	}
	s.mu.Unlock()
	ms := map[meterKey]meter{}
	for id, p := range ps {
		p.cfgMu.Lock()
		for nome, r := range p.cfg.Recursos {
			if m := r.Medidor; m != nil && m.Intervalo.Duration > 0 {
				ms[meterKey{id, nome}] = meter{core: p.core, raiz: p.raiz, recurso: nome, comando: m.Comando,
					mapa: slices.Clone(m.Mapa), intervalo: m.Intervalo.Duration}
			}
		}
		p.cfgMu.Unlock()
	}
	return ms
}

// wakeMeters avisa o RunMeters que um projeto abriu ou a configuração mudou.
func (s *Service) wakeMeters() {
	select {
	case s.meterWake <- struct{}{}:
	default:
	}
}

// RunMeters mantém, até ctx terminar, uma goroutine por recurso com medidor
// de cada projeto aberto, que mede no intervalo configurado e grava a leitura
// (core.RecordSample). Um erro vira o evento medidor.falhou, no máximo um por
// minuto por recurso. Quando um projeto abre ou a configuração muda, para os
// medidores que saíram ou mudaram e liga os novos. Volta só depois de parar
// todos.
func (s *Service) RunMeters(ctx context.Context) {
	type running struct {
		m      meter
		cancel context.CancelFunc
		done   chan struct{}
	}
	ativos := map[meterKey]*running{}
	stop := func(k meterKey) {
		r := ativos[k]
		r.cancel()
		<-r.done
		delete(ativos, k)
	}
	defer func() {
		for k := range ativos {
			stop(k)
		}
	}()
	for {
		quer := s.wantedMeters()
		for k, r := range ativos {
			if m, ok := quer[k]; !ok || !m.same(r.m) {
				stop(k)
			}
		}
		for k, m := range quer {
			if _, ok := ativos[k]; ok {
				continue
			}
			mctx, cancel := context.WithCancel(ctx)
			r := &running{m: m, cancel: cancel, done: make(chan struct{})}
			ativos[k] = r
			go func() { defer close(r.done); s.runMeter(mctx, k.projeto, m) }()
		}
		select {
		case <-ctx.Done():
			return
		case <-s.meterWake:
		}
	}
}

// runMeter mede agora e depois a cada intervalo, até ctx terminar.
func (s *Service) runMeter(ctx context.Context, projeto string, m meter) {
	t := time.NewTicker(m.intervalo)
	defer t.Stop()
	for {
		s.measure(ctx, projeto, m)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// measure roda o medidor uma vez e grava a leitura, ou o evento medidor.falhou.
func (s *Service) measure(ctx context.Context, projeto string, m meter) {
	// A leitura vale para o início da medição: uma concessão ativada enquanto
	// o comando rodava não aparece nela.
	quando := s.now()
	d, err := RunMeter(ctx, MeterDir(m.raiz, s.dataDir), m.comando, m.mapa, MeterTimeout)
	if err == nil {
		err = m.core.RecordSample(ctx, m.recurso, core.Sample{Quando: quando, Valores: d})
	}
	if err == nil || ctx.Err() != nil {
		return // parar o medidor no meio da leitura não é falha
	}
	gravou, ferr := m.core.MeterFailed(ctx, m.recurso, err.Error())
	if s.logf != nil && (gravou || ferr != nil) {
		s.logf("medidor %s/%s: %v", projeto, m.recurso, errors.Join(err, ferr))
	}
}
