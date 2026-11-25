//go:build windows

package win

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"sync"
	"sync/atomic"
	"unsafe"

	"golang.org/x/sys/windows"

	"remoteit/internal/protocol"
	"remoteit/internal/viewer/client"
	"remoteit/internal/viewer/decode"
)

// Mensagens próprias da janela de sessão (vindas das goroutines).
const (
	wmFrame  = wmApp + 10 // quadro novo para desenhar
	wmTitle  = wmApp + 11 // estatísticas novas no título
	wmStatus = wmApp + 12 // status novo (aguardando, protegida, encerrada)
	wmCursor = wmApp + 13 // formato do cursor mudou
)

// Itens de menu da sessão.
const (
	idPreset0      = 1000 // + índice em client.Presets
	idColor444     = 1010
	idActualSize   = 1101
	idFullscreen   = 1102
	idCtrlShiftEsc = 1201
	idLock         = 1202
	idClipboard    = 1203
	idCapture      = 1301
)

var statusText = map[string]string{
	protocol.TypeWaiting: "Aguardando o usuário do PC permitir o acesso…",
	protocol.TypeDenied:  "O usuário do PC recusou o acesso.",
	protocol.TypeSecure:  "Tela protegida (UAC ou bloqueio) — aguardando…",
}

// cursores do Windows pelos nomes CSS que o agente manda
var cursorIDs = map[string]uintptr{
	"default": 32512, "text": 32513, "wait": 32514, "crosshair": 32515, "move": 32646,
	"not-allowed": 32648, "pointer": 32649, "progress": 32650, "help": 32651,
	"nwse-resize": 32642, "nesw-resize": 32643, "ew-resize": 32644, "ns-resize": 32645,
}

// sessionWin é a janela de uma máquina. Os campos sem mutex só são tocados
// pela thread da interface.
type sessionWin struct {
	app     *App
	hwnd    windows.HWND
	menu    uintptr
	machine client.Machine
	preset  string

	actualSize bool
	color444   bool // oferecer cor 4:4:4 ao agente (menu Qualidade)
	full       bool
	saved      rect
	savedStyle uintptr
	capture    bool
	keys       *keyTranslator
	dest       rect // onde o vídeo foi desenhado (para mapear o mouse)

	cancel  context.CancelFunc
	out     chan func(*client.Session) // envio sem travar a interface
	pending atomic.Bool                // já há um wmFrame na fila

	mu     sync.Mutex
	can    decoders        // o que este PC decodifica (achado ao conectar)
	broken map[string]bool // codecs que falharam nesta sessão: fora do hello
	sess   *client.Session
	frame  *decode.Frame
	status string
	title  string
	cursor string
}

func (a *App) openSession(m client.Machine) {
	w := &sessionWin{app: a, machine: m, preset: "quality", capture: true, color444: true, keys: newKeyTranslator(),
		broken: map[string]bool{},
		out:    make(chan func(*client.Session), 256), status: "Conectando…"}
	w.title = "RemoteIT — " + m.Name
	w.hwnd = a.createWindow(sessionClass, w.title, wsOverlappedWindow|wsClipChildren, 1280, 800, 0)
	a.sessions[w.hwnd] = w
	a.handlers[w.hwnd] = w.proc
	openSessions.Add(1)
	w.buildMenu()
	show(w.hwnd, true)
	pUpdateWindow.Call(uintptr(w.hwnd))

	ctx, cancel := context.WithCancel(context.Background())
	w.cancel = cancel
	go w.run(ctx)
	go w.sender(ctx)
}

