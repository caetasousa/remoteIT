package media

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"remoteit/internal/protocol"
)

// codecSource manda quadros sempre (tela mexendo) com o codec dado. Com
// failAfter > 0, o encoder quebra depois desse número de quadros, como um
// driver que trava no meio da sessão.
type codecSource struct {
	codec     byte
	failAfter int
	n         int
}

func (c *codecSource) Next(time.Duration, int) (*Frame, error) {
	c.n++
	if c.failAfter > 0 && c.n > c.failAfter {
		return nil, errors.New("driver travou")
	}
	time.Sleep(2 * time.Millisecond)
	return &Frame{Codec: c.codec, Keyframe: true, W: 2, H: 2, Data: []byte{1}}, nil
}
func (*codecSource) SetBitrate(int) {}
func (*codecSource) Name() string   { return "fake" }
func (*codecSource) Close()         {}

// fakeEncoders tem os quatro codecs; fail lista os que não abrem nesta
// "máquina" e broken os que abrem mas quebram depois de 3 quadros.
func fakeEncoders(opened *[]string, fail, broken map[string]bool) Encoders {
	mk := func(name string, codec byte) (Source, error) {
		*opened = append(*opened, name)
		if fail[name] {
			return nil, errors.New("sem encoder " + name)
		}
		s := &codecSource{codec: codec}
		if broken[name] {
			s.failAfter = 3
		}
		return s, nil
	}
	return Encoders{
		AVC444: func(int, int) (Source, error) { return mk("avc444", protocol.CodecAVC444) },
		AV1:    func(int, int, int) (Source, error) { return mk("av1", protocol.CodecAV1) },
		H264:   func(int, int, int) (Source, error) { return mk("h264", protocol.CodecH264) },
		JPEG:   func(int, int) (Source, error) { return mk("jpeg", protocol.CodecJPEG) },
	}
}

// streamUntil transmite até sair um quadro do codec want (ou 3 s) e devolve
// a ordem em que os encoders foram abertos.
func streamUntil(t *testing.T, enc Encoders, opened *[]string, preset string, hello []string, want byte) {
	t.Helper()
	send := &ackSender{}
	s := NewStream(enc, send, slog.New(slog.NewTextHandler(io.Discard, nil)))
	send.ack = s.OnAck
	s.SetPreset(preset)
	s.OnHello(hello)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { s.Run(ctx); close(done) }()
	defer func() { cancel(); <-done }()
	deadline := time.Now().Add(3 * time.Second)
	for send.count(want) == 0 {
		if time.Now().After(deadline) {
			t.Fatalf("nenhum quadro do codec %d; quadros %v, aberturas %v", want, send.codecs, *opened)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

var everything = []string{"avc444", "av1", "h264", "jpeg"}

func TestCodecFollowsTechnicianPreference(t *testing.T) {
	cases := []struct {
		name   string
		preset string
		hello  []string
		fail   map[string]bool
		want   byte
		opened []string
	}{
		{"4:4:4 na resolução nativa", "quality", everything, nil, protocol.CodecAVC444, []string{"avc444"}},
		// 4:4:4 só na nativa: com redução, segue a lista
		{"preset reduzido pula o 4:4:4", "balanced", everything, nil, protocol.CodecAV1, []string{"av1"}},
		{"sem GPU AV1 cai para H.264", "balanced", everything, map[string]bool{"av1": true}, protocol.CodecH264, []string{"av1", "h264"}},
		{"navegador sem AV1", "quality", []string{"h264", "jpeg"}, nil, protocol.CodecH264, []string{"h264"}},
		{"nada de vídeo: JPEG", "quality", everything, map[string]bool{"avc444": true, "av1": true, "h264": true},
			protocol.CodecJPEG, []string{"avc444", "av1", "h264", "jpeg"}},
		// a ordem é do técnico, não do agente
		{"AV1 antes do 4:4:4 se o técnico pedir", "quality", []string{"av1", "avc444", "jpeg"}, nil, protocol.CodecAV1, []string{"av1"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var opened []string
			streamUntil(t, fakeEncoders(&opened, c.fail, nil), &opened, c.preset, c.hello, c.want)
			if len(opened) < len(c.opened) || !slices.Equal(opened[:len(c.opened)], c.opened) {
				t.Errorf("aberturas = %v, want começo %v", opened, c.opened)
			}
		})
	}
}

// Um encoder que quebra no meio da sessão fica marcado e a transmissão segue
// no próximo codec da lista, sem voltar a tentar o quebrado.
func TestBrokenEncoderFallsBackAndStaysOff(t *testing.T) {
	var opened []string
	enc := fakeEncoders(&opened, nil, map[string]bool{"avc444": true})
	streamUntil(t, enc, &opened, "quality", everything, protocol.CodecAV1)
	if opened[0] != "avc444" || opened[1] != "av1" {
		t.Fatalf("aberturas = %v", opened)
	}
	for _, o := range opened[1:] {
		if o == "avc444" {
			t.Fatalf("reabriu o encoder quebrado: %v", opened)
		}
	}
}

// Um hello novo reabre a fonte: é assim que o técnico desliga o 4:4:4 ou
// desiste de um codec que o decodificador dele não aguentou.
func TestNewHelloSwitchesCodec(t *testing.T) {
	var opened []string
	var n atomic.Int32
	enc := fakeEncoders(&opened, nil, nil)
	send := &ackSender{}
	s := NewStream(enc, send, slog.New(slog.NewTextHandler(io.Discard, nil)))
	send.ack = func(seq uint32) { n.Add(1); s.OnAck(seq) }
	s.OnHello(everything)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { s.Run(ctx); close(done) }()
	defer func() { cancel(); <-done }()

	wait := func(codec byte) {
		t.Helper()
		deadline := time.Now().Add(3 * time.Second)
		for send.count(codec) == 0 {
			if time.Now().After(deadline) {
				t.Fatalf("nenhum quadro do codec %d (%v)", codec, send.codecs)
			}
			time.Sleep(5 * time.Millisecond)
		}
	}
	wait(protocol.CodecAVC444)
	s.OnHello([]string{"h264", "jpeg"})
	wait(protocol.CodecH264)
}
