package media

import (
	"bytes"
	"image"
	"image/png"
	"log/slog"
	"slices"
	"time"
)

// StillDelay é quanto tempo um bloco da tela precisa ficar sem mudar para
// ganhar o retalho sem perda. Curto o bastante para o texto ficar nítido
// logo depois de digitar ou rolar; longo o bastante para não mandar PNG a
// cada quadro de uma animação.
const StillDelay = 300 * time.Millisecond

// fullShare é a fração da tela, em blocos prontos, a partir da qual o
// refresh vai inteiro (um PNG só, [protocol.CodecPNG]) em vez de retalhos.
const fullShare = 0.6

// stillPart é um pedaço comprimido e os blocos que ele cobre, com a geração
// de cada um no momento da captura.
type stillPart struct {
	x, y  int
	png   []byte
	tiles []int
	gens  []uint32
}

// stillShot é o resultado de uma captura sem perda: a tela inteira (full)
// ou retalhos.
type stillShot struct {
	w, h  int
	full  bool
	parts []stillPart
}

// refresher faz o refresh sem perda: captura os blocos da tela que pararam
// de mudar e os comprime em PNG numa goroutine, sem segurar o vídeo.
//
// O vídeo perde detalhe (cor em meia resolução no H.264 4:2:0, artefatos de
// compressão); o retalho PNG tem os pixels exatos. O técnico desenha os
// retalhos por cima e, com [protocol.Region], só deixa o vídeo cobrir o que
// mudou depois. Uma captura que ficou velha durante a compressão (o bloco
// mudou nesse meio-tempo) é descartada: senão cobriria a tela nova com uma
// imagem antiga.
type refresher struct {
	legacy     func() (Still, error) // capturador sem compressão das fontes sem StillReader
	logger     *slog.Logger
	cap        Still
	pending    bool // há uma compressão em andamento
	failed     bool // a falha já foi registrada no log
	fullFailed bool // o último refresh inteiro foi descartado: tente retalhos
	noReader   bool // a fonte não consegue ler a própria imagem: só o legado
	ready      chan stillShot
}

func newRefresher(legacy func() (Still, error), logger *slog.Logger) *refresher {
	return &refresher{legacy: legacy, logger: logger, ready: make(chan stillShot, 1)}
}

// shoot captura os blocos idx (já sem mudar há StillDelay) e os comprime
// fora da thread de captura. reader nil quer dizer fonte sem leitura
// própria (JPEG): aí só a tela inteira, pelo capturador legado, e só se o
// vídeo estiver na resolução nativa (native).
func (r *refresher) shoot(t *tileState, idx []int, reader StillReader, native bool) {
	if r.pending || len(idx) == 0 {
		return
	}
	full := reader == nil || (!r.fullFailed && float64(len(idx)) >= fullShare*float64(len(t.lossy)))
	frame := image.Rect(0, 0, t.w, t.h)
	var shot stillShot
	shot.w, shot.h, shot.full = t.w, t.h, full
	var imgs []*image.RGBA
	if full {
		img, err := r.capture(frame, reader, native)
		if err != nil || img == nil {
			return
		}
		all := make([]int, len(t.lossy))
		for i := range all {
			all[i] = i
		}
		shot.parts = []stillPart{{tiles: all, gens: slices.Clone(t.gen)}}
		imgs = append(imgs, img)
	} else {
		in := make([]bool, len(t.lossy))
		for _, i := range idx {
			in[i] = true
		}
		for _, rc := range t.rects(func(i int) bool { return in[i] }) {
			img, err := reader.ReadStill(rc)
			if err != nil {
				r.fail(err)
				r.noReader = true
				return
			}
			tiles := t.tilesIn(rc)
			gens := make([]uint32, len(tiles))
			for k, i := range tiles {
				gens[k] = t.gen[i]
			}
			shot.parts = append(shot.parts, stillPart{x: rc.Min.X, y: rc.Min.Y, tiles: tiles, gens: gens})
			imgs = append(imgs, img)
		}
	}
	r.pending = true
	go func() {
		enc := png.Encoder{CompressionLevel: png.BestSpeed}
		for k, img := range imgs {
			var b bytes.Buffer
			if enc.Encode(&b, img) != nil {
				shot.parts = nil
				break
			}
			shot.parts[k].png = b.Bytes()
		}
		r.ready <- shot // nunca bloqueia: só há uma compressão por vez (pending)
	}()
}

// capture lê a tela inteira, pela fonte ou pelo capturador legado.
func (r *refresher) capture(frame image.Rectangle, reader StillReader, native bool) (*image.RGBA, error) {
	if reader != nil {
		img, err := reader.ReadStill(frame)
		if err != nil {
			r.fail(err)
			r.noReader = true
		}
		return img, err
	}
	if !native || r.legacy == nil {
		return nil, nil // o legado só enxerga a tela nativa, e o vídeo está reduzido
	}
	if r.cap == nil {
		c, err := r.legacy()
		if err != nil {
			r.fail(err)
			return nil, err
		}
		r.cap = c
	}
	img, err := r.cap.Capture()
	if err != nil {
		r.fail(err)
		return nil, err
	}
	if img.Bounds().Size() != frame.Size() {
		return nil, nil // a tela mudou de tamanho e o vídeo ainda não
	}
	return img, nil
}

// take devolve a captura comprimida, se já terminou.
func (r *refresher) take() (stillShot, bool) {
	if !r.pending {
		return stillShot{}, false
	}
	select {
	case shot := <-r.ready:
		r.pending = false
		return shot, len(shot.parts) > 0
	default:
		return stillShot{}, false
	}
}

func (r *refresher) fail(err error) {
	if !r.failed {
		r.failed = true
		r.logger.Info("refresh sem perda indisponível", "err", err)
	}
}

func (r *refresher) close() {
	if r.cap != nil {
		r.cap.Close()
		r.cap = nil
	}
}

// tilesIn lista os blocos que encostam em rc.
func (t *tileState) tilesIn(rc image.Rectangle) []int {
	var out []int
	for y := rc.Min.Y / t.size; y <= (rc.Max.Y-1)/t.size && y < t.rows; y++ {
		for x := rc.Min.X / t.size; x <= (rc.Max.X-1)/t.size && x < t.cols; x++ {
			out = append(out, y*t.cols+x)
		}
	}
	return out
}

// same informa se nenhum dos blocos mudou desde a captura.
func (t *tileState) same(tiles []int, gens []uint32) bool {
	for k, i := range tiles {
		if t.gen[i] != gens[k] {
			return false
		}
	}
	return true
}

// clean marca os blocos como sem perda: o técnico acabou de receber os
// pixels exatos deles.
func (t *tileState) clean(tiles []int) {
	for _, i := range tiles {
		t.lossy[i] = false
	}
}