func (w *sessionWin) buildMenu() {
	bar, _, _ := pCreateMenu.Call()
	q, _, _ := pCreatePopupMenu.Call()
	for i, p := range client.Presets {
		pAppendMenuW.Call(q, mfString, uintptr(idPreset0+i), uintptr(unsafe.Pointer(utf16(p.Label))))
	}
	pAppendMenuW.Call(q, mfSeparator, 0, 0)
	pAppendMenuW.Call(q, mfString, idColor444, uintptr(unsafe.Pointer(utf16("Cor 4:4:4 (texto colorido nítido; só na resolução nativa)"))))
	v, _, _ := pCreatePopupMenu.Call()
	pAppendMenuW.Call(v, mfString, idActualSize, uintptr(unsafe.Pointer(utf16("Tamanho real (1:1)"))))
	pAppendMenuW.Call(v, mfString, idFullscreen, uintptr(unsafe.Pointer(utf16("Tela cheia\tCtrl+Alt+F"))))
	ac, _, _ := pCreatePopupMenu.Call()
	pAppendMenuW.Call(ac, mfString, idCtrlShiftEsc, uintptr(unsafe.Pointer(utf16("Enviar Ctrl+Shift+Esc (Gerenciador de Tarefas)"))))
	pAppendMenuW.Call(ac, mfString, idClipboard, uintptr(unsafe.Pointer(utf16("Enviar área de transferência (cole com Ctrl+V lá)"))))
	pAppendMenuW.Call(ac, mfSeparator, 0, 0)
	pAppendMenuW.Call(ac, mfString, idLock, uintptr(unsafe.Pointer(utf16("Bloquear a tela remota"))))
	k, _, _ := pCreatePopupMenu.Call()
	pAppendMenuW.Call(k, mfString, idCapture, uintptr(unsafe.Pointer(utf16("Enviar Win, Alt+Tab e atalhos do sistema"))))
	pAppendMenuW.Call(bar, mfPopup, q, uintptr(unsafe.Pointer(utf16("&Qualidade"))))
	pAppendMenuW.Call(bar, mfPopup, v, uintptr(unsafe.Pointer(utf16("&Exibir"))))
	pAppendMenuW.Call(bar, mfPopup, ac, uintptr(unsafe.Pointer(utf16("&Ações"))))
	pAppendMenuW.Call(bar, mfPopup, k, uintptr(unsafe.Pointer(utf16("&Teclado"))))
	pSetMenu.Call(uintptr(w.hwnd), bar)
	w.menu = bar
	w.syncMenu()
}

func (w *sessionWin) syncMenu() {
	for i, p := range client.Presets {
		if p.Value == w.preset {
			pCheckMenuRadioItem.Call(w.menu, idPreset0, uintptr(idPreset0+len(client.Presets)-1), uintptr(idPreset0+i), mfByCommand)
		}
	}
	check := func(id uintptr, on bool) {
		f := uintptr(mfUnchecked)
		if on {
			f = mfChecked
		}
		pCheckMenuItem.Call(w.menu, id, f)
	}
	check(idActualSize, w.actualSize)
	check(idColor444, w.color444)
	check(idCapture, w.capture)
	pDrawMenuBar.Call(uintptr(w.hwnd))
}

// ---------- rede e decodificação (goroutines) ----------

// run conecta e lê a sessão numa thread do SO só: o decodificador H.264 do
// Windows (COM/Media Foundation) fica preso à thread que o criou.
func (w *sessionWin) run(ctx context.Context) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	w.mu.Lock()
	w.can = probeDecoders()
	codecs := w.codecsLocked()
	w.mu.Unlock()
	h := &sessionHandler{w: w, vids: map[byte]decode.Video{}, errs: map[byte]int{}}
	defer h.closeAll()

	s, err := w.app.api.Dial(ctx, w.machine.ID, codecs, w.preset)
	if err != nil {
		if errors.Is(err, client.ErrUnauthorized) {
			post(w.app.main, wmLoggedOut)
		}
		w.setStatus("Não foi possível conectar: " + err.Error())
		return
	}
	w.mu.Lock()
	w.sess = s
	w.mu.Unlock()
	err = s.Run(ctx, h)
	if ctx.Err() == nil {
		msg := "Conexão encerrada."
		if err != nil {
			msg = "Conexão encerrada: " + err.Error()
		}
		w.setStatus(msg)
	}
}

// sender manda as mensagens da interface na ordem, sem travar a janela.
func (w *sessionWin) sender(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case f := <-w.out:
			w.mu.Lock()
			s := w.sess
			w.mu.Unlock()
			if s != nil {
				f(s)
			}
		}
	}
}

// send enfileira; com a fila cheia, descarta (só movimentos de mouse chegam
// a encher, e o próximo corrige).
func (w *sessionWin) send(f func(*client.Session)) {
	select {
	case w.out <- f:
	default:
	}
}

func (w *sessionWin) setStatus(s string) {
	w.mu.Lock()
	w.status = s
	w.mu.Unlock()
	post(w.hwnd, wmStatus)
}

// decoders diz o que este PC decodifica.
type decoders struct{ h264, av1 bool }

