// Package input injeta no sistema os eventos de mouse e teclado enviados
// pelo técnico.
//
// No Windows usa SendInput. Teclas imprimíveis sem Ctrl/Alt/Win são enviadas
// como caractere Unicode, o que independe do layout do teclado remoto (ç, ~,
// acentos); as demais viram virtual-key codes (veja keymap.go). Fora do
// Windows os eventos só são registrados no log.
package input

import "remoteit/internal/protocol"

// Injector aplica eventos de entrada. Não é seguro para uso concorrente: o
// agente chama Handle só a partir da goroutine de leitura da conexão. No
// Windows, Handle só enfileira; a injeção roda numa thread própria.
type Injector interface {
	Handle(m protocol.Message)
}
