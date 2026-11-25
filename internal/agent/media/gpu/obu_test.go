package gpu

import (
	"bytes"
	"testing"
)

// obuBytes monta um OBU com campo de tamanho.
func obuBytes(typ byte, payload ...byte) []byte {
	return append([]byte{typ<<3 | obuHasSizeField, byte(len(payload))}, payload...)
}

var (
	td    = obuBytes(obuTemporalDelimiter)
	seq   = obuBytes(obuSequenceHeader, 0x00, 0x00, 0x00, 0x0A)
	frame = obuBytes(6, 1, 2, 3) // OBU_FRAME
)

func cat(parts ...[]byte) []byte { return bytes.Join(parts, nil) }

func TestWithSequenceHeaderInsertsAfterTemporalDelimiter(t *testing.T) {
	got := WithSequenceHeader(cat(td, frame), seq)
	if want := cat(td, seq, frame); !bytes.Equal(got, want) {
		t.Fatalf("got %x, want %x", got, want)
	}
	// sem delimitador temporal: vai na frente
	if got := WithSequenceHeader(frame, seq); !bytes.Equal(got, cat(seq, frame)) {
		t.Fatalf("sem TD: %x", got)
	}
}

func TestWithSequenceHeaderLeavesFrameAlone(t *testing.T) {
	has := cat(td, seq, frame)
	if got := WithSequenceHeader(has, seq); !bytes.Equal(got, has) {
		t.Error("quadro que já tem cabeçalho de sequência não pode ganhar outro")
	}
	if got := WithSequenceHeader(frame, nil); !bytes.Equal(got, frame) {
		t.Error("sem cabeçalho guardado, nada muda")
	}
	// OBU sem campo de tamanho: não dá para separar, vai como veio
	noSize := []byte{6 << 3, 1, 2, 3}
	if got := WithSequenceHeader(noSize, seq); !bytes.Equal(got, noSize) {
		t.Error("fluxo que não dá para separar deveria passar intacto")
	}
}

func TestSequenceHeaderFromMediaType(t *testing.T) {
	if got := SequenceHeader(seq); !bytes.Equal(got, seq) {
		t.Errorf("OBU cru: %x", got)
	}
	av1C := cat([]byte{0x81, 0x00, 0x0C, 0x00}, seq)
	if got := SequenceHeader(av1C); !bytes.Equal(got, seq) {
		t.Errorf("av1C: %x", got)
	}
	if SequenceHeader([]byte{0x81, 0, 0, 0}) != nil || SequenceHeader(frame) != nil {
		t.Error("sem cabeçalho de sequência deveria dar nil")
	}
}

func TestSplitOBUsWithExtensionAndLongSize(t *testing.T) {
	payload := bytes.Repeat([]byte{7}, 200) // tamanho em 2 bytes LEB128
	ext := append([]byte{6<<3 | obuHasSizeField | obuExtensionFlag, 0x00, 0xC8, 0x01}, payload...)
	obus, ok := splitOBUs(cat(td, ext))
	if !ok || len(obus) != 2 || obus[1].typ != 6 || len(obus[1].data) != len(ext) {
		t.Fatalf("split = %v %v", obus, ok)
	}
	if _, ok := splitOBUs(ext[:50]); ok {
		t.Error("OBU truncado deveria falhar")
	}
}
