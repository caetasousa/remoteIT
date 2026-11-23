//go:build !windows

package consent

// Dialog permite sempre: fora do Windows o agente só roda em desenvolvimento
// e testes, com a tela sintética.
func Dialog(host string) bool { return true }
