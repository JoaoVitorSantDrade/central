//go:build windows

package cli

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

// pathUsuario é o PATH do usuário em HKCU\Environment (sem administrador).
type pathUsuario struct{}

func (pathUsuario) Ler() (string, error) {
	k, err := registry.OpenKey(registry.CURRENT_USER, `Environment`, registry.QUERY_VALUE)
	if err != nil {
		return "", err
	}
	defer k.Close()
	v, _, err := k.GetStringValue("Path")
	if errors.Is(err, registry.ErrNotExist) {
		return "", nil
	}
	return v, err
}

func (pathUsuario) Gravar(v string) error {
	k, err := registry.OpenKey(registry.CURRENT_USER, `Environment`, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()
	if err := k.SetExpandStringValue("Path", v); err != nil {
		return err
	}
	avisarAmbienteMudou()
	return nil
}

// avisarAmbienteMudou manda WM_SETTINGCHANGE para os terminais novos verem o PATH.
func avisarAmbienteMudou() {
	user32 := windows.NewLazySystemDLL("user32.dll")
	send := user32.NewProc("SendMessageTimeoutW")
	env, _ := syscall.UTF16PtrFromString("Environment")
	const hwndBroadcast, wmSettingChange, smtoAbortIfHung = 0xffff, 0x001A, 0x0002
	var resultado uintptr
	send.Call(hwndBroadcast, wmSettingChange, 0, uintptr(unsafe.Pointer(env)), smtoAbortIfHung, 2000, uintptr(unsafe.Pointer(&resultado)))
}

func pastaInstalacaoPadrao() (string, error) {
	base := os.Getenv("LOCALAPPDATA")
	if base == "" {
		return "", errors.New("LOCALAPPDATA não definida")
	}
	return filepath.Join(base, "Programs", "central"), nil
}

const pathWindows = true
