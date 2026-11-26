package client

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"slices"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"remoteit/internal/protocol"
)

// Handler recebe o que chega do agente. Os métodos são chamados em ordem,
// de uma goroutine só (a de [Session.Run]).
type Handler interface {
	// Frame recebe um quadro (H.264, AVC444, AV1, JPEG ou PNG). O ack já
	// saiu quando Frame é chamado, como no navegador: o tempo de decodificar
	// não entra no RTT, que limita os quadros por segundo. Decodificar aqui
	// mesmo segura a leitura do próximo quadro, então os acks seguintes
	// esperam e o agente não manda mais do que este PC dá conta.
	Frame(h protocol.FrameHeader, payload []byte)
	// Region diz onde vale o próximo quadro de vídeo (veja protocol.Region).
	Region(r protocol.Region)
	// Status recebe waiting, granted, denied e secure.
	Status(t string)
	Stats(s protocol.Stats)
	Cursor(x, y int, visible bool)
	CursorShape(name string)
}

// Presets de qualidade (os mesmos do console).
var Presets = []struct{ Value, Label string }{
	{"turbo", "Ultra-rápido (1280p 60fps)"},
	{"balanced", "Equilibrado (1080p 60fps)"},
	{"quality", "Nítido (nativa 60fps)"},
	{"quality60", "Máximo (nativa 60fps alta banda)"},
}

// Session é uma conexão de tela remota com uma máquina.
type Session struct {
	c    *websocket.Conn
	mu   sync.Mutex // gorilla/websocket aceita um escritor por vez
	view View       // o último cfg mandado
}

// View é o que o técnico pede ao agente no cfg: o preset de qualidade e a
// área, em pixels físicos, onde mostra a tela (0 x 0 = resolução nativa,
// como no "Tamanho real"). Os dois vão sempre juntos: um cfg sem a área
// seria lido como "nativa".
type View struct {
	Preset string
	W, H   int
}

func (v View) message() protocol.Message {
	return protocol.Message{T: protocol.TypeConfig, P: v.Preset, VW: v.W, VH: v.H}
}

// Codecs monta a lista do hello, em ordem de preferência, a partir do que
// este PC decodifica. O visualizador nativo prefere a cor 4:4:4 (color444
// ligado, precisa de H.264), depois AV1 (menos banda) e H.264; JPEG sempre
// fecha a lista.
func Codecs(h264, av1, color444 bool) []string {
	var out []string
	if h264 && color444 {
		out = append(out, protocol.CodecNameAVC444)
	}
	if av1 {
		out = append(out, protocol.CodecNameAV1)
	}
	if h264 {
		out = append(out, protocol.CodecNameH264)
	}
	return append(out, protocol.CodecNameJPEG)
}

// hello avisa o agente do que este visualizador decodifica. H264 vai junto
// para agentes de antes da negociação, que só olham esse campo.
func hello(codecs []string) protocol.Message {
	return protocol.Message{T: protocol.TypeHello, Codecs: codecs, H264: slices.Contains(codecs, protocol.CodecNameH264)}
}

// Dial abre a sessão com a máquina id pelo endereço do relay. codecs é a
// lista de [Codecs] (sem ela o agente manda JPEG); v é o cfg inicial.
func (a *API) Dial(ctx context.Context, id string, codecs []string, v View) (*Session, error) {
	return a.DialVia(ctx, nil, id, codecs, v)
}

// DialVia abre a sessão por outro endereço do mesmo relay (base, a rota
// local de [API.LocalRoute]); nil = o endereço configurado. A sessão vai
// num cabeçalho Cookie: o cookie guardado é do endereço público.
func (a *API) DialVia(ctx context.Context, base *url.URL, id string, codecs []string, v View) (*Session, error) {
	hdr := http.Header{}
	d := websocket.Dialer{
		Jar: a.http.Jar, Proxy: http.ProxyFromEnvironment, HandshakeTimeout: 20 * time.Second,
		ReadBufferSize: 256 << 10, WriteBufferSize: 16 << 10,
	}
	if base == nil {
		base = a.Base
	} else {
		d.Jar, d.Proxy = nil, nil // direto, só com o cabeçalho abaixo
		if tok := a.Token(); tok != "" {
			hdr.Set("Cookie", (&http.Cookie{Name: SessionCookie, Value: tok}).String())
		}
		hdr.Set("Origin", base.Scheme+"://"+base.Host) // o relay exige Origin igual ao Host
	}
	u := *base
	u.Scheme = map[string]string{"https": "wss", "http": "ws"}[base.Scheme]
	u.Path, u.RawQuery = "/ws/viewer", url.Values{"id": {id}}.Encode()
	c, resp, err := d.DialContext(ctx, u.String(), hdr)
	if err != nil {
		if resp != nil {
			switch resp.StatusCode {
			case http.StatusUnauthorized:
				return nil, ErrUnauthorized
			case http.StatusNotFound:
				return nil, errors.New("máquina offline ou sem acesso")
			}
		}
		return nil, err
	}
	s := &Session{c: c, view: v}
	s.send(hello(codecs))
	s.send(v.message())
	return s, nil
}

