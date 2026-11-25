//go:build windows

package win

import (
	"syscall"
	"unsafe"

	"remoteit/internal/protocol"
)

// keyTranslator converte as teclas apertadas no PC do técnico (virtual-key +
// scancode do hook de teclado) nos nomes do protocolo (KeyboardEvent.code e
// .key do navegador). Guarda o estado dos modificadores e das teclas mortas
// do layout local, para "´" + "a" chegar como "á", igual ao navegador.
type keyTranslator struct {
	down  [256]bool
	caps  bool
	keyOf map[string]string // tecla apertada → key enviado no keydown (o keyup repete)
}

func newKeyTranslator() *keyTranslator {
	r, _, _ := pGetKeyState.Call(0x14) // Caps Lock ligado agora?
	return &keyTranslator{caps: r&1 != 0, keyOf: map[string]string{}}
}

const (
	vkShift, vkControl, vkMenu, vkCapital                        = 0x10, 0x11, 0x12, 0x14
	vkLShift, vkRShift, vkLControl, vkRControl, vkLMenu, vkRMenu = 0xA0, 0xA1, 0xA2, 0xA3, 0xA4, 0xA5
	vkLWin, vkRWin                                               = 0x5B, 0x5C
)

func (k *keyTranslator) ctrl() bool { return k.down[vkLControl] || k.down[vkRControl] }
func (k *keyTranslator) alt() bool  { return k.down[vkLMenu] || k.down[vkRMenu] }
func (k *keyTranslator) win() bool  { return k.down[vkLWin] || k.down[vkRWin] }

// event registra a tecla e devolve o que mandar ao agente. ok = false para
// teclas sem nome no protocolo.
func (k *keyTranslator) event(vk, scan uint32, extended, up bool) (code, key string, ok bool) {
	if vk > 255 {
		return "", "", false
	}
	code = protocol.CodeForVK(uint16(vk), extended)
	if code == "" {
		return "", "", false
	}
	if up {
		k.down[vk] = false
		key = k.keyOf[code]
		delete(k.keyOf, code)
		if key == "" {
			key = code
		}
		return code, key, true
	}
	if vk == vkCapital && !k.down[vk] {
		k.caps = !k.caps
	}
	k.down[vk] = true
	key = code
	// com Ctrl, Alt ou Win o agente injeta a tecla física (atalho); sem eles,
	// manda o caractere, que independe do layout do PC remoto
	if !k.ctrl() && !k.alt() && !k.win() {
		if s := k.char(vk, scan); s != "" {
			key = s
		}
	}
	k.keyOf[code] = key
	return code, key, true
}

// char devolve o caractere da tecla no layout local, "Dead" para tecla morta
// ou "" para teclas sem caractere (setas, Enter...).
func (k *keyTranslator) char(vk, scan uint32) string {
	var state [256]byte
	if k.down[vkLShift] || k.down[vkRShift] {
		state[vkShift], state[vkLShift] = 0x80, 0x80
	}
	if k.caps {
		state[vkCapital] = 1
	}
	var buf [8]uint16
	hkl, _, _ := pGetKeyboardLayout.Call(0)
	n, _, _ := pToUnicodeEx.Call(uintptr(vk), uintptr(scan), uintptr(unsafe.Pointer(&state[0])),
		uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)), 0, hkl)
	switch r := int32(n); {
	case r < 0:
		return "Dead"
	case r == 0:
		return ""
	default:
		s := syscall.UTF16ToString(buf[:r])
		if len(s) == 1 && s[0] < 0x20 { // Enter, Tab, Backspace, Esc: vão pelo nome
			return ""
		}
		return s
	}
}

// releaseAll solta todas as teclas ainda apertadas (a janela perdeu o foco):
// sem isso, um Ctrl apertado na hora da troca ficaria preso no PC remoto.
func (k *keyTranslator) releaseAll() (codes, keys []string) {
	for vk := range k.down {
		if !k.down[vk] {
			continue
		}
		k.down[vk] = false
		code := protocol.CodeForVK(uint16(vk), false)
		if code == "" {
			continue
		}
		key := k.keyOf[code]
		if key == "" {
			key = code
		}
		codes, keys = append(codes, code), append(keys, key)
	}
	k.keyOf = map[string]string{}
	return codes, keys
}