// probeDecoders abre e fecha cada decodificador do Windows: H.264 vem com o
// sistema (falta no Windows N sem o Media Feature Pack) e AV1 vem da
// "Extensão de Vídeo AV1" da Microsoft Store.
func probeDecoders() decoders {
	var d decoders
	if v, err := decode.NewH264(); err == nil {
		v.Close()
		d.h264 = true
	}
	if v, err := decode.NewAV1(); err == nil {
		v.Close()
		d.av1 = true
	}
	return d
}

// codecsLocked monta o hello com o que o PC decodifica, a escolha do menu e
// sem os codecs que falharam. Chame com w.mu travado.
func (w *sessionWin) codecsLocked() []string {
	var out []string
	for _, c := range client.Codecs(w.can.h264, w.can.av1, w.color444) {
		if !w.broken[c] {
			out = append(out, c)
		}
	}
	return out
}

// resendCodecs manda ao agente a lista atual: ele reabre a captura com o
// primeiro codec que conseguir.
func (w *sessionWin) resendCodecs() {
	w.mu.Lock()
	codecs := w.codecsLocked()
	w.mu.Unlock()
	w.send(func(s *client.Session) { s.SetCodecs(codecs) })
}

// sessionHandler decodifica os quadros na goroutine de [sessionWin.run].
type sessionHandler struct {
	w    *sessionWin
	vids map[byte]decode.Video // um decodificador por codec, aberto no primeiro quadro
	errs map[byte]int          // falhas seguidas por codec
	last byte                  // codec do último quadro de vídeo
}

// codecNames liga o código do quadro ao nome do hello e ao construtor.
var codecNames = map[byte]struct {
	name string
	open func() (decode.Video, error)
}{
	protocol.CodecH264:   {protocol.CodecNameH264, decode.NewH264},
	protocol.CodecAVC444: {protocol.CodecNameAVC444, decode.NewAVC444},
	protocol.CodecAV1:    {protocol.CodecNameAV1, decode.NewAV1},
}

// maxDecodeErrors é quantas falhas seguidas tiram um codec do hello. Uma
// falha isolada (quadro perdido) só reinicia o decodificador e pede keyframe.
const maxDecodeErrors = 3

func (h *sessionHandler) closeAll() {
	for _, v := range h.vids {
		v.Close()
	}
}

// fail descarta o decodificador do codec e pede um keyframe; na terceira
// falha seguida, desiste do codec e avisa o agente para trocar.
func (h *sessionHandler) fail(codec byte) {
	if v := h.vids[codec]; v != nil {
		v.Close()
		delete(h.vids, codec)
	}
	h.errs[codec]++
	if h.errs[codec] >= maxDecodeErrors {
		h.w.mu.Lock()
		h.w.broken[codecNames[codec].name] = true
		h.w.mu.Unlock()
		h.w.resendCodecs()
		return
	}
	h.w.send(func(s *client.Session) { s.Keyframe() })
}

func (h *sessionHandler) Frame(hd protocol.FrameHeader, payload []byte) {
	var f *decode.Frame
	var err error
	if c, ok := codecNames[hd.Codec]; ok {
		h.last = hd.Codec
		v := h.vids[hd.Codec]
		if v == nil {
			if v, err = c.open(); err != nil {
				h.fail(hd.Codec)
				return
			}
			h.vids[hd.Codec] = v
		}
		if f, err = v.Decode(payload, int(hd.W), int(hd.H)); err != nil {
			h.fail(hd.Codec)
			return
		}
		if f != nil {
			h.errs[hd.Codec] = 0
		}
	} else { // JPEG e o refresh de nitidez em PNG
		f, err = decode.Image(payload)
	}
	if f == nil || err != nil {
		return
	}
	h.w.mu.Lock()
	h.w.frame = f
	h.w.mu.Unlock()
	if !h.w.pending.Swap(true) {
		post(h.w.hwnd, wmFrame)
	}
}

func (h *sessionHandler) Status(t string) {
	if t == protocol.TypeGranted {
		h.w.setStatus("")
		return
	}
	if s, ok := statusText[t]; ok {
		h.w.setStatus(s)
	}
}

func (h *sessionHandler) Stats(st protocol.Stats) {
	gpu := ""
	if v := h.vids[h.last]; v != nil && v.GPU() && st.Encoder != "" {
		gpu = " · decodificação na GPU"
	}
	t := fmt.Sprintf("RemoteIT — %s · %s · %dx%d · %d fps · %.1f Mbps · RTT %d ms%s",
		h.w.machine.Name, st.Encoder, st.W, st.H, st.FPS, float64(st.Kbps)/1000, st.RTTms, gpu)
	h.w.mu.Lock()
	h.w.title = t
	h.w.mu.Unlock()
	post(h.w.hwnd, wmTitle)
}

