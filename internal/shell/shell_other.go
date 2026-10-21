//go:build !windows

package shell

import (
	"context"
	"os/exec"
	"syscall"
)

// Command roda comando no sh, num grupo de processos próprio para o
// cancelamento matar a árvore inteira.
func Command(ctx context.Context, comando string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, "sh", "-c", comando)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	return cmd
}
