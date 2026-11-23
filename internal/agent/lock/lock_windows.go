//go:build windows

package lock

import "syscall"

var pLockWorkStation = syscall.NewLazyDLL("user32.dll").NewProc("LockWorkStation")

// Station chama LockWorkStation. Funciona mesmo rodando como serviço, desde
// que o processo esteja na sessão interativa (o worker, não o serviço na
// sessão 0 — veja internal/agent/lifecycle).
func Station() { pLockWorkStation.Call() }
