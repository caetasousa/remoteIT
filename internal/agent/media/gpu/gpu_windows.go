//go:build windows && cgo

package gpu

/*
#cgo LDFLAGS: -ld3d11 -ldxgi -lmfuuid -lole32 -loleaut32 -luuid -lgdi32 -luser32 -lwinmm
#include "rcenc.h"
*/
import "C"

import (
	"errors"
	"image"
	"time"
	"unsafe"

	"remoteit/internal/agent/media"
	"remoteit/internal/protocol"
)

type source struct {
	c      *C.rc_ctx
	mode   C.int
	seqhdr []byte // AV1: cabeçalho de sequência para repor nos keyframes
}

// NewH264 abre captura + encoder H.264. Devolve [media.ErrReopen] quando o
// desktop está em transição (UAC, tela de login) e vale tentar de novo.
func NewH264(p media.Params) (media.Source, error) { return open(C.RC_H264, p) }

// NewAVC444 abre captura + dois encoders H.264 de GPU (cor 4:4:4).
func NewAVC444(p media.Params) (media.Source, error) { return open(C.RC_AVC444, p) }

// NewAV1 abre captura + encoder AV1 de GPU.
func NewAV1(p media.Params) (media.Source, error) { return open(C.RC_AV1, p) }

func open(mode C.int, p media.Params) (media.Source, error) {
	var c *C.rc_ctx
	var buf [256]C.char
	r := C.rc_open(&c, mode, C.int(p.MaxW), C.int(p.MaxH), C.int(p.FPS), C.int(p.Kbps), 1, &buf[0], C.int(len(buf)))
	if r == C.RC_REOPEN {
		return nil, media.ErrReopen
	}
	if r != 0 {
		return nil, errors.New(C.GoString(&buf[0]))
	}
	s := &source{c: c, mode: mode}
	if mode == C.RC_AV1 {
		var p *C.uint8_t
		var n C.int
		C.rc_seqhdr(c, &p, &n)
		if n > 0 {
			s.seqhdr = SequenceHeader(C.GoBytes(unsafe.Pointer(p), n))
		}
	}
	return s, nil
}

func (s *source) output(i int) ([]byte, bool) {
	var p *C.uint8_t
	var n, key C.int
	C.rc_output(s.c, C.int(i), &p, &n, &key)
	if n == 0 {
		return nil, false
	}
	return C.GoBytes(unsafe.Pointer(p), n), key != 0
}

func (s *source) Next(timeout time.Duration, flags int) (*media.Frame, error) {
	switch C.rc_frame(s.c, C.int(timeout/time.Millisecond), C.int(flags)) {
	case C.RC_NOTHING:
		return nil, nil
	case C.RC_REOPEN:
		return nil, media.ErrReopen
	case C.RC_OK_FRAME:
	default:
		return nil, errors.New("falha no encoder de GPU")
	}
	f := &media.Frame{W: int(C.rc_width(s.c)), H: int(C.rc_height(s.c))}
	main, key := s.output(0)
	switch s.mode {
	case C.RC_AVC444:
		aux, auxKey := s.output(1)
		f.Codec, f.Keyframe = protocol.CodecAVC444, key && auxKey
		f.Data = protocol.AppendAVC444(nil, key, auxKey, main, aux)
	case C.RC_AV1:
		if main == nil {
			return nil, nil
		}
		f.Codec, f.Keyframe = protocol.CodecAV1, key
		if key {
			f.Data = WithSequenceHeader(main, s.seqhdr)
		} else {
			f.Data = main
		}
	default:
		if main == nil {
			return nil, nil
		}
		f.Codec, f.Keyframe, f.Data = protocol.CodecH264, key, main
	}
	return f, nil
}

func (s *source) SetBitrate(kbps int) { C.rc_set_bitrate(s.c, C.int(kbps)) }

// CursorPos implementa [media.CursorReporter].
func (s *source) CursorPos() (x, y int, visible bool) {
	var cx, cy, vis C.int
	C.rc_cursor_pos(s.c, &cx, &cy, &vis)
	return int(cx), int(cy), vis != 0
}

