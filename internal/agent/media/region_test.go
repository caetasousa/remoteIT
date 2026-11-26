package media

import (
	"bytes"
	"context"
	"image"
	"image/png"
	"io"
	"log/slog"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"remoteit/internal/protocol"
)

// tiledSource imita a fonte de GPU: quadro 128x64 em dois blocos de 64 px.
// Com blink ligado, o bloco 0 muda em todo quadro (o cursor de texto
// piscando) e o bloco 1 fica parado; desligado, a tela inteira para.
type tiledSource struct {
	blink   *atomic.Bool
	reads   *atomic.Int32
	speeds  *[]int
	encTime time.Duration
	started bool
	dirty   [2]bool
}

func (s *tiledSource) Next(timeout time.Duration, flags int) (*Frame, error) {
	key := flags&FlagKey != 0 || !s.started
	if s.started && !key && flags&FlagRepeat == 0 && !s.blink.Load() {
		time.Sleep(timeout)
		return nil, nil
	}
	if !s.started {
		s.dirty = [2]bool{true, true}
	} else if flags&FlagRepeat == 0 {
		s.dirty[0] = true
	}
	s.started = true
	time.Sleep(4 * time.Millisecond)
	return &Frame{Codec: protocol.CodecH264, Keyframe: key, W: 128, H: 64, Data: []byte{1}}, nil
}
func (*tiledSource) SetBitrate(int)                {}
func (*tiledSource) Name() string                  { return "fake GPU" }
func (*tiledSource) Close()                        {}
func (*tiledSource) Tiles() (int, int, int)        { return 2, 1, 64 }
func (*tiledSource) Native() (int, int)            { return 1920, 1080 }
func (s *tiledSource) EncodeTime() time.Duration   { return s.encTime }
func (s *tiledSource) CursorPos() (int, int, bool) { return 0, 0, true }
func (s *tiledSource) SetSpeed(l int) bool         { *s.speeds = append(*s.speeds, l); return true }

func (s *tiledSource) Dirty(dst []bool) {
	for i := range dst {
		dst[i] = dst[i] || s.dirty[i]
	}
	s.dirty = [2]bool{}
}

func (s *tiledSource) ReadStill(r image.Rectangle) (*image.RGBA, error) {
	s.reads.Add(1)
	img := image.NewRGBA(image.Rect(0, 0, r.Dx(), r.Dy()))
	for i := range img.Pix {
		img.Pix[i] = 200
	}
	return img, nil
}

// event é uma mensagem mandada ao técnico, na ordem.
type event struct {
	region *protocol.Region
	stats  *protocol.Stats
	hdr    protocol.FrameHeader
	data   []byte
	binary bool
}

// logSender registra tudo o que o agente manda e confirma os quadros na
// hora, como o técnico faz.
type logSender struct {
	mu  sync.Mutex
	ev  []event
	ack func(uint32)
}

func (l *logSender) SendJSON(v any) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	switch m := v.(type) {
	case protocol.Region:
		l.ev = append(l.ev, event{region: &m})
	case protocol.Stats:
		l.ev = append(l.ev, event{stats: &m})
	}
	return nil
}

func (l *logSender) SendBinary(b []byte) error {
	h, p, err := protocol.ParseFrame(b)
	if err != nil {
		return err
	}
	l.mu.Lock()
	l.ev = append(l.ev, event{hdr: h, data: bytes.Clone(p), binary: true})
	l.mu.Unlock()
	go l.ack(h.Seq)
	return nil
}

func (l *logSender) events() []event {
	l.mu.Lock()
	defer l.mu.Unlock()
	return slices.Clone(l.ev)
}

func runTiled(t *testing.T, src *tiledSource, d time.Duration, setup func(*Stream)) *logSender {
	t.Helper()
	enc := Encoders{H264: func(Params) (Source, error) { return src, nil }}
	send := &logSender{}
	s := NewStream(enc, send, slog.New(slog.NewTextHandler(io.Discard, nil)))
	send.ack = s.OnAck
	s.OnHello([]string{protocol.CodecNameH264})
	if setup != nil {
		setup(s)
	}
	ctx, cancel := context.WithTimeout(context.Background(), d)
	defer cancel()
	s.Run(ctx)
	return send
}

func newTiled(blink bool) *tiledSource {
	var b atomic.Bool
	b.Store(blink)
	var speeds []int
	return &tiledSource{blink: &b, reads: new(atomic.Int32), speeds: &speeds, encTime: 3 * time.Millisecond}
}

