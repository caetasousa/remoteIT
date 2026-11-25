package input

import "remoteit/internal/protocol"

// vkFor converte KeyboardEvent.code em virtual-key code. A tabela fica em
// internal/protocol, porque o visualizador nativo usa o caminho inverso.
func vkFor(code string) (vk uint16, extended bool) { return protocol.KeyToVK(code) }
