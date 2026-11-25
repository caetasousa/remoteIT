//go:build !windows

package main

import (
	"fmt"
	"os"
)

func run() {
	fmt.Fprintln(os.Stderr, "o visualizador nativo só existe para Windows; use o console no navegador")
	os.Exit(1)
}
