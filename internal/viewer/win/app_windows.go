//go:build windows

package win

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"slices"
	"strings"
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

// Medidas da janela principal, em pixels lógicos (96 dpi): tudo passa por
// a.px, inclusive o tamanho da janela, para nada sair da borda a 125% ou 150%.
const (
	clientW  = 440 // área útil da janela principal
	clientH  = 620 // altura preferida; em tela baixa, a lista encolhe
	minH     = 560 // o que o formulário de login precisa
	margin   = 24  // margem lateral do conteúdo
	contentW = clientW - 2*margin
	headerH  = 56 // faixa da marca
	footerH  = 28 // rodapé com versão e decodificadores
	rowH     = 52 // linha da lista de máquinas
	fieldH   = 40 // caixa de um campo de texto
	buttonH  = 42
)

// Posição dos elementos (y lógico do topo).
const (
	loginTitleY = headerH + 28 // "ENTRAR NO CONSOLE"
	relayY      = loginTitleY + 42
	userY       = relayY + 86
	passY       = userY + 86
	loginBtnY   = passY + 84
	errorY      = loginBtnY + buttonH + 16

	filterY  = headerH + 28 // "BUSCAR MÁQUINA"
	summaryY = filterY + 20 + fieldH + 12
	listY    = summaryY + 26
)

