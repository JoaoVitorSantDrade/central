// Package daemon roda o processo único da máquina que serve a API e escreve nos bancos.
package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/gofrs/flock"

	"github.com/JoaoVitorSantDrade/central/internal/api"
	"github.com/JoaoVitorSantDrade/central/internal/service"
	"github.com/JoaoVitorSantDrade/central/internal/version"
	"github.com/JoaoVitorSantDrade/central/internal/web"
)

// Arquivos do daemon dentro da pasta de dados.
const (
	LockFile = "daemon.lock"
	InfoFile = "daemon.json"
	LogFile  = "logs/daemon.log"
)

// TickEvery é o intervalo do trabalho periódico dos projetos (prazos, sumidos).
const TickEvery = 2 * time.Second

// BackupEvery é o intervalo das cópias dos bancos.
const BackupEvery = time.Hour

// ExecutorEvery é o tique do executor de vagas; uma mudança num projeto o acorda antes.
const ExecutorEvery = 2 * time.Second

// Rotação do log: ao subir, um daemon.log acima de LogMaxBytes vira .1, e as
// cópias antigas andam até LogKeep (a mais velha é apagada).
const (
	LogMaxBytes = 10 << 20
	LogKeep     = 3
)

var (
	ErrAlreadyRunning = errors.New("já existe um daemon usando esta pasta de dados")
	ErrNotLoopback    = errors.New("o daemon só escuta em endereço de loopback (127.0.0.1)")
)

// Info é o que o daemon grava em daemon.json enquanto roda.
type Info struct {
	PID    int    `json:"pid"`
	Addr   string `json:"addr"`
	Versao string `json:"versao"`
	Inicio string `json:"inicio"`
}

func checkLoopback(addr string) error {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrNotLoopback, err)
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return fmt.Errorf("%w: %q", ErrNotLoopback, addr)
	}
	return nil
}

