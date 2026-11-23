package protocol

import (
	"bytes"
	"encoding/json"
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
