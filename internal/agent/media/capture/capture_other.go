//go:build !windows

package capture

import (
	"image"
	"image/color"
)

// synthetic gera um gradiente que muda a cada captura, para que o pipeline
// sempre tenha quadros novos a enviar.
type synthetic struct{ n int }

// New cria o capturador sintético de 640x360.
func New() (Capturer, error) { return &synthetic{}, nil }

func (s *synthetic) Close() {}

func (s *synthetic) Capture() (*image.RGBA, error) {
	s.n++
	img := image.NewRGBA(image.Rect(0, 0, 640, 360))
	for y := range 360 {
		for x := range 640 {
			img.SetRGBA(x, y, color.RGBA{uint8(x), uint8(y), uint8(s.n * 5), 255})
		}
	}
	return img, nil
}

// CursorShape devolve sempre "default" fora do Windows.
func CursorShape() string { return "default" }
