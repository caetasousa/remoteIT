//go:build !cgo

package decode

import "remoteit/internal/avc444"

// Sem cgo (só builds de verificação: o visualizador sai sempre com cgo) a
// conversão usa as referências em Go, mais lentas.

func nv12ToBGRA(nv12 []byte, w, h int, dst []byte) {
	uv := nv12[w*h:]
	cw, ch := (w+1)/2, (h+1)/2
	// a mesma interpolação da cor de yuv.h (posição padrão do H.264)
	hc := func(j, x, c int) int {
		i := x >> 1
		if x&1 == 0 {
			return 2 * int(uv[j*w+2*i+c])
		}
		return int(uv[j*w+2*i+c]) + int(uv[j*w+2*min(i+1, cw-1)+c])
	}
	for y := range h {
		j, jn := y>>1, max(y>>1-1, 0)
		if y&1 == 1 {
			jn = min(j+1, ch-1)
		}
		for x := range w {
			Y := 1192 * (int(nv12[y*w+x]) - 16)
			U := (3*hc(j, x, 0)+hc(jn, x, 0)+4)>>3 - 128
			V := (3*hc(j, x, 1)+hc(jn, x, 1)+4)>>3 - 128
			o := (y*w + x) * 4
			dst[o] = clamp8((Y + 2163*U) >> 10)
			dst[o+1] = clamp8((Y - 218*U - 546*V) >> 10)
			dst[o+2] = clamp8((Y + 1836*V) >> 10)
			dst[o+3] = 255
		}
	}
}

func clamp8(v int) byte { return byte(min(max(v, 0), 255)) }

func avc444ToBGRA(main, aux []byte, w, h int, dst []byte) { avc444.Combine(main, aux, w, h, dst) }