// actionsY é o topo dos botões da lista: presos ao pé da janela, que tem
// a.height de altura útil.
func (a *App) actionsY() int32 { return a.height - footerH - margin - buttonH }

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

	font, bgBrush, sheetBrush, editBrush uintptr
	fontBold, fontSmall, fontTitle       uintptr
	fontMono, fontLabel, fontButton      uintptr
	embedded                             bool // a JetBrains Mono embutida carregou
	dpi                                  int32
	height                               int32    // altura útil, em pixels lógicos
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
	a.sheetBrush, _, _ = pCreateSolidBrush.Call(colorSheet)
	a.editBrush, _, _ = pCreateSolidBrush.Call(colorInput)
	a.embedded = loadFonts()

	cb := syscall.NewCallback(wndProc)
	for _, name := range []string{mainClass, sessionClass, videoClass} {
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

	// a escala do monitor só se sabe com a janela criada: ela nasce
	// escondida e ganha o tamanho certo (fitMain) antes de aparecer
	a.main = a.createWindow(mainClass, "RemoteIT — Técnico", wsOverlappedWindow&^0x00040000&^0x00010000|wsClipChildren, clientW, clientH, 0)
	a.handlers[a.main] = a.mainProc
	dpi, _, _ := pGetDpiForWindow.Call(uintptr(a.main))
	a.dpi = int32(max(dpi, 96))
	a.fitMain(nil)
	a.makeFonts()
	a.buildControls()
	a.place()
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

// makeFonts (re)cria as fontes na escala atual: a JetBrains Mono do
// console, nos tamanhos dele (13 px o texto, 11 px os rótulos).
func (a *App) makeFonts() {
	old := []uintptr{a.font, a.fontBold, a.fontSmall, a.fontTitle, a.fontMono, a.fontLabel, a.fontButton}
	face, bold := faceMono, faceMonoBold
	if !a.embedded {
		face, bold = faceFallback, faceFallback
	}
	a.font = a.newFont(13, 400, face)
	a.fontBold = a.newFont(13, 600, bold)
	a.fontSmall = a.newFont(11, 400, face)
	a.fontLabel = a.newFont(11, 600, bold)
	a.fontButton = a.newFont(12, 600, bold)
	a.fontTitle = a.newFont(14, 600, bold)
	a.fontMono = a.newFont(12, 400, face)
	for _, f := range old {
		if f != 0 {
			pDeleteObject.Call(f)
		}
	}
}

// fitMain dá à janela principal a área útil clientW x clientH na escala
// atual; com at, também a posição (a sugerida pelo Windows ao mudar de
// monitor).
func (a *App) fitMain(at *rect) {
	style, _, _ := pGetWindowLongPtrW.Call(uintptr(a.main), uintptr(gwlStyleIndex()))
	outer := func(h int32) rect {
		r := rect{0, 0, a.px(clientW), a.px(h)}
		if pAdjustWindowRectDpi.Find() == nil {
			pAdjustWindowRectDpi.Call(uintptr(unsafe.Pointer(&r)), style, 0, 0, uintptr(a.dpi))
		} else {
			pAdjustWindowRectEx.Call(uintptr(unsafe.Pointer(&r)), style, 0, 0)
		}
		return r
	}
	// em notebook a 150% (1280 x 720 lógicos menos a barra de tarefas) a
	// altura preferida pode não caber: a janela encolhe até minH
	a.height = clientH
	mon, _, _ := pMonitorFromWindow.Call(uintptr(a.main), monitorDefaultToNearest)
	mi := monitorInfo{}
	mi.Size = uint32(unsafe.Sizeof(mi))
	if r, _, _ := pGetMonitorInfoW.Call(mon, uintptr(unsafe.Pointer(&mi))); r != 0 {
		if extra := outer(clientH).h() - mi.Work.h(); extra > 0 {
			a.height = max(clientH-(extra*96+a.dpi-1)/a.dpi, minH)
		}
	}
	r := outer(a.height)
	flags := uintptr(swpNoZOrder | swpNoActivate)
	var x, y int32
	if at != nil {
		x, y = at.Left, at.Top
	} else {
		flags |= swpNoMove
	}
	pSetWindowPos.Call(uintptr(a.main), 0, uintptr(x), uintptr(y), uintptr(r.w()), uintptr(r.h()), flags)
	if at == nil { // nasce no alto da área de trabalho, para caber inteira
		var w rect
		pGetWindowRect.Call(uintptr(a.main), uintptr(unsafe.Pointer(&w)))
		if w.Bottom > mi.Work.Bottom && mi.Size != 0 {
			pSetWindowPos.Call(uintptr(a.main), 0, uintptr(w.Left), uintptr(max(mi.Work.Top, mi.Work.Bottom-w.h())), 0, 0, swpNoZOrder|swpNoActivate|0x0001) // SWP_NOSIZE
		}
	}
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

func (a *App) control(id int, class, text string, style uintptr) windows.HWND {
	hwnd, _, _ := pCreateWindowExW.Call(0, uintptr(unsafe.Pointer(utf16(class))), uintptr(unsafe.Pointer(utf16(text))),
		wsChild|style, 0, 0, 1, 1, uintptr(a.main), uintptr(id), 0, 0)
	a.ctl[id] = windows.HWND(hwnd)
	return windows.HWND(hwnd)
}

// field é um campo de texto como os do console: rótulo em maiúsculas em
// cima e uma caixa preta com contorno, que a janela principal desenha (o
// EDIT fica dentro dela, sem borda própria).
type field struct {
	id    int
	label string
	y     int32 // topo do rótulo; a caixa vem 20 px abaixo
}

var (
	loginFields = []field{{idRelay, "Endereço do relay", relayY}, {idUser, "Usuário ou e-mail", userY}, {idPass, "Senha", passY}}
	listFields  = []field{{idFilter, "Buscar máquina", filterY}}
)

// box é a caixa de um campo, em pixels físicos.
func (a *App) box(f field) rect {
	y := f.y + 20
	return rect{a.px(margin), a.px(y), a.px(margin + contentW), a.px(y + fieldH)}
}

// listBox é a moldura da lista de máquinas, em pixels físicos.
func (a *App) listBox() rect {
	return rect{a.px(margin), a.px(listY), a.px(margin + contentW), a.px(a.actionsY() - 16)}
}

func (a *App) buildControls() {
	a.control(idRelay, "EDIT", a.settings.Relay, wsTabStop|esAutoHScroll)
	a.control(idUser, "EDIT", a.settings.Username, wsTabStop|esAutoHScroll)
	a.control(idPass, "EDIT", "", wsTabStop|esAutoHScroll|esPassword)
	a.control(idLogin, "BUTTON", "Entrar", wsTabStop|bsOwnerDraw)
	a.control(idError, "STATIC", "", ssLeft)
	a.control(idFilter, "EDIT", "", wsTabStop|esAutoHScroll)
	a.control(idSummary, "STATIC", "", ssLeft)
	a.control(idList, "LISTBOX", "", wsTabStop|wsVScroll|lbsNotify|lbsNoIntegralHeight|lbsOwnerDrawFixed|lbsHasStrings)
	a.control(idConnect, "BUTTON", "Conectar", wsTabStop|bsOwnerDraw)
	a.control(idLogout, "BUTTON", "Sair", wsTabStop|bsOwnerDraw)
}

// place posiciona os controles e lhes dá a fonte, na escala atual (de novo
// a cada mudança de monitor).
func (a *App) place() {
	move := func(id int, r rect) {
		pMoveWindow.Call(uintptr(a.ctl[id]), uintptr(r.Left), uintptr(r.Top), uintptr(r.w()), uintptr(r.h()), 1)
	}
	at := func(x, y, w, h int32) rect { return rect{a.px(x), a.px(y), a.px(x + w), a.px(y + h)} }
	for _, f := range append(slices.Clone(loginFields), listFields...) {
		b := a.box(f)
		// o EDIT de uma linha escreve no topo: centralizado na caixa à mão
		h := a.px(18)
		top := b.Top + (b.h()-h)/2
		move(f.id, rect{b.Left + a.px(12), top, b.Right - a.px(12), top + h})
	}
	move(idLogin, at(margin, loginBtnY, contentW, buttonH))
	move(idError, at(margin, errorY, contentW, 72))
	move(idSummary, at(margin, summaryY, contentW, 18))
	l := a.listBox()
	move(idList, rect{l.Left + 1, l.Top + 1, l.Right - 1, l.Bottom - 1})
	pSendMessageW.Call(uintptr(a.ctl[idList]), lbSetItemHeight, 0, uintptr(a.px(rowH)))
	move(idConnect, at(margin, a.actionsY(), contentW-128, buttonH))
	move(idLogout, at(margin+contentW-120, a.actionsY(), 120, buttonH))
	for id, h := range a.ctl {
		f := a.font
		if id == idError || id == idSummary {
			f = a.fontSmall
		}
		pSendMessageW.Call(uintptr(h), wmSetFont, f, 1)
	}
	pInvalidateRect.Call(uintptr(a.main), 0, 1)
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
	for _, id := range []int{idRelay, idUser, idPass, idLogin, idError} {
		show(a.ctl[id], on)
	}
	for _, id := range []int{idFilter, idSummary, idList, idConnect, idLogout} {
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

// paintMain desenha o que não é controle, no visual do console: o
// cabeçalho com a marca ("> REMOTEIT") e o usuário, os rótulos e as caixas
// dos campos, a moldura da lista e o rodapé com a versão.
func (a *App) paintMain() {
	var ps paintStruct
	hdc, _, _ := pBeginPaint.Call(uintptr(a.main), uintptr(unsafe.Pointer(&ps)))
	defer pEndPaint.Call(uintptr(a.main), uintptr(unsafe.Pointer(&ps)))
	var rc rect
	pGetClientRect.Call(uintptr(a.main), uintptr(unsafe.Pointer(&rc)))
	left, right := a.px(margin), rc.Right-a.px(margin)

	head := rect{0, 0, rc.Right, a.px(headerH)}
	fillRect(hdc, head, colorPanel)
	fillRect(hdc, rect{0, head.Bottom - 1, rc.Right, head.Bottom}, colorLine)
	drawString(hdc, rect{left, 0, right, head.Bottom}, ">", a.fontTitle, colorAccent, dtLeft)
	x := left + textWidth(hdc, ">", a.fontTitle) + a.px(10)
	drawSpaced(hdc, rect{x, 0, right, head.Bottom}, "REMOTEIT", a.fontTitle, colorStrong, dtLeft, a.px(2))

	a.mu.Lock()
	logged := a.api != nil && a.user.Username != ""
	user, role := a.user.Username, a.user.Role
	caps, known := a.caps, a.capsKnown
	a.mu.Unlock()
	login := isVisible(a.ctl[idLogin])
	if logged && !login {
		label := "técnico"
		if role == "admin" {
			label = "administrador"
		}
		drawString(hdc, rect{0, a.px(9), right, a.px(29)}, user, a.fontBold, colorStrong, dtRight)
		drawSpaced(hdc, rect{0, a.px(29), right, a.px(46)}, strings.ToUpper(label), a.fontSmall, colorDim, dtRight, a.px(1))
	} else {
		drawSpaced(hdc, rect{0, 0, right, head.Bottom}, "TÉCNICO", a.fontSmall, colorDim, dtRight, a.px(1))
	}

	fields := listFields
	if login {
		fields = loginFields
		y := a.px(loginTitleY)
		drawSpaced(hdc, rect{left, y, right, y + a.px(18)}, "ENTRAR NO CONSOLE", a.fontLabel, colorAccent, dtLeft, a.px(2))
		fillRect(hdc, rect{left, y + a.px(26), right, y + a.px(27)}, colorLine)
	} else {
		l := a.listBox()
		fillRect(hdc, l, colorSheet)
		frame(hdc, l, colorLine)
	}
	focus, _, _ := pGetFocus.Call()
	for _, f := range fields {
		y := a.px(f.y)
		drawSpaced(hdc, rect{left, y, right, y + a.px(16)}, strings.ToUpper(f.label), a.fontSmall, colorMuted, dtLeft, a.px(1))
		b := a.box(f)
		fillRect(hdc, b, colorInput)
		border := uintptr(colorLineStrong)
		if windows.HWND(focus) == a.ctl[f.id] {
			border = colorAccent
		}
		frame(hdc, b, border)
	}

	foot := rect{0, rc.Bottom - a.px(footerH), rc.Right, rc.Bottom}
	fillRect(hdc, foot, colorPanel)
	fillRect(hdc, rect{0, foot.Top, rc.Right, foot.Top + 1}, colorLine)
	text := "versão " + orDash(a.opts.Version)
	if known {
		text += "  ·  " + ui.Decoders(caps.h264, caps.av1)
	}
	drawString(hdc, rect{left, foot.Top, right, foot.Bottom}, text, a.fontSmall, colorDim, dtLeft)
}

// fieldAt é o campo cuja caixa contém (x, y), na tela que está à mostra.
func (a *App) fieldAt(x, y int32) (field, bool) {
	fields := listFields
	if isVisible(a.ctl[idLogin]) {
		fields = loginFields
	}
	for _, f := range fields {
		b := a.box(f)
		if x >= b.Left && x < b.Right && y >= b.Top && y < b.Bottom {
			return f, true
		}
	}
	return field{}, false
}

// redrawBox repinta a caixa do campo id (o contorno acende com o foco).
func (a *App) redrawBox(id int) {
	for _, f := range append(slices.Clone(loginFields), listFields...) {
		if f.id == id {
			b := a.box(f)
			pInvalidateRect.Call(uintptr(a.main), uintptr(unsafe.Pointer(&b)), 0)
		}
	}
}

// drawRow desenha uma máquina como a linha da tabela do console: LED de
// estado, nome e detalhes.
func (a *App) drawRow(d *drawItemStruct) {
	r := d.RcItem
	if int(d.ItemID) < 0 || int(d.ItemID) >= len(a.rows) {
		fillRect(d.HDC, r, colorSheet)
		return
	}
	row := a.rows[d.ItemID]
	selected := d.ItemState&odsSelected != 0
	bg := uintptr(colorSheet)
	if selected {
		bg = colorSel
	}
	fillRect(d.HDC, r, bg)
	if selected {
		fillRect(d.HDC, rect{r.Left, r.Top, r.Left + a.px(2), r.Bottom}, colorAccent)
	}
	fillRect(d.HDC, rect{r.Left, r.Bottom - 1, r.Right, r.Bottom}, colorLineSoft)
	led, title, detail := uintptr(colorBad), uintptr(colorMuted), uintptr(colorDim)
	switch row.State {
	case ui.Online:
		led, title, detail = colorAccent, colorStrong, colorMuted
	case ui.InUse:
		led, title, detail = colorWarn, colorStrong, colorWarn
	}
	mid := (r.Top + r.Bottom) / 2
	dot(d.HDC, r.Left+a.px(17), mid-a.px(8), a.px(3), led)
	x := r.Left + a.px(30)
	drawString(d.HDC, rect{x, r.Top + a.px(6), r.Right - a.px(10), mid}, row.Title, a.fontBold, title, dtLeft)
	drawString(d.HDC, rect{x, mid, r.Right - a.px(10), r.Bottom - a.px(6)}, row.Detail, a.fontSmall, detail, dtLeft)
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
	a.endSession(false)
	a.showLogin(true)
}

// endSession fecha as telas remotas abertas e encerra a sessão do técnico
// no relay, apagando o token guardado: quem abrir o programa depois precisa
// entrar de novo. wait espera o relay confirmar (até 3 s), para quando o
// processo vai acabar logo em seguida.
func (a *App) endSession(wait bool) {
	if a.stopList != nil {
		a.stopList()
		a.stopList = nil
	}
	for h, s := range a.sessions {
		s.cancel()
		pDestroyWindow.Call(uintptr(h))
	}
	a.mu.Lock()
	api := a.api // fica em a.api: uma conexão em andamento ainda a lê
	a.mu.Unlock()
	if api != nil {
		done := make(chan struct{})
		go func() {
			defer close(done)
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			api.Logout(ctx)
		}()
		if wait {
			<-done
		}
	}
	a.settings.Token = ""
	if a.opts.Save != nil {
		a.opts.Save(a.settings)
	}
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
		case code == enSetFocus || code == enKillFocus:
			a.redrawBox(id)
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
		fg, bg, brush := uintptr(colorText), uintptr(colorBg), a.bgBrush
		switch m {
		case wmCtlColorEdit:
			fg, bg, brush = colorStrong, colorInput, a.editBrush
		case wmCtlColorListbox:
			bg, brush = colorSheet, a.sheetBrush
		}
		switch windows.HWND(lp) {
		case a.ctl[idError]:
			fg = colorBad
		case a.ctl[idSummary]:
			fg = colorMuted
		}
		pSetTextColor.Call(wp, fg)
		pSetBkColor.Call(wp, bg)
		return brush, true
	case wmLButtonDown: // clique na margem da caixa também foca o campo
		if f, ok := a.fieldAt(int32(int16(loword(lp))), int32(int16(hiword(lp)))); ok {
			pSetFocus.Call(uintptr(a.ctl[f.id]))
		}
		return 0, true
	case wmDpiChanged: // foi para um monitor com outra escala
		a.dpi = int32(max(loword(wp), 96))
		var r rect
		pRtlMoveMemory.Call(uintptr(unsafe.Pointer(&r)), lp, unsafe.Sizeof(r))
		a.makeFonts()
		a.fitMain(&r)
		a.place()
		return 0, true
	case wmClose:
		// fechar o programa é sair: a sessão termina no relay e o token
		// guardado some (a atualização automática reinicia por outro
		// caminho e mantém o login)
		show(hwnd, false)
		a.endSession(true)
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
