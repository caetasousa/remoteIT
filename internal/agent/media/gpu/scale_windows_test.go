//go:build windows && cgo

package gpu

import (
	"image"
	"testing"

	"remoteit/internal/scale"
)

// O shader de redução (rcenc.h, scale_ps) tem de dar o mesmo que a
// referência em Go (internal/scale.Area), a menos do arredondamento de float:
// no máximo 1 nível por canal. Roda no Windows ou no Wine (veja
// CONTRIBUTING.md, seção Testes); num PC sem GPU o Windows usa o WARP.
func TestScaleShaderMatchesReference(t *testing.T) {
	const sw, sh = 397, 223 // ímpar de propósito: fatores quebrados nas duas direções
	src := image.NewRGBA(image.Rect(0, 0, sw, sh))
	for y := range sh {
		for x := range sw {
			i := src.PixOffset(x, y)
			// texto fino: listras de 1 px e um degradê, o pior caso da redução
			v := uint8(0)
			if (x/1)%3 == 0 || y%7 == 0 {
				v = 255
			}
			src.Pix[i], src.Pix[i+1], src.Pix[i+2], src.Pix[i+3] = v, uint8(x*255/sw), uint8(y*255/sh), 255
		}
	}
	bgra := make([]byte, len(src.Pix))
	for i := 0; i < len(bgra); i += 4 {
		bgra[i], bgra[i+1], bgra[i+2], bgra[i+3] = src.Pix[i+2], src.Pix[i+1], src.Pix[i], 255
	}
	for _, sz := range [][2]int{{264, 148}, {198, 111}, {130, 74}} {
		dw, dh := sz[0], sz[1]
		got, err := scaleSelfTest(sw, sh, bgra, dw, dh)
		if err != nil {
			t.Fatal(err)
		}
		want := scale.Area(src, dw, dh)
		worst := 0
		for i := 0; i < len(got); i += 4 {
			w := want.Pix[i:]
			for c, g := range []byte{got[i+2], got[i+1], got[i]} { // BGRA -> RGB
				d := int(g) - int(w[c])
				worst = max(worst, d, -d)
			}
		}
		if worst > 1 {
			t.Fatalf("%dx%d -> %dx%d: diferença máxima %d (esperado <= 1)", sw, sh, dw, dh, worst)
		}
	}
}
