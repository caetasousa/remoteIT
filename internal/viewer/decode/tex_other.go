//go:build !windows || !cgo

package decode

import "unsafe"

// NewTex não existe fora do Windows: o visualizador usa o caminho da CPU.
func NewTex(byte, unsafe.Pointer) (TexVideo, error) { return nil, ErrUnavailable }