func (h *sessionHandler) Cursor(int, int, bool) {}

func (h *sessionHandler) CursorShape(name string) {
	h.w.mu.Lock()
	h.w.cursor = name
	h.w.mu.Unlock()
	post(h.w.hwnd, wmCursor)
}

// ---------- interface (thread da janela) ----------

func (w *sessionWin) proc(hwnd windows.HWND, m uint32, wp, lp uintptr) (uintptr, bool) {
	switch m {
	case wmFrame:
		w.pending.Store(false)
		pInvalidateRect.Call(uintptr(hwnd), 0, 0)
		return 0, true
	case wmTitle:
		w.mu.Lock()
		t := w.title
		w.mu.Unlock()
		setText(hwnd, t)
		return 0, true
	case wmStatus:
		pInvalidateRect.Call(uintptr(hwnd), 0, 0)
		return 0, true
	case wmCursor:
		return 0, true
	case wmEraseBkgnd:
		return 1, true // o paint desenha tudo: sem piscar
	case wmPaint:
		w.paint()
		return 0, true
	case wmSize:
		pInvalidateRect.Call(uintptr(hwnd), 0, 0)
		return 0, true
	case wmSetCursor:
		if loword(lp) == htClient {
			w.mu.Lock()
			name := w.cursor
			w.mu.Unlock()
			if name == "none" {
				pSetCursor.Call(0)
				return 1, true
			}
			id, ok := cursorIDs[name]
			if !ok {
				id = idcArrow
			}
			c, _, _ := pLoadCursorW.Call(0, id)
			pSetCursor.Call(c)
			return 1, true
		}
	case wmMouseMove:
		x, y := w.norm(lp)
		w.send(func(s *client.Session) { s.MouseMove(x, y) })
		return 0, true
	case wmLButtonDown, wmRButtonDown, wmMButtonDown, wmLButtonUp, wmRButtonUp, wmMButtonUp:
		w.mouseButton(m, lp)
		return 0, true
	case wmMouseWheel:
		d := -int(hiword(wp)) // Windows: positivo = para cima; protocolo: positivo = para baixo
		w.send(func(s *client.Session) { s.Wheel(d) })
		return 0, true
	case wmKeyDown, wmSysKeyDown, wmKeyUp, wmSysKeyUp:
		if w.capture {
			return 0, true // o hook já mandou
		}
		w.keyFromMessage(m, wp, lp)
		return 0, true
	case 0x0008: // WM_KILLFOCUS
		w.releaseKeys()
	case wmCommand:
		w.command(loword(wp))
		return 0, true
	case wmClose:
		w.releaseKeys()
		w.cancel()
		pDestroyWindow.Call(uintptr(hwnd))
		return 0, true
	case wmDestroy:
		delete(w.app.sessions, hwnd)
		delete(w.app.handlers, hwnd)
		openSessions.Add(-1)
		return 0, true
	}
	return 0, false
}

func (w *sessionWin) command(id int32) {
	switch {
	case id >= idPreset0 && int(id) < idPreset0+len(client.Presets):
		w.preset = client.Presets[id-idPreset0].Value
		p := w.preset
		w.send(func(s *client.Session) { s.Preset(p) })
	case id == idColor444:
		w.mu.Lock()
		w.color444 = !w.color444
		w.mu.Unlock()
		w.resendCodecs()
	case id == idActualSize:
		w.actualSize = !w.actualSize
		pInvalidateRect.Call(uintptr(w.hwnd), 0, 0)
	case id == idFullscreen:
		w.toggleFullscreen()
	case id == idCtrlShiftEsc:
		w.send(func(s *client.Session) {
			s.Key(true, "ControlLeft", "Control")
			s.Key(true, "ShiftLeft", "Shift")
			s.Key(true, "Escape", "Escape")
			s.Key(false, "Escape", "Escape")
			s.Key(false, "ShiftLeft", "Shift")
			s.Key(false, "ControlLeft", "Control")
		})
	case id == idClipboard:
		text := clipboardText(w.hwnd)
		if text == "" {
			messageBox(w.hwnd, "Área de transferência", "Não há texto copiado neste computador.")
			return
		}
		w.send(func(s *client.Session) { s.Clipboard(text) })
	case id == idLock:
		w.send(func(s *client.Session) { s.Lock() })
	case id == idCapture:
		w.releaseKeys()
		w.capture = !w.capture
	}
	w.syncMenu()
}

