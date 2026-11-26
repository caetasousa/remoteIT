//go:build windows

package win

import (
	"strings"
	"unsafe"

	"remoteit/internal/viewer/ui"
)

// Auxiliares de desenho com GDI: o visual segue o console (fundo quase
// preto, ciano de destaque, verde/âmbar/rosa para estado).

// levelColor é a cor de cada tom de ui.Level.
func levelColor(l ui.Level) uintptr {
	switch l {
	case ui.Muted:
		return colorMuted
	case ui.Accent:
		return colorAccent
	case ui.Good:
		return colorGood
	case ui.Warn:
		return colorWarn
	case ui.Bad:
		return colorBad
	}
	return colorText
}

// fillRect pinta r com uma cor sólida.
func fillRect(hdc uintptr, r rect, color uintptr) {
	b, _, _ := pCreateSolidBrush.Call(color)
	pFillRect.Call(hdc, uintptr(unsafe.Pointer(&r)), b)
	pDeleteObject.Call(b)
}

// drawString escreve s em r, numa linha só, com reticências se não couber.
func drawString(hdc uintptr, r rect, s string, font, color uintptr, flags uintptr) {
	pSelectObject.Call(hdc, font)
	pSetBkMode.Call(hdc, transparent)
	pSetTextColor.Call(hdc, color)
	pDrawTextW.Call(hdc, uintptr(unsafe.Pointer(utf16(s))), ^uintptr(0), uintptr(unsafe.Pointer(&r)),
		flags|dtSingleLine|dtVCenter|dtNoPrefix|dtEndEllipsis)
}

// textWidth mede s na fonte dada (para pôr segmentos lado a lado).
func textWidth(hdc uintptr, s string, font uintptr) int32 {
	pSelectObject.Call(hdc, font)
	r := rect{0, 0, 10000, 100}
	pDrawTextW.Call(hdc, uintptr(unsafe.Pointer(utf16(s))), ^uintptr(0), uintptr(unsafe.Pointer(&r)), dtSingleLine|dtNoPrefix|0x0400) // DT_CALCRECT
	return r.w()
}

// dot desenha um círculo cheio de raio rad centrado em (x, y).
func dot(hdc uintptr, x, y, rad int32, color uintptr) {
	b, _, _ := pCreateSolidBrush.Call(color)
	pen, _, _ := pCreatePen.Call(0, 1, color)
	ob, _, _ := pSelectObject.Call(hdc, b)
	op, _, _ := pSelectObject.Call(hdc, pen)
	pEllipse.Call(hdc, uintptr(x-rad), uintptr(y-rad), uintptr(x+rad+1), uintptr(y+rad+1))
	pSelectObject.Call(hdc, ob)
	pSelectObject.Call(hdc, op)
	pDeleteObject.Call(b)
	pDeleteObject.Call(pen)
}

// frame desenha a borda de r com 1 px.
func frame(hdc uintptr, r rect, color uintptr) {
	fillRect(hdc, rect{r.Left, r.Top, r.Right, r.Top + 1}, color)
	fillRect(hdc, rect{r.Left, r.Bottom - 1, r.Right, r.Bottom}, color)
	fillRect(hdc, rect{r.Left, r.Top, r.Left + 1, r.Bottom}, color)
	fillRect(hdc, rect{r.Right - 1, r.Top, r.Right, r.Bottom}, color)
}

// drawSpaced é o drawString com espaço extra entre as letras: os rótulos
// em maiúsculas do console (letter-spacing no CSS).
func drawSpaced(hdc uintptr, r rect, s string, font, color uintptr, flags uintptr, extra int32) {
	pSetTextCharExtra.Call(hdc, uintptr(extra))
	drawString(hdc, r, s, font, color, flags)
	pSetTextCharExtra.Call(hdc, 0)
}

// drawButton desenha um botão nosso como os do console: o principal cheio
// de ciano, rótulo à esquerda e seta à direita; os outros só com contorno.
func (a *App) drawButton(d *drawItemStruct, text string, primary bool) {
	r := d.RcItem
	pressed := d.ItemState&odsSelected != 0
	disabled := d.ItemState&odsDisabled != 0
	focused := d.ItemState&odsFocus != 0
	label := strings.ToUpper(text)
	if primary {
		bg := uintptr(colorAccent)
		switch {
		case disabled:
			bg = colorAccentOff
		case pressed:
			bg = colorAccentDown
		}
		fillRect(d.HDC, r, bg)
		in := rect{r.Left + a.px(16), r.Top, r.Right - a.px(16), r.Bottom}
		drawSpaced(d.HDC, in, label, a.fontButton, colorBg, dtLeft, a.px(1))
		drawString(d.HDC, in, "→", a.fontBold, colorBg, dtRight)
		if focused && !disabled {
			frame(d.HDC, rect{r.Left + 2, r.Top + 2, r.Right - 2, r.Bottom - 2}, colorBg)
		}
		return
	}
	bg, fg, border := uintptr(colorBg), uintptr(colorText), uintptr(colorLineStrong)
	switch {
	case disabled:
		fg = colorDim
	case pressed:
		bg, fg = colorSel, colorStrong
	case focused:
		border = colorAccent
	}
	fillRect(d.HDC, r, bg)
	frame(d.HDC, r, border)
	drawSpaced(d.HDC, r, label, a.fontButton, fg, dtCenter, a.px(1))
}
