//go:build integration

package integration

import (
	"context"
	"errors"
	"net/http/httptest"
	"sync"
	"testing"

	"remoteit/internal/protocol"
	"remoteit/internal/relay/httpapi"
	viewer "remoteit/internal/viewer/client"
)

// recordingHandler guarda o que a sessão do visualizador recebeu.
type recordingHandler struct {
	mu     sync.Mutex
	frames []protocol.FrameHeader
	status []string
	stats  int
}

func (h *recordingHandler) Frame(hd protocol.FrameHeader, payload []byte) {
	h.mu.Lock()
	h.frames = append(h.frames, hd)
	h.mu.Unlock()
}
func (h *recordingHandler) Status(t string) {
	h.mu.Lock()
	h.status = append(h.status, t)
	h.mu.Unlock()
}
func (h *recordingHandler) Stats(protocol.Stats) {
	h.mu.Lock()
	h.stats++
	h.mu.Unlock()
}
func (h *recordingHandler) Region(protocol.Region) {}
func (h *recordingHandler) Cursor(int, int, bool)  {}
func (h *recordingHandler) CursorShape(string)     {}
func (h *recordingHandler) count() (frames, stats int) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.frames), h.stats
}

// O visualizador nativo usa a mesma API e o mesmo protocolo do console: o
// cliente dele, testado aqui no Linux contra o relay e o agente reais, faz
// login, lista, recebe quadros (com ack, senão o agente pararia em 2) e
// manda mouse, teclado e área de transferência.
func TestNativeViewerClient(t *testing.T) {
	e := newEnv(t)
	in := &recordingInjector{}
	e.realAgent("pc1", func(string) bool { return true }, in)

	api, err := viewer.New(e.srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	ctx := t.Context()
	if _, err := api.Login(ctx, adminUser, "errada"); err == nil {
		t.Fatal("senha errada entrou")
	}
	if _, err := api.Login(ctx, adminUser, adminPass); err != nil {
		t.Fatal(err)
	}

	// a sessão sobrevive a fechar e abrir o programa (o token é guardado)
	again, _ := viewer.New(e.srv.URL)
	again.SetToken(api.Token())
	if u, err := again.Me(ctx); err != nil || u.Username != adminUser {
		t.Fatalf("sessão restaurada: %+v, %v", u, err)
	}

	list, err := api.Machines(ctx)
	if err != nil || len(list) != 1 || list[0].ID != "pc1" || !list[0].Online {
		t.Fatalf("máquinas = %+v, %v", list, err)
	}

	// o visualizador oferece 4:4:4, AV1 e H.264; o agente de teste só tem
	// JPEG e precisa cair nele sem travar a negociação
	s, err := api.Dial(ctx, "pc1", viewer.Codecs(true, true, true), viewer.View{Preset: "quality"})
	if err != nil {
		t.Fatal(err)
	}
	h := &recordingHandler{}
	runCtx, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { done <- s.Run(runCtx, h) }()

	// mais de 2 quadros só chegam se os acks voltarem
	eventually(t, func() bool { f, st := h.count(); return f >= 5 && st >= 1 }, "quadros/stats não chegaram")
	h.mu.Lock()
	first := h.frames[0]
	granted := len(h.status) > 0 && h.status[len(h.status)-1] == protocol.TypeGranted
	h.mu.Unlock()
	if first.Codec != protocol.CodecJPEG || first.W != 640 || first.H != 360 || !granted {
		t.Errorf("primeiro quadro %+v, granted=%v", first, granted)
	}

	s.MouseMove(0.5, 0.25)
	s.Key(true, "KeyA", "a")
	s.Key(false, "KeyA", "a")
	s.Clipboard("ipconfig /all")
	eventually(t, func() bool { return len(in.received()) >= 3 && len(e.clipboard()) == 1 }, "entrada não chegou ao agente")
	got := in.received()
	if got[0].T != protocol.TypeMouseMove || got[0].X != 0.5 || got[1].Code != "KeyA" {
		t.Errorf("entrada recebida = %+v", got)
	}
	if c := e.clipboard(); c[0] != "ipconfig /all" {
		t.Errorf("área de transferência = %q", c)
	}

	cancel()
	if err := <-done; err != nil {
		t.Errorf("Run terminou com erro: %v", err)
	}

	// técnico sem acesso à máquina: o visualizador recebe o mesmo "sem acesso"
	e.newTech("bia")
	bia, _ := viewer.New(e.srv.URL)
	bia.Login(ctx, "bia", "senha-bia")
	if l, _ := bia.Machines(ctx); len(l) != 0 {
		t.Errorf("bia vê máquinas alheias: %+v", l)
	}
	if _, err := bia.Dial(ctx, "pc1", viewer.Codecs(true, false, false), viewer.View{Preset: "quality"}); err == nil {
		t.Error("bia abriu a tela de uma máquina que não é dela")
	}

	api.Logout(ctx)
	if _, err := again.Me(ctx); !errors.Is(err, viewer.ErrUnauthorized) {
		t.Errorf("depois do logout: %v, want ErrUnauthorized", err)
	}
}

// lastFrame devolve o último quadro recebido.
func (h *recordingHandler) lastFrame() (protocol.FrameHeader, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.frames) == 0 {
		return protocol.FrameHeader{}, false
	}
	return h.frames[len(h.frames)-1], true
}

// Com RELAY_LOCAL_URL, o visualizador descobre o endereço local do relay,
// confere que é o mesmo processo e abre a tela por ele (sem a volta pela
// internet). E o vídeo vem no tamanho da área que o técnico informa: o
// agente reduz a tela sintética de 640x360 para 320x180, e volta à nativa
// no "Tamanho real" (0 x 0).
func TestNativeViewerLocalRouteAndViewport(t *testing.T) {
	local := httptest.NewUnstartedServer(nil)
	localURL := "http://" + local.Listener.Addr().String()
	e := newEnv(t, func(c *httpapi.Config) { c.LocalURL = localURL })
	local.Config.Handler = e.srv.Config.Handler // o mesmo relay, por outra porta
	local.Start()
	t.Cleanup(local.Close)
	e.realAgent("pc1", func(string) bool { return true }, &recordingInjector{})

	api, _ := viewer.New(e.srv.URL)
	ctx := t.Context()
	if _, err := api.Login(ctx, adminUser, adminPass); err != nil {
		t.Fatal(err)
	}
	route := api.LocalRoute(ctx)
	if route == nil || route.String() != localURL {
		t.Fatalf("rota local = %v, quero %s", route, localURL)
	}
	s, err := api.DialVia(ctx, route, "pc1", viewer.Codecs(true, false, false), viewer.View{Preset: "quality", W: 320, H: 180})
	if err != nil {
		t.Fatalf("tela pela rota local: %v", err)
	}
	h := &recordingHandler{}
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	go s.Run(runCtx, h)
	eventually(t, func() bool { f, ok := h.lastFrame(); return ok && f.W == 320 && f.H == 180 }, "o vídeo não veio no tamanho da área (320x180)")
	s.Viewport(0, 0)
	eventually(t, func() bool { f, ok := h.lastFrame(); return ok && f.W == 640 && f.H == 360 }, "no tamanho real o vídeo deveria voltar a 640x360")
}
