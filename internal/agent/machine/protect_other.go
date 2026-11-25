//go:build !windows

package machine

// Fora do Windows (agente de desenvolvimento e testes) não há DPAPI: o
// arquivo fica com permissão 0600.
func protect(b []byte, machineScope bool) ([]byte, error)   { return b, nil }
func unprotect(b []byte, machineScope bool) ([]byte, error) { return b, nil }
