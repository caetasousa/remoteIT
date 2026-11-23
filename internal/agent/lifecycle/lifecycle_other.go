//go:build !windows

package lifecycle

import (
	"io"
	"os"
)

// Dispatch fora do Windows não tem subcomandos nem edição de serviço:
// sempre [Portable].
func Dispatch(args []string, installer bool) Mode { return Portable }

// Init não faz nada fora do Windows.
func Init(Mode) {}

// LogOutput fora do Windows é sempre o stderr.
func LogOutput(Mode) io.Writer { return os.Stderr }
