// Package hub mantém, em memória, os agentes conectados ao relay e o técnico
// que está vendo cada um.
//
// Regras de negócio deste contexto:
//
//   - Um ID de agente tem no máximo uma conexão: um novo registro com o mesmo
//     ID derruba o anterior (o agente reconectou).
//   - Cada agente tem no máximo um técnico. Um novo técnico assume a sessão e
//     o anterior é desconectado.
//   - Quando um técnico entra, o agente recebe "start"; quando sai, "stop".
//
// O estado é efêmero de propósito: é reconstruído sozinho quando os agentes
// reconectam e por isso não vai para o banco de dados.
package hub

import (
	"errors"
	"io"
	"sort"
	"sync"
	"time"

	"remoteit/internal/protocol"
)

// Tipos de mensagem WebSocket (os mesmos valores de gorilla/websocket).
const (
	TextMessage   = 1
	BinaryMessage = 2
	PingMessage   = 9
)

const writeTimeout = 15 * time.Second

// ErrOffline é devolvido por [Hub.Attach] quando o agente não está conectado.
var ErrOffline = errors.New("hub: agente offline")

// Conn é a parte de uma conexão WebSocket que o hub usa. *websocket.Conn a
// satisfaz; os testes usam uma implementação em memória.
type Conn interface {
	WriteMessage(messageType int, data []byte) error
	NextWriter(messageType int) (io.WriteCloser, error)
	SetWriteDeadline(t time.Time) error
	Close() error
}

// Agent é uma máquina conectada ao relay.
type Agent struct {
	ID, Host, User string
	Since          time.Time

	conn Conn
	wmu  sync.Mutex // gorilla/websocket aceita só um escritor por vez

	vmu      sync.Mutex
	viewer   Conn
	viewedBy string
}

// Send escreve uma mensagem para o agente. É seguro chamar de várias
// goroutines ao mesmo tempo.
func (a *Agent) Send(messageType int, data []byte) error {
	a.wmu.Lock()
	defer a.wmu.Unlock()
	a.conn.SetWriteDeadline(time.Now().Add(writeTimeout))
	return a.conn.WriteMessage(messageType, data)
}

// Forward repassa uma mensagem vinda do agente para o técnico atual, em
// streaming, sem copiar o quadro inteiro para a memória. Sem técnico, a
// mensagem é descartada. Se a escrita falhar, o técnico é desconectado.
//
// Só o loop de leitura do agente deve chamar Forward: ele é o único escritor
// da conexão do técnico.
func (a *Agent) Forward(messageType int, r io.Reader) {
	v := a.currentViewer()
	if v == nil {
		io.Copy(io.Discard, r)
		return
	}
	v.SetWriteDeadline(time.Now().Add(10 * time.Second))
	w, err := v.NextWriter(messageType)
	if err == nil {
		_, err = io.Copy(w, r)
		if cerr := w.Close(); err == nil {
			err = cerr
		}
	}
	if err != nil {
		io.Copy(io.Discard, r)
		v.Close()
	}
}

func (a *Agent) currentViewer() Conn {
	a.vmu.Lock()
	defer a.vmu.Unlock()
	return a.viewer
}

// Info é a visão pública de um agente, usada pela API de listagem.
type Info struct {
	ID       string    `json:"id"`
	Host     string    `json:"host"`
	User     string    `json:"user"`
	Since    time.Time `json:"since"`
	Busy     bool      `json:"busy"`
	ViewedBy string    `json:"viewedBy,omitempty"`
}

// Hub é o registro de agentes online. O valor zero não é utilizável; use [New].
type Hub struct {
	mu     sync.Mutex
	agents map[string]*Agent
	now    func() time.Time
}

// New cria um hub vazio.
func New() *Hub {
	return &Hub{agents: map[string]*Agent{}, now: time.Now}
}

// Register adiciona um agente. Se já existe um agente com o mesmo ID, a
// conexão antiga é fechada e substituída.
func (h *Hub) Register(id, host, user string, c Conn) *Agent {
	a := &Agent{ID: id, Host: host, User: user, Since: h.now(), conn: c}
	h.mu.Lock()
	old := h.agents[id]
	h.agents[id] = a
	h.mu.Unlock()
	if old != nil {
		old.conn.Close()
	}
	return a
}

// Unregister remove o agente, se ele ainda for o registrado com seu ID, e
// desconecta o técnico que o via.
func (h *Hub) Unregister(a *Agent) {
	h.mu.Lock()
	if h.agents[a.ID] == a {
		delete(h.agents, a.ID)
	}
	h.mu.Unlock()
	if v := a.currentViewer(); v != nil {
		v.Close()
	}
}

// Get devolve o agente com o ID informado ou nil se estiver offline.
func (h *Hub) Get(id string) *Agent {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.agents[id]
}

// List devolve os agentes online ordenados pelo nome da máquina.
func (h *Hub) List() []Info {
	h.mu.Lock()
	out := make([]Info, 0, len(h.agents))
	for _, a := range h.agents {
		a.vmu.Lock()
		out = append(out, Info{a.ID, a.Host, a.User, a.Since, a.viewer != nil, a.viewedBy})
		a.vmu.Unlock()
	}
	h.mu.Unlock()
	sort.Slice(out, func(i, j int) bool { return out[i].Host < out[j].Host })
	return out
}

// Attach liga o técnico v, identificado por by, ao agente id. O técnico
// anterior, se houver, é desconectado, e o agente recebe "start".
func (h *Hub) Attach(id, by string, v Conn) (*Agent, error) {
	a := h.Get(id)
	if a == nil {
		return nil, ErrOffline
	}
	a.vmu.Lock()
	if a.viewer != nil {
		a.viewer.Close()
	}
	a.viewer, a.viewedBy = v, by
	a.vmu.Unlock()
	a.Send(TextMessage, []byte(`{"t":"`+protocol.TypeStart+`"}`))
	return a, nil
}

// DisconnectUser derruba as conexões de técnico abertas por username. O fim
// normal de cada uma (Detach, auditoria) roda como em uma saída comum.
func (h *Hub) DisconnectUser(username string) {
	h.mu.Lock()
	var open []Conn
	for _, a := range h.agents {
		a.vmu.Lock()
		if a.viewer != nil && a.viewedBy == username {
			open = append(open, a.viewer)
		}
		a.vmu.Unlock()
	}
	h.mu.Unlock()
	for _, c := range open {
		c.Close()
	}
}

// Detach desliga o técnico v do agente, se v ainda for o técnico atual, e
// envia "stop" ao agente. Um técnico que já foi substituído não afeta nada.
func (h *Hub) Detach(a *Agent, v Conn) {
	a.vmu.Lock()
	current := a.viewer == v
	if current {
		a.viewer, a.viewedBy = nil, ""
	}
	a.vmu.Unlock()
	if current {
		a.Send(TextMessage, []byte(`{"t":"`+protocol.TypeStop+`"}`))
	}
}
