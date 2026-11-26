package decode

import "unsafe"

// TexVideo decodifica direto nas texturas NV12 do desenho na GPU
// (internal/viewer/render), sem passar pela memória.
type TexVideo interface {
	// Decode entrega um quadro. target(v, w, h) devolve a textura NV12
	// (ID3D11Texture2D) da visão v — 0 = principal, 1 = auxiliar do AVC444 —
	// no tamanho w x h. Devolve quais visões receberam imagem nova (bit 0 =
	// principal, bit 1 = auxiliar).
	Decode(data []byte, w, h int, target func(v, w, h int) unsafe.Pointer) (int, error)
	GPU() bool
	Close()
}
