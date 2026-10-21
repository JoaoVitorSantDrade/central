//go:build !windows

package cli

import (
	"os/exec"
	"syscall"
)

// detach põe o processo numa sessão nova. breakaway só existe no Windows.
func detach(cmd *exec.Cmd, _ bool) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}
