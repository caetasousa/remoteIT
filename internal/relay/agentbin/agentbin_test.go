package agentbin

import (
	"bytes"
	"errors"
	"testing"

	"remoteit/internal/protocol"
)

func fakeExe() []byte {
	return []byte("MZ...cabeçalho..." + protocol.EnrollMarker + protocol.EnrollEmpty + "...resto do binário")
}

func TestStampWritesCodeAndPadding(t *testing.T) {
	exe := fakeExe()
	out, err := Stamp(exe, "abc123")
	if err != nil {
		t.Fatal(err)
	}
	want := protocol.EnrollMarker + "abc123" + protocol.EnrollEmpty[6:]
	if !bytes.Contains(out, []byte(want)) {
		t.Fatalf("espaço não gravado: %q", out)
	}
	if len(out) != len(exe) {
		t.Fatalf("tamanho mudou: %d → %d", len(exe), len(out))
	}
	if !bytes.Contains(exe, []byte(protocol.EnrollEmpty)) {
		t.Fatal("Stamp alterou o binário original")
	}
}

func TestStampRejectsBadInput(t *testing.T) {
	long := string(bytes.Repeat([]byte("a"), protocol.EnrollCodeLen+1))
	for _, code := range []string{"", long, "com_pad"} {
		if _, err := Stamp(fakeExe(), code); !errors.Is(err, ErrBadCode) {
			t.Errorf("código %q: err = %v, want ErrBadCode", code, err)
		}
	}
	twice := append(fakeExe(), fakeExe()...)
	for name, exe := range map[string][]byte{"sem espaço": []byte("MZ binário antigo"), "dois espaços": twice} {
		if _, err := Stamp(exe, "abc"); !errors.Is(err, ErrNoSlot) {
			t.Errorf("%s: err = %v, want ErrNoSlot", name, err)
		}
	}
}
