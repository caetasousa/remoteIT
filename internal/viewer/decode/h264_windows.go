//go:build windows && cgo

package decode

/*
#cgo LDFLAGS: -ld3d11 -ldxgi -lmfuuid -lole32 -luuid
#include "rcdec.h"
*/
import "C"

import (
	"errors"
	"unsafe"
)

type h264 struct {
	c *C.rd_ctx
}

func newH264() (H264, error) {
	var c *C.rd_ctx
	var buf [256]C.char
	if C.rd_open(&c, 1, &buf[0], C.int(len(buf))) != 0 {
		return nil, errors.Join(ErrNoH264, errors.New(C.GoString(&buf[0])))
	}
	return &h264{c: c}, nil
}

func (d *h264) Decode(data []byte, w, h int) (*Frame, error) {
	if len(data) == 0 {
		return nil, nil
	}
	var out *C.uint8_t
	var ow, oh C.int
	r := C.rd_decode(d.c, (*C.uint8_t)(unsafe.Pointer(&data[0])), C.int(len(data)), C.int(w), C.int(h), &out, &ow, &oh)
	switch r {
	case C.RD_FRAME:
		n := int(ow) * int(oh) * 4
		return &Frame{W: int(ow), H: int(oh), BGRA: C.GoBytes(unsafe.Pointer(out), C.int(n))}, nil
	case C.RD_NONE:
		return nil, nil
	}
	return nil, errors.New("decode: falha no decodificador H.264")
}

func (d *h264) GPU() bool { return C.rd_is_hw(d.c) != 0 }

func (d *h264) Close() { C.rd_close(d.c) }
