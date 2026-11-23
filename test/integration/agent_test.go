//go:build integration

package integration

import (
	"bytes"
	"image/jpeg"
	"sync/atomic"
	"testing"
	"time"

	"remoteit/internal/agent/consent"
	"remoteit/internal/agent/media"
	"remoteit/internal/protocol"
)

func TestRealAgentStreamsJPEG(t *testing.T) {
	e := newEnv(t)
	e.realAgent("pc1", consent.Always, &recordingInjector{})
	v, _, err := e.login(adminUser, adminPass).viewer("pc1")
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()
	sendJSON(t, v, protocol.Message{T: protocol.TypeHello, H264: false})

	readText(t, v, protocol.TypeGranted, 3*time.Second)
	for range 5 {
		h, payload := readFrame(t, v, 3*time.Second)
		if h.Codec != protocol.CodecJPEG || !h.Keyframe || h.W != 640 || h.H != 360 {
			t.Fatalf("cabeçalho = %+v", h)
		}
		img, err := jpeg.Decode(bytes.NewReader(payload))
		if err != nil || img.Bounds().Dx() != 640 {
			t.Fatalf("JPEG inválido: %v", err)
		}
		sendJSON(t, v, protocol.Message{T: protocol.TypeAck, S: h.Seq})
	}
	if s := readText(t, v, protocol.TypeStats, 3*time.Second); s.T != protocol.TypeStats {
		t.Errorf("stats = %+v", s)
	}
}

func TestConsentDenied(t *testing.T) {
	e := newEnv(t)
	e.realAgent("pc1", func(string) bool { return false }, &recordingInjector{})
	v, _, err := e.login(adminUser, adminPass).viewer("pc1")
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()
	readText(t, v, protocol.TypeWaiting, 3*time.Second)
	readText(t, v, protocol.TypeDenied, 3*time.Second)

	v.SetReadDeadline(time.Now().Add(2 * time.Second))
	for {
		mt, _, err := v.ReadMessage()
		if err != nil {
			break // timeout: nada mais chegou, como esperado
		}
		if mt == 2 {
			t.Fatal("quadro enviado depois de consentimento negado")
		}
	}
}

// O controle de fluxo não deixa mais que MaxInflight quadros sem ACK.
func TestFlowControlWithoutAcks(t *testing.T) {
	e := newEnv(t)
	e.realAgent("pc1", consent.Always, &recordingInjector{})
	v, _, err := e.login(adminUser, adminPass).viewer("pc1")
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()
	sendJSON(t, v, protocol.Message{T: protocol.TypeHello})
	readText(t, v, protocol.TypeGranted, 3*time.Second)

	// Uma leitura com timeout corrompe a conexão no gorilla/websocket, então
	// uma goroutine lê sem parar e entrega os quadros por canal.
	seqs := make(chan uint32, 64)
	go func() {
		for {
			mt, data, err := v.ReadMessage()
			if err != nil {
				close(seqs)
				return
			}
			if h, _, err := protocol.ParseFrame(data); mt == 2 && err == nil {
				seqs <- h.Seq
			}
		}
	}()

	frames, last := 0, uint32(0)
	window := time.After(1500 * time.Millisecond) // menor que os 3 s do "ACK perdido"
collect:
	for {
		select {
		case s := <-seqs:
			frames, last = frames+1, s
		case <-window:
			break collect
		}
	}
	if frames == 0 || frames > media.MaxInflight {
		t.Fatalf("sem ACK chegaram %d quadros, want 1..%d", frames, media.MaxInflight)
	}

	sendJSON(t, v, protocol.Message{T: protocol.TypeAck, S: last})
	select {
	case s := <-seqs:
		if s <= last {
			t.Errorf("depois do ACK veio seq %d, want > %d", s, last)
		}
	case <-time.After(2 * time.Second):
		t.Error("nenhum quadro depois do ACK")
	}
}

func TestInputOnlyAfterConsent(t *testing.T) {
	e := newEnv(t)
	allow := make(chan bool)
	in := &recordingInjector{}
	e.realAgent("pc1", func(string) bool { return <-allow }, in)
	v, _, err := e.login(adminUser, adminPass).viewer("pc1")
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()
	readText(t, v, protocol.TypeWaiting, 3*time.Second)

	sendJSON(t, v, protocol.Message{T: protocol.TypeKeyDown, Code: "KeyA", Key: "a"})
	time.Sleep(200 * time.Millisecond)
	if got := in.received(); len(got) != 0 {
		t.Fatalf("input aplicado antes do consentimento: %+v", got)
	}

	allow <- true
	readText(t, v, protocol.TypeGranted, 3*time.Second)
	sendJSON(t, v, protocol.Message{T: protocol.TypeMouseMove, X: 0.5, Y: 0.25})
	sendJSON(t, v, protocol.Message{T: protocol.TypeKeyDown, Code: "KeyB", Key: "b"})
	eventually(t, func() bool { return len(in.received()) == 2 }, "input não chegou: %+v", in.received())
	got := in.received()
	if got[0].T != protocol.TypeMouseMove || got[0].X != 0.5 || got[1].Code != "KeyB" {
		t.Errorf("input = %+v", got)
	}
}

// Win+L não pode ser simulado por SendInput (o Windows recusa entrada
// sintética para trocar de estação de janela), por isso o pedido de bloqueio
// chama uma função própria em vez de passar pelo injetor de teclado — mas
// continua sujeito à mesma regra: nada antes do consentimento.
func TestLockOnlyAfterConsent(t *testing.T) {
	e := newEnv(t)
	allow := make(chan bool)
	var locks atomic.Int32
	e.realAgentWithLock("pc1", func(string) bool { return <-allow }, &recordingInjector{}, func() { locks.Add(1) })
	v, _, err := e.login(adminUser, adminPass).viewer("pc1")
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()
	readText(t, v, protocol.TypeWaiting, 3*time.Second)

	sendJSON(t, v, protocol.Message{T: protocol.TypeLock})
	time.Sleep(200 * time.Millisecond)
	if n := locks.Load(); n != 0 {
		t.Fatalf("bloqueou antes do consentimento: %d vez(es)", n)
	}

	allow <- true
	readText(t, v, protocol.TypeGranted, 3*time.Second)
	sendJSON(t, v, protocol.Message{T: protocol.TypeLock})
	eventually(t, func() bool { return locks.Load() == 1 }, "bloqueio não chegou")
}
