//go:build windows

package win

import (
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

// drawButton desenha um botão nosso: o principal cheio de ciano, os outros
// só com contorno.
func (a *App) drawButton(d *drawItemStruct, text string, primary bool) {
	r := d.RcItem
	pressed := d.ItemState&odsSelected != 0
	disabled := d.ItemState&odsDisabled != 0
	bg, fg, border := uintptr(colorPanel), uintptr(colorText), uintptr(colorLine)
	if primary {
		bg, fg, border = colorAccent, colorBg, colorAccent
		if pressed {
			bg = 0x00C0B400 // ciano mais escuro
		}
	} else if pressed {
		bg = colorSel
	}
	if disabled {
		bg, fg, border = colorPanel, colorMuted, colorLine
	}
	fillRect(d.HDC, r, bg)
	frame(d.HDC, r, border)
	drawString(d.HDC, r, text, a.fontBold, fg, dtCenter)
}
