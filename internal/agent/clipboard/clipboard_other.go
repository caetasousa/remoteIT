//go:build !windows

package clipboard

import "log/slog"

func set(text string) { slog.Debug("área de transferência", "bytes", len(text)) }
