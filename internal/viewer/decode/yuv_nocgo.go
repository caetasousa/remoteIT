//go:build !cgo

package decode

import "remoteit/internal/avc444"

// Sem cgo (só builds de verificação: o visualizador sai sempre com cgo) a
// conversão usa as referências em Go, mais lentas.

func nv12ToBGRA(nv12 []byte, w, h int, dst []byte) {
	uv := nv12[w*h:]
	for y := range h {
		for x := range w {
			Y := 1192 * (int(nv12[y*w+x]) - 16)
			i := (y/2)*w + x&^1
			U, V := int(uv[i])-128, int(uv[i+1])-128
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
