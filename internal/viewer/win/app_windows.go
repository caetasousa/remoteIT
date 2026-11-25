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
	"remoteit/internal/viewer/ui"
)

const (
	mainClass    = "RemoteITTecnico"
	sessionClass = "RemoteITSessao"

	wmLoggedIn  = wmApp + 1 // login terminou (ok ou erro em a.loginErr)
	wmList      = wmApp + 2 // lista de máquinas nova em a.machines
	wmLoggedOut = wmApp + 3 // sessão expirou ou o usuário saiu
	wmCaps      = wmApp + 4 // terminou a sondagem dos decodificadores (a.caps)

	idRelay   = 101
	idUser    = 102
	idPass    = 103
	idLogin   = 104
	idError   = 105
	idList    = 201
	idConnect = 202
	idLogout  = 203
	idSummary = 206
	idFilter  = 207
)

// Medidas da janela principal, em pixels lógicos (96 dpi).
const (
	headerH = 64 // faixa da marca
	footerH = 30 // rodapé com versão e decodificadores
	rowH    = 52 // linha da lista de máquinas
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
	fontBold, fontSmall, fontTitle       uintptr
	fontMono                             uintptr
	dpi                                  int32
	caps                                 decoders // o que este PC decodifica
	capsKnown                            bool
	rows                                 []ui.Row // o que está na lista agora

	mu       sync.Mutex
	loginErr error
	fromForm bool // o último login veio do formulário (não da sessão guardada)
	machines []client.Machine
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

	a.main = a.createWindow(mainClass, "RemoteIT — Técnico", wsOverlappedWindow&^0x00040000&^0x00010000|wsClipChildren, 480, 640, 0)
	a.handlers[a.main] = a.mainProc
	dpi, _, _ := pGetDpiForWindow.Call(uintptr(a.main))
	a.dpi = int32(max(dpi, 96))
	a.font = a.newFont(15, 400, "Segoe UI")
	a.fontBold = a.newFont(15, 600, "Segoe UI")
	a.fontSmall = a.newFont(12, 400, "Segoe UI")
	a.fontTitle = a.newFont(22, 600, "Segoe UI")
	a.fontMono = a.newFont(13, 400, "Consolas")
	a.buildControls()
	// o que este PC decodifica aparece no rodapé; os decodificadores do
	// Windows ficam presos à thread, então a sondagem tem a sua
	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		c := probeDecoders()
		a.mu.Lock()
		a.caps, a.capsKnown = c, true
		a.mu.Unlock()
		post(a.main, wmCaps)
	}()

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
		if m.Message == wmKeyDown && m.WParam == vkReturn && a.isLoginField(m.Hwnd) { // Enter no formulário
			a.login()
			continue
		}
		if m.Message == wmKeyDown && m.WParam == vkReturn && (m.Hwnd == a.ctl[idList] || m.Hwnd == a.ctl[idFilter]) {
			a.connectSelected() // Enter na lista ou na busca conecta
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

// newFont cria uma fonte de size pixels lógicos (ClearType).
func (a *App) newFont(size, weight int32, face string) uintptr {
	f, _, _ := pCreateFontW.Call(uintptr(-a.px(size)), 0, 0, 0, uintptr(weight), 0, 0, 0, 1, 0, 0, 5, 0, uintptr(unsafe.Pointer(utf16(face))))
	return f
}

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
	top := int32(headerH + 24)
	// login
	a.control(301, "STATIC", "Endereço do relay", ssLeft, 32, top, 400, 20)
	a.control(idRelay, "EDIT", a.settings.Relay, wsBorder|wsTabStop|esAutoHScroll, 32, top+22, 400, 30)
	a.control(302, "STATIC", "Usuário ou e-mail", ssLeft, 32, top+66, 400, 20)
	a.control(idUser, "EDIT", a.settings.Username, wsBorder|wsTabStop|esAutoHScroll, 32, top+88, 400, 30)
	a.control(303, "STATIC", "Senha", ssLeft, 32, top+132, 400, 20)
	a.control(idPass, "EDIT", "", wsBorder|wsTabStop|esAutoHScroll|esPassword, 32, top+154, 400, 30)
	a.control(idLogin, "BUTTON", "Entrar", wsTabStop|bsOwnerDraw, 32, top+204, 400, 38)
	a.control(idError, "STATIC", "", ssLeft, 32, top+254, 400, 60)
	// lista
	a.control(304, "STATIC", "Buscar por nome, dono ou usuário", ssLeft, 24, headerH+12, 416, 20)
	a.control(idFilter, "EDIT", "", wsBorder|wsTabStop|esAutoHScroll, 24, headerH+34, 416, 30)
	a.control(idSummary, "STATIC", "", ssLeft, 24, headerH+74, 416, 20)
	a.control(idList, "LISTBOX", "", wsTabStop|wsVScroll|lbsNotify|lbsNoIntegralHeight|lbsOwnerDrawFixed|lbsHasStrings,
		24, headerH+98, 416, 310)
	a.control(idConnect, "BUTTON", "Conectar", wsTabStop|bsOwnerDraw, 24, headerH+420, 264, 38)
	a.control(idLogout, "BUTTON", "Sair", wsTabStop|bsOwnerDraw, 296, headerH+420, 144, 38)
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
	for _, id := range []int{304, idFilter, idSummary, idList, idConnect, idLogout} {
		show(a.ctl[id], !on)
	}
	pInvalidateRect.Call(uintptr(a.main), 0, 1) // o cabeçalho muda (usuário)
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
	setText(a.ctl[idSummary], "Conectando ao relay…")
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		u, err := api.Me(ctx)
		a.mu.Lock()
		a.loginErr, a.fromForm = err, false
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
		a.loginErr, a.fromForm = err, true
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
	err, form := a.loginErr, a.fromForm
	a.mu.Unlock()
	if err != nil {
		a.showLogin(true)
		setText(a.ctl[idError], ui.LoginError(err, form))
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
	setText(a.ctl[idSummary], "Carregando as máquinas…")
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
	lb := uintptr(a.ctl[idList])
	sel, _, _ := pSendMessageW.Call(lb, lbGetCurSel, 0, 0)
	selID := ""
	if int(sel) >= 0 && int(sel) < len(a.rows) {
		selID = a.rows[sel].Machine.ID
	}
	rows := ui.Rows(list, getText(a.ctl[idFilter]), a.user)
	pSendMessageW.Call(lb, 0x000B, 0, 0) // WM_SETREDRAW off: sem piscar a cada 3 s
	pSendMessageW.Call(lb, lbResetContent, 0, 0)
	newSel := -1
	for i, r := range rows {
		pSendMessageW.Call(lb, lbAddString, 0, uintptr(unsafe.Pointer(utf16(r.Title))))
		if r.Machine.ID == selID || (selID == "" && newSel < 0 && r.State != ui.Offline) {
			newSel = i // mantém a seleção; sem ela, a primeira online
		}
	}
	if newSel >= 0 {
		pSendMessageW.Call(lb, lbSetCurSel, uintptr(newSel), 0)
	}
	pSendMessageW.Call(lb, 0x000B, 1, 0)
	pInvalidateRect.Call(lb, 0, 1)
	a.rows = rows
	switch {
	case len(list) == 0:
		setText(a.ctl[idSummary], "Nenhuma máquina. Gere um link de instalação no console web.")
	case len(rows) == 0:
		setText(a.ctl[idSummary], "Nenhuma máquina com esse nome.")
	default:
		setText(a.ctl[idSummary], ui.Summary(list))
	}
}

// paintMain desenha o cabeçalho (marca e usuário) e o rodapé (versão e o
// que este PC decodifica); os controles se desenham sozinhos por cima.
func (a *App) paintMain() {
	var ps paintStruct
	hdc, _, _ := pBeginPaint.Call(uintptr(a.main), uintptr(unsafe.Pointer(&ps)))
	defer pEndPaint.Call(uintptr(a.main), uintptr(unsafe.Pointer(&ps)))
	var rc rect
	pGetClientRect.Call(uintptr(a.main), uintptr(unsafe.Pointer(&rc)))
	head := rect{0, 0, rc.Right, a.px(headerH)}
	fillRect(hdc, head, colorPanel)
	fillRect(hdc, rect{0, head.Bottom - 1, rc.Right, head.Bottom}, colorLine)
	x := a.px(24)
	drawString(hdc, rect{x, 0, rc.Right, head.Bottom}, "RemoteIT", a.fontTitle, colorAccent, dtLeft)
	x += textWidth(hdc, "RemoteIT", a.fontTitle) + a.px(8)
	drawString(hdc, rect{x, a.px(4), rc.Right, head.Bottom}, "técnico", a.fontSmall, colorMuted, dtLeft)

	a.mu.Lock()
	logged := a.api != nil && a.user.Username != ""
	user, role := a.user.Username, a.user.Role
	caps, known := a.caps, a.capsKnown
	a.mu.Unlock()
	if logged && !isVisible(a.ctl[idLogin]) {
		r := rect{0, a.px(12), rc.Right - a.px(24), a.px(34)}
		drawString(hdc, r, user, a.fontBold, colorText, dtRight)
		label := "técnico"
		if role == "admin" {
			label = "administrador"
		}
		drawString(hdc, rect{0, a.px(32), rc.Right - a.px(24), a.px(52)}, label, a.fontSmall, colorMuted, dtRight)
	}

	foot := rect{0, rc.Bottom - a.px(footerH), rc.Right, rc.Bottom}
	fillRect(hdc, foot, colorPanel)
	fillRect(hdc, rect{0, foot.Top, rc.Right, foot.Top + 1}, colorLine)
	text := "versão " + orDash(a.opts.Version)
	if known {
		text += "  ·  " + ui.Decoders(caps.h264, caps.av1)
	}
	drawString(hdc, rect{a.px(24), foot.Top, rc.Right - a.px(24), foot.Bottom}, text, a.fontSmall, colorMuted, dtLeft)
}

// drawRow desenha uma máquina: ponto de estado, nome e detalhes.
func (a *App) drawRow(d *drawItemStruct) {
	r := d.RcItem
	if int(d.ItemID) < 0 || int(d.ItemID) >= len(a.rows) {
		fillRect(d.HDC, r, colorBg)
		return
	}
	row := a.rows[d.ItemID]
	selected := d.ItemState&odsSelected != 0
	bg := uintptr(colorBg)
	if selected {
		bg = colorSel
	}
	fillRect(d.HDC, r, bg)
	if selected {
		fillRect(d.HDC, rect{r.Left, r.Top, r.Left + a.px(3), r.Bottom}, colorAccent)
	}
	fillRect(d.HDC, rect{r.Left, r.Bottom - 1, r.Right, r.Bottom}, colorLine)
	dotColor, titleColor := uintptr(colorMuted), uintptr(colorMuted)
	switch row.State {
	case ui.Online:
		dotColor, titleColor = colorGood, colorText
	case ui.InUse:
		dotColor, titleColor = colorWarn, colorText
	}
	mid := (r.Top + r.Bottom) / 2
	dot(d.HDC, r.Left+a.px(18), mid, a.px(5), dotColor)
	x := r.Left + a.px(34)
	drawString(d.HDC, rect{x, r.Top + a.px(6), r.Right - a.px(8), mid}, row.Title, a.fontBold, titleColor, dtLeft)
	detail := uintptr(colorMuted)
	if row.State == ui.InUse {
		detail = colorWarn
	}
	drawString(d.HDC, rect{x, mid, r.Right - a.px(8), r.Bottom - a.px(6)}, row.Detail, a.fontSmall, detail, dtLeft)
}

func (a *App) connectSelected() {
	sel, _, _ := pSendMessageW.Call(uintptr(a.ctl[idList]), lbGetCurSel, 0, 0)
	if int(sel) < 0 || int(sel) >= len(a.rows) {
		return
	}
	m := a.rows[sel].Machine
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
	case wmCaps:
		pInvalidateRect.Call(uintptr(hwnd), 0, 1)
		return 0, true
	case wmPaint:
		a.paintMain()
		return 0, true
	case wmMeasureItem:
		var mi measureItemStruct
		pRtlMoveMemory.Call(uintptr(unsafe.Pointer(&mi)), lp, unsafe.Sizeof(mi))
		if mi.CtlType == odtListbox {
			mi.ItemHeight = uint32(a.px(rowH))
			pRtlMoveMemory.Call(lp, uintptr(unsafe.Pointer(&mi)), unsafe.Sizeof(mi))
		}
		return 1, true
	case wmDrawItem:
		d := new(drawItemStruct)
		pRtlMoveMemory.Call(uintptr(unsafe.Pointer(d)), lp, unsafe.Sizeof(*d))
		switch {
		case d.CtlType == odtListbox:
			a.drawRow(d)
		case d.CtlType == odtButton && d.CtlID == idLogin:
			a.drawButton(d, "Entrar", true)
		case d.CtlType == odtButton && d.CtlID == idConnect:
			a.drawButton(d, "Conectar", true)
		case d.CtlType == odtButton && d.CtlID == idLogout:
			a.drawButton(d, "Sair", false)
		}
		return 1, true
	case wmCommand:
		id, code := int(loword(wp)), hiword(wp)
		switch {
		case id == idFilter && code == enChange:
			a.updateList()
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
		switch windows.HWND(lp) {
		case a.ctl[idError]:
			pSetTextColor.Call(wp, colorBad)
		case a.ctl[idSummary], a.ctl[301], a.ctl[302], a.ctl[303], a.ctl[304]:
			pSetTextColor.Call(wp, colorMuted)
		}
		switch m {
		case wmCtlColorEdit:
			bg, brush = colorPanel, a.panelBrush
		case wmCtlColorListbox:
			bg, brush = colorBg, a.bgBrush
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
