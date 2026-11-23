package input

import "testing"

func TestVKFor(t *testing.T) {
	cases := []struct {
		code string
		vk   uint16
		ext  bool
	}{
		{"KeyA", 'A', false},
		{"Digit7", '7', false},
		{"Numpad3", 0x63, false},
		{"F1", 0x70, false},
		{"F12", 0x7B, false},
		{"F24", 0x87, false},
		{"F25", 0, false},
		{"Fn", 0, false},
		{"Enter", 0x0D, false},
		{"NumpadEnter", 0x0D, true},
		{"ArrowUp", 0x26, true},
		{"ControlRight", 0xA3, true},
		{"ControlLeft", 0xA2, false},
		{"Desconhecida", 0, false},
	}
	for _, c := range cases {
		vk, ext := vkFor(c.code)
		if vk != c.vk || ext != c.ext {
			t.Errorf("vkFor(%q) = (%#x, %v), want (%#x, %v)", c.code, vk, ext, c.vk, c.ext)
		}
	}
}
