package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/gorilla/websocket"

	"remoteit/internal/relay/audit"
	"remoteit/internal/relay/machines"
)

const (
	// pongWait é quanto o relay espera por qualquer tráfego do agente antes
	// de considerá-lo morto; pingEvery mantém a conexão viva em proxies e
	// firewalls que derrubam conexões ociosas.
	pongWait  = 60 * time.Second
	pingEvery = 20 * time.Second
	maxField  = 64
)

// agentWS aceita duas formas de agente:
//
//   - cadastrado: ?mid=<id da máquina> e "Authorization: Bearer <credencial>".
//     401 = máquina desconhecida ou credencial errada (o agente pode se
//     cadastrar de novo); 403 = máquina removida (não deve voltar sozinha);
//   - legado: ?id=<nome do computador>&key=<chave compartilhada>, só com
//     LegacyAgents ligado. Aparece apenas para administradores.
func (s *server) agentWS(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	host, user := truncate(q.Get("host")), truncate(q.Get("user"))
	var id string
	if mid := q.Get("mid"); mid != "" {
		if !s.authMachine(w, r, mid) {
			return
		}
		id = mid
		s.Machines.Seen(r.Context(), id, host)
	} else {
		id = q.Get("id")
		if !s.LegacyAgents || s.AgentKey == "" || !equalSecret(q.Get("key"), s.AgentKey) || !machines.ValidLegacyID(id) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		switch err := s.Machines.SeenLegacy(r.Context(), id, host); {
		case errors.Is(err, machines.ErrRevoked):
			http.Error(w, "máquina removida", http.StatusForbidden)
			return
		case err != nil:
			s.Logger.Error("registrar agente legado", "err", err)
			http.Error(w, "erro interno", http.StatusInternalServerError)
			return
		}
	}
	c, err := s.upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer c.Close()

	a := s.Hub.Register(id, host, user, truncate(q.Get("ver")), c)
	s.Logger.Info("agente conectado", "id", a.ID, "host", a.Host, "user", a.User, "versao", truncate(q.Get("ver")))
	defer func() {
		s.Hub.Unregister(a)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		s.Machines.Seen(ctx, a.ID, a.Host) // "visto por último" = quando caiu
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
	switch ok, err := s.Machines.CanAccess(r.Context(), actorOf(user), id); {
	case err != nil:
		s.Logger.Error("conferir acesso à máquina", "err", err)
		http.Error(w, "erro interno", http.StatusInternalServerError)
		return
	case !ok:
		// mesma resposta para "não existe" e "não é sua": não revela máquinas alheias
		http.Error(w, "máquina não encontrada", http.StatusNotFound)
		return
	}
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

// authMachine confere a credencial da máquina mid (Authorization: Bearer).
// Responde e devolve false se não passar: 401 credencial inválida (o agente
// pode se cadastrar de novo), 403 máquina removida.
func (s *server) authMachine(w http.ResponseWriter, r *http.Request, mid string) bool {
	secret := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	switch err := s.Machines.Authenticate(r.Context(), mid, secret); {
	case errors.Is(err, machines.ErrUnauthorized):
		http.Error(w, "credencial da máquina inválida", http.StatusUnauthorized)
	case errors.Is(err, machines.ErrRevoked):
		http.Error(w, "máquina removida", http.StatusForbidden)
	case err != nil:
		s.Logger.Error("autenticar máquina", "err", err)
		http.Error(w, "erro interno", http.StatusInternalServerError)
	default:
		return true
	}
	return false
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