func (s *Session) send(m protocol.Message) error {
	b, _ := json.Marshal(m)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.c.SetWriteDeadline(time.Now().Add(10 * time.Second))
	return s.c.WriteMessage(websocket.TextMessage, b)
}

// Run lê até a conexão cair ou ctx acabar.
func (s *Session) Run(ctx context.Context, h Handler) error {
	stop := context.AfterFunc(ctx, func() { s.c.Close() })
	defer stop()
	for {
		mt, data, err := s.c.ReadMessage()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		if mt == websocket.BinaryMessage {
			hdr, payload, err := protocol.ParseFrame(data)
			if err != nil {
				continue
			}
			s.send(protocol.Message{T: protocol.TypeAck, S: hdr.Seq})
			h.Frame(hdr, payload)
			continue
		}
		var m protocol.Message
		if json.Unmarshal(data, &m) != nil {
			continue
		}
		switch m.T {
		case protocol.TypeRegion:
			var r protocol.Region
			if json.Unmarshal(data, &r) == nil && r.R != nil && len(r.R)%4 == 0 {
				h.Region(r)
			}
		case protocol.TypeStats:
			var st protocol.Stats
			json.Unmarshal(data, &st)
			h.Stats(st)
		case protocol.TypeCursorPos:
			var c protocol.CursorPos
			json.Unmarshal(data, &c)
			h.Cursor(c.X, c.Y, c.Visible)
		case protocol.TypeCursorShape:
			var c protocol.CursorShape
			json.Unmarshal(data, &c)
			h.CursorShape(c.Shape)
		default:
			h.Status(m.T)
		}
	}
}

// Close encerra a sessão.
func (s *Session) Close() error { return s.c.Close() }

// Preset troca a qualidade sem reconectar (mantém a área).
func (s *Session) Preset(p string) error {
	s.mu.Lock()
	s.view.Preset = p
	m := s.view.message()
	s.mu.Unlock()
	return s.send(m)
}

// Viewport informa a área onde a tela é mostrada, em pixels físicos (0 x 0
// = nativa); o agente reduz o vídeo para caber nela. Mantém o preset.
func (s *Session) Viewport(w, h int) error {
	s.mu.Lock()
	if s.view.W == w && s.view.H == h {
		s.mu.Unlock()
		return nil
	}
	s.view.W, s.view.H = w, h
	m := s.view.message()
	s.mu.Unlock()
	return s.send(m)
}

// SetCodecs troca a lista de codecs sem reconectar: o agente reabre a
// captura com o primeiro que conseguir.
func (s *Session) SetCodecs(codecs []string) error { return s.send(hello(codecs)) }

// Keyframe pede um quadro completo (o decodificador foi reiniciado).
func (s *Session) Keyframe() error { return s.send(protocol.Message{T: protocol.TypeKeyframe}) }

// MouseMove move o mouse; x, y em [0,1] relativos à tela remota.
func (s *Session) MouseMove(x, y float64) error {
	return s.send(protocol.Message{T: protocol.TypeMouseMove, X: x, Y: y})
}

// MouseButton aperta (down) ou solta um botão: 0 esquerdo, 1 meio, 2 direito.
func (s *Session) MouseButton(down bool, b int, x, y float64) error {
	t := protocol.TypeMouseUp
	if down {
		t = protocol.TypeMouseDown
	}
	return s.send(protocol.Message{T: t, B: b, X: x, Y: y})
}

// Wheel gira a roda; d em múltiplos de 120, positivo para baixo (como o navegador).
func (s *Session) Wheel(d int) error { return s.send(protocol.Message{T: protocol.TypeWheel, D: d}) }

// Key aperta ou solta uma tecla. code e key seguem o KeyboardEvent do
// navegador (veja internal/viewer/keys).
func (s *Session) Key(down bool, code, key string) error {
	t := protocol.TypeKeyUp
	if down {
		t = protocol.TypeKeyDown
	}
	return s.send(protocol.Message{T: t, Code: code, Key: key})
}

// Lock bloqueia a tela remota (como Win+L).
func (s *Session) Lock() error { return s.send(protocol.Message{T: protocol.TypeLock}) }

// Clipboard põe text na área de transferência do PC remoto.
func (s *Session) Clipboard(text string) error {
	if len(text) > protocol.MaxClipboard {
		return errors.New("texto grande demais para a área de transferência")
	}
	return s.send(protocol.Message{T: protocol.TypeClipboard, Text: text})
}