func (w *sessionWin) paint() {
	var ps paintStruct
	hdc, _, _ := pBeginPaint.Call(uintptr(w.hwnd), uintptr(unsafe.Pointer(&ps)))
	defer pEndPaint.Call(uintptr(w.hwnd), uintptr(unsafe.Pointer(&ps)))
	var rc rect
	pGetClientRect.Call(uintptr(w.hwnd), uintptr(unsafe.Pointer(&rc)))

	w.mu.Lock()
	f, status := w.frame, w.status
	w.mu.Unlock()
	bg := w.app.bgBrush

	if f == nil {
		pFillRect.Call(hdc, uintptr(unsafe.Pointer(&rc)), bg)
		w.dest = rect{}
		w.drawText(hdc, rc, status)
		return
	}

	// ajustado à janela (sem distorcer) ou em tamanho real; se a ampliação for
	// pequena (até 10%), fica em 1:1, que é mais nítido
	dw, dh := int32(f.W), int32(f.H)
	if !w.actualSize && rc.w() > 0 && rc.h() > 0 {
		sx, sy := float64(rc.w())/float64(f.W), float64(rc.h())/float64(f.H)
		s := min(sx, sy)
		if s < 1 || s > 1.1 {
			dw, dh = int32(float64(f.W)*s), int32(float64(f.H)*s)
		}
	}
	dx, dy := (rc.w()-dw)/2, (rc.h()-dh)/2
	if w.actualSize { // maior que a janela: ancora no canto, sem centralizar fora da tela
		dx, dy = max(dx, 0), max(dy, 0)
	}
	w.dest = rect{dx, dy, dx + dw, dy + dh}

	// bordas pintadas à parte: o vídeo não pisca ao redimensionar
	for _, b := range []rect{{0, 0, rc.Right, dy}, {0, dy + dh, rc.Right, rc.Bottom}, {0, dy, dx, dy + dh}, {dx + dw, dy, rc.Right, dy + dh}} {
		if b.w() > 0 && b.h() > 0 {
			pFillRect.Call(hdc, uintptr(unsafe.Pointer(&b)), bg)
		}
	}
	mode := uintptr(3) // COLORONCOLOR: 1:1, sem custo
	if dw != int32(f.W) {
		mode = halftone
	}
	pSetStretchBltMode.Call(hdc, mode)
	bmi := bitmapInfoHeader{Width: int32(f.W), Height: -int32(f.H), Planes: 1, BitCount: 32}
	bmi.Size = uint32(unsafe.Sizeof(bmi))
	pStretchDIBits.Call(hdc, uintptr(dx), uintptr(dy), uintptr(dw), uintptr(dh), 0, 0, uintptr(f.W), uintptr(f.H),
		uintptr(unsafe.Pointer(&f.BGRA[0])), uintptr(unsafe.Pointer(&bmi)), dibRGB, srcCopy)
	if status != "" {
		band := rect{0, 0, rc.Right, 36}
		pFillRect.Call(hdc, uintptr(unsafe.Pointer(&band)), w.app.panelBrush)
		w.drawText(hdc, band, status)
	}
}

func (w *sessionWin) drawText(hdc uintptr, r rect, s string) {
	pSelectObject.Call(hdc, w.app.font)
	pSetBkMode.Call(hdc, transparent)
	pSetTextColor.Call(hdc, colorText)
	pDrawTextW.Call(hdc, uintptr(unsafe.Pointer(utf16(s))), ^uintptr(0), uintptr(unsafe.Pointer(&r)), dtCenter|dtVCenter|dtSingleLine)
}

// norm converte a posição do mouse na janela em [0,1] da tela remota.
func (w *sessionWin) norm(lp uintptr) (float64, float64) {
	d := w.dest
	if d.w() <= 0 || d.h() <= 0 {
		return 0, 0
	}
	x := float64(loword(lp)-d.Left) / float64(d.w())
	y := float64(hiword(lp)-d.Top) / float64(d.h())
	return min(max(x, 0), 1), min(max(y, 0), 1)
}

