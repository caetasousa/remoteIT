package httpapi

import (
	"net/http"
	"time"

	"github.com/gorilla/websocket"

	"remoteit/internal/relay/audit"
)

const (
	// pongWait é quanto o relay espera por qualquer tráfego do agente antes
	// de considerá-lo morto; pingEvery mantém a conexão viva em proxies e
	// firewalls que derrubam conexões ociosas.
	pongWait  = 60 * time.Second
	pingEvery = 20 * time.Second
	maxField  = 64
)

func (s *server) agentWS(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	id := q.Get("id")
	if !equalSecret(q.Get("key"), s.AgentKey) || id == "" || len(id) > maxField {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	c, err := s.upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer c.Close()

	a := s.Hub.Register(id, truncate(q.Get("host")), truncate(q.Get("user")), c)
	s.Logger.Info("agente conectado", "id", a.ID, "host", a.Host, "user", a.User)
	defer func() {
		s.Hub.Unregister(a)
		s.Logger.Info("agente desconectado", "id", a.ID)
	}()

	c.SetReadDeadline(time.Now().Add(pongWait))
	c.SetPongHandler(func(string) error { return c.SetReadDeadline(time.Now().Add(pongWait)) })
	done := make(chan struct{})
	defer close(done)
	go func() {
		t := time.NewTicker(pingEvery)
		defer t.Stop()
		for {
			select {
			case <-done:
				return
			case <-t.C:
				if a.Send(websocket.PingMessage, nil) != nil {
					return
				}
			}
		}
	}()

	for {
		mt, rd, err := c.NextReader()
		if err != nil {
			return
		}
		c.SetReadDeadline(time.Now().Add(pongWait))
		a.Forward(mt, rd)
	}
}

func (s *server) viewerWS(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	id := r.URL.Query().Get("id")
	agent := s.Hub.Get(id)
	if agent == nil {
		http.Error(w, "agente offline", http.StatusNotFound)
		return
	}
	c, err := s.upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer c.Close()

	end, err := s.Audit.Begin(r.Context(), audit.Entry{
		UserID: user.ID, Username: user.Username,
		AgentID: agent.ID, Host: agent.Host, RemoteAddr: s.clientAddr(r),
	})
	if err != nil {
		s.Logger.Error("auditoria indisponível: acesso recusado", "err", err)
		c.WriteControl(websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.CloseInternalServerErr, "auditoria indisponível"),
			time.Now().Add(time.Second))
		return
	}
	defer end()

	a, err := s.Hub.Attach(id, user.Username, c)
	if err != nil {
		return
	}
	s.Logger.Info("técnico conectado", "agent", id, "user", user.Username, "from", s.clientAddr(r))
	defer func() {
		s.Hub.Detach(a, c)
		s.Logger.Info("técnico desconectado", "agent", id, "user", user.Username)
	}()
	// a senha pode ter sido trocada entre a autenticação e o Attach, quando
	// DisconnectUser ainda não enxergava esta conexão
	if cookie, err := r.Cookie(SessionCookie); err != nil {
		return
	} else if _, err := s.Identity.Authenticate(r.Context(), cookie.Value); err != nil {
		return
	}

	for {
		mt, data, err := c.ReadMessage()
		if err != nil {
			return
		}
		if mt == websocket.TextMessage && a.Send(mt, data) != nil {
			return
		}
	}
}

// clientAddr devolve o endereço do técnico: o cabeçalho confiável, se
// configurado e presente, senão o endereço da conexão TCP.
func (s *server) clientAddr(r *http.Request) string {
	if s.ClientIPHeader != "" {
		if ip := r.Header.Get(s.ClientIPHeader); ip != "" {
			return truncate(ip)
		}
	}
	return r.RemoteAddr
}

func truncate(s string) string {
	if len(s) > maxField {
		return s[:maxField]
	}
	return s
}
