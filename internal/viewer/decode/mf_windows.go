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

// mfDecoder é um decodificador do Media Foundation (rcdec.h).
type mfDecoder struct{ c *C.rd_ctx }

func openNV12(codec int) (nv12Decoder, error) {
	var c *C.rd_ctx
	var buf [256]C.char
	if C.rd_open(&c, C.int(codec), 1, &buf[0], C.int(len(buf))) != 0 {
		return nil, errors.Join(ErrUnavailable, errors.New(C.GoString(&buf[0])))
	}
	return &mfDecoder{c: c}, nil
}

func (d *mfDecoder) decodeNV12(data []byte, w, h int) ([]byte, int, int, error) {
	var out *C.uint8_t
	var ow, oh C.int
	r := C.rd_decode(d.c, (*C.uint8_t)(unsafe.Pointer(&data[0])), C.int(len(data)), C.int(w), C.int(h), &out, &ow, &oh)
	switch r {
	case C.RD_FRAME:
		return unsafe.Slice((*byte)(unsafe.Pointer(out)), int(ow)*int(oh)*3/2), int(ow), int(oh), nil
	case C.RD_NONE:
		return nil, 0, 0, nil
	}
	return nil, 0, 0, errors.New("decode: falha no decodificador do Windows")
}

func (d *mfDecoder) gpu() bool { return C.rd_is_hw(d.c) != 0 }
func (d *mfDecoder) close()    { C.rd_close(d.c) }
