package protocol

import (
	"encoding/binary"
	"errors"
)

// HeaderSize é o tamanho em bytes de um [FrameHeader] na rede.
const HeaderSize = 12

// Codecs de vídeo identificados em [FrameHeader.Codec].
const (
	CodecH264 byte = 1 // NAL units H.264 Annex-B, decodificadas com WebCodecs
	CodecJPEG byte = 2 // uma imagem JPEG completa; todo quadro é keyframe
)

// ErrShortFrame é devolvido por [ParseFrame] quando a mensagem é menor que
// [HeaderSize].
var ErrShortFrame = errors.New("protocol: quadro menor que o cabeçalho")

// FrameHeader antecede toda mensagem binária de vídeo. Layout (big-endian):
//
//	offset 0  u8   codec
//	offset 1  u8   flags (bit 0 = keyframe)
//	offset 2  u16  largura
//	offset 4  u16  altura
//	offset 6  u32  número de sequência (circular)
//	offset 10 u16  reservado, zero
type FrameHeader struct {
	Codec    byte
	Keyframe bool
	W, H     uint16
	Seq      uint32
}

// AppendFrame acrescenta o cabeçalho e o payload a dst e devolve o slice
// estendido. Um dst com capacidade suficiente evita alocação.
func AppendFrame(dst []byte, h FrameHeader, payload []byte) []byte {
	var hdr [HeaderSize]byte
	hdr[0] = h.Codec
	if h.Keyframe {
		hdr[1] = 1
	}
	binary.BigEndian.PutUint16(hdr[2:], h.W)
	binary.BigEndian.PutUint16(hdr[4:], h.H)
	binary.BigEndian.PutUint32(hdr[6:], h.Seq)
	return append(append(dst, hdr[:]...), payload...)
}

// ParseFrame separa uma mensagem binária em cabeçalho e payload. O payload
// aponta para a mesma memória de msg; copie-o se msg for reutilizada.
func ParseFrame(msg []byte) (FrameHeader, []byte, error) {
	if len(msg) < HeaderSize {
		return FrameHeader{}, nil, ErrShortFrame
	}
	h := FrameHeader{
		Codec:    msg[0],
		Keyframe: msg[1]&1 == 1,
		W:        binary.BigEndian.Uint16(msg[2:]),
		H:        binary.BigEndian.Uint16(msg[4:]),
		Seq:      binary.BigEndian.Uint32(msg[6:]),
	}
	return h, msg[HeaderSize:], nil
}
