//go:build windows

package win

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"

	"remoteit/internal/viewer/client"
)

const (
	mainClass    = "RemoteITTecnico"
	sessionClass = "RemoteITSessao"

	wmLoggedIn  = wmApp + 1 // login terminou (ok ou erro em a.loginErr)
	wmList      = wmApp + 2 // lista de máquinas nova em a.machines
	wmLoggedOut = wmApp + 3 // sessão expirou ou o usuário saiu

	idRelay   = 101
	idUser    = 102
	idPass    = 103
	idLogin   = 104
	idError   = 105
	idList    = 201
	idConnect = 202
	idLogout  = 203
	idHello   = 204
	idStatus  = 205
)

// Settings é o que o visualizador lembra entre execuções.
type Settings struct {
	Relay    string `json:"relay"`
	Username string `json:"username"`
	Token    string `json:"-"` // guardado à parte, protegido pelo DPAPI
}

// Options configura o visualizador.
type Options struct {
	Settings Settings
	Version  string
	// Save guarda as configurações (com o token de sessão) depois do login.
	Save func(Settings)
	// OnLogin é avisado a cada login bem-sucedido (a atualização automática
	// usa a sessão do técnico).
	OnLogin func(*client.API)
}

// Busy informa se há alguma sessão de tela remota aberta (a atualização
// automática espera todas fecharem).
func Busy() bool { return openSessions.Load() > 0 }

var openSessions atomic.Int32

// App é o visualizador: a janela principal e as sessões abertas.
type App struct {
	opts     Options
	api      *client.API
	user     client.User
	settings Settings

	main     windows.HWND
	ctl      map[int]windows.HWND
	sessions map[windows.HWND]*sessionWin
	handlers map[windows.HWND]func(windows.HWND, uint32, uintptr, uintptr) (uintptr, bool)
	hook     uintptr

	font, bgBrush, panelBrush, editBrush uintptr
	dpi                                  int32

	mu       sync.Mutex
	loginErr error
	machines []client.Machine
	shown    []client.Machine // o que está na lista agora (thread da janela)
	stopList context.CancelFunc
}

var theApp *App // o hook e o wndproc do Win32 não levam contexto

// Run abre o visualizador e só volta quando a janela principal fecha. Deve
// ser chamado da goroutine principal.
func Run(opts Options) error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	pSetProcessDpiAwareness.Call(^uintptr(3)) // PER_MONITOR_AWARE_V2 (-4)

	a := &App{opts: opts, settings: opts.Settings, ctl: map[int]windows.HWND{},
		sessions: map[windows.HWND]*sessionWin{}, handlers: map[windows.HWND]func(windows.HWND, uint32, uintptr, uintptr) (uintptr, bool){}}
	theApp = a
	a.bgBrush, _, _ = pCreateSolidBrush.Call(colorBg)
	a.panelBrush, _, _ = pCreateSolidBrush.Call(colorPanel)
	a.editBrush, _, _ = pCreateSolidBrush.Call(0x00000000)

	cb := syscall.NewCallback(wndProc)
	for _, name := range []string{mainClass, sessionClass} {
		wc := wndClassEx{WndProc: cb, ClassName: utf16(name), Background: windows.Handle(a.bgBrush)}
		wc.Size = uint32(unsafe.Sizeof(wc))
		h, _, _ := pGetModuleHandleW.Call(0)
		wc.Instance = windows.Handle(h)
		c, _, _ := pLoadCursorW.Call(0, idcArrow)
		wc.Cursor = windows.Handle(c)
		if r, _, err := pRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc))); r == 0 {
			return fmt.Errorf("RegisterClassEx: %w", err)
		}
	}

	a.main = a.createWindow(mainClass, "RemoteIT — Técnico", wsOverlappedWindow&^0x00040000&^0x00010000|wsClipChildren, 460, 560, 0)
	a.handlers[a.main] = a.mainProc
	dpi, _, _ := pGetDpiForWindow.Call(uintptr(a.main))
	a.dpi = int32(max(dpi, 96))
	a.font, _, _ = pCreateFontW.Call(uintptr(-a.px(15)), 0, 0, 0, 400, 0, 0, 0, 1, 0, 0, 5, 0, uintptr(unsafe.Pointer(utf16("Segoe UI"))))
	a.buildControls()

	// teclado: um hook só, que entrega as teclas à sessão em primeiro plano
	hcb := syscall.NewCallback(keyboardHook)
	mod, _, _ := pGetModuleHandleW.Call(0)
	a.hook, _, _ = pSetWindowsHookExW.Call(whKeyboardLL, hcb, mod, 0)
	defer pUnhookWindowsHookEx.Call(a.hook)

	show(a.main, true)
	a.start()

	var m msg
	for {
		r, _, _ := pGetMessageW.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0)
		if int32(r) <= 0 {
			return nil
		}
		if m.Message == wmKeyDown && m.WParam == 0x0D && a.isLoginField(m.Hwnd) { // Enter no formulário
			a.login()
			continue
		}
		if r, _, _ := pIsDialogMessageW.Call(uintptr(a.main), uintptr(unsafe.Pointer(&m))); r != 0 {
			continue // Tab entre os campos
		}
		pTranslateMessage.Call(uintptr(unsafe.Pointer(&m)))
		pDispatchMessageW.Call(uintptr(unsafe.Pointer(&m)))
	}
}

