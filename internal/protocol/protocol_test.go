package protocol

import (
	"bytes"
	"encoding/json"
	"slices"
	"testing"
)

func TestFrameRoundTrip(t *testing.T) {
	in := FrameHeader{Codec: CodecH264, Keyframe: true, W: 1920, H: 1080, Seq: 0xFFFFFFFE}
	payload := []byte{0, 0, 0, 1, 0x67}

	msg := AppendFrame(nil, in, payload)
	if len(msg) != HeaderSize+len(payload) {
		t.Fatalf("len = %d, want %d", len(msg), HeaderSize+len(payload))
	}
	out, got, err := ParseFrame(msg)
	if err != nil {
		t.Fatal(err)
	}
	if out != in {
		t.Errorf("header = %+v, want %+v", out, in)
	}
	if !bytes.Equal(got, payload) {
		t.Errorf("payload = %x, want %x", got, payload)
	}
}

// The byte layout is a contract with the browser; this pins it.
func TestFrameLayout(t *testing.T) {
	msg := AppendFrame(nil, FrameHeader{Codec: CodecJPEG, W: 0x0102, H: 0x0304, Seq: 0x05060708}, nil)
	want := []byte{2, 0, 1, 2, 3, 4, 5, 6, 7, 8, 0, 0}
	if !bytes.Equal(msg, want) {
		t.Errorf("layout = %v, want %v", msg, want)
	}
}

func TestParseFrameShort(t *testing.T) {
	if _, _, err := ParseFrame(make([]byte, HeaderSize-1)); err != ErrShortFrame {
		t.Errorf("err = %v, want ErrShortFrame", err)
	}
}

func TestMessageOmitsUnusedFields(t *testing.T) {
	b, _ := json.Marshal(Message{T: TypeAck, S: 7})
	if string(b) != `{"t":"ack","s":7}` {
		t.Errorf("json = %s", b)
	}
}

func TestIsInput(t *testing.T) {
	for _, typ := range []string{TypeMouseMove, TypeMouseDown, TypeMouseUp, TypeWheel, TypeKeyDown, TypeKeyUp} {
		if !(Message{T: typ}).IsInput() {
			t.Errorf("%q should be input", typ)
		}
	}
	for _, typ := range []string{TypeHello, TypeAck, TypeStart, TypeConfig} {
		if (Message{T: typ}).IsInput() {
			t.Errorf("%q should not be input", typ)
		}
	}
}

func TestAcceptsKeepsOrderAndEndsWithJPEG(t *testing.T) {
	cases := []struct {
		m    Message
		want []string
	}{
		{Message{H264: true}, []string{"h264", "jpeg"}},
		{Message{}, []string{"jpeg"}},
		{Message{Codecs: []string{"avc444", "jpeg", "h264", "h264"}}, []string{"avc444", "h264", "jpeg"}},
		// com Codecs, o campo antigo é ignorado: quem manda a lista manda tudo
		{Message{H264: true, Codecs: []string{"av1"}}, []string{"av1", "jpeg"}},
	}
	for _, c := range cases {
		if got := c.m.Accepts(); !slices.Equal(got, c.want) {
			t.Errorf("Accepts(%+v) = %v, want %v", c.m, got, c.want)
		}
	}
}

func TestAVC444RoundTrip(t *testing.T) {
	main, aux := []byte{0, 0, 0, 1, 0x65}, []byte{0, 0, 1, 0x41, 9}
	p := AppendAVC444(nil, true, false, main, aux)
	// o layout é contrato com o visualizador nativo
	if !bytes.Equal(p[:5], []byte{1, 0, 0, 0, 5}) {
		t.Fatalf("cabeçalho = %v", p[:5])
	}
	m, a, mk, ak, err := ParseAVC444(p)
	if err != nil || !bytes.Equal(m, main) || !bytes.Equal(a, aux) || !mk || ak {
		t.Fatalf("ParseAVC444 = %x %x %v %v %v", m, a, mk, ak, err)
	}
	// só a auxiliar neste quadro
	m, a, _, ak, err = ParseAVC444(AppendAVC444(nil, false, true, nil, aux))
	if err != nil || len(m) != 0 || !bytes.Equal(a, aux) || !ak {
		t.Fatalf("só auxiliar: %x %x %v %v", m, a, ak, err)
	}
	for _, bad := range [][]byte{{1, 0, 0}, {0, 0, 0, 0, 9, 1}} {
		if _, _, _, _, err := ParseAVC444(bad); err != ErrShortAVC444 {
			t.Errorf("ParseAVC444(%v) err = %v", bad, err)
		}
	}
}
