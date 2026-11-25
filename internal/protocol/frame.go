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
	// CodecPNG é uma imagem PNG completa, sem perda, enviada uma vez quando a
	// tela fica parada (o "refresh de nitidez"). Não substitui o vídeo: o
	// navegador só a desenha por cima do último quadro, no mesmo tamanho.
	CodecPNG byte = 3
	// CodecAVC444 é cor 4:4:4 em dois fluxos H.264 4:2:0 (o truque do RDP):
	// a visão principal é um H.264 comum e a auxiliar carrega as amostras de
	// cor que a principal descarta. Payload em [AppendAVC444]; o arranjo dos
	// planos está em internal/avc444. Só o visualizador nativo decodifica.
	CodecAVC444 byte = 4
	// CodecAV1 é AV1 em OBUs no formato "low overhead" (com tamanho), uma
	// unidade temporal por quadro, como o WebCodecs espera.
	CodecAV1 byte = 5
)

// Nomes dos codecs na negociação (campo Codecs do hello).
const (
	CodecNameAVC444 = "avc444"
	CodecNameAV1    = "av1"
	CodecNameH264   = "h264"
	CodecNameJPEG   = "jpeg"
)

// ErrShortAVC444 é devolvido por [ParseAVC444] quando o payload não tem o
// cabeçalho de 5 bytes ou o tamanho declarado passa do fim.
var ErrShortAVC444 = errors.New("protocol: payload AVC444 truncado")

// AppendAVC444 monta o payload de um quadro [CodecAVC444] (big-endian):
//
//	offset 0  u8   flags: bit 0 = principal é keyframe, bit 1 = auxiliar é keyframe
//	offset 1  u32  tamanho da parte principal (n)
//	offset 5  n    H.264 Annex-B da visão principal (pode ser vazio)
//	offset 5+n     H.264 Annex-B da visão auxiliar (o resto; pode ser vazio)
//
// Cada parte pode faltar num quadro (o encoder de hardware às vezes entrega a
// saída um quadro depois): o visualizador combina a última de cada uma.
func AppendAVC444(dst []byte, mainKey, auxKey bool, main, aux []byte) []byte {
	var hdr [5]byte
	if mainKey {
		hdr[0] |= 1
	}
	if auxKey {
		hdr[0] |= 2
	}
	binary.BigEndian.PutUint32(hdr[1:], uint32(len(main)))
	return append(append(append(dst, hdr[:]...), main...), aux...)
}

// ParseAVC444 é o inverso de [AppendAVC444]. main e aux apontam para a
// memória de payload.
func ParseAVC444(payload []byte) (main, aux []byte, mainKey, auxKey bool, err error) {
	if len(payload) < 5 {
		return nil, nil, false, false, ErrShortAVC444
	}
	n := binary.BigEndian.Uint32(payload[1:])
	if uint64(n) > uint64(len(payload)-5) {
		return nil, nil, false, false, ErrShortAVC444
	}
	return payload[5 : 5+n], payload[5+n:], payload[0]&1 != 0, payload[0]&2 != 0, nil
}

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