func (a *App) px(v int32) int32 { return v * a.dpi / 96 }

func (a *App) createWindow(class, title string, style uintptr, w, h int32, parent windows.HWND) windows.HWND {
	scale := func(v int32) uintptr {
		if a.dpi == 0 {
			return uintptr(v)
		}
		return uintptr(a.px(v))
	}
	hwnd, _, _ := pCreateWindowExW.Call(0, uintptr(unsafe.Pointer(utf16(class))), uintptr(unsafe.Pointer(utf16(title))),
		style, cwUseDefault, cwUseDefault, scale(w), scale(h), uintptr(parent), 0, 0, 0)
	return windows.HWND(hwnd)
}

func (a *App) control(id int, class, text string, style uintptr, x, y, w, h int32) windows.HWND {
	hwnd, _, _ := pCreateWindowExW.Call(0, uintptr(unsafe.Pointer(utf16(class))), uintptr(unsafe.Pointer(utf16(text))),
		wsChild|style, uintptr(a.px(x)), uintptr(a.px(y)), uintptr(a.px(w)), uintptr(a.px(h)), uintptr(a.main), uintptr(id), 0, 0)
	pSendMessageW.Call(hwnd, wmSetFont, a.font, 1)
	a.ctl[id] = windows.HWND(hwnd)
	return windows.HWND(hwnd)
}

func (a *App) buildControls() {
	// login
	a.control(301, "STATIC", "Relay", ssLeft, 32, 28, 380, 20)
	a.control(idRelay, "EDIT", a.settings.Relay, wsBorder|wsTabStop|esAutoHScroll, 32, 50, 380, 28)
	a.control(302, "STATIC", "Usuário ou e-mail", ssLeft, 32, 92, 380, 20)
	a.control(idUser, "EDIT", a.settings.Username, wsBorder|wsTabStop|esAutoHScroll, 32, 114, 380, 28)
	a.control(303, "STATIC", "Senha", ssLeft, 32, 156, 380, 20)
	a.control(idPass, "EDIT", "", wsBorder|wsTabStop|esAutoHScroll|esPassword, 32, 178, 380, 28)
	a.control(idLogin, "BUTTON", "Entrar", wsTabStop|bsDefPushButton, 32, 222, 380, 34)
	a.control(idError, "STATIC", "", ssLeft, 32, 270, 380, 60)
	// lista
	a.control(idHello, "STATIC", "", ssLeft, 24, 18, 400, 22)
	a.control(idList, "LISTBOX", "", wsBorder|wsTabStop|wsVScroll|lbsNotify|lbsNoIntegralHeight, 24, 46, 396, 380)
	a.control(idConnect, "BUTTON", "Conectar", wsTabStop|bsDefPushButton, 24, 436, 200, 34)
	a.control(idLogout, "BUTTON", "Sair", wsTabStop, 232, 436, 188, 34)
	a.control(idStatus, "STATIC", "versão "+orDash(a.opts.Version), ssLeft, 24, 480, 396, 20)
}

func orDash(s string) string {
	if s == "" {
		return "de desenvolvimento"
	}
	return s
}

func (a *App) isLoginField(h windows.HWND) bool {
	return h == a.ctl[idUser] || h == a.ctl[idPass] || h == a.ctl[idRelay]
}

