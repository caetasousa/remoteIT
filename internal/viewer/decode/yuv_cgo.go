//go:build cgo

package decode

/*
#include "yuv.h"
*/
import "C"

import "unsafe"

func ptr(b []byte) *C.uint8_t { return (*C.uint8_t)(unsafe.Pointer(&b[0])) }

// nv12ToBGRA converte NV12 contíguo (faixa de vídeo 16-235) de w x h.
func nv12ToBGRA(nv12 []byte, w, h int, dst []byte) {
	C.nv12_to_bgra(ptr(nv12), C.long(w), C.unsigned(h), C.int(w), C.int(h), ptr(dst))
}

// avc444ToBGRA recompõe a cor 4:4:4; aux nil usa só a visão principal.
func avc444ToBGRA(main, aux []byte, w, h int, dst []byte) {
	var a *C.uint8_t
	if aux != nil {
		a = ptr(aux)
	}
	C.avc444_to_bgra(ptr(main), a, C.int(w), C.int(h), ptr(dst))
}
