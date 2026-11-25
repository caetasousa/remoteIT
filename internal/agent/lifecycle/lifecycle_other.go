//go:build !windows

package lifecycle

import (
	"errors"
	"io"
	"os"
	"path/filepath"
)

// Dispatch fora do Windows não tem subcomandos nem edição de serviço:
// sempre [Portable].
func Dispatch(args []string, installer bool) Mode { return Portable }

// Init não faz nada fora do Windows.
func Init(Mode) {}

// LogOutput fora do Windows é sempre o stderr.
func LogOutput(Mode) io.Writer { return os.Stderr }

// StateDir é onde o agente guarda a credencial da máquina:
// REMOTEIT_STATE_DIR, ou a pasta de configuração do usuário.
func StateDir(Mode) string {
	if d := os.Getenv("REMOTEIT_STATE_DIR"); d != "" {
		return d
	}
	if d, err := os.UserConfigDir(); err == nil {
		return filepath.Join(d, "remoteit")
	}
	return filepath.Join(os.TempDir(), "remoteit")
}

// ApplyUpdate só existe no Windows; o agente de desenvolvimento não se troca.
func ApplyUpdate(Mode, []byte) error {
	return errors.New("lifecycle: atualização automática só no Windows")
}
