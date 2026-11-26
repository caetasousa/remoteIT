package media

import (
	"image"
	"time"
)

// TileSize é o lado do bloco de tela, em pixels do quadro, das fontes que
// não informam o próprio (veja [TileSource]).
const TileSize = 64

// maxRects é o máximo de retângulos numa [protocol.Region] ou num lote de
// retalhos. Acima disso, a região vira o retângulo que envolve todos.
const maxRects = 64

// tileState acompanha, bloco a bloco, o que o técnico está mostrando: um
// bloco "com perda" veio do vídeo; "sem perda" veio de um retalho PNG e só
// volta a ter perda quando a tela muda ali.
//
// É o que permite (1) mandar sem perda só as partes que pararam de mudar,
// em vez da tela inteira, e (2) dizer ao técnico onde cada quadro de vídeo
// vale: o cursor de texto piscando num canto não borra mais a tela toda.
type tileState struct {
	cols, rows, size int
	w, h             int // tamanho do quadro
	lossy            []bool
	changed          []time.Time // última mudança de cada bloco
	gen              []uint32    // mudanças de cada bloco (descarta retalho velho)
}

func newTileState(w, h, cols, rows, size int) *tileState {
	n := cols * rows
	return &tileState{cols: cols, rows: rows, size: size, w: w, h: h,
		lossy: make([]bool, n), changed: make([]time.Time, n), gen: make([]uint32, n)}
}

// markAll dá a tela inteira como mudada agora (keyframe, codec novo).
func (t *tileState) markAll(now time.Time) {
	for i := range t.lossy {
		t.lossy[i], t.changed[i] = true, now
		t.gen[i]++
	}
}

// mark registra os blocos que mudaram neste quadro.
func (t *tileState) mark(dirty []bool, now time.Time) {
	for i, d := range dirty {
		if d {
			t.lossy[i], t.changed[i] = true, now
			t.gen[i]++
		}
	}
}

func (t *tileState) anyLossy() bool {
	for _, l := range t.lossy {
		if l {
			return true
		}
	}
	return false
}

// allLossy informa se o vídeo vale na tela inteira (aí a Region é dispensada).
func (t *tileState) allLossy() bool {
	for _, l := range t.lossy {
		if !l {
			return false
		}
	}
	return true
}

// lossyRects é onde o vídeo vale agora, em retângulos do quadro.
func (t *tileState) lossyRects() []image.Rectangle {
	return t.rects(func(i int) bool { return t.lossy[i] })
}

// settled devolve os blocos com perda que não mudam há pelo menos d: são
// os que valem um retalho sem perda.
func (t *tileState) settled(now time.Time, d time.Duration) []int {
	var out []int
	for i, l := range t.lossy {
		if l && now.Sub(t.changed[i]) >= d {
			out = append(out, i)
		}
	}
	return out
}

// tileRect é o retângulo do bloco i, cortado na borda do quadro.
func (t *tileState) tileRect(i int) image.Rectangle {
	x, y := (i%t.cols)*t.size, (i/t.cols)*t.size
	return image.Rect(x, y, min(x+t.size, t.w), min(y+t.size, t.h))
}

// rects junta os blocos escolhidos por sel em retângulos: emenda os blocos
// vizinhos de cada linha e depois as faixas iguais de linhas seguidas. Com
// mais de maxRects, devolve o retângulo que envolve todos (para a Region,
// usar o vídeo numa área maior só custa nitidez, nunca mostra coisa errada).
func (t *tileState) rects(sel func(i int) bool) []image.Rectangle {
	type run struct{ c0, c1 int }
	var out []image.Rectangle
	open := map[run]int{} // faixa da linha anterior -> índice em out
	for r := 0; r < t.rows; r++ {
		next := map[run]int{}
		for c := 0; c < t.cols; {
			if !sel(r*t.cols + c) {
				c++
				continue
			}
			c0 := c
			for c < t.cols && sel(r*t.cols+c) {
				c++
			}
			k := run{c0, c}
			if i, ok := open[k]; ok {
				out[i].Max.Y = min((r+1)*t.size, t.h)
				next[k] = i
			} else {
				out = append(out, image.Rect(c0*t.size, r*t.size, min(c*t.size, t.w), min((r+1)*t.size, t.h)))
				next[k] = len(out) - 1
			}
		}
		open = next
	}
	if len(out) > maxRects {
		b := out[0]
		for _, r := range out[1:] {
			b = b.Union(r)
		}
		return []image.Rectangle{b}
	}
	return out
}

// flat converte retângulos para o campo R da Region: x, y, largura, altura.
func flat(rs []image.Rectangle) []int {
	out := make([]int, 0, len(rs)*4)
	for _, r := range rs {
		out = append(out, r.Min.X, r.Min.Y, r.Dx(), r.Dy())
	}
	return out
}
