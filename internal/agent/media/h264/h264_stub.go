//go:build !windows || !cgo

package h264

import (
	"errors"

	"remoteit/internal/agent/media"
)

// New sempre falha neste build: não há encoder de GPU sem Windows + CGO.
func New(maxw, fps, kbps int) (media.Source, error) {
	return nil, errors.New("H.264 indisponível neste build (compile com CGO_ENABLED=1 via build.sh)")
}
