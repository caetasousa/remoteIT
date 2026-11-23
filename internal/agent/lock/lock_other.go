//go:build !windows

package lock

// Station não faz nada fora do Windows: o agente só roda em produção no
// Windows; fora dele (desenvolvimento, testes) não há estação para bloquear.
func Station() {}
