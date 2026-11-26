package scale

import (
	"image"
	"image/color"
	"testing"
)

func TestFit(t *testing.T) {
	for _, c := range []struct{ w, h, mw, mh, ow, oh int }{
		{1920, 1080, 0, 0, 1920, 1080},       // sem limite
		{1920, 1080, 2560, 1440, 1920, 1080}, // nunca amplia
		{3840, 2160, 1920, 1080, 1920, 1080}, // 4K numa janela 1080p
		{1920, 1080, 1280, 0, 1280, 720},     // preset Ultra-rápido
		{1920, 1080, 1600, 600, 1066, 600},   // janela larga e baixa: a altura manda
		{1920, 1080, 1281, 721, 1280, 720},   // sempre par
		{1920, 1080, 100, 50, 320, 180},      // janela minúscula: mínimo legível
		{2560, 1600, 1920, 1080, 1728, 1080},
	} {
		if ow, oh := Fit(c.w, c.h, c.mw, c.mh); ow != c.ow || oh != c.oh {
			t.Errorf("Fit(%d, %d, %d, %d) = %dx%d, quero %dx%d", c.w, c.h, c.mw, c.mh, ow, oh, c.ow, c.oh)
		}
	}
}

func fill(w, h int, f func(x, y int) color.RGBA) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			img.SetRGBA(x, y, f(x, y))
		}
	}
	return img
}

// Reduzir pela metade é a média de cada bloco 2x2.
func TestAreaHalfIsBlockAverage(t *testing.T) {
	src := fill(8, 4, func(x, y int) color.RGBA {
		return color.RGBA{uint8(x * 30), uint8(y * 60), uint8((x + y) * 10), 255}
	})
	dst := Area(src, 4, 2)
	for y := range 2 {
		for x := range 4 {
			var want [3]int
			for dy := range 2 {
				for dx := range 2 {
					p := src.RGBAAt(2*x+dx, 2*y+dy)
					want[0] += int(p.R)
					want[1] += int(p.G)
					want[2] += int(p.B)
				}
			}
			got := dst.RGBAAt(x, y)
			for c, g := range []uint8{got.R, got.G, got.B} {
				if w := (want[c] + 2) / 4; int(g) != w {
					t.Fatalf("pixel (%d,%d) canal %d = %d, quero %d", x, y, c, g, w)
				}
			}
		}
	}
}

// Uma cor lisa continua idêntica em qualquer fator (os pesos somam 1), e o
// branco e o preto não saem do lugar: texto preto no branco não fica cinza
// onde não há borda.
func TestAreaKeepsFlatColors(t *testing.T) {
	for _, c := range []color.RGBA{{255, 255, 255, 255}, {0, 0, 0, 255}, {12, 200, 77, 255}} {
		src := fill(1920, 1080, func(int, int) color.RGBA { return c })
		for _, sz := range [][2]int{{1280, 720}, {1366, 768}, {333, 187}} {
			dst := Area(src, sz[0], sz[1])
			for _, p := range [][2]int{{0, 0}, {sz[0] - 1, sz[1] - 1}, {sz[0] / 2, sz[1] / 3}} {
				if got := dst.RGBAAt(p[0], p[1]); got != c {
					t.Fatalf("%v reduzido para %v: pixel %v = %v", c, sz, p, got)
				}
			}
		}
	}
}

// Fator quebrado (1,5x): um pixel de saída pega 1 pixel inteiro e metade do
// vizinho, com os pesos certos.
func TestAreaFractionalWeights(t *testing.T) {
	// linha 0 1 2: valores 0, 90, 180 em R
	src := fill(3, 1, func(x, _ int) color.RGBA { return color.RGBA{uint8(x * 90), 0, 0, 255} })
	dst := Area(src, 2, 1)
	// saída 0 cobre [0, 1.5): 0 com peso 1/1.5 e 90 com 0.5/1.5 = 30
	// saída 1 cobre [1.5, 3): 90 com 0.5/1.5 e 180 com 1/1.5 = 150
	if a, b := dst.RGBAAt(0, 0).R, dst.RGBAAt(1, 0).R; a != 30 || b != 150 {
		t.Fatalf("pesos fracionários: %d %d, quero 30 150", a, b)
	}
}

func TestAreaNoUpscale(t *testing.T) {
	src := fill(10, 6, func(x, y int) color.RGBA { return color.RGBA{uint8(x), uint8(y), 0, 255} })
	dst := Area(src, 20, 12)
	if dst.Bounds().Dx() != 10 || dst.RGBAAt(9, 5) != src.RGBAAt(9, 5) {
		t.Fatalf("ampliou ou mudou a imagem: %v", dst.Bounds())
	}
}