func (s *source) Name() string {
	codec := map[C.int]string{C.RC_H264: "H.264", C.RC_AVC444: "H.264 4:4:4", C.RC_AV1: "AV1"}[s.mode]
	where := " CPU: "
	if C.rc_is_hw(s.c) != 0 {
		where = " GPU: "
	}
	return codec + where + C.GoString(C.rc_name(s.c))
}

func (s *source) Close() { C.rc_close(s.c); s.c = nil }

// Tiles implementa [media.TileSource].
func (s *source) Tiles() (cols, rows, size int) {
	var c, r, z C.int
	C.rc_tiles(s.c, &c, &r, &z)
	return int(c), int(r), int(z)
}

// Dirty implementa [media.TileSource].
func (s *source) Dirty(dst []bool) {
	cols, rows, _ := s.Tiles()
	if len(dst) < cols*rows {
		return
	}
	tmp := make([]byte, cols*rows)
	C.rc_dirty(s.c, (*C.uint8_t)(unsafe.Pointer(&tmp[0])), C.int(len(tmp)))
	for i, b := range tmp {
		dst[i] = dst[i] || b != 0
	}
}

// ReadStill implementa [media.StillReader]: lê da GPU a tela já no tamanho
// do vídeo.
func (s *source) ReadStill(r image.Rectangle) (*image.RGBA, error) {
	img := image.NewRGBA(image.Rect(0, 0, r.Dx(), r.Dy()))
	if r.Empty() || C.rc_still(s.c, C.int(r.Min.X), C.int(r.Min.Y), C.int(r.Dx()), C.int(r.Dy()),
		(*C.uint8_t)(unsafe.Pointer(&img.Pix[0]))) != 0 {
		return nil, errors.New("leitura da tela na GPU indisponível")
	}
	return img, nil
}

// EncodeTime implementa [media.EncodeTimer].
func (s *source) EncodeTime() time.Duration {
	return time.Duration(C.rc_encode_us(s.c)) * time.Microsecond
}

// SetSpeed implementa [media.SpeedTuner].
func (s *source) SetSpeed(level int) bool { return C.rc_set_speed(s.c, C.int(level)) != 0 }

// Native implementa [media.NativeSizer].
func (s *source) Native() (w, h int) {
	var cw, ch C.int
	C.rc_native(s.c, &cw, &ch)
	return int(cw), int(ch)
}

// scaleSelfTest reduz bgra (sw x sh) para dw x dh com o shader de redução e
// devolve o resultado em BGRA. Só para o teste.
func scaleSelfTest(sw, sh int, bgra []byte, dw, dh int) ([]byte, error) {
	out := make([]byte, dw*dh*4)
	var buf [512]C.char
	if C.rc_scale_selftest(C.int(sw), C.int(sh), (*C.uint8_t)(unsafe.Pointer(&bgra[0])), C.int(dw), C.int(dh),
		(*C.uint8_t)(unsafe.Pointer(&out[0])), &buf[0], C.int(len(buf))) != 0 {
		return nil, errors.New(C.GoString(&buf[0]))
	}
	return out, nil
}

// avc444SelfTest roda os shaders do AVC444 sobre bgra (w x h) e devolve as
// visões principal e auxiliar em NV12 contíguo. Só para o teste.
func avc444SelfTest(w, h int, bgra []byte) (main, aux []byte, err error) {
	out := make([]byte, w*h*3)
	var buf [512]C.char
	if C.rc_avc444_selftest(C.int(w), C.int(h), (*C.uint8_t)(unsafe.Pointer(&bgra[0])),
		(*C.uint8_t)(unsafe.Pointer(&out[0])), &buf[0], C.int(len(buf))) != 0 {
		return nil, nil, errors.New(C.GoString(&buf[0]))
	}
	return out[:w*h*3/2], out[w*h*3/2:], nil
}
