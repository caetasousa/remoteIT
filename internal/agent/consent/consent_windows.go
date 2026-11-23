//go:build windows

package consent

import (
	"syscall"
	"unsafe"
)

var pMessageBoxW = syscall.NewLazyDLL("user32.dll").NewProc("MessageBoxW")

// Dialog mostra uma caixa Sim/Não sempre por cima das outras janelas.
func Dialog(host string) bool {
	txt, _ := syscall.UTF16PtrFromString("O Suporte de TI deseja VER e CONTROLAR a tela deste computador (" + host + ").\n\nPermitir o acesso remoto?")
	caption, _ := syscall.UTF16PtrFromString("Suporte de TI - Acesso remoto")
	// MB_YESNO | MB_ICONQUESTION | MB_SETFOREGROUND | MB_TOPMOST
	r, _, _ := pMessageBoxW.Call(0, uintptr(unsafe.Pointer(txt)), uintptr(unsafe.Pointer(caption)), 0x4|0x20|0x10000|0x40000)
	return r == 6 // IDYES
}
