// Package relayclient mantém o agente conectado ao relay: abre o WebSocket
// de saída (porta 443, atravessa firewall e proxy corporativo), responde ao
// keepalive, entrega as mensagens a um [Handler] e reconecta com backoff
// exponencial quando a conexão cai.
package relayclient

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"remoteit/internal/agent/media"
	"remoteit/internal/protocol"
)

const readTimeout = 60 * time.Second

// Config identifica o agente perante o relay.
type Config struct {
	URL  string // wss://dominio
	Key  string // chave compartilhada com o relay
	ID   string // identificador estável da máquina
	Host string
	User string
}

// Handler trata as mensagens de uma conexão. Um Handler novo é criado a cada
// conexão e fechado quando ela cai.
type Handler interface {
	Handle(m protocol.Message)
	Close()
}

// Run conecta e reconecta até ctx ser cancelado. A espera entre tentativas
// começa em 1 s, dobra até 30 s e volta a 1 s depois de uma conexão que durou
// mais de 30 s.
func Run(ctx context.Context, cfg Config, newHandler func(media.Sender) Handler, logger *slog.Logger) {
	backoff := time.Second
	for ctx.Err() == nil {
		start := time.Now()
		err := connect(ctx, cfg, newHandler, logger)
		if ctx.Err() != nil {
			return
		}
		logger.Info("desconectado do relay", "err", err, "retry", backoff)
		if time.Since(start) > 30*time.Second {
			backoff = time.Second
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		backoff = min(backoff*2, 30*time.Second)
	}
}

func connect(ctx context.Context, cfg Config, newHandler func(media.Sender) Handler, logger *slog.Logger) error {
	u, err := url.Parse(cfg.URL)
	if err != nil {
		return err
	}
	u.Path = "/ws/agent"
	u.RawQuery = url.Values{"id": {cfg.ID}, "host": {cfg.Host}, "user": {cfg.User}, "key": {cfg.Key}}.Encode()

	d := websocket.Dialer{
		Proxy:            http.ProxyFromEnvironment, // respeita HTTP_PROXY/HTTPS_PROXY
		HandshakeTimeout: 20 * time.Second,
		ReadBufferSize:   16 << 10,
		WriteBufferSize:  256 << 10,
	}
	c, resp, err := d.DialContext(ctx, u.String(), nil)
	if err != nil {
		if resp != nil {
			return fmt.Errorf("%w (HTTP %d)", err, resp.StatusCode)
		}
		return err
	}
	defer c.Close()
	logger.Info("conectado ao relay", "id", cfg.ID)

	stop := context.AfterFunc(ctx, func() { c.Close() })
	defer stop()

	conn := &sender{c: c}
	c.SetReadDeadline(time.Now().Add(readTimeout))
	c.SetPingHandler(func(m string) error {
		c.SetReadDeadline(time.Now().Add(readTimeout))
		return c.WriteControl(websocket.PongMessage, []byte(m), time.Now().Add(5*time.Second))
	})

	h := newHandler(conn)
	defer h.Close()
	for {
		_, data, err := c.ReadMessage()
		if err != nil {
			return err
		}
		c.SetReadDeadline(time.Now().Add(readTimeout))
		var m protocol.Message
		if json.Unmarshal(data, &m) == nil {
			h.Handle(m)
		}
	}
}

// sender serializa as escritas: gorilla/websocket aceita um escritor por vez.
type sender struct {
	mu sync.Mutex
	c  *websocket.Conn
}

func (s *sender) write(mt int, b []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.c.SetWriteDeadline(time.Now().Add(15 * time.Second))
	return s.c.WriteMessage(mt, b)
}

func (s *sender) SendJSON(v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return s.write(websocket.TextMessage, b)
}

func (s *sender) SendBinary(b []byte) error { return s.write(websocket.BinaryMessage, b) }
