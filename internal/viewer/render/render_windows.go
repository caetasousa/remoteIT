//go:build windows && cgo

package render

/*
#cgo LDFLAGS: -ld3d11 -ldxgi -luuid -lole32
#include "rcrend.h"
*/
import "C"

import (
	"errors"
	"image"
	"sync"
	"unsafe"
)

var errClosed = errors.New("render: fechado")

// Renderer é o desenho de uma janela de sessão. Os métodos podem ser
// chamados de threads diferentes (decodificação e janela): um mutex ordena
// as chamadas, e o dispositivo é protegido para várias threads.
type Renderer struct {
	mu sync.Mutex
	c  *C.rr_ctx
}

// New cria o dispositivo e a cadeia de troca da janela hwnd (w x h).
func New(hwnd uintptr, w, h int) (*Renderer, error) {
	var c *C.rr_ctx
	var buf [512]C.char
	if C.rr_open(&c, C.uintptr_t(hwnd), C.int(w), C.int(h), &buf[0], C.int(len(buf))) != 0 {
		return nil, errors.Join(ErrUnavailable, errors.New(C.GoString(&buf[0])))
	}
	return &Renderer{c: c}, nil
}

// Device é o ID3D11Device do desenho, para o decodificador entregar os
// quadros nele (decode.NewTex).
func (r *Renderer) Device() unsafe.Pointer {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.c == nil {
		return nil
	}
	return unsafe.Pointer(C.rr_dev(r.c))
}

// Video devolve a textura NV12 (ID3D11Texture2D) da visão v em w x h.
func (r *Renderer) Video(v, w, h int) unsafe.Pointer {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.c == nil {
		return nil
	}
	return unsafe.Pointer(C.rr_video(r.c, C.int(v), C.int(w), C.int(h)))
}

// UploadNV12 sobe um quadro NV12 contíguo da memória para a visão v.
func (r *Renderer) UploadNV12(v int, nv12 []byte, w, h int) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.c == nil {
		return errClosed
	}
	if len(nv12) < w*h*3/2 || C.rr_upload_nv12(r.c, C.int(v), (*C.uint8_t)(unsafe.Pointer(&nv12[0])), C.int(w), C.int(h)) != 0 {
		return errors.New("render: upload NV12")
	}
	return nil
}

// ComposeVideo desenha o vídeo das visões na tela composta, só dentro de
// rects (nil = o quadro inteiro; vazio = nada).
func (r *Renderer) ComposeVideo(k Kind, w, h int, rects []image.Rectangle) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.c == nil {
		return errClosed
	}
	n := C.int(-1)
	var flat []C.int
	if rects != nil {
		n = C.int(len(rects))
		for _, rc := range rects {
			flat = append(flat, C.int(rc.Min.X), C.int(rc.Min.Y), C.int(rc.Dx()), C.int(rc.Dy()))
		}
	}
	var p *C.int
	if len(flat) > 0 {
		p = &flat[0]
	}
	if C.rr_compose_video(r.c, C.int(k), C.int(w), C.int(h), p, n) != 0 {
		return errors.New("render: composição do vídeo")
	}
	return nil
}

// ComposeBGRA põe uma imagem BGRA (w x h) na composta em (x, y); fw x fh é o
// tamanho do quadro inteiro.
func (r *Renderer) ComposeBGRA(x, y, w, h int, bgra []byte, fw, fh int) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.c == nil {
		return errClosed
	}
	if len(bgra) < w*h*4 || C.rr_compose_bgra(r.c, C.int(x), C.int(y), C.int(w), C.int(h),
		(*C.uint8_t)(unsafe.Pointer(&bgra[0])), C.int(fw), C.int(fh)) != 0 {
		return errors.New("render: imagem na composta")
	}
	return nil
}

// Present mostra a composta na janela, em dest (com fundo bg, 0xRRGGBB, no
// resto). Não espera o vsync.
func (r *Renderer) Present(dest image.Rectangle, bg uint32) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.c == nil {
		return errClosed
	}
	if C.rr_present(r.c, C.int(dest.Min.X), C.int(dest.Min.Y), C.int(dest.Dx()), C.int(dest.Dy()), C.uint(bg)) != 0 {
		return errors.New("render: present")
	}
	return nil
}

// Resize acompanha o tamanho da janela.
func (r *Renderer) Resize(w, h int) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.c == nil {
		return errClosed
	}
	if C.rr_resize(r.c, C.int(w), C.int(h)) != 0 {
		return errors.New("render: resize")
	}
	return nil
}

// Close libera o dispositivo.
func (r *Renderer) Close() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.c != nil {
		C.rr_close(r.c)
		r.c = nil
	}
}

// selftest roda o caminho de shaders sem janela (veja rr_selftest).
func selftest(k Kind, w, h int, main, aux, patch []byte, px, py, pw, ph int, region *image.Rectangle, ow, oh int) (comp, out []byte, err error) {
	comp, out = make([]byte, w*h*4), make([]byte, ow*oh*4)
	var auxp, patchp *C.uint8_t
	if aux != nil {
		auxp = (*C.uint8_t)(unsafe.Pointer(&aux[0]))
	}
	if patch != nil {
		patchp = (*C.uint8_t)(unsafe.Pointer(&patch[0]))
	}
	var reg *C.int
	if region != nil {
		r := []C.int{C.int(region.Min.X), C.int(region.Min.Y), C.int(region.Dx()), C.int(region.Dy())}
		reg = &r[0]
	}
	var buf [512]C.char
	if C.rr_selftest(C.int(k), C.int(w), C.int(h), (*C.uint8_t)(unsafe.Pointer(&main[0])), auxp, patchp,
		C.int(px), C.int(py), C.int(pw), C.int(ph), reg, C.int(ow), C.int(oh),
		(*C.uint8_t)(unsafe.Pointer(&comp[0])), (*C.uint8_t)(unsafe.Pointer(&out[0])), &buf[0], C.int(len(buf))) != 0 {
		return nil, nil, errors.New(C.GoString(&buf[0]))
	}
	return comp, out, nil
}
