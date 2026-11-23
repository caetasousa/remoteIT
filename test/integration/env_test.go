//go:build integration

package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/gorilla/websocket"
	"github.com/jackc/pgx/v5"

	"remoteit/internal/agent/consent"
	"remoteit/internal/agent/media"
	"remoteit/internal/agent/media/jpeg"
	"remoteit/internal/agent/relayclient"
	"remoteit/internal/agent/session"
	"remoteit/internal/protocol"
	"remoteit/internal/relay/audit"
	"remoteit/internal/relay/httpapi"
	"remoteit/internal/relay/hub"
	"remoteit/internal/relay/identity"
	"remoteit/internal/relay/storage/postgres"
)

const (
	agentKey  = "chave-de-teste-1234567890"
	adminUser = "admin"
	adminPass = "senha-do-admin"
)

// env é um relay completo rodando em memória sobre um banco limpo.
type env struct {
	t   *testing.T
	srv *httptest.Server
	db  *postgres.DB
}

// newEnv sobe o relay; opts ajustam a configuração antes de criá-lo.
func newEnv(t *testing.T, opts ...func(*httpapi.Config)) *env {
	t.Helper()
	ctx := t.Context()
	db, err := postgres.Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	if err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	execSQL(t, `TRUNCATE users, sessions, access_log RESTART IDENTITY CASCADE`)

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	ids := identity.NewService(db)
	if _, err := ids.EnsureAdmin(ctx, adminUser, adminPass); err != nil {
		t.Fatal(err)
	}
	cfg := httpapi.Config{
		Hub:      hub.New(),
		Identity: ids,
		Audit:    audit.New(db, logger),
		AgentKey: agentKey,
		Static:   fstest.MapFS{"index.html": {Data: []byte("console")}},
		Health:   db.Ping,
		Logger:   logger,
	}
	for _, o := range opts {
		o(&cfg)
	}
	srv := httptest.NewServer(httpapi.New(cfg))
	t.Cleanup(srv.Close)
	return &env{t: t, srv: srv, db: db}
}

// execSQL roda SQL direto no banco de teste, fora da aplicação.
func execSQL(t *testing.T, sql string) {
	t.Helper()
	conn, err := pgx.Connect(context.Background(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(context.Background())
	if _, err := conn.Exec(context.Background(), sql); err != nil {
		t.Fatal(err)
	}
}

// client é um navegador simulado: guarda o cookie de sessão.
type client struct {
	e    *env
	http *http.Client
}

func (e *env) anonymous() *client {
	jar, _ := cookiejar.New(nil)
	return &client{e: e, http: &http.Client{Jar: jar, Timeout: 5 * time.Second}}
}

func (e *env) login(user, pass string) *client {
	e.t.Helper()
	c := e.anonymous()
	if status := c.do("POST", "/api/login", map[string]string{"username": user, "password": pass}, nil); status != 200 {
		e.t.Fatalf("login %s: status %d", user, status)
	}
	return c
}

// do faz uma requisição JSON e decodifica a resposta em out (se não for nil).
func (c *client) do(method, path string, body, out any) int {
	c.e.t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, c.e.srv.URL+path, rd)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		c.e.t.Fatal(err)
	}
	defer resp.Body.Close()
	if out != nil && resp.StatusCode < 300 {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			c.e.t.Fatal(err)
		}
	}
	return resp.StatusCode
}

func (e *env) wsURL(path string) string {
	return "ws" + strings.TrimPrefix(e.srv.URL, "http") + path
}

// viewer abre o WebSocket de técnico com o cookie do client e Origin do
// próprio relay, como um navegador faria.
func (c *client) viewer(agentID string) (*websocket.Conn, *http.Response, error) {
	return c.viewerWithOrigin(agentID, c.e.srv.URL)
}

func (c *client) viewerWithOrigin(agentID, origin string) (*websocket.Conn, *http.Response, error) {
	return c.viewerWithHeader(agentID, http.Header{"Origin": {origin}})
}

