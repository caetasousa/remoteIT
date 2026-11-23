//go:build windows

package desktop

import (
	"fmt"
	"sync"
	"syscall"
	"unsafe"
)

var user32 = syscall.NewLazyDLL("user32.dll")

var (
	pOpenInputDesktop = user32.NewProc("OpenInputDesktop")
	pSetThreadDesktop = user32.NewProc("SetThreadDesktop")
	pCloseDesktop     = user32.NewProc("CloseDesktop")
	pGetUserObjectInf = user32.NewProc("GetUserObjectInformationW")

	pGetCurrentThreadID = syscall.NewLazyDLL("kernel32.dll").NewProc("GetCurrentThreadId")
)

// DESKTOP_SWITCHDESKTOP | os demais direitos que a captura (GDI/DXGI) e o
// SendInput usam no desktop.
const desktopGenericAll = 0x10000000

// lastDesktop guarda, por thread do SO, o handle que ela deixou aberto da
// última vez. É por thread porque mais de uma thread segue o desktop (a da
// captura e a da entrada): com um campo único, uma fecharia o handle que a
// outra ainda usa. Serve só para fechar o handle depois de trocar — não para decidir se pula a troca: o
// Windows não garante o mesmo valor de handle para o mesmo desktop lógico
// entre chamadas, então comparar handles para "já está certo, não troca" é
// arriscado. SetThreadDesktop já é idempotente e barato o bastante para
// chamar sempre.
var lastDesktop struct {
	mu sync.Mutex
	m  map[uintptr]uintptr // thread id -> handle do desktop
}

func followInput() error {
	h, _, _ := pOpenInputDesktop.Call(0, 0, desktopGenericAll)
	if h == 0 {
		return fmt.Errorf("desktop: OpenInputDesktop falhou (sessão sem desktop interativo?)")
	}

	if r, _, err := pSetThreadDesktop.Call(h); r == 0 {
		name := objectName(h)
		pCloseDesktop.Call(h)
		// o caso típico é ERROR_BUSY: a thread já tem janelas ou hooks
		// (Media Foundation cria janelas ocultas) e o Windows não a deixa
		// mais trocar de desktop
		return fmt.Errorf("desktop: SetThreadDesktop(%s): %w", name, err)
	}

	// o handle anterior desta thread pode ser fechado: o Windows mantém o
	// desktop em si (Default/Winlogon) vivo enquanto a sessão existir,
	// independente de quantos handles próprios abrimos e fechamos para ele.
	tid, _, _ := pGetCurrentThreadID.Call()
	lastDesktop.mu.Lock()
	if lastDesktop.m == nil {
		lastDesktop.m = map[uintptr]uintptr{}
	}
	old := lastDesktop.m[tid]
	lastDesktop.m[tid] = h
	lastDesktop.mu.Unlock()
	if old != 0 && old != h {
		pCloseDesktop.Call(old)
	}
	return nil
}

// objectName devolve o nome do desktop h ("Default", "Winlogon"), só para log.
func objectName(h uintptr) string {
	const uoiName = 2
	var buf [64]uint16
	var n uint32
	if r, _, _ := pGetUserObjectInf.Call(h, uoiName, uintptr(unsafe.Pointer(&buf[0])),
		uintptr(len(buf)*2), uintptr(unsafe.Pointer(&n))); r == 0 {
		return "?"
	}
	return syscall.UTF16ToString(buf[:])
}

func inputName() string {
	h, _, _ := pOpenInputDesktop.Call(0, 0, desktopReadObjects)
	if h == 0 {
		return "inacessível"
	}
	defer pCloseDesktop.Call(h)
	return objectName(h)
}

const desktopReadObjects = 0x0001
