//go:build !windows || !cgo

package decode

func newH264() (H264, error) { return nil, ErrNoH264 }