func (c *client) viewerWithHeader(agentID string, h http.Header) (*websocket.Conn, *http.Response, error) {
	if h.Get("Origin") == "" {
		h.Set("Origin", c.e.srv.URL)
	}
	d := websocket.Dialer{Jar: c.http.Jar, HandshakeTimeout: 5 * time.Second}
	return d.Dial(c.e.wsURL("/ws/viewer?id="+agentID), h)
}

// rawAgent conecta um agente falso que fala o protocolo direto no socket.
func (e *env) rawAgent(id, key string) (*websocket.Conn, error) {
	c, _, err := websocket.DefaultDialer.Dial(e.wsURL("/ws/agent?id="+id+"&host="+strings.ToUpper(id)+"&user=ana&key="+key), nil)
	return c, err
}

// realAgent roda o agente de verdade (relayclient + session + media com
// JPEG e tela sintética) contra o relay, até o fim do teste.
func (e *env) realAgent(id string, ask consent.Asker, in *recordingInjector) {
	e.realAgentWithLock(id, ask, in, nil)
}

// realAgentWithLock é realAgent com uma função de bloqueio de tela própria,
// para testar o pedido de lock sem depender da API real do Windows.
func (e *env) realAgentWithLock(id string, ask consent.Asker, in *recordingInjector, onLock func()) {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	e.t.Cleanup(func() { cancel(); <-done })
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	enc := media.Encoders{JPEG: jpeg.New}
	go func() {
		defer close(done)
		relayclient.Run(ctx, relayclient.Config{URL: e.wsURL(""), Key: agentKey, ID: id, Host: strings.ToUpper(id), User: "ana"},
			func(send media.Sender) relayclient.Handler {
				return session.New(session.Config{
					Host: id, Consent: ask, Input: in, Lock: onLock, Logger: logger,
					NewStream: func(s media.Sender) session.Stream { return media.NewStream(enc, s, logger) },
				}, send)
			}, logger)
	}()
	e.waitAgent(id)
}

func (e *env) waitAgent(id string) {
	e.t.Helper()
	admin := e.login(adminUser, adminPass)
	eventually(e.t, func() bool {
		var list []hub.Info
		admin.do("GET", "/api/agents", nil, &list)
		for _, a := range list {
			if a.ID == id {
				return true
			}
		}
		return false
	}, "agente %s não apareceu na lista", id)
}

// recordingInjector faz o papel do SendInput: registra o que chegou.
type recordingInjector struct {
	mu     sync.Mutex
	events []protocol.Message
}

func (r *recordingInjector) Handle(m protocol.Message) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, m)
}

func (r *recordingInjector) received() []protocol.Message {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]protocol.Message(nil), r.events...)
}

func eventually(t *testing.T, cond func() bool, format string, args ...any) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf(format, args...)
}

// readText lê mensagens até chegar uma de texto com o tipo t, ignorando as
// demais (quadros, stats). Falha depois de timeout.
func readText(t *testing.T, c *websocket.Conn, typ string, timeout time.Duration) protocol.Message {
	t.Helper()
	c.SetReadDeadline(time.Now().Add(timeout))
	defer c.SetReadDeadline(time.Time{})
	for {
		mt, data, err := c.ReadMessage()
		if err != nil {
			t.Fatalf("esperando %q: %v", typ, err)
		}
		var m protocol.Message
		if mt == websocket.TextMessage && json.Unmarshal(data, &m) == nil && m.T == typ {
			return m
		}
	}
}

// readFrame lê até o próximo quadro binário.
func readFrame(t *testing.T, c *websocket.Conn, timeout time.Duration) (protocol.FrameHeader, []byte) {
	t.Helper()
	c.SetReadDeadline(time.Now().Add(timeout))
	defer c.SetReadDeadline(time.Time{})
	for {
		mt, data, err := c.ReadMessage()
		if err != nil {
			t.Fatalf("esperando quadro: %v", err)
		}
		if mt == websocket.BinaryMessage {
			h, payload, err := protocol.ParseFrame(data)
			if err != nil {
				t.Fatal(err)
			}
			return h, payload
		}
	}
}

func sendJSON(t *testing.T, c *websocket.Conn, m protocol.Message) {
	t.Helper()
	if err := c.WriteJSON(m); err != nil {
		t.Fatal(err)
	}
}
