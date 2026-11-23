//go:build windows && cgo

package h264

/*
#cgo LDFLAGS: -ld3d11 -ldxgi -lmfuuid -lole32 -loleaut32 -luuid -lgdi32 -luser32
#include "rcenc.h"
*/
import "C"

import (
	"errors"
	"time"
	"unsafe"

	"remoteit/internal/agent/media"
	"remoteit/internal/protocol"
)

type source struct{ c *C.rc_ctx }

// New abre captura + encoder. Devolve [media.ErrReopen] quando o desktop está
// em transição (UAC, tela de login) e vale tentar de novo em seguida.
func New(maxw, fps, kbps int) (media.Source, error) {
	var c *C.rc_ctx
	var buf [256]C.char
	r := C.rc_open(&c, C.int(maxw), C.int(fps), C.int(kbps), 1, &buf[0], C.int(len(buf)))
	if r == C.RC_REOPEN {
		return nil, media.ErrReopen
	}
	if r != 0 {
		return nil, errors.New(C.GoString(&buf[0]))
	}
	return &source{c}, nil
}

func (s *source) Next(timeout time.Duration, flags int) (*media.Frame, error) {
	var data *C.uint8_t
	var n, key C.int
	r := C.rc_frame(s.c, C.int(timeout/time.Millisecond), C.int(flags), &data, &n, &key)
	switch r {
	case C.RC_NOTHING:
		return nil, nil
	case C.RC_REOPEN:
		return nil, media.ErrReopen
	case C.RC_OK_FRAME:
		return &media.Frame{Codec: protocol.CodecH264, Keyframe: key != 0,
			W: int(C.rc_width(s.c)), H: int(C.rc_height(s.c)),
			Data: C.GoBytes(unsafe.Pointer(data), n)}, nil
	}
	return nil, errors.New("falha no encoder H.264")
}

func (s *source) SetBitrate(kbps int) { C.rc_set_bitrate(s.c, C.int(kbps)) }

// CursorPos implementa [media.CursorReporter].
func (s *source) CursorPos() (x, y int, visible bool) {
	var cx, cy, vis C.int
	C.rc_cursor_pos(s.c, &cx, &cy, &vis)
	return int(cx), int(cy), vis != 0
}

func (s *source) Name() string {
	n := C.GoString(C.rc_name(s.c))
	if C.rc_is_hw(s.c) != 0 {
		return "H.264 GPU: " + n
	}
	return "H.264 CPU: " + n
}

func (s *source) Close() { C.rc_close(s.c); s.c = nil }
