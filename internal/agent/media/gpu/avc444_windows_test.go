//go:build windows && cgo

package gpu

import (
	"image"
	"math/rand/v2"
	"testing"

	"remoteit/internal/avc444"
)

// Os shaders do AVC444 (HLSL dentro de rcenc.h) têm de montar as mesmas
// visões que a referência em Go: é ela que o visualizador desfaz. A GPU
// arredonda em float32, então aceita 1 nível de diferença.
//
// Roda no Windows (GPU ou WARP) e no Wine; o caminho NV12 direto, que só
// existe com encoder de hardware, fica no checklist manual (docs/TESTES.md).
func TestAVC444ShadersMatchReference(t *testing.T) {
	const w, h = 64, 48
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	r := rand.New(rand.NewPCG(11, 3))
	for i := range img.Pix {
		img.Pix[i] = byte(r.IntN(256))
	}
	bgra := make([]byte, len(img.Pix))
	for i := 0; i < len(bgra); i += 4 {
		bgra[i], bgra[i+1], bgra[i+2], bgra[i+3] = img.Pix[i+2], img.Pix[i+1], img.Pix[i], 255
	}
	gotMain, gotAux, err := avc444SelfTest(w, h, bgra)
	if err != nil {
		t.Skipf("sem Direct3D 11 para rodar os shaders: %v", err)
	}
	wantMain, wantAux := avc444.Pack(img)
	for _, c := range []struct {
		name      string
		got, want []byte
	}{{"principal", gotMain, wantMain}, {"auxiliar", gotAux, wantAux}} {
		bad := 0
		for i := range c.want {
			if d := int(c.got[i]) - int(c.want[i]); d > 1 || d < -1 {
				if bad < 5 {
					t.Errorf("%s byte %d: shader %d, referência %d", c.name, i, c.got[i], c.want[i])
				}
				bad++
			}
		}
		if bad > 0 {
			t.Errorf("%s: %d bytes fora da tolerância", c.name, bad)
		}
	}
}
