package decode

import "image"

// NV12ToBGRA converte NV12 contíguo de w x h (BT.709 16-235, o H.264/AV1 do
// agente) para BGRA. É a referência do shader do desenho na GPU.
func NV12ToBGRA(nv12 []byte, w, h int) []byte {
	out := make([]byte, w*h*4)
	nv12ToBGRA(nv12, w, h, out)
	return out
}

// AVC444ToBGRA recompõe a cor 4:4:4 das duas visões (aux nil = só a
// principal). É a referência do shader do desenho na GPU.
func AVC444ToBGRA(main, aux []byte, w, h int) []byte {
	out := make([]byte, w*h*4)
	avc444ToBGRA(main, aux, w, h, out)
	return out
}

// Canvas é a tela composta do desenho pela CPU (o mesmo papel da textura
// composta do desenho na GPU, internal/viewer/render): guarda a imagem
// inteira, recebe o vídeo só nas regiões que mudaram e os retalhos sem
// perda por cima.
type Canvas struct {
	W, H int
	BGRA []byte
}

// Video aplica um quadro de vídeo decodificado: dentro de rects, ou inteiro
// se rects for nil (ou se o tamanho mudou: aí não há o que manter).
func (c *Canvas) Video(f *Frame, rects []image.Rectangle) {
	if f == nil || f.W <= 0 || f.H <= 0 {
		return
	}
	if rects == nil || c.W != f.W || c.H != f.H || c.BGRA == nil {
		c.W, c.H = f.W, f.H
		c.BGRA = append(c.BGRA[:0], f.BGRA[:f.W*f.H*4]...)
		return
	}
	frame := image.Rect(0, 0, c.W, c.H)
	for _, r := range rects {
		r = r.Intersect(frame)
		for y := r.Min.Y; y < r.Max.Y; y++ {
			o := (y*c.W + r.Min.X) * 4
			copy(c.BGRA[o:o+r.Dx()*4], f.BGRA[o:o+r.Dx()*4])
		}
	}
}

// Patch põe a imagem p em (x, y). w x h é o tamanho do quadro: se a tela
// composta é de outro tamanho, o retalho é de uma tela que já mudou e fica
// de fora. Um retalho do tamanho do quadro inteiro (o refresh inteiro) vale
// também como primeira imagem.
func (c *Canvas) Patch(x, y int, p *Frame, w, h int) {
	if p == nil {
		return
	}
	if c.W != w || c.H != h {
		if x != 0 || y != 0 || p.W != w || p.H != h {
			return
		}
		c.W, c.H = w, h
		c.BGRA = make([]byte, w*h*4)
	}
	r := image.Rect(x, y, x+p.W, y+p.H).Intersect(image.Rect(0, 0, c.W, c.H))
	for yy := r.Min.Y; yy < r.Max.Y; yy++ {
		src := ((yy-y)*p.W + (r.Min.X - x)) * 4
		dst := (yy*c.W + r.Min.X) * 4
		copy(c.BGRA[dst:dst+r.Dx()*4], p.BGRA[src:src+r.Dx()*4])
	}
}

// Frame devolve a tela composta como quadro (sem copiar).
func (c *Canvas) Frame() *Frame {
	if c.BGRA == nil {
		return nil
	}
	return &Frame{W: c.W, H: c.H, BGRA: c.BGRA}
}
