package decode

import (
	"bytes"
	"image"
	"testing"
)

func solid(w, h int, v byte) *Frame {
	return &Frame{W: w, H: h, BGRA: bytes.Repeat([]byte{v, v, v, 255}, w*h)}
}

func px(c *Canvas, x, y int) byte { return c.BGRA[(y*c.W+x)*4] }

// O vídeo só entra nas regiões que mudaram: o retalho sem perda de fora
// delas sobrevive aos quadros seguintes.
func TestCanvasKeepsPatchOutsideRegion(t *testing.T) {
	var c Canvas
	c.Video(solid(8, 4, 10), nil) // primeiro quadro: inteiro
	c.Patch(4, 0, solid(4, 4, 200), 8, 4)
	c.Video(solid(8, 4, 50), []image.Rectangle{image.Rect(0, 0, 4, 4)})
	if px(&c, 0, 0) != 50 || px(&c, 3, 3) != 50 {
		t.Fatal("dentro da região deveria vir o vídeo novo")
	}
	if px(&c, 4, 0) != 200 || px(&c, 7, 3) != 200 {
		t.Fatal("fora da região o retalho sem perda deveria ficar")
	}
	c.Video(solid(8, 4, 60), []image.Rectangle{}) // região vazia: nada muda
	if px(&c, 0, 0) != 50 || px(&c, 7, 3) != 200 {
		t.Fatal("região vazia não deveria mexer na tela")
	}
	c.Video(solid(8, 4, 70), nil) // sem região: o quadro inteiro
	if px(&c, 7, 3) != 70 {
		t.Fatal("sem Region o vídeo vale na tela inteira")
	}
}

func TestCanvasSizeChanges(t *testing.T) {
	var c Canvas
	c.Video(solid(8, 4, 10), nil)
	c.Video(solid(4, 2, 90), []image.Rectangle{image.Rect(0, 0, 1, 1)})
	if c.W != 4 || c.H != 2 || px(&c, 3, 1) != 90 {
		t.Fatal("tamanho novo: o quadro entra inteiro, mesmo com região")
	}
	c.Patch(0, 0, solid(2, 2, 5), 8, 4) // retalho de uma tela que já mudou
	if px(&c, 0, 0) != 90 {
		t.Fatal("retalho de outro tamanho de quadro deveria ficar de fora")
	}
	var d Canvas
	d.Patch(0, 0, solid(4, 2, 33), 4, 2) // refresh inteiro antes de qualquer vídeo
	if d.Frame() == nil || px(&d, 3, 1) != 33 {
		t.Fatal("o refresh do quadro inteiro vale como primeira imagem")
	}
}