// Cursor de texto piscando num canto: o resto da tela recebe o retalho sem
// perda e, depois dele, os quadros de vídeo só valem no bloco que muda.
func TestRegionKeepsSettledAreaLossless(t *testing.T) {
	send := runTiled(t, newTiled(true), StillDelay+700*time.Millisecond, nil)
	ev := send.events()

	patch := -1
	for i, e := range ev {
		if e.binary && e.hdr.Codec == protocol.CodecPNGRect {
			patch = i
			break
		}
	}
	if patch < 0 {
		t.Fatal("o bloco parado deveria ter recebido um retalho sem perda")
	}
	rects, err := protocol.ParsePNGRects(ev[patch].data)
	if err != nil || len(rects) != 1 || rects[0].X != 64 || rects[0].Y != 0 {
		t.Fatalf("retalho deveria ser só o bloco 1 em (64,0): %+v %v", rects, err)
	}
	img, err := png.Decode(bytes.NewReader(rects[0].PNG))
	if err != nil || img.Bounds().Dx() != 64 || img.Bounds().Dy() != 64 {
		t.Fatalf("PNG do retalho: %v %v", img, err)
	}
	if h := ev[patch].hdr; h.W != 128 || h.H != 64 {
		t.Fatalf("o cabeçalho do retalho leva o tamanho do quadro inteiro: %dx%d", h.W, h.H)
	}

	// antes do retalho, a tela inteira tinha perda: sem Region
	for _, e := range ev[:patch] {
		if e.region != nil {
			t.Fatalf("Region antes de existir área sem perda: %+v", e.region)
		}
	}
	// depois: cada quadro de vídeo vem logo após a Region dele, que só
	// libera o bloco 0
	videos := 0
	for i, e := range ev[patch+1:] {
		if !e.binary || e.hdr.Codec != protocol.CodecH264 {
			continue
		}
		videos++
		prev := ev[patch+i]
		if prev.region == nil || prev.region.S != e.hdr.Seq {
			t.Fatalf("quadro %d sem a Region dele logo antes (anterior: %+v)", e.hdr.Seq, prev)
		}
		if !slices.Equal(prev.region.R, []int{0, 0, 64, 64}) {
			t.Fatalf("Region deveria ser só o bloco que muda: %v", prev.region.R)
		}
	}
	if videos == 0 {
		t.Fatal("nenhum quadro de vídeo depois do retalho")
	}
}

// Tela inteira parada: um PNG só (CodecPNG), lido da própria fonte.
func TestStillRefreshWholeScreenFromSource(t *testing.T) {
	src := newTiled(false)
	send := runTiled(t, src, StillDelay+600*time.Millisecond, nil)
	full, rects := 0, 0
	for _, e := range send.events() {
		if e.binary && e.hdr.Codec == protocol.CodecPNG {
			full++
		}
		if e.binary && e.hdr.Codec == protocol.CodecPNGRect {
			rects++
		}
	}
	if full != 1 || rects != 0 {
		t.Fatalf("esperava 1 PNG inteiro e nenhum retalho: png=%d retalhos=%d", full, rects)
	}
	if n := src.reads.Load(); n != 1 {
		t.Fatalf("esperava 1 leitura da imagem, vieram %d", n)
	}
}

// A área do técnico só reabre a fonte quando o tamanho de saída muda de
// verdade: maior que a tela (já nativa) ou com o mesmo resultado, não.
func TestViewportReopensOnlyWhenOutputChanges(t *testing.T) {
	src := newTiled(false)
	var mu sync.Mutex
	var opened []Params
	enc := Encoders{H264: func(p Params) (Source, error) {
		mu.Lock()
		opened = append(opened, p)
		mu.Unlock()
		src.started = false
		return src, nil
	}}
	send := &logSender{}
	s := NewStream(enc, send, slog.New(slog.NewTextHandler(io.Discard, nil)))
	send.ack = s.OnAck
	s.OnHello([]string{protocol.CodecNameH264})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { s.Run(ctx); close(done) }()
	step := func(w, h int) {
		s.SetViewport(w, h)
		time.Sleep(150 * time.Millisecond)
	}
	time.Sleep(150 * time.Millisecond)
	step(2560, 1440) // maior que a tela de 1920x1080: continua nativa
	step(1280, 720)  // menor: reabre reduzida
	step(1300, 720)  // dá o mesmo 1280x720: não reabre
	step(0, 0)       // "Tamanho real": volta à nativa
	cancel()
	<-done
	mu.Lock()
	defer mu.Unlock()
	want := []Params{{0, 0, 60, 12000}, {1280, 720, 60, 12000}, {0, 0, 60, 12000}}
	if len(opened) != len(want) {
		t.Fatalf("aberturas = %+v, quero %+v", opened, want)
	}
	for i := range want {
		if opened[i].MaxW != want[i].MaxW || opened[i].MaxH != want[i].MaxH || opened[i].FPS != want[i].FPS {
			t.Fatalf("abertura %d = %+v, quero %+v", i, opened[i], want[i])
		}
	}
}

// A telemetria leva o tempo de codificação, e um encoder que não dá conta
// do quadro no tempo dele passa a priorizar velocidade.
func TestStatsAndSlowEncoder(t *testing.T) {
	src := newTiled(true)
	src.encTime = 15 * time.Millisecond // > 60% de 16,7 ms (60 fps)
	send := runTiled(t, src, 3300*time.Millisecond, nil)
	var st *protocol.Stats
	for _, e := range send.events() {
		if e.stats != nil {
			st = e.stats
		}
	}
	if st == nil || st.EncodeUs != 15000 {
		t.Fatalf("stats deveria trazer o tempo de codificação (15 ms): %+v", st)
	}
	if len(*src.speeds) == 0 || (*src.speeds)[0] != 1 {
		t.Fatalf("encoder lento por 2 s deveria passar ao nível 1 de velocidade: %v", *src.speeds)
	}
}
