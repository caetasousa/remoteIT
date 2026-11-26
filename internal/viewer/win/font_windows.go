//go:build windows

package win

import (
	_ "embed"
	"unsafe"
)

// A fonte do console web (JetBrains Mono) vai dentro do .exe, só com os
// caracteres que o visualizador usa (latim, pontuação, setas): o técnico vê
// a mesma letra no navegador e aqui, sem instalar nada. Licença OFL 1.1 em
// fonts/OFL.txt.
var (
	//go:embed fonts/JetBrainsMono-Regular.ttf
	fontRegular []byte
	//go:embed fonts/JetBrainsMono-SemiBold.ttf
	fontSemiBold []byte
)

var pAddFontMemResourceEx = gdi32.NewProc("AddFontMemResourceEx")

// Nomes de família no GDI: a SemiBold é uma família à parte.
const (
	faceMono     = "JetBrains Mono"
	faceMonoBold = "JetBrains Mono SemiBold"
	faceFallback = "Consolas" // se o Windows recusar a fonte embutida
)

// loadFonts registra as fontes embutidas só para este processo. Devolve
// false se alguma não carregou (aí usa-se a Consolas).
func loadFonts() bool {
	ok := true
	for _, f := range [][]byte{fontRegular, fontSemiBold} {
		var n uint32
		h, _, _ := pAddFontMemResourceEx.Call(uintptr(unsafe.Pointer(&f[0])), uintptr(len(f)), 0, uintptr(unsafe.Pointer(&n)))
		if h == 0 || n == 0 {
			ok = false
		}
	}
	return ok
}
