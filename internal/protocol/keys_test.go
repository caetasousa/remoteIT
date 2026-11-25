package protocol

import "testing"

// Toda tecla que o agente sabe injetar volta para o mesmo nome no
// visualizador: a tabela é uma só para os dois lados.
func TestCodeForVKRoundTrip(t *testing.T) {
	codes := []string{"KeyA", "KeyZ", "Digit0", "Digit9", "Numpad5", "F1", "F12", "F24",
		"Enter", "NumpadEnter", "Escape", "Backspace", "Tab", "Space", "ArrowLeft", "ArrowDown",
		"Delete", "Home", "PageUp", "ControlLeft", "ControlRight", "AltLeft", "AltRight",
		"MetaLeft", "MetaRight", "Minus", "Slash", "Backquote", "IntlBackslash", "NumpadAdd"}
	for _, code := range codes {
		vk, ext := KeyToVK(code)
		if vk == 0 {
			t.Errorf("%s: sem virtual-key", code)
			continue
		}
		if got := CodeForVK(vk, ext); got != code {
			t.Errorf("CodeForVK(KeyToVK(%s)) = %q", code, got)
		}
	}
	if c := CodeForVK(0x11, false); c != "ControlLeft" {
		t.Errorf("Ctrl genérico = %q, want ControlLeft", c)
	}
	if c := CodeForVK(0xFF, false); c != "" {
		t.Errorf("tecla desconhecida = %q, want vazio", c)
	}
}
