//go:build windows

package win

import (
	"context"
	"errors"
	"image"
	"log/slog"
	"runtime"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"

	"remoteit/internal/protocol"
	"remoteit/internal/viewer/client"
	"remoteit/internal/viewer/decode"
	"remoteit/internal/viewer/render"
	"remoteit/internal/viewer/ui"
)

// Mensagens próprias da janela de sessão (vindas das goroutines).
const (
	wmFrame  = wmApp + 10 // quadro novo para desenhar
	wmTitle  = wmApp + 11 // estatísticas novas no título
	wmStatus = wmApp + 12 // status novo (aguardando, protegida, encerrada)
	wmCursor = wmApp + 13 // formato do cursor mudou
	wmStats  = wmApp + 14 // telemetria nova para a barra
	wmGDI    = wmApp + 15 // o desenho na GPU falhou: volta para o GDI
)

// idViewportTimer espera a janela parar de mudar de tamanho antes de pedir
// ao agente o vídeo no tamanho novo (reabrir o encoder custa um keyframe).
const (
	idViewportTimer = 7
	viewportDelay   = 400 // ms
)

// barH é a altura da barra de telemetria, em pixels lógicos.
const barH = 32

// Itens de menu da sessão.
const (
	idPreset0      = 1000 // + índice em client.Presets
	idColor444     = 1010
	idActualSize   = 1101
	idFullscreen   = 1102
	idBar          = 1103
	idDiag         = 1401
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
	showBar    bool // barra de telemetria (menu Exibir)
	full       bool
	saved      rect
	savedStyle uintptr
	capture    bool
	keys       *keyTranslator

	cancel  context.CancelFunc
	out     chan func(*client.Session) // envio sem travar a interface
	pending atomic.Bool                // já há um wmFrame na fila

	// desenho na GPU: uma janela filha cobre a área de vídeo e o render
	// desenha nela; os cliques passam para a janela da sessão. nil = GDI.
	video windows.HWND
	rend  *render.Renderer

	mu       sync.Mutex
	can      decoders        // o que este PC decodifica (achado ao conectar)
	broken   map[string]bool // codecs que falharam nesta sessão: fora do hello
	sess     *client.Session
	canvas   decode.Canvas // a tela composta do desenho pela CPU (GDI)
	status   string
	title    string
	stats    protocol.Stats // último stats do agente
	drawn    int            // quadros desenhados no último segundo
	gpu      bool           // decodificação na GPU
	gpuDraw  bool           // desenho na GPU (render)
	decodeUs int            // decodificação + desenho aqui, média por quadro
	route    string         // "local" (direto no PC do relay) ou "internet"
	hasSt    bool
	cursor   string
	area     rect // área de vídeo na janela (cliente menos a barra)
	frameW   int  // tamanho do quadro composto no render (desenho na GPU)
	frameH   int
	actual   bool // cópia de actualSize para a goroutine de desenho
	dest     rect // onde o vídeo foi desenhado (para mapear o mouse)
}

