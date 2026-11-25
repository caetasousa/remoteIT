package gpu

// AV1 chega do encoder como uma sequência de OBUs ("open bitstream units").
// O WebCodecs e o decodificador do Windows precisam do cabeçalho de
// sequência (OBU tipo 1) em todo keyframe para começar a decodificar dali.
// Alguns encoders do Media Foundation só o põem no tipo de mídia
// (MF_MT_MPEG_SEQUENCE_HEADER); estas funções o devolvem ao fluxo.

// Tipos de OBU usados aqui (especificação AV1, seção 6.2.2).
const (
	obuSequenceHeader     = 1
	obuTemporalDelimiter  = 2
	obuHasSizeField       = 0x02
	obuExtensionFlag      = 0x04
	av1CodecConfigVersion = 0x81 // marker = 1, version = 1 (av1C)
)

// obu é um OBU já separado: tipo e os bytes inteiros (cabeçalho + conteúdo).
type obu struct {
	typ  byte
	data []byte
}

// splitOBUs separa OBUs com campo de tamanho (o formato "low overhead" que
// o WebCodecs espera). ok = false se algum OBU não tem tamanho ou passa do
// fim: aí o fluxo vai sem mexer.
func splitOBUs(b []byte) (out []obu, ok bool) {
	for len(b) > 0 {
		h := b[0]
		if h&obuHasSizeField == 0 {
			return nil, false
		}
		n := 1
		if h&obuExtensionFlag != 0 {
			n++
		}
		size, used := leb128(b[min(n, len(b)):])
		if used == 0 || uint64(len(b)-n-used) < size {
			return nil, false
		}
		end := n + used + int(size)
		out = append(out, obu{typ: h >> 3 & 0x0F, data: b[:end]})
		b = b[end:]
	}
	return out, true
}

// leb128 lê um inteiro LEB128 (até 8 bytes); used = 0 se truncado.
func leb128(b []byte) (v uint64, used int) {
	for i := 0; i < 8 && i < len(b); i++ {
		v |= uint64(b[i]&0x7F) << (7 * i)
		if b[i]&0x80 == 0 {
			return v, i + 1
		}
	}
	return 0, 0
}

// SequenceHeader extrai o OBU de cabeçalho de sequência de um
// MF_MT_MPEG_SEQUENCE_HEADER, que pode vir como OBUs crus ou dentro de um
// AV1CodecConfigurationRecord (av1C: 4 bytes e depois os OBUs). nil se não
// achar.
func SequenceHeader(blob []byte) []byte {
	if len(blob) >= 4 && blob[0] == av1CodecConfigVersion {
		blob = blob[4:]
	}
	obus, ok := splitOBUs(blob)
	if !ok {
		return nil
	}
	for _, o := range obus {
		if o.typ == obuSequenceHeader {
			return o.data
		}
	}
	return nil
}

// WithSequenceHeader devolve o keyframe frame com o cabeçalho de sequência
// seq, logo depois do delimitador temporal (se houver). Sem seq, ou se o
// quadro já tem um, ou se não dá para separar os OBUs, devolve frame igual.
func WithSequenceHeader(frame, seq []byte) []byte {
	if seq == nil {
		return frame
	}
	obus, ok := splitOBUs(frame)
	if !ok {
		return frame
	}
	at := 0
	for i, o := range obus {
		if o.typ == obuSequenceHeader {
			return frame
		}
		if o.typ == obuTemporalDelimiter && i == 0 {
			at = len(o.data)
		}
	}
	out := make([]byte, 0, len(frame)+len(seq))
	return append(append(append(out, frame[:at]...), seq...), frame[at:]...)
}
