//go:build !windows || !cgo

package decode

func openNV12(int) (nv12Decoder, error) { return nil, ErrUnavailable }
