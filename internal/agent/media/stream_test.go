package media

import (
	"context"
	"errors"
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
	s.OnHello(false)

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