func (a *App) openSession(m client.Machine) {
	w := &sessionWin{app: a, machine: m, preset: "quality", capture: true, color444: true, showBar: true, keys: newKeyTranslator(),
		broken: map[string]bool{},
		out:    make(chan func(*client.Session), 256), status: "Conectando…"}
	w.title = "RemoteIT — " + m.Name
	w.hwnd = a.createWindow(sessionClass, w.title, wsOverlappedWindow|wsClipChildren, 1280, 800, 0)
	a.sessions[w.hwnd] = w
	a.handlers[w.hwnd] = w.proc
	openSessions.Add(1)
	w.buildMenu()
	w.openRenderer()
	// maximizada, como uma aba do navegador: o agente manda o vídeo do
	// tamanho da área, e uma janela pequena recebia a tela reduzida (texto
	// menos nítido que no console web)
	pShowWindow.Call(uintptr(w.hwnd), swShowMaximized)
	w.layout()
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
	pAppendMenuW.Call(q, mfString, idColor444, uintptr(unsafe.Pointer(utf16("Cor 4:4:4 (texto colorido nítido; nos modos Nítido e Máximo)"))))
	v, _, _ := pCreatePopupMenu.Call()
	pAppendMenuW.Call(v, mfString, idActualSize, uintptr(unsafe.Pointer(utf16("Tamanho real (1:1)"))))
	pAppendMenuW.Call(v, mfString, idFullscreen, uintptr(unsafe.Pointer(utf16("Tela cheia\tCtrl+Alt+F"))))
	pAppendMenuW.Call(v, mfString, idBar, uintptr(unsafe.Pointer(utf16("Barra de telemetria"))))
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
	h, _, _ := pCreatePopupMenu.Call()
	pAppendMenuW.Call(h, mfString, idDiag, uintptr(unsafe.Pointer(utf16("Diagnóstico de vídeo…"))))
	pAppendMenuW.Call(bar, mfPopup, h, uintptr(unsafe.Pointer(utf16("Aj&uda"))))
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
	check(idBar, w.showBar)
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
	h := &sessionHandler{w: w, vids: map[byte]decode.Video{}, tex: map[byte]decode.TexVideo{}, errs: map[byte]int{},
		regions: map[uint32][]image.Rectangle{}}
	defer h.closeAll()

	w.mu.Lock()
	h.rend = w.rend
	w.mu.Unlock()
	vw, vh := w.viewport()
	view := client.View{Preset: w.preset, W: vw, H: vh}
	// rota local: com o relay neste PC (ou na mesma rede), a tela vai direto
	// por ele, sem dar a volta pela internet
	var s *client.Session
	var err error
	route := "internet"
	if local := w.app.api.LocalRoute(ctx); local != nil {
		if s, err = w.app.api.DialVia(ctx, local, w.machine.ID, codecs, view); err == nil {
			route = "local"
		}
	}
	if s == nil {
		s, err = w.app.api.Dial(ctx, w.machine.ID, codecs, view)
	}
	w.mu.Lock()
	w.route = route
	w.mu.Unlock()
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

// sessionHandler decodifica e desenha os quadros na goroutine de
// [sessionWin.run].
type sessionHandler struct {
	w     *sessionWin
	rend  *render.Renderer         // desenho na GPU; nil = CPU/GDI
	vids  map[byte]decode.Video    // caminho da CPU: um por codec, aberto no primeiro quadro
	tex   map[byte]decode.TexVideo // caminho da GPU: decodifica direto nas texturas do render
	errs  map[byte]int             // falhas seguidas por codec
	last  byte                     // codec do último quadro de vídeo
	drawn int                      // quadros de vídeo prontos desde o último stats
	aux   bool                     // a visão auxiliar do AVC444 já chegou neste tamanho
	auxW  int
	auxH  int
	took  time.Duration // decodificação + desenho desde o último stats
	tookN int

	// regiões (protocol.Region) por quadro e o que se acumulou dos quadros
	// entregues ao decodificador que ainda não saíram: quando sair imagem,
	// o vídeo vale na união deles (ou na tela inteira, se algum não tinha)
	regions map[uint32][]image.Rectangle
	acc     []image.Rectangle
	accFull bool
	waiting bool
	patches []patch // retalhos que esperam o vídeo de antes deles sair
}

// patch é um retalho sem perda (ou a tela inteira) já decodificado.
type patch struct {
	x, y int
	f    *decode.Frame
	w, h int // tamanho do quadro
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
	for _, v := range h.tex {
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
	if v := h.tex[codec]; v != nil {
		v.Close()
		delete(h.tex, codec)
	}
	h.acc, h.accFull, h.waiting = nil, false, false
	h.flushPatches()
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

// Region guarda onde o próximo quadro de vídeo vale.
func (h *sessionHandler) Region(r protocol.Region) {
	rs := make([]image.Rectangle, 0, len(r.R)/4)
	for i := 0; i+3 < len(r.R); i += 4 {
		rs = append(rs, image.Rect(r.R[i], r.R[i+1], r.R[i]+r.R[i+2], r.R[i+1]+r.R[i+3]))
	}
	if len(h.regions) > 256 { // regiões de quadros que nunca chegaram: limpa
		clear(h.regions)
	}
	h.regions[r.S] = rs
}

func (h *sessionHandler) Frame(hd protocol.FrameHeader, payload []byte) {
	start := time.Now()
	switch hd.Codec {
	case protocol.CodecH264, protocol.CodecAVC444, protocol.CodecAV1:
		h.last = hd.Codec
		if rs, ok := h.regions[hd.Seq]; ok {
			h.acc = append(h.acc, rs...)
			delete(h.regions, hd.Seq)
		} else {
			h.accFull = true // quadro sem Region (keyframe, agente antigo): vale inteiro
		}
		h.waiting = true
		drew := false
		if h.rend != nil {
			drew = h.videoGPU(hd, payload)
		}
		if h.rend == nil { // sem GPU, ou ela acabou de falhar
			drew = h.videoCPU(hd, payload)
		}
		if drew {
			h.drawn++
			h.took += time.Since(start)
			h.tookN++
			h.waiting = false
			h.flushPatches()
			h.show()
		}
	case protocol.CodecJPEG:
		if f, err := decode.Image(payload); err == nil {
			h.acc, h.accFull, h.waiting = nil, false, false
			h.drawWhole(f)
			h.drawn++
			h.took += time.Since(start)
			h.tookN++
			h.flushPatches()
			h.show()
		}
	case protocol.CodecPNG: // refresh sem perda da tela inteira
		if f, err := decode.Image(payload); err == nil {
			h.queuePatch(patch{0, 0, f, int(hd.W), int(hd.H)})
		}
	case protocol.CodecPNGRect: // retalhos sem perda das partes que pararam
		rects, err := protocol.ParsePNGRects(payload)
		if err != nil {
			return
		}
		for _, r := range rects {
			if f, err := decode.Image(r.PNG); err == nil {
				h.queuePatch(patch{r.X, r.Y, f, int(hd.W), int(hd.H)})
			}
		}
	}
}

// region é onde o vídeo que acabou de sair vale: nil = a tela inteira.
func (h *sessionHandler) region() []image.Rectangle {
	defer func() { h.acc, h.accFull = nil, false }()
	if h.accFull {
		return nil
	}
	if h.acc == nil {
		return []image.Rectangle{}
	}
	return h.acc
}

// videoGPU decodifica direto nas texturas do render e compõe. Uma falha do
// render (não do decodificador) passa a sessão para o GDI.
func (h *sessionHandler) videoGPU(hd protocol.FrameHeader, payload []byte) bool {
	v := h.tex[hd.Codec]
	if v == nil {
		var err error
		if v, err = decode.NewTex(hd.Codec, h.rend.Device()); err != nil {
			h.toGDI(err)
			return false
		}
		h.tex[hd.Codec] = v
	}
	w, hh := int(hd.W)&^1, int(hd.H)&^1
	got, err := v.Decode(payload, int(hd.W), int(hd.H), h.rend.Video)
	if err != nil {
		h.fail(hd.Codec)
		return false
	}
	if got == 0 {
		return false
	}
	h.errs[hd.Codec] = 0
	kind := render.NV12
	if hd.Codec == protocol.CodecAVC444 {
		if got&2 != 0 {
			h.aux, h.auxW, h.auxH = true, w, hh
		}
		kind = render.AVC444Main
		if h.aux && h.auxW == w && h.auxH == hh {
			kind = render.AVC444
		}
	}
	if err := h.rend.ComposeVideo(kind, w, hh, h.region()); err != nil {
		h.toGDI(err)
		return false
	}
	h.w.mu.Lock()
	h.w.frameW, h.w.frameH, h.w.gpu = w, hh, v.GPU()
	h.w.mu.Unlock()
	return true
}

// videoCPU é o caminho de antes: decodifica para a memória, converte na
// CPU e compõe na Canvas, que o GDI desenha.
func (h *sessionHandler) videoCPU(hd protocol.FrameHeader, payload []byte) bool {
	c := codecNames[hd.Codec]
	v := h.vids[hd.Codec]
	if v == nil {
		var err error
		if v, err = c.open(); err != nil {
			h.fail(hd.Codec)
			return false
		}
		h.vids[hd.Codec] = v
	}
	f, err := v.Decode(payload, int(hd.W), int(hd.H))
	if err != nil {
		h.fail(hd.Codec)
		return false
	}
	if f == nil {
		return false
	}
	h.errs[hd.Codec] = 0
	region := h.region()
	h.w.mu.Lock()
	h.w.canvas.Video(f, region)
	h.w.gpu = v.GPU()
	h.w.mu.Unlock()
	return true
}

// drawWhole põe um quadro inteiro (JPEG) na tela composta.
func (h *sessionHandler) drawWhole(f *decode.Frame) {
	if h.rend != nil {
		err := h.rend.ComposeBGRA(0, 0, f.W, f.H, f.BGRA, f.W, f.H)
		if err == nil {
			h.w.mu.Lock()
			h.w.frameW, h.w.frameH = f.W, f.H
			h.w.mu.Unlock()
			return
		}
		h.toGDI(err)
	}
	h.w.mu.Lock()
	h.w.canvas.Video(f, nil)
	h.w.mu.Unlock()
}

// queuePatch aplica o retalho já, ou depois que sair o vídeo dos quadros
// anteriores a ele (senão o vídeo mais velho o cobriria).
func (h *sessionHandler) queuePatch(p patch) {
	h.patches = append(h.patches, p)
	if !h.waiting {
		h.flushPatches()
	}
}

func (h *sessionHandler) flushPatches() {
	if len(h.patches) == 0 {
		return
	}
	for _, p := range h.patches {
		if h.rend != nil {
			if err := h.rend.ComposeBGRA(p.x, p.y, p.f.W, p.f.H, p.f.BGRA, p.w, p.h); err != nil {
				h.toGDI(err)
			} else {
				continue
			}
		}
		h.w.mu.Lock()
		h.w.canvas.Patch(p.x, p.y, p.f, p.w, p.h)
		h.w.mu.Unlock()
	}
	h.patches = nil
	h.show()
}

// show leva a tela composta à janela.
func (h *sessionHandler) show() {
	if h.rend != nil {
		h.w.present()
		return
	}
	if !h.w.pending.Swap(true) {
		post(h.w.hwnd, wmFrame)
	}
}

// toGDI desliga o desenho na GPU desta sessão (o render falhou) e pede um
// keyframe: o caminho da CPU começa do zero.
func (h *sessionHandler) toGDI(err error) {
	if h.rend == nil {
		return
	}
	slog.Info("desenho na GPU falhou; voltando para o GDI", "err", err)
	for c, v := range h.tex {
		v.Close()
		delete(h.tex, c)
	}
	h.rend = nil
	post(h.w.hwnd, wmGDI)
	h.w.send(func(s *client.Session) { s.Keyframe() })
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
	v := h.vids[h.last]
	codec, _ := ui.Codec(st.Encoder)
	t := "RemoteIT — " + h.w.machine.Name
	if codec != "" {
		t += " · " + codec
	}
	h.w.mu.Lock()
	h.w.title = t
	h.w.stats, h.w.drawn, h.w.hasSt = st, h.drawn, true
	if v != nil {
		h.w.gpu = v.GPU()
	}
	if h.tookN > 0 {
		h.w.decodeUs = int((h.took / time.Duration(h.tookN)).Microseconds())
	}
	h.w.mu.Unlock()
	h.drawn, h.took, h.tookN = 0, 0, 0
	post(h.w.hwnd, wmTitle)
	post(h.w.hwnd, wmStats)
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
		w.layout() // aviso na tela: a janela filha do vídeo sai da frente
		pInvalidateRect.Call(uintptr(hwnd), 0, 0)
		return 0, true
	case wmGDI:
		w.useGDI()
		return 0, true
	case wmTimer:
		if wp == idViewportTimer {
			pKillTimer.Call(uintptr(hwnd), idViewportTimer)
			w.sendViewport()
			return 0, true
		}
	case wmCursor:
		return 0, true
	case wmEraseBkgnd:
		return 1, true // o paint desenha tudo: sem piscar
	case wmPaint:
		w.paint()
		return 0, true
	case wmSize:
		w.layout()
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
	case wmStats:
		var rc rect
		pGetClientRect.Call(uintptr(hwnd), uintptr(unsafe.Pointer(&rc)))
		if b := w.barRect(rc); b.h() > 0 {
			pInvalidateRect.Call(uintptr(hwnd), uintptr(unsafe.Pointer(&b)), 0)
		}
		return 0, true
	case wmMouseMove:
		if w.overBar(lp) {
			return 0, true // a barra é daqui: não move o mouse remoto
		}
		x, y := w.norm(lp)
		w.send(func(s *client.Session) { s.MouseMove(x, y) })
		return 0, true
	case wmLButtonDown, wmRButtonDown, wmMButtonDown, wmLButtonUp, wmRButtonUp, wmMButtonUp:
		if w.overBar(lp) && (m == wmLButtonDown || m == wmRButtonDown || m == wmMButtonDown) {
			return 0, true
		}
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
		pKillTimer.Call(uintptr(hwnd), idViewportTimer)
		if w.video != 0 {
			delete(w.app.handlers, w.video)
		}
		w.mu.Lock()
		r := w.rend
		w.mu.Unlock()
		if r != nil {
			go func() { // depois que a goroutine de desenho parar de usar
				time.Sleep(time.Second)
				r.Close()
			}()
		}
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
	case id == idBar:
		w.showBar = !w.showBar
		w.layout()
		pInvalidateRect.Call(uintptr(w.hwnd), 0, 0)
	case id == idDiag:
		w.mu.Lock()
		text := ui.Diagnostics(w.app.opts.Version, w.can.h264, w.can.av1, w.codecsLocked(), w.stats, w.drawn, w.preset, w.localLocked())
		w.mu.Unlock()
		infoBox(w.hwnd, "Diagnóstico de vídeo — "+w.machine.Name, text)
	case id == idActualSize:
		w.actualSize = !w.actualSize
		w.layout() // pede ao agente a resolução nativa (1:1) ou a da janela
		w.present()
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

// barRect é onde fica a barra de telemetria (vazio se escondida ou em tela cheia).
func (w *sessionWin) barRect(rc rect) rect {
	if !w.showBar || w.full {
		return rect{}
	}
	return rect{rc.Left, rc.Bottom - w.app.px(barH), rc.Right, rc.Bottom}
}

func (w *sessionWin) paint() {
	var ps paintStruct
	hdc, _, _ := pBeginPaint.Call(uintptr(w.hwnd), uintptr(unsafe.Pointer(&ps)))
	defer pEndPaint.Call(uintptr(w.hwnd), uintptr(unsafe.Pointer(&ps)))
	var full rect
	pGetClientRect.Call(uintptr(w.hwnd), uintptr(unsafe.Pointer(&full)))
	bar := w.barRect(full)
	rc := full
	if bar.h() > 0 {
		rc.Bottom = bar.Top
		w.paintBar(hdc, bar)
	}

	// a GDI só desenha o vídeo quando não há desenho na GPU (a janela filha
	// cobre a área) ou quando um aviso tirou a filha da frente
	w.mu.Lock()
	defer w.mu.Unlock()
	f, status := w.canvas.Frame(), w.status
	bg := w.app.bgBrush
	if w.rend != nil {
		f = nil
	}

	if f == nil {
		pFillRect.Call(hdc, uintptr(unsafe.Pointer(&rc)), bg)
		if w.rend == nil {
			w.dest = rect{}
		}
		w.drawCard(hdc, rc, status)
		return
	}

	d := fitDest(f.W, f.H, rect{0, 0, rc.w(), rc.h()}, w.actualSize)
	dx, dy, dw, dh := d.Left, d.Top, d.w(), d.h()
	w.dest = d

	// bordas pintadas à parte: o vídeo não pisca ao redimensionar
	for _, b := range []rect{{0, 0, rc.Right, dy}, {0, dy + dh, rc.Right, rc.Bottom}, {0, dy, dx, dy + dh}, {dx + dw, dy, rc.Right, dy + dh}} {
		b.Bottom = min(b.Bottom, rc.Bottom)
		if b.w() > 0 && b.h() > 0 {
			pFillRect.Call(hdc, uintptr(unsafe.Pointer(&b)), bg)
		}
	}
	// o vídeo não pinta por cima da barra (tamanho real maior que a janela)
	if bar.h() > 0 {
		pIntersectClipRect.Call(hdc, uintptr(rc.Left), uintptr(rc.Top), uintptr(rc.Right), uintptr(rc.Bottom))
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
		w.drawCard(hdc, rc, status)
	}
}

// drawCard mostra um aviso (aguardando, tela protegida, conexão encerrada)
// num cartão no centro da área de vídeo.
func (w *sessionWin) drawCard(hdc uintptr, area rect, text string) {
	if text == "" {
		return
	}
	px := w.app.px
	cw := min(textWidth(hdc, text, w.app.font)+px(48), area.w()-px(32))
	ch := px(56)
	x, y := area.Left+(area.w()-cw)/2, area.Top+(area.h()-ch)/2
	card := rect{x, y, x + cw, y + ch}
	fillRect(hdc, card, colorPanel)
	frame(hdc, card, colorLine)
	fillRect(hdc, rect{card.Left, card.Top, card.Left + px(3), card.Bottom}, colorAccent)
	drawString(hdc, card, text, w.app.font, colorText, dtCenter)
}

// paintBar desenha a barra de telemetria: os segmentos de ui.Telemetry e, à
// direita, por que o 4:4:4 pedido não está em uso.
func (w *sessionWin) paintBar(hdc uintptr, bar rect) {
	px := w.app.px
	fillRect(hdc, bar, colorPanel)
	fillRect(hdc, rect{bar.Left, bar.Top, bar.Right, bar.Top + 1}, colorLine)
	w.mu.Lock()
	st, drawn, has, local := w.stats, w.drawn, w.hasSt, w.localLocked()
	hint := ui.ColorHint(w.color444, w.can.h264, w.broken[protocol.CodecNameAVC444], w.preset, st.Encoder)
	w.mu.Unlock()
	x := bar.Left + px(14)
	if !has {
		drawString(hdc, rect{x, bar.Top, bar.Right, bar.Bottom}, "aguardando vídeo…", w.app.fontSmall, colorMuted, dtLeft)
		return
	}
	right := bar.Right - px(14)
	// cada segmento num espaço de largura fixa (o maior valor esperado): a
	// barra não muda de lugar quando os valores mudam
	for _, sg := range ui.Telemetry(st, drawn, local) {
		lw := textWidth(hdc, sg.Label, w.app.fontSmall)
		vw := max(textWidth(hdc, sg.Wide, w.app.fontMono), textWidth(hdc, sg.Value, w.app.fontMono))
		if x+lw+px(6)+vw > right {
			break // janela estreita: mostra o que couber, sempre a partir do começo
		}
		drawString(hdc, rect{x, bar.Top, x + lw, bar.Bottom}, sg.Label, w.app.fontSmall, colorMuted, dtLeft)
		x += lw + px(6)
		drawString(hdc, rect{x, bar.Top, x + vw, bar.Bottom}, sg.Value, w.app.fontMono, levelColor(sg.Level), dtLeft)
		x += vw + px(16)
	}
	// o motivo do 4:4:4 inativo vai no que sobrar à direita, sem empurrar nada
	if hint != "" && right-x > px(40) {
		drawString(hdc, rect{x, bar.Top, right, bar.Bottom}, hint, w.app.fontSmall, colorWarn, dtRight)
	}
}

// localLocked é o que este PC sabe da sessão, para a barra e o
// diagnóstico. Chame com w.mu travado.
func (w *sessionWin) localLocked() ui.Local {
	return ui.Local{GPUDecode: w.gpu, GPUDraw: w.gpuDraw, DecodeUs: w.decodeUs, Route: w.route}
}

// overBar informa se o ponto lp (coordenadas do cliente) está na barra.
func (w *sessionWin) overBar(lp uintptr) bool {
	var rc rect
	pGetClientRect.Call(uintptr(w.hwnd), uintptr(unsafe.Pointer(&rc)))
	b := w.barRect(rc)
	return b.h() > 0 && hiword(lp) >= b.Top
}

// norm converte a posição do mouse na janela em [0,1] da tela remota.
func (w *sessionWin) norm(lp uintptr) (float64, float64) {
	w.mu.Lock()
	d := w.dest
	w.mu.Unlock()
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
		w.layout() // sem a barra em tela cheia
		return
	}
	pSetWindowLongPtrW.Call(h, uintptr(gwlStyleIndex()), w.savedStyle)
	pSetMenu.Call(h, w.menu)
	r := w.saved
	pSetWindowPos.Call(h, 0, uintptr(r.Left), uintptr(r.Top), uintptr(r.w()), uintptr(r.h()), swpNoZOrder|swpFrameChanged)
	w.full = false
	w.layout()
}

// gwlStyleIndex é GWL_STYLE (-16) como o Win32 espera receber.
func gwlStyleIndex() int { return gwlStyle }
