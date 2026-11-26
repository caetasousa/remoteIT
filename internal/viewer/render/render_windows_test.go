//go:build windows && cgo

package render

import (
	"image"
	"testing"

	"remoteit/internal/scale"
	"remoteit/internal/viewer/decode"
)

// Roda no Windows ou no Wine (veja CONTRIBUTING.md, seção Testes): sem GPU,
// o Windows usa o rasterizador em software (WARP).

const tw, th = 96, 48

// planes gera um NV12 de w x h com valores variados (qualquer byte é um
// NV12 válido): degradês e listras, para pegar troca de plano e de canal.
func planes(w, h, seed int) []byte {
	b := make([]byte, w*h*3/2)
	for i := range w * h {
		x, y := i%w, i/w
		b[i] = byte((x*7 + y*13 + seed*31) % 256)
	}
	for i := w * h; i < len(b); i++ {
		j := i - w*h
		b[i] = byte((j*11 + seed*17) % 256)
	}
	return b
}

func maxDiff(t *testing.T, what string, got, want []byte, limit int) {
	t.Helper()
	worst, at := 0, 0
	for i := range want {
		if i%4 == 3 {
			continue // alfa
		}
		d := int(got[i]) - int(want[i])
		if d < 0 {
			d = -d
		}
		if d > worst {
			worst, at = d, i
		}
	}
	if worst > limit {
		t.Fatalf("%s: diferença máxima %d no byte %d (pixel %d), limite %d", what, worst, at, at/4, limit)
	}
}

func TestShadersMatchCPUConversion(t *testing.T) {
	main, aux := planes(tw, th, 1), planes(tw, th, 2)
	cases := []struct {
		name string
		kind Kind
		aux  []byte
		want []byte
		lim  int
	}{
		// o C usa ponto fixo com truncamento; o shader, float com
		// arredondamento: até 2 níveis na faixa limitada
		{"H.264 (NV12, 16-235)", NV12, nil, decode.NV12ToBGRA(main, tw, th), 2},
		{"AVC444 (4:4:4)", AVC444, aux, decode.AVC444ToBGRA(main, aux, tw, th), 1},
		{"AVC444 só a principal", AVC444Main, nil, decode.AVC444ToBGRA(main, nil, tw, th), 1},
	}
	for _, c := range cases {
		comp, out, err := selftest(c.kind, tw, th, main, c.aux, nil, 0, 0, 0, 0, nil, tw, th)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		maxDiff(t, c.name+": composta", comp, c.want, c.lim)
		maxDiff(t, c.name+": 1:1 na janela", out, comp, 0) // 1:1 é cópia exata
	}
}

// O retalho sem perda sobrevive fora da região do vídeo, e dentro dela o
// vídeo volta a valer.
func TestRegionKeepsPatch(t *testing.T) {
	main := planes(tw, th, 3)
	video := decode.NV12ToBGRA(main, tw, th)
	patch := make([]byte, 32*32*4)
	for i := range patch {
		patch[i] = 0xEE
	}
	region := image.Rect(0, 0, 48, 48) // cobre metade do retalho em (32,8)
	comp, _, err := selftest(NV12, tw, th, main, nil, patch, 32, 8, 32, 32, &region, tw, th)
	if err != nil {
		t.Fatal(err)
	}
	at := func(x, y int) []byte { return comp[(y*tw+x)*4 : (y*tw+x)*4+3] }
	vid := func(x, y int) []byte { return video[(y*tw+x)*4 : (y*tw+x)*4+3] }
	near := func(a, b []byte) bool {
		for i := range a {
			if d := int(a[i]) - int(b[i]); d > 2 || d < -2 {
				return false
			}
		}
		return true
	}
	if p := at(50, 20); p[0] != 0xEE || p[1] != 0xEE || p[2] != 0xEE {
		t.Fatalf("fora da região o retalho deveria ficar: %v", p)
	}
	if !near(at(40, 20), vid(40, 20)) {
		t.Fatalf("dentro da região deveria voltar o vídeo: %v x %v", at(40, 20), vid(40, 20))
	}
	if !near(at(80, 44), vid(80, 44)) {
		t.Fatal("fora do retalho e da região fica o vídeo do primeiro quadro")
	}
}

// Reduzir na janela usa a média da área (internal/scale.Area).
func TestPresentDownscaleMatchesArea(t *testing.T) {
	main := planes(tw, th, 4)
	comp, out, err := selftest(NV12, tw, th, main, nil, nil, 0, 0, 0, 0, nil, 62, 31)
	if err != nil {
		t.Fatal(err)
	}
	img := image.NewRGBA(image.Rect(0, 0, tw, th))
	for i := 0; i < len(comp); i += 4 { // BGRA -> RGBA
		img.Pix[i], img.Pix[i+1], img.Pix[i+2], img.Pix[i+3] = comp[i+2], comp[i+1], comp[i], 255
	}
	want := scale.Area(img, 62, 31)
	got := make([]byte, len(out))
	for i := 0; i < len(out); i += 4 {
		got[i], got[i+1], got[i+2], got[i+3] = out[i+2], out[i+1], out[i], 255
	}
	maxDiff(t, "redução 96x48 -> 62x31", got, want.Pix, 1)
}
