//go:build !windows

package service

import (
	"errors"
	"syscall"
)

// processoVivo diz se o pid é de um processo que ainda roda. Sem permissão
// para sinalizá-lo, o processo existe.
func processoVivo(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}
