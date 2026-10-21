//go:build windows

package shell

import (
	"context"
	"os/exec"
	"strconv"
	"syscall"
	"time"
)

// createNoWindow impede que o cmd abra uma janela de console: o daemon roda
// sem console e o medidor roda a cada poucos segundos; as extensões também rodam assim.
const createNoWindow = 0x08000000

// taskkillTimeout é quanto o taskkill pode demorar antes de cair no Process.Kill.
const taskkillTimeout = 2 * time.Second

// Command roda comando no cmd do Windows. /S /C com o comando entre
// aspas passa o texto ao cmd como está, sem a citação de argumentos do Go.
// O cancelamento mata a árvore: um filho do cmd preso seguraria a saída aberta.
func Command(ctx context.Context, comando string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, "cmd")
	cmd.SysProcAttr = &syscall.SysProcAttr{CmdLine: `cmd /S /C "` + comando + `"`, HideWindow: true, CreationFlags: createNoWindow}
	cmd.Cancel = killTree(cmd)
	return cmd
}

// createNewProcessGroup separa o processo do console do daemon: um Ctrl+C no
// daemon não chega a um turno do executor de vagas.
const createNewProcessGroup = 0x00000200

// Exec roda o programa direto, sem o cmd, com os argumentos citados pelo Go, e o
// mesmo cancelamento que mata a árvore. O executor de vagas usa este caminho.
func Exec(ctx context.Context, nome string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, nome, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: createNoWindow | createNewProcessGroup}
	cmd.Cancel = killTree(cmd)
	return cmd
}

// killTree mata o processo e os filhos dele pelo taskkill.
func killTree(cmd *exec.Cmd) func() error {
	return func() error {
		// O taskkill também tem prazo: preso, ele prenderia o medidor e o desligamento do daemon.
		kctx, cancel := context.WithTimeout(context.Background(), taskkillTimeout)
		defer cancel()
		kill := exec.CommandContext(kctx, "taskkill", "/T", "/F", "/PID", strconv.Itoa(cmd.Process.Pid))
		kill.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: createNoWindow}
		if err := kill.Run(); err != nil {
			return cmd.Process.Kill()
		}
		return nil
	}
}
