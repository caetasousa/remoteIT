package hub

import (
	"bytes"
	"io"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeConn grava o que foi escrito e se foi fechada.
type fakeConn struct {
	mu       sync.Mutex
	messages []string
	closed   bool
	failNext bool
}

func (c *fakeConn) WriteMessage(_ int, data []byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.messages = append(c.messages, string(data))
	return nil
}

func (c *fakeConn) NextWriter(int) (io.WriteCloser, error) {
	if c.failNext {
		return nil, io.ErrClosedPipe
	}
	return &fakeWriter{c: c}, nil
}

func (c *fakeConn) SetWriteDeadline(time.Time) error { return nil }

func (c *fakeConn) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.closed = true
	return nil
}

func (c *fakeConn) sent() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.messages...)
}

type fakeWriter struct {
	c   *fakeConn
	buf bytes.Buffer
}

func (w *fakeWriter) Write(p []byte) (int, error) { return w.buf.Write(p) }
func (w *fakeWriter) Close() error                { return w.c.WriteMessage(0, w.buf.Bytes()) }

func TestRegisterReplacesSameID(t *testing.T) {
	h := New()
	first, second := &fakeConn{}, &fakeConn{}
	old := h.Register("pc1", "PC1", "ana", "", first)
	cur := h.Register("pc1", "PC1", "ana", "", second)

	if !first.closed {
		t.Error("conexão antiga deveria ser fechada")
	}
	h.Unregister(old) // o registro antigo não pode remover o novo
	if h.Get("pc1") != cur {
		t.Error("Unregister do agente antigo removeu o novo")
	}
}

func TestAttachSendsStartAndTakeover(t *testing.T) {
	h := New()
	agentConn := &fakeConn{}
	a := h.Register("pc1", "PC1", "ana", "", agentConn)
	v1, v2 := &fakeConn{}, &fakeConn{}

	if _, err := h.Attach("pc1", "tec1", v1); err != nil {
		t.Fatal(err)
	}
	if _, err := h.Attach("pc1", "tec2", v2); err != nil {
		t.Fatal(err)
	}
	if !v1.closed {
		t.Error("técnico anterior deveria ser desconectado")
	}
	if got := h.List()[0]; !got.Busy || got.ViewedBy != "tec2" {
		t.Errorf("info = %+v", got)
	}

	h.Detach(a, v1) // técnico substituído: não pode mandar stop
	if n := strings.Count(strings.Join(agentConn.sent(), ""), `"stop"`); n != 0 {
		t.Errorf("stop enviado %d vezes por técnico substituído", n)
	}
	h.Detach(a, v2)
	msgs := agentConn.sent()
	if want := []string{`{"t":"start"}`, `{"t":"start"}`, `{"t":"stop"}`}; strings.Join(msgs, ",") != strings.Join(want, ",") {
		t.Errorf("mensagens ao agente = %v, want %v", msgs, want)
	}
}

func TestAttachOffline(t *testing.T) {
	if _, err := New().Attach("nada", "tec", &fakeConn{}); err != ErrOffline {
		t.Errorf("err = %v, want ErrOffline", err)
	}
}

func TestUnregisterClosesViewer(t *testing.T) {
	h := New()
	a := h.Register("pc1", "PC1", "ana", "", &fakeConn{})
	v := &fakeConn{}
	h.Attach("pc1", "tec", v)
	h.Unregister(a)
	if !v.closed {
		t.Error("técnico deveria ser desconectado quando o agente sai")
	}
	if len(h.List()) != 0 {
		t.Error("agente continua na lista")
	}
}

func TestForward(t *testing.T) {
	h := New()
	a := h.Register("pc1", "PC1", "ana", "", &fakeConn{})
	a.Forward(BinaryMessage, strings.NewReader("sem técnico")) // não pode travar nem falhar

	v := &fakeConn{}
	h.Attach("pc1", "tec", v)
	a.Forward(BinaryMessage, strings.NewReader("quadro"))
	if got := v.sent(); len(got) != 1 || got[0] != "quadro" {
		t.Errorf("técnico recebeu %v", got)
	}

	v.failNext = true
	a.Forward(BinaryMessage, strings.NewReader("falha"))
	if !v.closed {
		t.Error("técnico com erro de escrita deveria ser fechado")
	}
}

func TestListSortedByHost(t *testing.T) {
	h := New()
	h.Register("b", "Zeta", "", "", &fakeConn{})
	h.Register("a", "Alfa", "", "", &fakeConn{})
	l := h.List()
	if l[0].Host != "Alfa" || l[1].Host != "Zeta" {
		t.Errorf("ordem = %v", l)
	}
}

func TestKick(t *testing.T) {
	h := New()
	agentConn, v := &fakeConn{}, &fakeConn{}
	h.Register("pc1", "PC1", "ana", "", agentConn)
	if _, err := h.Attach("pc1", "tec1", v); err != nil {
		t.Fatal(err)
	}

	h.Kick("pc1")
	if !agentConn.closed {
		t.Fatal("máquina removida continuou conectada")
	}
	h.Kick("offline") // não pode entrar em pânico
}