func (a *App) showLogin(on bool) {
	for _, id := range []int{301, idRelay, 302, idUser, 303, idPass, idLogin, idError} {
		show(a.ctl[id], on)
	}
	for _, id := range []int{idHello, idList, idConnect, idLogout} {
		show(a.ctl[id], !on)
	}
	if on {
		f := a.ctl[idPass]
		if getText(a.ctl[idUser]) == "" {
			f = a.ctl[idUser]
		}
		pSetFocus.Call(uintptr(f))
	} else {
		pSetFocus.Call(uintptr(a.ctl[idList]))
	}
}

// start tenta a sessão guardada; sem ela, mostra o login.
func (a *App) start() {
	if a.settings.Token == "" || a.settings.Relay == "" {
		a.showLogin(true)
		return
	}
	api, err := client.New(a.settings.Relay)
	if err != nil {
		a.showLogin(true)
		return
	}
	api.SetToken(a.settings.Token)
	a.showLogin(false)
	setText(a.ctl[idHello], "Conectando ao relay…")
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		u, err := api.Me(ctx)
		a.mu.Lock()
		a.loginErr = err
		if err == nil {
			a.api, a.user = api, u
		}
		a.mu.Unlock()
		post(a.main, wmLoggedIn)
	}()
}

func (a *App) login() {
	relay, user, pass := getText(a.ctl[idRelay]), getText(a.ctl[idUser]), getText(a.ctl[idPass])
	api, err := client.New(relay)
	if err != nil {
		setText(a.ctl[idError], "Endereço do relay inválido (ex.: https://suporte.exemplo.com).")
		return
	}
	pEnableWindow.Call(uintptr(a.ctl[idLogin]), 0)
	setText(a.ctl[idError], "Entrando…")
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		u, err := api.Login(ctx, user, pass)
		a.mu.Lock()
		a.loginErr = err
		if err == nil {
			a.api, a.user = api, u
			a.settings = Settings{Relay: relay, Username: user, Token: api.Token()}
		}
		a.mu.Unlock()
		post(a.main, wmLoggedIn)
	}()
}

func (a *App) afterLogin() {
	pEnableWindow.Call(uintptr(a.ctl[idLogin]), 1)
	a.mu.Lock()
	err := a.loginErr
	a.mu.Unlock()
	if err != nil {
		a.showLogin(true)
		msg := err.Error()
		if errors.Is(err, client.ErrUnauthorized) {
			msg = "Sua sessão expirou. Entre de novo."
		}
		setText(a.ctl[idError], msg)
		return
	}
	setText(a.ctl[idError], "")
	setText(a.ctl[idPass], "")
	if a.opts.Save != nil {
		a.opts.Save(a.settings)
	}
	if a.opts.OnLogin != nil {
		a.opts.OnLogin(a.api)
	}
	setText(a.ctl[idHello], "Olá, "+a.user.Username+". Suas máquinas:")
	a.showLogin(false)
	ctx, cancel := context.WithCancel(context.Background())
	a.stopList = cancel
	go a.refreshLoop(ctx)
}

func (a *App) refreshLoop(ctx context.Context) {
	for {
		list, err := a.api.Machines(ctx)
		if errors.Is(err, client.ErrUnauthorized) {
			post(a.main, wmLoggedOut)
			return
		}
		if err == nil {
			a.mu.Lock()
			a.machines = list
			a.mu.Unlock()
			post(a.main, wmList)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(3 * time.Second):
		}
	}
}

func (a *App) updateList() {
	a.mu.Lock()
	list := a.machines
	a.mu.Unlock()
	sel, _, _ := pSendMessageW.Call(uintptr(a.ctl[idList]), lbGetCurSel, 0, 0)
	selID := ""
	if int(sel) >= 0 && int(sel) < len(a.shown) {
		selID = a.shown[sel].ID
	}
	pSendMessageW.Call(uintptr(a.ctl[idList]), lbResetContent, 0, 0)
	newSel := -1
	for i, m := range list {
		state := "offline"
		if m.Online {
			state = "online"
			if m.ViewedBy != "" {
				state = "em uso por " + m.ViewedBy
			}
		}
		line := fmt.Sprintf("%s  %s  —  %s", map[bool]string{true: "●", false: "○"}[m.Online], m.Name, state)
		if a.user.Role == "admin" && m.Owner != "" && m.Owner != a.user.Username {
			line += "  (" + m.Owner + ")"
		}
		pSendMessageW.Call(uintptr(a.ctl[idList]), lbAddString, 0, uintptr(unsafe.Pointer(utf16(line))))
		if m.ID == selID {
			newSel = i
		}
	}
	if newSel >= 0 {
		pSendMessageW.Call(uintptr(a.ctl[idList]), lbSetCurSel, uintptr(newSel), 0)
	}
	a.shown = list
	if len(list) == 0 {
		setText(a.ctl[idStatus], "Nenhuma máquina. Gere um link de instalação no console web.")
	} else {
		setText(a.ctl[idStatus], "versão "+orDash(a.opts.Version))
	}
}

