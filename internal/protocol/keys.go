package protocol

import "strings"

// Teclas: o protocolo usa os nomes de KeyboardEvent.code do navegador
// ("KeyA", "ArrowLeft", "NumpadEnter"...). O agente converte para o
// virtual-key code do Windows ao injetar ([KeyToVK]); o visualizador nativo
// faz o caminho inverso a partir da tecla apertada no PC do técnico
// ([CodeForVK]). A mesma tabela serve aos dois lados.

// namedKeys mapeia KeyboardEvent.code para o virtual-key code do Windows.
var namedKeys = map[string]uint16{
	"Enter": 0x0D, "Escape": 0x1B, "Backspace": 0x08, "Tab": 0x09, "Space": 0x20, "CapsLock": 0x14,
	"ShiftLeft": 0xA0, "ShiftRight": 0xA1, "ControlLeft": 0xA2, "ControlRight": 0xA3, "AltLeft": 0xA4, "AltRight": 0xA5,
	"MetaLeft": 0x5B, "MetaRight": 0x5C, "ContextMenu": 0x5D,
	"ArrowLeft": 0x25, "ArrowUp": 0x26, "ArrowRight": 0x27, "ArrowDown": 0x28,
	"Insert": 0x2D, "Delete": 0x2E, "Home": 0x24, "End": 0x23, "PageUp": 0x21, "PageDown": 0x22,
	"Minus": 0xBD, "Equal": 0xBB, "BracketLeft": 0xDB, "BracketRight": 0xDD, "Backslash": 0xDC,
	"Semicolon": 0xBA, "Quote": 0xDE, "Comma": 0xBC, "Period": 0xBE, "Slash": 0xBF, "Backquote": 0xC0, "IntlBackslash": 0xE2,
	"NumpadMultiply": 0x6A, "NumpadAdd": 0x6B, "NumpadSubtract": 0x6D, "NumpadDecimal": 0x6E, "NumpadDivide": 0x6F,
	"NumpadEnter": 0x0D, "NumLock": 0x90, "PrintScreen": 0x2C, "ScrollLock": 0x91, "Pause": 0x13,
}

// extendedKeys precisam do flag KEYEVENTF_EXTENDEDKEY para o Windows
// distingui-las das teclas equivalentes do teclado numérico.
var extendedKeys = map[string]bool{
	"ArrowLeft": true, "ArrowUp": true, "ArrowRight": true, "ArrowDown": true,
	"Insert": true, "Delete": true, "Home": true, "End": true, "PageUp": true, "PageDown": true,
	"ControlRight": true, "AltRight": true, "MetaLeft": true, "MetaRight": true, "ContextMenu": true,
	"NumpadDivide": true, "NumpadEnter": true, "NumLock": true, "PrintScreen": true,
}

// KeyToVK converte KeyboardEvent.code em virtual-key code do Windows. vk = 0
// significa tecla desconhecida. Usado pelo agente para injetar a tecla.
func KeyToVK(code string) (vk uint16, extended bool) {
	switch {
	case strings.HasPrefix(code, "Key") && len(code) == 4:
		return uint16(code[3]), false
	case strings.HasPrefix(code, "Digit") && len(code) == 6:
		return uint16(code[5]), false
	case strings.HasPrefix(code, "Numpad") && len(code) == 7 && code[6] >= '0' && code[6] <= '9':
		return 0x60 + uint16(code[6]-'0'), false
	case len(code) >= 2 && code[0] == 'F' && code[1] >= '1' && code[1] <= '9':
		n := 0
		for _, c := range code[1:] {
			if c < '0' || c > '9' {
				return 0, false
			}
			n = n*10 + int(c-'0')
		}
		if n >= 1 && n <= 24 {
			return 0x6F + uint16(n), false
		}
		return 0, false
	}
	return namedKeys[code], extendedKeys[code]
}

// CodeForVK é o inverso de [KeyToVK]: o nome KeyboardEvent.code de um
// virtual-key code do Windows. extended é o bit de tecla estendida do
// Windows (separa Enter de NumpadEnter, Ctrl esquerdo de direito...). ""
// para teclas sem nome no protocolo.
func CodeForVK(vk uint16, extended bool) string {
	switch {
	case vk >= 'A' && vk <= 'Z':
		return "Key" + string(rune(vk))
	case vk >= '0' && vk <= '9':
		return "Digit" + string(rune(vk))
	case vk >= 0x60 && vk <= 0x69:
		return "Numpad" + string(rune('0'+vk-0x60))
	case vk >= 0x70 && vk <= 0x87:
		n := int(vk) - 0x6F
		if n < 10 {
			return "F" + string(rune('0'+n))
		}
		return "F" + string(rune('0'+n/10)) + string(rune('0'+n%10))
	}
	// teclas genéricas que o Windows entrega sem lado (Shift, Ctrl, Alt)
	switch vk {
	case 0x10:
		return "ShiftLeft"
	case 0x11:
		if extended {
			return "ControlRight"
		}
		return "ControlLeft"
	case 0x12:
		if extended {
			return "AltRight"
		}
		return "AltLeft"
	case 0x0D:
		if extended {
			return "NumpadEnter"
		}
		return "Enter"
	}
	for code, v := range namedKeys {
		if v == vk && code != "NumpadEnter" {
			return code
		}
	}
	return ""
}
