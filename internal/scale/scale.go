// Package scale reduz imagens de tela para o tamanho em que o técnico as
// mostra.
//
// Fit decide o tamanho de saída a partir dos limites (a janela do técnico, o
// preset) e Area faz a redução pela média da área coberta, o filtro que
// preserva texto: sem o serrilhado do vizinho mais próximo e sem o halo dos
// filtros com lóbulos negativos. Area é a referência do shader do agente
// (internal/agent/media/gpu/rcenc.h): os testes comparam os dois, então
// mudanças aqui precisam ir para lá também.
package scale

import (
	"image"
	"math"
)

// MinW e MinH são o menor tamanho de saída que Fit devolve (se a tela for
// maior que isso): abaixo, o texto fica ilegível e não vale a pena reduzir.
const (
	MinW = 320
	MinH = 180
)

// Fit devolve o tamanho de saída de uma tela w x h que precisa caber em
// maxw x maxh (0 = sem limite naquela direção), mantendo a proporção. Nunca
// amplia e devolve dimensões pares (o NV12 exige). A mesma conta está em
// rc_fit, no agente: mude as duas juntas.
func Fit(w, h, maxw, maxh int) (int, int) {
	if maxw > 0 {
		maxw = max(maxw, MinW)
	}
	if maxh > 0 {
		maxh = max(maxh, MinH)
	}
	ow, oh := w, h
	if maxw > 0 && ow > maxw {
		ow, oh = maxw, h*maxw/w
	}
	if maxh > 0 && oh > maxh {
		ow, oh = w*maxh/h, maxh
	}
	return max(ow&^1, 2), max(oh&^1, 2)
}

// Area reduz src para w x h pela média da área: cada pixel de saída cobre um
// retângulo de (largura/w) x (altura/h) pixels de origem, e cada pixel de
// origem entra com o peso da fração coberta. Para w x h maior ou igual ao
// original, devolve uma cópia sem mudança de tamanho.
func Area(src *image.RGBA, w, h int) *image.RGBA {
	sb := src.Bounds()
	sw, sh := sb.Dx(), sb.Dy()
	if w >= sw && h >= sh {
		dst := image.NewRGBA(image.Rect(0, 0, sw, sh))
		for y := range sh {
			o := src.PixOffset(sb.Min.X, sb.Min.Y+y)
			copy(dst.Pix[y*dst.Stride:y*dst.Stride+sw*4], src.Pix[o:o+sw*4])
		}
		return dst
	}
	// separável: a média da área é o produto dos pesos em x e em y, então
	// reduzir as linhas e depois as colunas dá exatamente o mesmo resultado
	// que somar o retângulo inteiro (com acumulação em float64)
	cols := spans(sw, w)
	rows := spans(sh, h)
	tmp := make([]float64, sh*w*4)
	for y := range sh {
		line := src.Pix[src.PixOffset(sb.Min.X, sb.Min.Y+y):]
		for ox, sp := range cols {
			var acc [4]float64
			for _, t := range sp {
				p := line[t.i*4:]
				for c := range 4 {
					acc[c] += float64(p[c]) * t.w
				}
			}
			copy(tmp[(y*w+ox)*4:], acc[:])
		}
	}
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	for oy, sp := range rows {
		for ox := range w {
			var acc [4]float64
			for _, t := range sp {
				p := tmp[(t.i*w+ox)*4:]
				for c := range 4 {
					acc[c] += p[c] * t.w
				}
			}
			o := dst.Pix[oy*dst.Stride+ox*4:]
			for c := range 4 {
				o[c] = uint8(min(255, max(0, math.Round(acc[c]))))
			}
		}
	}
	return dst
}

// tap é um pixel de origem e o peso dele num pixel de saída.
type tap struct {
	i int
	w float64
}

// spans calcula, para cada um dos n pixels de saída de uma linha de s
// pixels de origem, quais pixels de origem ele cobre e com que peso. Os
// pesos de cada saída somam 1.
func spans(s, n int) [][]tap {
	out := make([][]tap, n)
	f := float64(s) / float64(n)
	for o := range n {
		x0, x1 := float64(o)*f, float64(o+1)*f
		for i := int(math.Floor(x0)); float64(i) < x1 && i < s; i++ {
			cov := math.Min(float64(i+1), x1) - math.Max(float64(i), x0)
			if cov > 1e-9 {
				out[o] = append(out[o], tap{i, cov / f})
			}
		}
	}
	return out
}
