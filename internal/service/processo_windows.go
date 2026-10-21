//go:build windows

package service

import (
	"errors"

	"golang.org/x/sys/windows"
)

// stillActive é o código de saída de um processo que ainda roda.
const stillActive = 259

// processoVivo diz se o pid é de um processo que ainda roda. Sem permissão
// para abri-lo, o processo existe.
func processoVivo(pid int) bool {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return errors.Is(err, windows.ERROR_ACCESS_DENIED)
	}
	defer windows.CloseHandle(h)
	var code uint32
	if err := windows.GetExitCodeProcess(h, &code); err != nil {
		return true
	}
	return code == stillActive
}
