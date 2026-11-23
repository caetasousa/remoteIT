//go:build !windows

package input

import (
	"log/slog"

	"remoteit/internal/protocol"
)

type logInjector struct{}

// New cria um injetor que só registra os eventos no log. followDesktop só
// tem efeito no Windows.
func New(followDesktop func() error) Injector { return logInjector{} }

func (logInjector) Handle(m protocol.Message) {
	slog.Debug("input", "t", m.T, "code", m.Code, "x", m.X, "y", m.Y)
}
