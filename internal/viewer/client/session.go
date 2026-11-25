package client

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"remoteit/internal/protocol"
)

// Handler recebe o que chega do agente. Os métodos são chamados em ordem,
// de uma goroutine só (a de [Session.Run]).
type Handler interface {
	// Frame recebe um quadro (H.264, JPEG ou PNG). O ack é enviado quando
	// Frame retorna: decodifique e desenhe antes de retornar, como o
	// navegador faz, para o controle de fluxo medir o caminho inteiro.
	Frame(h protocol.FrameHeader, payload []byte)
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
	c  *websocket.Conn
	mu sync.Mutex // gorilla/websocket aceita um escritor por vez
}

// Dial abre a sessão com a máquina id. h264 diz se este visualizador
// decodifica H.264 (sem isso o agente manda JPEG); preset é o inicial.
func (a *API) Dial(ctx context.Context, id string, h264 bool, preset string) (*Session, error) {
	u := *a.Base
	u.Scheme = map[string]string{"https": "wss", "http": "ws"}[a.Base.Scheme]
	u.Path, u.RawQuery = "/ws/viewer", url.Values{"id": {id}}.Encode()
	d := websocket.Dialer{
		Jar: a.http.Jar, Proxy: http.ProxyFromEnvironment, HandshakeTimeout: 20 * time.Second,
		ReadBufferSize: 256 << 10, WriteBufferSize: 16 << 10,
	}
	// sem cabeçalho Origin: o relay só barra Origin de outro site (CSWSH)
	c, resp, err := d.DialContext(ctx, u.String(), nil)
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
	s := &Session{c: c}
	s.send(protocol.Message{T: protocol.TypeHello, H264: h264})
	s.send(protocol.Message{T: protocol.TypeConfig, P: preset})
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
			h.Frame(hdr, payload)
			s.send(protocol.Message{T: protocol.TypeAck, S: hdr.Seq})
			continue
		}
		var m protocol.Message
		if json.Unmarshal(data, &m) != nil {
			continue
		}
		switch m.T {
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

// Preset troca a qualidade sem reconectar.
func (s *Session) Preset(p string) error {
	return s.send(protocol.Message{T: protocol.TypeConfig, P: p})
}

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