// Run serve até ctx terminar. Só uma instância por pasta de dados.
func Run(ctx context.Context, dataDir, addr string) error {
	// encerrar é o mesmo desligamento do sinal, pedido pela API (central parar).
	ctx, encerrar := context.WithCancel(ctx)
	defer encerrar()
	if err := checkLoopback(addr); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(dataDir, "logs"), 0o755); err != nil {
		return err
	}
	lock := flock.New(filepath.Join(dataDir, LockFile))
	ok, err := lock.TryLock()
	if err != nil {
		return err
	}
	if !ok {
		return ErrAlreadyRunning
	}
	defer lock.Unlock()

	// Só gira com a trava na mão: um segundo daemon não mexe no log do primeiro.
	// Uma falha (ex.: no Windows, um editor com o log aberto) não impede o
	// daemon: ele continua escrevendo no log atual e anota a falha.
	rotErr := rotateLog(filepath.Join(dataDir, LogFile))
	logf, err := os.OpenFile(filepath.Join(dataDir, LogFile), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer logf.Close()
	logger := log.New(logf, "", log.LstdFlags|log.LUTC)
	if rotErr != nil {
		logger.Printf("girar o log falhou (continuo no atual): %v", rotErr)
	}

	svc, err := service.New(dataDir, nil)
	if err != nil {
		return err
	}
	defer svc.Close()
	// O Tick só alcança projetos abertos: abre todos os registrados ao subir.
	// Um projeto que não abre fica no log e não impede o daemon.
	if err := svc.OpenAll(ctx); err != nil {
		logger.Printf("abrir projetos: %v", err)
	}
	// Uma cópia já ao subir: um daemon reiniciado mais de uma vez por hora
	// nunca chegaria à primeira cópia periódica.
	if err := svc.BackupAll(ctx); err != nil {
		logger.Printf("backup: %v", err)
	}
	svc.SetLogf(logger.Printf)
	tickCtx, stopTick := context.WithCancel(ctx)
	tickDone := make(chan struct{})
	go func() { defer close(tickDone); svc.RunTicker(tickCtx, TickEvery) }()
	// Para o ticker antes de fechar os bancos (os defer rodam na ordem inversa).
	defer func() { stopTick(); <-tickDone }()
	// Os medidores também param antes de fechar os bancos.
	meterCtx, stopMeters := context.WithCancel(ctx)
	metersDone := make(chan struct{})
	go func() { defer close(metersDone); svc.RunMeters(meterCtx) }()
	defer func() { stopMeters(); <-metersDone }()
	// As cópias de hora em hora também param antes de fechar os bancos.
	backupCtx, stopBackups := context.WithCancel(ctx)
	backupsDone := make(chan struct{})
	go func() { defer close(backupsDone); runBackups(backupCtx, svc, BackupEvery, logger.Printf) }()
	defer func() { stopBackups(); <-backupsDone }()
	// O adaptador do backlog em arquivos (spec §6.5) também para antes de fechar os bancos.
	adapterCtx, stopAdapters := context.WithCancel(ctx)
	adaptersDone := make(chan struct{})
	go func() { defer close(adaptersDone); svc.RunAdapters(adapterCtx, time.Second) }()
	defer func() { stopAdapters(); <-adaptersDone }()
	// A pasta de entrada das extensões (spec §9) também para antes de fechar os bancos.
	entradaCtx, stopEntradas := context.WithCancel(ctx)
	entradasDone := make(chan struct{})
	go func() { defer close(entradasDone); svc.RunEntradas(entradaCtx, service.PadraoIntervaloEntrada) }()
	defer func() { stopEntradas(); <-entradasDone }()
	// O executor de vagas também para antes de fechar os bancos; os turnos em
	// curso seguem sozinhos e o próximo daemon os reconhece pelo pid.
	execCtx, stopExec := context.WithCancel(ctx)
	execDone := make(chan struct{})
	go func() { defer close(execDone); svc.RunExecutor(execCtx, ExecutorEvery) }()
	defer func() { stopExec(); <-execDone }()

	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	srv := &http.Server{
		Handler:           web.Handler(comEncerrar(api.NewHandler(svc), encerrar)),
		ReadHeaderTimeout: 10 * time.Second,
		// Sem WriteTimeout: o long-poll de esperar dura até 60 min.
		BaseContext: func(net.Listener) context.Context { return ctx },
		ErrorLog:    logger,
	}

	info := Info{PID: os.Getpid(), Addr: ln.Addr().String(), Versao: version.Versao, Inicio: time.Now().UTC().Format(time.RFC3339)}
	infoPath := filepath.Join(dataDir, InfoFile)
	b, _ := json.Marshal(info)
	if err := os.WriteFile(infoPath, b, 0o644); err != nil {
		ln.Close()
		return err
	}
	defer os.Remove(infoPath)
	logger.Printf("daemon %s escutando em %s (pid %d)", version.Versao, info.Addr, info.PID)

	errc := make(chan error, 1)
	go func() { errc <- srv.Serve(ln) }()
	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}
	shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdown); err != nil {
		srv.Close()
	}
	logger.Printf("daemon encerrado")
	return nil
}

// runBackups chama BackupAll a cada intervalo até ctx terminar.
func runBackups(ctx context.Context, svc *service.Service, every time.Duration, logf func(string, ...any)) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if err := svc.BackupAll(ctx); err != nil && ctx.Err() == nil {
				logf("backup: %v", err)
			}
		}
	}
}

// renameFile é os.Rename; os testes trocam para simular falha.
var renameFile = os.Rename

// rotateLog gira path se ele passa de LogMaxBytes: path.(LogKeep-1) vira
// path.LogKeep (a mais velha some), ..., path vira path.1.
func rotateLog(path string) error {
	st, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if st.Size() <= LogMaxBytes {
		return nil
	}
	for i := LogKeep - 1; i >= 1; i-- {
		from := fmt.Sprintf("%s.%d", path, i)
		if err := renameFile(from, fmt.Sprintf("%s.%d", path, i+1)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return renameFile(path, path+".1")
}

// RotaEncerrar é a rota que encerra o daemon (central parar, troca de versão).
const RotaEncerrar = "POST /v1/daemon/encerrar"

// comEncerrar acrescenta a rota de encerrar à API: responde e então cancela,
// para a resposta sair antes do desligamento.
func comEncerrar(api http.Handler, encerrar context.CancelFunc) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/", api)
	mux.HandleFunc(RotaEncerrar, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Write([]byte(`{"ok":true,"encerrando":true}` + "\n"))
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		go func() { time.Sleep(100 * time.Millisecond); encerrar() }()
	})
	return mux
}
