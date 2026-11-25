package decode

import (
	"bytes"
	"errors"
	"image"
	"math/rand/v2"
	"testing"

	"remoteit/internal/avc444"
	"remoteit/internal/protocol"
)

func noise(w, h int, seed uint64) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	r := rand.New(rand.NewPCG(seed, 2))
	for i := range img.Pix {
		img.Pix[i] = byte(r.IntN(256))
	}
	return img
}

// A recomposição em C (yuv.h) tem de dar exatamente o mesmo resultado que a
// referência em Go, com e sem a visão auxiliar.
func TestAVC444MatchesReference(t *testing.T) {
	const w, h = 66, 34
	main, aux := avc444.Pack(noise(w, h, 3))
	for _, a := range [][]byte{aux, nil} {
		want := make([]byte, w*h*4)
		got := make([]byte, w*h*4)
		avc444.Combine(main, a, w, h, want)
		avc444ToBGRA(main, a, w, h, got)
		if !bytes.Equal(got, want) {
			t.Fatalf("aux=%v: C difere da referência Go", a != nil)
		}
	}
}

// Cinza médio em faixa de vídeo (Y=126, U=V=128) vira RGB (128,128,128).
func TestNV12ToBGRAGray(t *testing.T) {
	nv := append(bytes.Repeat([]byte{126}, 4), 128, 128, 128, 128)
	out := make([]byte, 2*2*4)
	nv12ToBGRA(nv, 2, 2, out)
	for i := 0; i < len(out); i += 4 {
		if out[i] < 127 || out[i] > 129 || out[i+1] != out[i] || out[i+2] != out[i] || out[i+3] != 255 {
			t.Fatalf("pixel %d = %v", i/4, out[i:i+4])
		}
	}
}

// fakeNV12 "decodifica" devolvendo o próprio payload como NV12.
type fakeNV12 struct {
	w, h  int
	fail  bool
	calls int
}

func (f *fakeNV12) decodeNV12(data []byte, w, h int) ([]byte, int, int, error) {
	f.calls++
	if f.fail {
		return nil, 0, 0, errors.New("perdido")
	}
	return data, f.w, f.h, nil
}
func (f *fakeNV12) gpu() bool { return true }
func (f *fakeNV12) close()    {}

// Um quadro pode trazer só uma das visões: a última de cada uma é guardada.
// Antes da auxiliar chegar, a imagem usa só a principal (cor em blocos 2x2);
// depois, sai a cor completa.
func TestAVC444VideoPairsLatestViews(t *testing.T) {
	const w, h = 8, 6
	img := noise(w, h, 5)
	main, aux := avc444.Pack(img)
	v := &avc444Video{main: &fakeNV12{w: w, h: h}, aux: &fakeNV12{w: w, h: h}}

	f, err := v.Decode(protocol.AppendAVC444(nil, true, false, main, nil), w, h)
	if err != nil || f == nil {
		t.Fatalf("só principal: %v %v", f, err)
	}
	onlyMain := make([]byte, w*h*4)
	avc444.Combine(main, nil, w, h, onlyMain)
	if !bytes.Equal(f.BGRA, onlyMain) {
		t.Fatal("sem auxiliar, deveria usar só a principal")
	}

	f, err = v.Decode(protocol.AppendAVC444(nil, false, true, nil, aux), w, h)
	if err != nil || f == nil {
		t.Fatalf("só auxiliar: %v %v", f, err)
	}
	full := make([]byte, w*h*4)
	avc444.Combine(main, aux, w, h, full)
	if !bytes.Equal(f.BGRA, full) {
		t.Fatal("com a auxiliar guardada, deveria sair a cor completa")
	}
}

func TestAVC444VideoErrors(t *testing.T) {
	v := &avc444Video{main: &fakeNV12{w: 2, h: 2}, aux: &fakeNV12{w: 2, h: 2, fail: true}}
	if _, err := v.Decode([]byte{1, 2}, 2, 2); err == nil {
		t.Error("payload truncado deveria dar erro")
	}
	if _, err := v.Decode(protocol.AppendAVC444(nil, false, true, nil, make([]byte, 6)), 2, 2); err == nil {
		t.Error("falha na auxiliar deveria dar erro (o visualizador recomeça de um keyframe)")
	}
	// auxiliar antes de qualquer principal: nada para desenhar ainda
	v = &avc444Video{main: &fakeNV12{w: 2, h: 2}, aux: &fakeNV12{w: 2, h: 2}}
	if f, err := v.Decode(protocol.AppendAVC444(nil, false, true, nil, make([]byte, 6)), 2, 2); f != nil || err != nil {
		t.Errorf("sem principal: %v %v", f, err)
	}
}

func BenchmarkAVC444ToBGRA1080p(b *testing.B) {
	main, aux := avc444.Pack(noise(1920, 1080, 1))
	out := make([]byte, 1920*1080*4)
	for b.Loop() {
		avc444ToBGRA(main, aux, 1920, 1080, out)
	}
}
