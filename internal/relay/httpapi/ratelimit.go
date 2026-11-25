package httpapi

import (
	"net"
	"net/netip"
	"sync"
	"time"
)

// emailRequestsPerHour é por IP. Folgado o bastante para um escritório atrás
// de NAT; os tetos por destinatário e por dia ficam em identity.
const emailRequestsPerHour = 20

// rateLimit é uma janela fixa em memória: ao virar a janela o mapa é
// descartado inteiro, então não cresce para sempre.
type rateLimit struct {
	max    int
	window time.Duration
	now    func() time.Time

	mu    sync.Mutex
	since time.Time
	hits  map[string]int
}

func newRateLimit(limit int, window time.Duration) *rateLimit {
	return &rateLimit{max: limit, window: window, now: time.Now, hits: map[string]int{}}
}

func (l *rateLimit) allow(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if now := l.now(); now.Sub(l.since) >= l.window {
		l.since, l.hits = now, map[string]int{}
	}
	if l.hits[key] >= l.max {
		return false
	}
	l.hits[key]++
	return true
}

// clientKey tira a porta e agrupa IPv6 por /64, que é o que um único cliente
// costuma receber inteiro.
func clientKey(addr string) string {
	if host, _, err := net.SplitHostPort(addr); err == nil {
		addr = host
	}
	ip, err := netip.ParseAddr(addr)
	if err != nil {
		return addr
	}
	if ip = ip.Unmap(); ip.Is6() {
		if p, err := ip.Prefix(64); err == nil {
			return p.String()
		}
	}
	return ip.String()
}

// Limites de login: falhas, não tentativas. Quem acerta a senha nunca é
// barrado por quem errou antes no mesmo escritório, até o limite por IP.
const (
	loginFailuresPerIP   = 20
	loginFailuresPerUser = 10
	loginFailureWindow   = 15 * time.Minute
)

// failureLimit barra o login depois de muitas senhas erradas, por IP e por
// usuário: contra força bruta de um lugar só e contra um usuário-alvo
// atacado de vários lugares.
type failureLimit struct {
	byIP, byUser *rateLimit
}

func newFailureLimit() *failureLimit {
	return &failureLimit{
		byIP:   newRateLimit(loginFailuresPerIP, loginFailureWindow),
		byUser: newRateLimit(loginFailuresPerUser, loginFailureWindow),
	}
}

func (f *failureLimit) blocked(ip, user string) bool {
	return f.byIP.reached(ip) || f.byUser.reached(user)
}

func (f *failureLimit) failed(ip, user string) {
	f.byIP.allow(ip)
	f.byUser.allow(user)
}

// reached informa, sem contar, se key já esgotou a janela atual.
func (l *rateLimit) reached(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if now := l.now(); now.Sub(l.since) >= l.window {
		l.since, l.hits = now, map[string]int{}
	}
	return l.hits[key] >= l.max
}
