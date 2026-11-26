//go:build windows

package win

import (
	"image"
	"log/slog"
	"os"
	"unsafe"

	"golang.org/x/sys/windows"

	"remoteit/internal/viewer/client"
	"remoteit/internal/viewer/render"
)

// videoClass é a janela filha onde o render desenha (veja openRenderer).
const videoClass = "RemoteITVideo"

// openRenderer cria a janela filha da área de vídeo e o desenho na GPU
// nela. Sem GPU que sirva (ou com REMOTEIT_VIEWER_GDI=1, para diagnóstico),
// fica o desenho pela CPU com GDI, como antes.
func (w *sessionWin) openRenderer() {
	if os.Getenv("REMOTEIT_VIEWER_GDI") == "1" {
		return
	}
	h, _, _ := pCreateWindowExW.Call(0, uintptr(unsafe.Pointer(utf16(videoClass))), 0,
		wsChild|wsClipSiblings, 0, 0, 1, 1, uintptr(w.hwnd), 0, 0, 0)
	if h == 0 {
		return
	}
	r, err := render.New(h, 1, 1)
	if err != nil {
		slog.Info("desenho na GPU indisponível; usando GDI", "err", err)
		pDestroyWindow.Call(h)
		return
	}
	w.video = windows.HWND(h)
	w.app.handlers[w.video] = w.videoProc
	w.mu.Lock()
	w.rend, w.gpuDraw = r, true
	w.mu.Unlock()
}

// useGDI desliga o desenho na GPU (falhou no meio da sessão): a janela
// filha some e a da sessão volta a desenhar com GDI. Thread da janela.
func (w *sessionWin) useGDI() {
	if w.video != 0 {
		delete(w.app.handlers, w.video)
		pDestroyWindow.Call(uintptr(w.video))
		w.video = 0
	}
	w.mu.Lock()
	r := w.rend
	w.rend, w.gpuDraw = nil, false
	w.mu.Unlock()
	if r != nil {
		r.Close()
	}
	pInvalidateRect.Call(uintptr(w.hwnd), 0, 0)
}

// videoProc é a janela filha: transparente para o mouse (os cliques vão
// para a janela da sessão, que já sabe mapear e mandar), e repinta o último
// quadro quando o Windows pede.
func (w *sessionWin) videoProc(hwnd windows.HWND, m uint32, wp, lp uintptr) (uintptr, bool) {
	switch m {
	case wmNcHitTest:
		return htTransparent, true
	case wmEraseBkgnd:
		return 1, true
	case wmSize:
		w.mu.Lock()
		r := w.rend
		w.mu.Unlock()
		if r != nil {
			r.Resize(int(loword(lp)), int(hiword(lp)))
			w.present()
		}
		return 0, true
	case wmPaint:
		pValidateRect.Call(uintptr(hwnd), 0)
		w.present()
		return 0, true
	}
	return 0, false
}

// layout recalcula a área de vídeo (o cliente menos a barra), ajusta a
// janela filha e agenda o pedido do tamanho novo ao agente. Thread da janela.
func (w *sessionWin) layout() {
	var rc rect
	pGetClientRect.Call(uintptr(w.hwnd), uintptr(unsafe.Pointer(&rc)))
	if b := w.barRect(rc); b.h() > 0 {
		rc.Bottom = b.Top
	}
	w.mu.Lock()
	w.area, w.actual = rc, w.actualSize
	status := w.status
	w.mu.Unlock()
	if w.video != 0 {
		pMoveWindow.Call(uintptr(w.video), 0, 0, uintptr(max(rc.w(), 1)), uintptr(max(rc.h(), 1)), 1)
		// o cartão de aviso (aguardando, tela protegida) é desenhado pela
		// janela da sessão: a filha sai da frente enquanto ele existe
		show(w.video, status == "")
	}
	pSetTimer.Call(uintptr(w.hwnd), idViewportTimer, viewportDelay, 0)
}

// viewport é o tamanho a pedir ao agente: a área de vídeo em pixels
// físicos no modo ajustado; 0 x 0 (nativa) no "Tamanho real".
func (w *sessionWin) viewport() (int, int) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.actual || w.area.w() <= 0 || w.area.h() <= 0 {
		return 0, 0
	}
	return int(w.area.w()), int(w.area.h())
}

func (w *sessionWin) sendViewport() {
	vw, vh := w.viewport()
	w.send(func(s *client.Session) { s.Viewport(vw, vh) })
}

// fitDest é onde um quadro fw x fh fica na área: ajustado à janela (sem
// distorcer) ou em tamanho real; com até 10% de sobra, fica em 1:1, que é
// mais nítido. Em tamanho real maior que a janela, ancora no canto.
func fitDest(fw, fh int, area rect, actual bool) rect {
	dw, dh := int32(fw), int32(fh)
	if !actual && area.w() > 0 && area.h() > 0 && fw > 0 && fh > 0 {
		s := min(float64(area.w())/float64(fw), float64(area.h())/float64(fh))
		if s < 1 || s > 1.1 {
			dw, dh = int32(float64(fw)*s), int32(float64(fh)*s)
		}
	}
	dx, dy := area.Left+(area.w()-dw)/2, area.Top+(area.h()-dh)/2
	if actual {
		dx, dy = max(dx, area.Left), max(dy, area.Top)
	}
	return rect{dx, dy, dx + dw, dy + dh}
}

// present mostra a composta do render na janela filha. Chamado pela
// goroutine de desenho a cada quadro e pela janela ao repintar.
func (w *sessionWin) present() {
	w.mu.Lock()
	r := w.rend
	if r == nil {
		w.mu.Unlock()
		return
	}
	fw, fh := w.frameW, w.frameH
	area := w.area
	d := fitDest(fw, fh, rect{0, 0, area.w(), area.h()}, w.actual)
	w.dest = rect{d.Left + area.Left, d.Top + area.Top, d.Right + area.Left, d.Bottom + area.Top}
	w.mu.Unlock()
	if fw == 0 {
		return
	}
	bg := uint32(colorBg)
	r.Present(image.Rect(int(d.Left), int(d.Top), int(d.Right), int(d.Bottom)), (bg&0xFF)<<16|bg&0xFF00|(bg>>16)&0xFF)
}