func (w *sessionWin) mouseButton(m uint32, lp uintptr) {
	b, down := 0, false
	switch m {
	case wmLButtonDown:
		b, down = 0, true
	case wmLButtonUp:
		b = 0
	case wmMButtonDown:
		b, down = 1, true
	case wmMButtonUp:
		b = 1
	case wmRButtonDown:
		b, down = 2, true
	case wmRButtonUp:
		b = 2
	}
	if down {
		pSetCapture.Call(uintptr(w.hwnd)) // o soltar chega mesmo fora da janela
	} else {
		pReleaseCapture.Call()
	}
	x, y := w.norm(lp)
	w.send(func(s *client.Session) { s.MouseButton(down, b, x, y) })
}

// keyFromMessage é o caminho do teclado com a captura desligada (mensagens
// normais da janela, sem Win nem Alt+Tab).
func (w *sessionWin) keyFromMessage(m uint32, wp, lp uintptr) {
	vk := uint32(wp)
	scan := uint32(lp>>16) & 0xFF
	ext := (lp>>24)&1 != 0
	switch vk { // a mensagem traz Shift/Ctrl/Alt sem lado
	case vkShift:
		vk = vkLShift
		if scan == 0x36 {
			vk = vkRShift
		}
	case vkControl:
		vk = vkLControl
		if ext {
			vk = vkRControl
		}
	case vkMenu:
		vk = vkLMenu
		if ext {
			vk = vkRMenu
		}
	}
	up := m == wmKeyUp || m == wmSysKeyUp
	if code, key, ok := w.keys.event(vk, scan, ext, up); ok {
		w.send(func(s *client.Session) { s.Key(!up, code, key) })
	}
}

// keyFromHook é o caminho com a captura ligada (hook de baixo nível): pega
// também Win, Alt+Tab, Ctrl+Esc. Devolve true se a tecla foi consumida.
func (w *sessionWin) keyFromHook(kb kbdLLHookStruct, up bool) bool {
	if kb.Flags&llkhfInjected != 0 {
		return false
	}
	// Ctrl+Alt+F: atalho local, não vai para o PC remoto
	if !up && kb.VkCode == 'F' && w.keys.ctrl() && w.keys.alt() {
		w.releaseKeys()
		w.toggleFullscreen()
		return true
	}
	if code, key, ok := w.keys.event(kb.VkCode, kb.ScanCode, kb.Flags&llkhfExtended != 0, up); ok {
		w.send(func(s *client.Session) { s.Key(!up, code, key) })
	}
	return true
}

func (w *sessionWin) releaseKeys() {
	codes, keys := w.keys.releaseAll()
	for i := range codes {
		c, k := codes[i], keys[i]
		w.send(func(s *client.Session) { s.Key(false, c, k) })
	}
}

func (w *sessionWin) toggleFullscreen() {
	h := uintptr(w.hwnd)
	if !w.full {
		w.savedStyle, _, _ = pGetWindowLongPtrW.Call(h, uintptr(gwlStyleIndex()))
		pGetWindowRect.Call(h, uintptr(unsafe.Pointer(&w.saved)))
		mon, _, _ := pMonitorFromWindow.Call(h, monitorDefaultToNearest)
		mi := monitorInfo{}
		mi.Size = uint32(unsafe.Sizeof(mi))
		pGetMonitorInfoW.Call(mon, uintptr(unsafe.Pointer(&mi)))
		pSetMenu.Call(h, 0)
		pSetWindowLongPtrW.Call(h, uintptr(gwlStyleIndex()), (w.savedStyle&^wsOverlappedWindow)|wsPopup|wsVisible)
		r := mi.Monitor
		pSetWindowPos.Call(h, 0, uintptr(r.Left), uintptr(r.Top), uintptr(r.w()), uintptr(r.h()), swpNoZOrder|swpFrameChanged)
		w.full = true
		return
	}
	pSetWindowLongPtrW.Call(h, uintptr(gwlStyleIndex()), w.savedStyle)
	pSetMenu.Call(h, w.menu)
	r := w.saved
	pSetWindowPos.Call(h, 0, uintptr(r.Left), uintptr(r.Top), uintptr(r.w()), uintptr(r.h()), swpNoZOrder|swpFrameChanged)
	w.full = false
}

// gwlStyleIndex é GWL_STYLE (-16) como o Win32 espera receber.
func gwlStyleIndex() int { return gwlStyle }
