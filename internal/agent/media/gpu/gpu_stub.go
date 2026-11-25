//go:build !windows || !cgo

package gpu

import (
	"errors"

	"remoteit/internal/agent/media"
)

var errBuild = errors.New("encoder de GPU indisponível neste build (compile com CGO_ENABLED=1 via build.sh)")

// NewH264 sempre falha neste build: não há encoder de GPU sem Windows + CGO.
func NewH264(maxw, fps, kbps int) (media.Source, error) { return nil, errBuild }

// NewAVC444 sempre falha neste build.
func NewAVC444(fps, kbps int) (media.Source, error) { return nil, errBuild }

// NewAV1 sempre falha neste build.
func NewAV1(maxw, fps, kbps int) (media.Source, error) { return nil, errBuild }
