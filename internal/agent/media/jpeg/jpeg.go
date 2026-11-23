// Package jpeg é a fonte de vídeo universal do agente: captura via
// [capture] e codifica cada quadro como JPEG em Go puro.
//
// É o fallback quando não há H.264 (build sem CGO, sem GPU, sem Media
// Foundation) ou quando o navegador não tem WebCodecs. Como GDI não avisa
// quando a tela muda, a fonte compara o hash da imagem e faz polling.
package jpeg

import (
	"bytes"
	"hash/fnv"
	"image"
	"image/jpeg"
	"time"

	"remoteit/internal/agent/media"
	"remoteit/internal/agent/media/capture"
	"remoteit/internal/protocol"
)

type source struct {
	cap     capture.Capturer
	maxw    int
	quality int
	last    uint64
	buf     bytes.Buffer
}

// New cria a fonte JPEG. maxw > 0 reduz a largura da imagem; kbps define a
// qualidade inicial (veja SetBitrate).
func New(maxw, kbps int) (media.Source, error) {
	c, err := capture.New()
	if err != nil {
		return nil, err
	}
	s := &source{cap: c, maxw: maxw}
	s.SetBitrate(kbps)
	return s, nil
}

func (s *source) Name() string { return "JPEG (CPU)" }
func (s *source) Close()       { s.cap.Close() }

// SetBitrate converte o alvo de bitrate em qualidade JPEG entre 25 e 85.
func (s *source) SetBitrate(kbps int) {
	s.quality = min(85, max(25, kbps/80))
}

func (s *source) Next(timeout time.Duration, flags int) (*media.Frame, error) {
	img, err := s.cap.Capture()
	if err != nil {
		time.Sleep(timeout)
		return nil, nil
	}
	if s.maxw > 0 && img.Bounds().Dx() > s.maxw {
		img = downscale(img, s.maxw)
	}
	h := fnv.New64a()
	h.Write(img.Pix)
	sum := h.Sum64()
	if sum == s.last && flags&media.FlagKey == 0 {
		time.Sleep(min(timeout, 30*time.Millisecond))
		return nil, nil
	}
	s.last = sum
	s.buf.Reset()
	if err := jpeg.Encode(&s.buf, img, &jpeg.Options{Quality: s.quality}); err != nil {
		return nil, err
	}
	b := img.Bounds()
	return &media.Frame{Codec: protocol.CodecJPEG, Keyframe: true, W: b.Dx(), H: b.Dy(), Data: bytes.Clone(s.buf.Bytes())}, nil
}

// downscale reduz por vizinho mais próximo: barato e suficiente para a tela.
func downscale(src *image.RGBA, w int) *image.RGBA {
	sw, sh := src.Bounds().Dx(), src.Bounds().Dy()
	h := sh * w / sw
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		sy := y * sh / h
		for x := range w {
			sx := x * sw / w
			si, di := sy*src.Stride+sx*4, y*dst.Stride+x*4
			copy(dst.Pix[di:di+4], src.Pix[si:si+4])
		}
	}
	return dst
}
