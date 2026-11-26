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

	"remoteit/internal/protocol"
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

// texDecoder é um decodificador do Windows no modo textura.
type texDecoder struct{ c *C.rd_ctx }

func openTex(codec int, dev unsafe.Pointer) (*texDecoder, error) {
	var c *C.rd_ctx
	var buf [256]C.char
	if C.rd_open_dev(&c, C.int(codec), (*C.ID3D11Device)(dev), &buf[0], C.int(len(buf))) != 0 {
		return nil, errors.Join(ErrUnavailable, errors.New(C.GoString(&buf[0])))
	}
	return &texDecoder{c: c}, nil
}

func (d *texDecoder) decode(data []byte, w, h int, tex unsafe.Pointer) (bool, error) {
	if len(data) == 0 || tex == nil {
		return false, nil
	}
	var ow, oh C.int
	switch C.rd_decode_tex(d.c, (*C.uint8_t)(unsafe.Pointer(&data[0])), C.int(len(data)), C.int(w), C.int(h),
		(*C.ID3D11Texture2D)(tex), &ow, &oh) {
	case C.RD_FRAME:
		return true, nil
	case C.RD_NONE:
		return false, nil
	}
	return false, errors.New("decode: falha no decodificador do Windows")
}

func (d *texDecoder) close() { C.rd_close(d.c) }

// NewTex abre o decodificador do codec (protocol.Codec*) que entrega os
// quadros no dispositivo dev do desenho.
func NewTex(codec byte, dev unsafe.Pointer) (TexVideo, error) {
	switch codec {
	case protocol.CodecH264, protocol.CodecAV1:
		mf := mfH264
		if codec == protocol.CodecAV1 {
			mf = mfAV1
		}
		d, err := openTex(mf, dev)
		if err != nil {
			return nil, err
		}
		return &texPlain{d: d}, nil
	case protocol.CodecAVC444:
		m, err := openTex(mfH264, dev)
		if err != nil {
			return nil, err
		}
		a, err := openTex(mfH264, dev)
		if err != nil {
			m.close()
			return nil, err
		}
		return &texAVC444{main: m, aux: a}, nil
	}
	return nil, ErrUnavailable
}

type texPlain struct{ d *texDecoder }

func (v *texPlain) Decode(data []byte, w, h int, target func(v, w, h int) unsafe.Pointer) (int, error) {
	ok, err := v.d.decode(data, w, h, target(0, w&^1, h&^1))
	if ok {
		return 1, err
	}
	return 0, err
}
func (v *texPlain) GPU() bool { return C.rd_is_hw(v.d.c) != 0 }
func (v *texPlain) Close()    { v.d.close() }

type texAVC444 struct{ main, aux *texDecoder }

func (v *texAVC444) Decode(data []byte, w, h int, target func(v, w, h int) unsafe.Pointer) (int, error) {
	mainData, auxData, _, _, err := protocol.ParseAVC444(data)
	if err != nil {
		return 0, errAVC444
	}
	got := 0
	if ok, err := v.main.decode(mainData, w, h, target(0, w&^1, h&^1)); err != nil {
		return 0, err
	} else if ok {
		got |= 1
	}
	if ok, err := v.aux.decode(auxData, w, h, target(1, w&^1, h&^1)); err != nil {
		return got, err
	} else if ok {
		got |= 2
	}
	return got, nil
}
func (v *texAVC444) GPU() bool { return C.rd_is_hw(v.main.c) != 0 }
func (v *texAVC444) Close()    { v.main.close(); v.aux.close() }
