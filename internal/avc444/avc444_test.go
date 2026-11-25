package avc444

import (
	"image"
	"math/rand/v2"
	"testing"
)

func randomImage(w, h int, seed uint64) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	r := rand.New(rand.NewPCG(seed, 1))
	for i := range img.Pix {
		img.Pix[i] = byte(r.IntN(256))
	}
	return img
}

// maxErr é a maior diferença por canal entre img e o BGRA recomposto.
func maxErr(img *image.RGBA, bgra []byte) int {
	worst := 0
	for i := 0; i < len(bgra); i += 4 {
		for c, want := range []byte{img.Pix[i+2], img.Pix[i+1], img.Pix[i]} {
			d := int(bgra[i+c]) - int(want)
			worst = max(worst, d, -d)
		}
	}
	return worst
}

// Sem compressão, ida e volta só perde o arredondamento para 8 bits: ruído
// aleatório (o pior caso para cor: cada pixel tem uma cor diferente do
// vizinho) volta com no máximo 2 níveis de erro por canal.
func TestPackCombineIsNearlyLossless(t *testing.T) {
	img := randomImage(64, 48, 7)
	main, aux := Pack(img)
	out := make([]byte, 64*48*4)
	Combine(main, aux, 64, 48, out)
	if e := maxErr(img, out); e > 2 {
		t.Fatalf("erro máximo %d, want <= 2", e)
	}
}

// Só com a principal, a cor de cada bloco 2x2 é a do pixel par: em ruído o
// erro é grande. É isso que a visão auxiliar corrige.
func TestMainOnlyLosesColor(t *testing.T) {
	img := randomImage(64, 48, 9)
	main, _ := Pack(img)
	out := make([]byte, 64*48*4)
	Combine(main, nil, 64, 48, out)
	if e := maxErr(img, out); e < 50 {
		t.Fatalf("erro máximo %d: a principal sozinha deveria perder cor", e)
	}
}

// O brilho da principal é o mesmo nos dois modos: um visualizador que só
// entende H.264 comum vê a imagem certa, só com a cor em meia resolução.
func TestMainLumaMatchesRGB(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	for i := range 4 {
		copy(img.Pix[i*4:], []byte{255, 255, 255, 255})
	}
	main, aux := Pack(img)
	for i := range 4 {
		if main[i] != 255 {
			t.Fatalf("Y branco = %d", main[i])
		}
	}
	// branco não tem cor: U e V no centro em todas as amostras
	for _, b := range append(main[4:], aux[4:]...) {
		if b != 128 {
			t.Fatalf("cor do branco = %d, want 128", b)
		}
	}
}

func BenchmarkCombine1080p(b *testing.B) {
	img := randomImage(1920, 1080, 1)
	main, aux := Pack(img)
	out := make([]byte, 1920*1080*4)
	for b.Loop() {
		Combine(main, aux, 1920, 1080, out)
	}
}
