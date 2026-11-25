package client

import (
	"slices"
	"testing"

	"remoteit/internal/protocol"
)

func TestCodecsOrder(t *testing.T) {
	cases := []struct {
		h264, av1, c444 bool
		want            []string
	}{
		{true, true, true, []string{"avc444", "av1", "h264", "jpeg"}},
		{true, true, false, []string{"av1", "h264", "jpeg"}},
		{true, false, true, []string{"avc444", "h264", "jpeg"}},
		// 4:4:4 são dois fluxos H.264: sem decodificador H.264, não entra
		{false, true, true, []string{"av1", "jpeg"}},
		{false, false, true, []string{"jpeg"}},
	}
	for _, c := range cases {
		if got := Codecs(c.h264, c.av1, c.c444); !slices.Equal(got, c.want) {
			t.Errorf("Codecs(%v, %v, %v) = %v, want %v", c.h264, c.av1, c.c444, got, c.want)
		}
	}
}

// Agentes de antes da negociação só leem o campo h264 do hello.
func TestHelloKeepsLegacyField(t *testing.T) {
	if m := hello(Codecs(true, false, false)); !m.H264 || m.T != protocol.TypeHello {
		t.Errorf("hello com H.264 = %+v", m)
	}
	if m := hello(Codecs(false, true, false)); m.H264 {
		t.Errorf("hello sem H.264 marcou h264: %+v", m)
	}
}
