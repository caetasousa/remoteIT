package decode

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"testing"
)

func TestImageToBGRA(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, 4, 2))
	src.Set(0, 0, color.RGBA{R: 255, A: 255}) // vermelho puro
	src.Set(3, 1, color.RGBA{B: 255, A: 255}) // azul puro
	var pb bytes.Buffer
	png.Encode(&pb, src)
	f, err := Image(pb.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if f.W != 4 || f.H != 2 || len(f.BGRA) != 4*2*4 {
		t.Fatalf("quadro %dx%d, %d bytes", f.W, f.H, len(f.BGRA))
	}
	if r := f.BGRA[0:4]; r[0] != 0 || r[2] != 255 { // B, G, R, A
		t.Errorf("vermelho virou %v (BGRA)", r)
	}
	if b := f.BGRA[(1*4+3)*4:][:4]; b[0] != 255 || b[2] != 0 {
		t.Errorf("azul virou %v (BGRA)", b)
	}

	var jb bytes.Buffer
	jpeg.Encode(&jb, src, nil)
	if f, err := Image(jb.Bytes()); err != nil || f.W != 4 || f.H != 2 {
		t.Errorf("JPEG: %+v, %v", f, err)
	}
	if _, err := Image([]byte("lixo")); err == nil {
		t.Error("dados inválidos deveriam dar erro")
	}
}
