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

// H264 decodifica H.264 Annex-B. Use de uma goroutine só.
type H264 interface {
	// Decode devolve um quadro quando sair um (nil, nil enquanto o
	// decodificador acumula). w e h são os do cabeçalho do protocolo.
	Decode(data []byte, w, h int) (*Frame, error)
	// GPU informa se a decodificação está na placa de vídeo.
	GPU() bool
	Close()
}

// ErrNoH264 significa que este build ou este PC não decodifica H.264: o
// visualizador avisa o agente (hello h264=false) e recebe JPEG.
var ErrNoH264 = errors.New("decode: H.264 indisponível")

// NewH264 abre o decodificador H.264 (veja ErrNoH264).
func NewH264() (H264, error) { return newH264() }

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
