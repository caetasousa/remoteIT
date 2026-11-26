//go:build !windows || !cgo

package render

import (
	"image"
	"unsafe"
)

// Renderer não existe fora do Windows: New sempre falha.
type Renderer struct{}

// New sempre falha fora do Windows.
func New(uintptr, int, int) (*Renderer, error) { return nil, ErrUnavailable }

func (*Renderer) Device() unsafe.Pointer                                 { return nil }
func (*Renderer) Video(int, int, int) unsafe.Pointer                     { return nil }
func (*Renderer) UploadNV12(int, []byte, int, int) error                 { return ErrUnavailable }
func (*Renderer) ComposeVideo(Kind, int, int, []image.Rectangle) error   { return ErrUnavailable }
func (*Renderer) ComposeBGRA(int, int, int, int, []byte, int, int) error { return ErrUnavailable }
func (*Renderer) Present(image.Rectangle, uint32) error                  { return ErrUnavailable }
func (*Renderer) Resize(int, int) error                                  { return ErrUnavailable }
func (*Renderer) Close()                                                 {}
