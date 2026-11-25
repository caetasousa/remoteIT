// Package avc444 define como uma imagem 4:4:4 vira duas imagens NV12 (4:2:0)
// e volta: é o truque que o RDP da Microsoft chama de AVC444.
//
// O H.264 dos encoders de GPU só aceita 4:2:0: brilho (Y) em resolução
// total, cor (U e V) em meia resolução nas duas direções. A cor descartada
// borra texto colorido. Em vez de um encoder 4:4:4 (que quase nenhuma GPU
// tem), o agente codifica DUAS imagens 4:2:0 comuns:
//
//   - Visão principal: Y inteiro + U e V das posições pares (x e y pares).
//     Sozinha, é um H.264 normal.
//   - Visão auxiliar: as amostras de cor que a principal deixou de fora.
//     Seu plano Y (W x H) leva as linhas ímpares de U (metade de cima) e de
//     V (metade de baixo); seu plano UV leva U e V das colunas ímpares das
//     linhas pares.
//
// Juntas, as duas têm exatamente W*H amostras de U e W*H de V: nada se perde
// além da compressão. O visualizador decodifica as duas e recompõe a cor de
// cada pixel. Este pacote é a referência: o shader do agente
// (internal/agent/media/gpu/rcenc.h) e a recomposição em C do visualizador
// (internal/viewer/decode/yuv.h) seguem este código, e os testes comparam
// os dois com ele.
//
// A cor usa BT.709 em faixa completa (0-255), não a faixa de vídeo (16-235)
// do H.264 comum: as duas pontas são nossas, então dá para aproveitar os
// 256 níveis e errar menos no arredondamento.
//
// Layout NV12 usado aqui (contíguo, sem padding): plano Y de W*H bytes, depois
// o plano UV intercalado (U, V, U, V...) de W bytes por linha e H/2 linhas.
// W e H precisam ser pares.
package avc444

import (
	"image"
	"math"
)

// Coeficientes BT.709.
const (
	kr = 0.2126
	kb = 0.0722
	kg = 1 - kr - kb
)

// ToYUV converte um pixel RGB (0-255) para Y, U, V em BT.709 faixa completa,
// sem arredondar. U e V ficam centrados em 128.
func ToYUV(r, g, b float64) (y, u, v float64) {
	y = kr*r + kg*g + kb*b
	u = (b-y)/(2*(1-kb)) + 128
	v = (r-y)/(2*(1-kr)) + 128
	return
}

func q(x float64) byte { return byte(math.Max(0, math.Min(255, math.Round(x)))) }

// Pack separa img em visão principal e auxiliar, como o shader do agente.
// img precisa ter largura e altura pares.
func Pack(img *image.RGBA) (main, aux []byte) {
	w, h := img.Rect.Dx(), img.Rect.Dy()
	main = make([]byte, w*h*3/2)
	aux = make([]byte, w*h*3/2)
	yuv := func(x, y int) (float64, float64, float64) {
		i := img.PixOffset(img.Rect.Min.X+x, img.Rect.Min.Y+y)
		p := img.Pix[i : i+3 : i+3]
		return ToYUV(float64(p[0]), float64(p[1]), float64(p[2]))
	}
	mainUV, auxUV := main[w*h:], aux[w*h:]
	for y := range h {
		for x := range w {
			Y, U, V := yuv(x, y)
			main[y*w+x] = q(Y)
			switch {
			case y%2 == 1: // linhas ímpares: U em cima, V embaixo no Y auxiliar
				aux[(y/2)*w+x] = q(U)
				aux[(h/2+y/2)*w+x] = q(V)
			case x%2 == 0: // par, par: cor da principal
				mainUV[(y/2)*w+x] = q(U)
				mainUV[(y/2)*w+x+1] = q(V)
			default: // linha par, coluna ímpar: UV auxiliar
				auxUV[(y/2)*w+x-1] = q(U)
				auxUV[(y/2)*w+x] = q(V)
			}
		}
	}
	return main, aux
}

// Combine recompõe a imagem BGRA (W*H*4 bytes em dst) a partir das duas
// visões decodificadas. Com aux nil, usa só a principal e repete a cor de
// cada bloco 2x2 (é o que se vê enquanto a auxiliar não chegou).
func Combine(main, aux []byte, w, h int, dst []byte) {
	mainUV := main[w*h:]
	var auxUV []byte
	if aux != nil {
		auxUV = aux[w*h:]
	}
	for y := range h {
		for x := range w {
			var U, V byte
			switch {
			case aux == nil:
				i := (y/2)*w + x&^1
				U, V = mainUV[i], mainUV[i+1]
			case y%2 == 1:
				U, V = aux[(y/2)*w+x], aux[(h/2+y/2)*w+x]
			case x%2 == 0:
				U, V = mainUV[(y/2)*w+x], mainUV[(y/2)*w+x+1]
			default:
				U, V = auxUV[(y/2)*w+x-1], auxUV[(y/2)*w+x]
			}
			r, g, b := ToRGB(main[y*w+x], U, V)
			o := (y*w + x) * 4
			dst[o], dst[o+1], dst[o+2], dst[o+3] = b, g, r, 255
		}
	}
}

// Coeficientes da volta em ponto fixo (x65536), os mesmos de yuv.h.
const (
	fVR = 103206 // 2(1-kr)          = 1.5748
	fUG = 12276  // 2 kb (1-kb) / kg = 0.187324
	fVG = 30679  // 2 kr (1-kr) / kg = 0.468124
	fUB = 121608 // 2(1-kb)          = 1.8556
)

// ToRGB converte Y, U, V (faixa completa) de volta para RGB, em ponto fixo.
func ToRGB(y, u, v byte) (r, g, b byte) {
	Y := int(y)<<16 + 1<<15
	U, V := int(u)-128, int(v)-128
	return clamp((Y + fVR*V) >> 16), clamp((Y - fUG*U - fVG*V) >> 16), clamp((Y + fUB*U) >> 16)
}

func clamp(v int) byte {
	switch {
	case v < 0:
		return 0
	case v > 255:
		return 255
	}
	return byte(v)
}
