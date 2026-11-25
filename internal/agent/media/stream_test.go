package media

import (
	"context"
	"errors"
	"image"
	"io"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"remoteit/internal/protocol"
)

type recSender struct {
	mu     sync.Mutex
	status []string
	frames chan struct{}
}

func (r *recSender) SendJSON(v any) error {
	if m, ok := v.(protocol.Message); ok {
		r.mu.Lock()
		r.status = append(r.status, m.T)
		r.mu.Unlock()
	}
	return nil
}

func (r *recSender) SendBinary([]byte) error {
	select {
	case r.frames <- struct{}{}:
	default:
	}
	return nil
}

type stillSource struct{}

func (stillSource) Next(time.Duration, int) (*Frame, error) {
	return &Frame{Codec: protocol.CodecJPEG, Keyframe: true, W: 1, H: 1, Data: []byte{1}}, nil
}
func (stillSource) SetBitrate(int) {}
func (stillSource) Name() string   { return "fake" }
func (stillSource) Close()         {}

// Simula a tela de bloqueio: as primeiras tentativas de seguir o desktop
// falham (thread presa ao Default) e a captura é negada. A transmissão tem de
// avisar "secure", trocar de thread e voltar a mandar quadros quando o desktop
// puder ser seguido.
func TestStreamRecoversWhenDesktopCanBeFollowedAgain(t *testing.T) {
	var follows atomic.Int32
	enc := Encoders{
		JPEG: func(int, int) (Source, error) {
			if follows.Load() <= 2 {
				return nil, ErrReopen
			}
			return stillSource{}, nil
		},
		FollowDesktop: func() error {
			if follows.Add(1) <= 2 {
				return errors.New("SetThreadDesktop: busy")
			}
			return nil
		},
	}
	send := &recSender{frames: make(chan struct{}, 1)}
	s := NewStream(enc, send, slog.New(slog.NewTextHandler(io.Discard, nil)))
	s.OnHello([]string{protocol.CodecNameJPEG})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { s.Run(ctx); close(done) }()

	select {
	case <-send.frames:
	case <-time.After(5 * time.Second):
		t.Fatal("nenhum quadro depois que o desktop voltou a ser seguido")
	}
	cancel()
	<-done

	send.mu.Lock()
	defer send.mu.Unlock()
	var secure, granted bool
	for _, st := range send.status {
		secure = secure || st == protocol.TypeSecure
		granted = granted || (secure && st == protocol.TypeGranted)
	}
	if !secure || !granted {
		t.Fatalf("esperava secure e depois granted, veio %v", send.status)
	}
	if n := follows.Load(); n < 3 {
		t.Fatalf("FollowDesktop chamado %d vezes, esperava ao menos 3", n)
	}
}

// ackSender registra o codec de cada quadro e confirma na hora, como o
// navegador faz, para o controle de fluxo não segurar a transmissão.
type ackSender struct {
	mu     sync.Mutex
	codecs []byte
	ack    func(seq uint32)
}

func (a *ackSender) SendJSON(any) error { return nil }

func (a *ackSender) SendBinary(b []byte) error {
	h, _, err := protocol.ParseFrame(b)
	if err != nil {
		return err
	}
	a.mu.Lock()
	a.codecs = append(a.codecs, h.Codec)
	a.mu.Unlock()
	go a.ack(h.Seq)
	return nil
}

func (a *ackSender) count(codec byte) int {
	a.mu.Lock()
	defer a.mu.Unlock()
	n := 0
	for _, c := range a.codecs {
		if c == codec {
			n++
		}
	}
	return n
}

// screenSource é uma tela 2x2 que só muda quando changing é true; parada,
// Next devolve (nil, nil) como o encoder real.
type screenSource struct {
	changing *atomic.Bool
	sent     bool
}

func (s *screenSource) Next(timeout time.Duration, flags int) (*Frame, error) {
	if s.sent && !s.changing.Load() && flags&FlagKey == 0 {
		time.Sleep(timeout)
		return nil, nil
	}
	s.sent = true
	time.Sleep(5 * time.Millisecond)
	return &Frame{Codec: protocol.CodecH264, Keyframe: flags&FlagKey != 0, W: 2, H: 2, Data: []byte{1}}, nil
}
func (*screenSource) SetBitrate(int) {}
func (*screenSource) Name() string   { return "fake" }
func (*screenSource) Close()         {}

type fakeStill struct{ captures *atomic.Int32 }

func (f fakeStill) Capture() (*image.RGBA, error) {
	f.captures.Add(1)
	return image.NewRGBA(image.Rect(0, 0, 2, 2)), nil
}
func (fakeStill) Close() {}

// runScreen transmite a tela falsa com o preset dado por d e devolve o
// registro de quadros enviados e o número de capturas sem perda.
func runScreen(t *testing.T, preset string, changing bool, d time.Duration) (*ackSender, int32) {
	t.Helper()
	var ch atomic.Bool
	ch.Store(changing)
	var captures atomic.Int32
	enc := Encoders{
		H264:  func(int, int, int) (Source, error) { return &screenSource{changing: &ch}, nil },
		JPEG:  func(int, int) (Source, error) { return nil, errors.New("sem JPEG no teste") },
		Still: func() (Still, error) { return fakeStill{&captures}, nil },
	}
	send := &ackSender{}
	s := NewStream(enc, send, slog.New(slog.NewTextHandler(io.Discard, nil)))
	send.ack = s.OnAck
	s.SetPreset(preset)
	s.OnHello([]string{protocol.CodecNameH264, protocol.CodecNameJPEG})
	ctx, cancel := context.WithTimeout(context.Background(), d)
	defer cancel()
	s.Run(ctx)
	return send, captures.Load()
}

// Tela parada no preset nativo: depois de StillDelay sai exatamente um PNG,
// e não outro enquanto a tela continuar igual.
func TestStillRefreshAfterScreenStops(t *testing.T) {
	send, captures := runScreen(t, "quality", false, StillDelay+700*time.Millisecond)
	if n := send.count(protocol.CodecPNG); n != 1 {
		t.Fatalf("esperava 1 refresh PNG, vieram %d (quadros: %v)", n, send.codecs)
	}
	if captures != 1 {
		t.Fatalf("esperava 1 captura sem perda, vieram %d", captures)
	}
}

// Tela mexendo o tempo todo: o refresh nunca dispara.
func TestNoStillRefreshWhileScreenChanges(t *testing.T) {
	send, captures := runScreen(t, "quality", true, StillDelay+500*time.Millisecond)
	if n := send.count(protocol.CodecPNG); n != 0 || captures != 0 {
		t.Fatalf("tela mudando não deveria gerar refresh (png=%d capturas=%d)", n, captures)
	}
}

// Preset com resolução reduzida: o refresh nativo não teria o tamanho do
// vídeo, então nem chega a capturar.
func TestNoStillRefreshOnScaledPreset(t *testing.T) {
	send, captures := runScreen(t, "balanced", false, StillDelay+500*time.Millisecond)
	if n := send.count(protocol.CodecPNG); n != 0 || captures != 0 {
		t.Fatalf("preset reduzido não deveria gerar refresh (png=%d capturas=%d)", n, captures)
	}
}
