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
	"remoteit/internal/relay/machines"
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

	clipMu sync.Mutex
	clips  []string // textos que os agentes reais puseram na área de transferência
}

func (e *env) clipboard() []string {
	e.clipMu.Lock()
	defer e.clipMu.Unlock()
	return append([]string(nil), e.clips...)
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
	execSQL(t, `TRUNCATE users, sessions, access_log, machines, enrollment_codes RESTART IDENTITY CASCADE`)

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	ids := identity.NewService(db)
	if _, err := ids.EnsureAdmin(ctx, adminUser, adminPass); err != nil {
		t.Fatal(err)
	}
	cfg := httpapi.Config{
		Hub:      hub.New(),
		Identity: ids,
		Audit:    audit.New(db, logger),
		Machines: machines.New(db),
		AgentKey: agentKey,
		// os testes mais antigos usam agentes legados (chave compartilhada),
		// que só administradores veem
		LegacyAgents: true,
		Static:       fstest.MapFS{"index.html": {Data: []byte("console")}},
		Health:       db.Ping,
		Logger:       logger,
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
		relayclient.Run(ctx, relayclient.Config{URL: e.wsURL(""), Host: strings.ToUpper(id), User: "ana",
			Auth: relayclient.LegacyKey{ID: id, Key: agentKey}},
			func(send media.Sender) relayclient.Handler {
				return session.New(session.Config{
					Host: id, Consent: ask, Input: in, Lock: onLock, Logger: logger,
					Clipboard: func(text string) { e.clipMu.Lock(); e.clips = append(e.clips, text); e.clipMu.Unlock() },
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
		for _, a := range admin.machines() {
			if a.ID == id && a.Online {
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

// machineView é uma linha de GET /api/agents.
type machineView struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Owner     string `json:"owner"`
	Legacy    bool   `json:"legacy"`
	Online    bool   `json:"online"`
	Version   string `json:"version"`
	Busy      bool   `json:"busy"`
	ViewedBy  string `json:"viewedBy"`
	Mine      bool   `json:"mine"`
	CanManage bool   `json:"canManage"`
}

func (c *client) machines() []machineView {
	c.e.t.Helper()
	var list []machineView
	if s := c.do("GET", "/api/agents", nil, &list); s != http.StatusOK {
		c.e.t.Fatalf("GET /api/agents: status %d", s)
	}
	return list
}

func (c *client) machine(id string) (machineView, bool) {
	for _, m := range c.machines() {
		if m.ID == id {
			return m, true
		}
	}
	return machineView{}, false
}

// ownMachine cadastra uma máquina no nome do usuário de c (pelo código de um
// link de instalação dele) e a conecta como um agente falso, até o fim do
// teste.
func (c *client) ownMachine() (id string, ag *websocket.Conn) {
	c.e.t.Helper()
	var link struct{ Path string }
	if s := c.do("POST", "/api/links", map[string]any{"edition": "portatil", "hours": 1, "uses": 1}, &link); s != http.StatusCreated {
		c.e.t.Fatalf("criar link: status %d", s)
	}
	id, secret, s := c.e.enroll(strings.TrimPrefix(link.Path, "/i/"), "PC-TESTE")
	if s != http.StatusCreated {
		c.e.t.Fatalf("cadastrar máquina: status %d", s)
	}
	ag, _, err := c.e.machineAgent(id, secret)
	if err != nil {
		c.e.t.Fatal(err)
	}
	c.e.t.Cleanup(func() { ag.Close() })
	eventually(c.e.t, func() bool { m, _ := c.machine(id); return m.Online }, "máquina não ficou online")
	return id, ag
}
