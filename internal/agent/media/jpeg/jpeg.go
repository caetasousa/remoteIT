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
	"image/jpeg"
	"time"

	"remoteit/internal/agent/media"
	"remoteit/internal/agent/media/capture"
	"remoteit/internal/protocol"
	"remoteit/internal/scale"
)

type source struct {
	cap        capture.Capturer
	maxw, maxh int
	quality    int
	last       uint64
	buf        bytes.Buffer
}

// New abre a captura de tela com saída JPEG, reduzida para caber em
// p.MaxW x p.MaxH (internal/scale).
func New(p media.Params) (media.Source, error) {
	c, err := capture.New()
	if err != nil {
		return nil, err
	}
	s := &source{cap: c, maxw: p.MaxW, maxh: p.MaxH}
	s.SetBitrate(p.Kbps)
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
	b := img.Bounds()
	if w, h := scale.Fit(b.Dx(), b.Dy(), s.maxw, s.maxh); w != b.Dx() || h != b.Dy() {
		img = scale.Area(img, w, h)
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
	b = img.Bounds()
	return &media.Frame{Codec: protocol.CodecJPEG, Keyframe: true, W: b.Dx(), H: b.Dy(), Data: bytes.Clone(s.buf.Bytes())}, nil
}
