// Package render desenha a sessão do visualizador nativo na GPU (Direct3D
// 11): composição das regiões de vídeo com os retalhos sem perda, conversão
// de cor em shader e o ajuste à janela. Veja rcrend.h.
//
// Fora do Windows (ou sem cgo), New falha e o visualizador desenha pela CPU
// (GDI), como antes.
package render

import "errors"

// Kind é como o vídeo das visões NV12 vira cor.
type Kind int

// Tipos de composição.
const (
	NV12       Kind = 0 // H.264 ou AV1: uma visão, BT.709 16-235
	AVC444     Kind = 1 // as duas visões do AVC444 (cor 4:4:4), BT.709 0-255
	AVC444Main Kind = 2 // só a principal do AVC444, enquanto a auxiliar não chega
)

// ErrUnavailable indica que o desenho na GPU não existe neste build ou PC.
var ErrUnavailable = errors.New("render: desenho na GPU indisponível")
