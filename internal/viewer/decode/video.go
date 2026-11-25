package decode

import (
	"errors"

	"remoteit/internal/protocol"
)

// plainVideo é H.264 ou AV1 comum: NV12 em faixa de vídeo (16-235) virando BGRA.
type plainVideo struct{ d nv12Decoder }

func newPlain(codec int) (Video, error) {
	d, err := openNV12(codec)
	if err != nil {
		return nil, err
	}
	return &plainVideo{d}, nil
}

func (v *plainVideo) Decode(data []byte, w, h int) (*Frame, error) {
	if len(data) == 0 {
		return nil, nil
	}
	nv12, ow, oh, err := v.d.decodeNV12(data, w, h)
	if err != nil || nv12 == nil {
		return nil, err
	}
	f := &Frame{W: ow, H: oh, BGRA: make([]byte, ow*oh*4)}
	nv12ToBGRA(nv12, ow, oh, f.BGRA)
	return f, nil
}

func (v *plainVideo) GPU() bool { return v.d.gpu() }
func (v *plainVideo) Close()    { v.d.close() }

// avc444Video junta as duas visões do AVC444. Cada uma tem o seu
// decodificador (são dois fluxos H.264 independentes) e a última saída de
// cada uma fica guardada: um quadro pode trazer só uma das partes.
type avc444Video struct {
	main, aux  nv12Decoder
	mainBuf    []byte // última visão principal decodificada
	auxBuf     []byte // última auxiliar (nil até a primeira chegar)
	w, h       int
	auxW, auxH int
	mainOK     bool
}

// errAVC444 é um payload que não segue protocol.AppendAVC444.
var errAVC444 = errors.New("decode: payload AVC444 inválido")

func (v *avc444Video) Decode(data []byte, w, h int) (*Frame, error) {
	mainData, auxData, _, _, err := protocol.ParseAVC444(data)
	if err != nil {
		return nil, errAVC444
	}
	got := false
	if len(mainData) > 0 {
		nv, ow, oh, err := v.main.decodeNV12(mainData, w, h)
		if err != nil {
			return nil, err
		}
		if nv != nil {
			v.mainBuf = append(v.mainBuf[:0], nv...)
			v.w, v.h, v.mainOK, got = ow, oh, true, true
		}
	}
	if len(auxData) > 0 {
		nv, ow, oh, err := v.aux.decodeNV12(auxData, w, h)
		if err != nil {
			return nil, err
		}
		if nv != nil {
			v.auxBuf = append(v.auxBuf[:0], nv...)
			v.auxW, v.auxH, got = ow, oh, true
		}
	}
	if !got || !v.mainOK {
		return nil, nil
	}
	var aux []byte
	if v.auxBuf != nil && v.auxW == v.w && v.auxH == v.h {
		aux = v.auxBuf // de outro tamanho (resolução mudou): só a principal
	}
	f := &Frame{W: v.w, H: v.h, BGRA: make([]byte, v.w*v.h*4)}
	avc444ToBGRA(v.mainBuf, aux, v.w, v.h, f.BGRA)
	return f, nil
}

func (v *avc444Video) GPU() bool { return v.main.gpu() }

func (v *avc444Video) Close() {
	v.main.close()
	v.aux.close()
}
