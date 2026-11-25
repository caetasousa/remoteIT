// Package relayclient mantém o agente conectado ao relay: abre o WebSocket
// de saída (porta 443, atravessa firewall e proxy corporativo), responde ao
// keepalive, entrega as mensagens a um [Handler] e reconecta com backoff
// exponencial quando a conexão cai.
package relayclient

import (
	"cmp"
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
	Host string // nome do computador, mostrado no console
	User string // usuário logado no Windows
	// Version é a versão deste agente, mostrada no console ("" em dev).
	Version string
	Auth    Auth
	// OnConnect, se definido, é chamado a cada conexão bem-sucedida (o
	// relay pode ter acabado de ser atualizado: hora de checar versão).
	OnConnect func()
}

// Credentials é como uma conexão se identifica: pela credencial da máquina
// cadastrada (MachineID + Secret) ou, no modo legado, pela chave
// compartilhada (LegacyID + LegacyKey).
type Credentials struct {
	MachineID, Secret   string
	LegacyID, LegacyKey string
}

// Auth fornece as credenciais de cada tentativa de conexão (implementado
// por internal/agent/machine). Rejected recebe o status HTTP quando o relay
// recusa a conexão (401 credencial inválida, 403 máquina removida).
type Auth interface {
	Credentials(ctx context.Context) (Credentials, error)
	Rejected(status int)
}

// LegacyKey é o [Auth] do modo antigo: ID e chave fixos.
type LegacyKey struct{ ID, Key string }

// Credentials devolve sempre o ID e a chave.
func (l LegacyKey) Credentials(context.Context) (Credentials, error) {
	return Credentials{LegacyID: l.ID, LegacyKey: l.Key}, nil
}

// Rejected não faz nada: a chave não muda.
func (LegacyKey) Rejected(int) {}

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
	creds, err := cfg.Auth.Credentials(ctx)
	if err != nil {
		return err
	}
	u.Path = "/ws/agent"
	q, header := url.Values{"host": {cfg.Host}, "user": {cfg.User}, "ver": {cfg.Version}}, http.Header{}
	if creds.MachineID != "" {
		q.Set("mid", creds.MachineID)
		header.Set("Authorization", "Bearer "+creds.Secret)
	} else {
		q.Set("id", creds.LegacyID)
		q.Set("key", creds.LegacyKey)
	}
	u.RawQuery = q.Encode()

	d := websocket.Dialer{
		Proxy:            http.ProxyFromEnvironment, // respeita HTTP_PROXY/HTTPS_PROXY
		HandshakeTimeout: 20 * time.Second,
		ReadBufferSize:   16 << 10,
		WriteBufferSize:  256 << 10,
	}
	c, resp, err := d.DialContext(ctx, u.String(), header)
	if err != nil {
		if resp != nil {
			cfg.Auth.Rejected(resp.StatusCode)
			return fmt.Errorf("%w (HTTP %d)", err, resp.StatusCode)
		}
		return err
	}
	defer c.Close()
	logger.Info("conectado ao relay", "id", cmp.Or(creds.MachineID, creds.LegacyID))
	if cfg.OnConnect != nil {
		cfg.OnConnect()
	}

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
