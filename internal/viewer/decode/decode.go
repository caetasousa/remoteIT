// Package decode transforma os quadros do agente (H.264, JPEG, PNG) em
// imagens BGRA para a janela do visualizador nativo desenhar.
//
// H.264 usa o decodificador do Windows (Media Foundation, na GPU quando
// possível) e só existe no build Windows com cgo; JPEG e PNG usam a
// biblioteca padrão e funcionam em qualquer lugar.
package decode

import (
	"bytes"
	"errors"
	"image"
	"image/draw"
	"image/jpeg"
	"image/png"
)

// Frame é um quadro pronto para desenhar: BGRA, de cima para baixo, W*4 bytes
// por linha.
type Frame struct {
	W, H int
	BGRA []byte
}

// Video decodifica um fluxo de vídeo do agente (H.264, AV1 ou AVC444). Use
// de uma goroutine só: os decodificadores do Windows ficam presos à thread.
type Video interface {
	// Decode devolve um quadro quando sair um (nil, nil enquanto o
	// decodificador acumula). w e h são os do cabeçalho do protocolo.
	Decode(data []byte, w, h int) (*Frame, error)
	// GPU informa se a decodificação está na placa de vídeo.
	GPU() bool
	Close()
}

// ErrUnavailable significa que este build ou este PC não decodifica o codec
// pedido: o visualizador o deixa fora do hello e o agente escolhe outro (no
// fim, JPEG).
var ErrUnavailable = errors.New("decode: codec indisponível neste PC")

// nv12Decoder é um decodificador do sistema que devolve NV12 contíguo
// (plano Y de w*h, depois UV): o Media Foundation no Windows
// (mf_windows.go), falsos nos testes.
type nv12Decoder interface {
	// decodeNV12 devolve nil enquanto não sai quadro. O slice só vale até a
	// próxima chamada.
	decodeNV12(data []byte, w, h int) (nv12 []byte, ow, oh int, err error)
	gpu() bool
	close()
}

// Codecs que o sistema decodifica (veja openNV12).
const (
	mfH264 = 0
	mfAV1  = 1
)

// NewH264 abre o decodificador H.264 (veja ErrUnavailable).
func NewH264() (Video, error) { return newPlain(mfH264) }

// NewAV1 abre o decodificador AV1. No Windows ele vem da "Extensão de Vídeo
// AV1" da Microsoft Store; sem ela, devolve ErrUnavailable.
func NewAV1() (Video, error) { return newPlain(mfAV1) }

// NewAVC444 abre dois decodificadores H.264, um por visão, e recompõe a cor
// 4:4:4 (veja internal/avc444).
func NewAVC444() (Video, error) {
	m, err := openNV12(mfH264)
	if err != nil {
		return nil, err
	}
	a, err := openNV12(mfH264)
	if err != nil {
		m.close()
		return nil, err
	}
	return &avc444Video{main: m, aux: a}, nil
}

// Image decodifica um quadro JPEG ou PNG.
func Image(data []byte) (*Frame, error) {
	var img image.Image
	var err error
	if bytes.HasPrefix(data, []byte("\x89PNG")) {
		img, err = png.Decode(bytes.NewReader(data))
	} else {
		img, err = jpeg.Decode(bytes.NewReader(data))
	}
	if err != nil {
		return nil, err
	}
	b := img.Bounds()
	rgba, ok := img.(*image.RGBA)
	if !ok || b.Min != (image.Point{}) {
		rgba = image.NewRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
		draw.Draw(rgba, rgba.Bounds(), img, b.Min, draw.Src)
	}
	px := rgba.Pix
	out := make([]byte, len(px))
	for i := 0; i+3 < len(px); i += 4 { // RGBA -> BGRA
		out[i], out[i+1], out[i+2], out[i+3] = px[i+2], px[i+1], px[i], 255
	}
	return &Frame{W: b.Dx(), H: b.Dy(), BGRA: out}, nil
}
