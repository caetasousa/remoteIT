//go:build !windows

package dpapi

// Fora do Windows (agente de desenvolvimento e testes) não há DPAPI: o
// arquivo fica com permissão 0600.
func Protect(b []byte, machineScope bool) ([]byte, error)   { return b, nil }
func Unprotect(b []byte, machineScope bool) ([]byte, error) { return b, nil }
