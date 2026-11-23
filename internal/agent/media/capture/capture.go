// Package capture lê a tela como imagem RGBA.
//
// No Windows usa GDI (BitBlt) via syscall, sem CGO: funciona em qualquer
// build e serve de base para o encoder JPEG. Nos demais sistemas gera uma
// tela sintética animada, o que permite rodar o agente em Linux para
// desenvolvimento e para os testes de integração e E2E.
package capture

import "image"

// Capturer captura a tela inteira do monitor principal.
type Capturer interface {
	Capture() (*image.RGBA, error)
	Close()
}