func (a *App) connectSelected() {
	sel, _, _ := pSendMessageW.Call(uintptr(a.ctl[idList]), lbGetCurSel, 0, 0)
	if int(sel) < 0 || int(sel) >= len(a.shown) {
		return
	}
	m := a.shown[sel]
	if !m.Online {
		messageBox(a.main, "RemoteIT", m.Name+" está offline.")
		return
	}
	for _, s := range a.sessions { // já aberta: traz para a frente
		if s.machine.ID == m.ID {
			pSetForegroundWindow.Call(uintptr(s.hwnd))
			return
		}
	}
	a.openSession(m)
}

func (a *App) logout() {
	if a.stopList != nil {
		a.stopList()
	}
	for h, s := range a.sessions {
		s.cancel()
		pDestroyWindow.Call(uintptr(h))
	}
	if a.api != nil {
		api := a.api
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			api.Logout(ctx)
		}()
	}
	a.settings.Token = ""
	if a.opts.Save != nil {
		a.opts.Save(a.settings)
	}
	a.showLogin(true)
}

func (a *App) mainProc(hwnd windows.HWND, m uint32, wp, lp uintptr) (uintptr, bool) {
	switch m {
	case wmLoggedIn:
		a.afterLogin()
		return 0, true
	case wmList:
		a.updateList()
		return 0, true
	case wmLoggedOut:
		a.logout()
		setText(a.ctl[idError], "Sua sessão expirou. Entre de novo.")
		return 0, true
	case wmCommand:
		id, code := int(loword(wp)), hiword(wp)
		switch {
		case id == idLogin && code == bnClicked:
			a.login()
		case id == idConnect && code == bnClicked:
			a.connectSelected()
		case id == idList && code == lbnDblClk:
			a.connectSelected()
		case id == idLogout && code == bnClicked:
			a.logout()
		}
		return 0, true
	case wmCtlColorStatic, wmCtlColorEdit, wmCtlColorListbox:
		pSetTextColor.Call(wp, colorText)
		bg, brush := uintptr(colorBg), a.bgBrush
		if m != wmCtlColorStatic {
			bg, brush = 0x00000000, a.editBrush
		}
		if windows.HWND(lp) == a.ctl[idError] {
			pSetTextColor.Call(wp, 0x008571FB) // #fb7185: erro
		}
		pSetBkColor.Call(wp, bg)
		return brush, true
	case wmClose:
		for h, s := range a.sessions {
			s.cancel()
			pDestroyWindow.Call(uintptr(h))
		}
		pDestroyWindow.Call(uintptr(hwnd))
		return 0, true
	case wmDestroy:
		pPostQuitMessage.Call(0)
		return 0, true
	}
	return 0, false
}

func wndProc(hwnd, m, wp, lp uintptr) uintptr {
	if a := theApp; a != nil {
		if h, ok := a.handlers[windows.HWND(hwnd)]; ok {
			if r, done := h(windows.HWND(hwnd), uint32(m), wp, lp); done {
				return r
			}
		}
	}
	r, _, _ := pDefWindowProcW.Call(hwnd, m, wp, lp)
	return r
}

// keyboardHook recebe todo o teclado do PC do técnico. Com uma sessão em
// primeiro plano e a captura ligada, a tecla vai para o PC remoto e não para
// este Windows (inclusive Win, Alt+Tab, Ctrl+Esc). Roda na thread da janela.
func keyboardHook(code int, wp, lp uintptr) uintptr {
	a := theApp
	if code == 0 && a != nil {
		fg, _, _ := pGetForegroundWindow.Call()
		if s := a.sessions[windows.HWND(fg)]; s != nil && s.capture {
			var kb kbdLLHookStruct
			pRtlMoveMemory.Call(uintptr(unsafe.Pointer(&kb)), lp, unsafe.Sizeof(kb))
			up := wp == wmKeyUp || wp == wmSysKeyUp
			if s.keyFromHook(kb, up) {
				return 1
			}
		}
	}
	r, _, _ := pCallNextHookEx.Call(0, uintptr(code), wp, lp)
	return r
}
