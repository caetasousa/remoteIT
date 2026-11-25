// Package client fala com o relay em nome do visualizador nativo do técnico
// (cmd/viewer): login, lista de máquinas e a sessão de tela remota.
//
// É a mesma API e o mesmo protocolo do console no navegador
// (docs/API.md, docs/PROTOCOLO.md), sem nada de Windows: por isso é testado no
// Linux contra o relay e o agente de verdade (test/integration). A janela, o
// decodificador e o teclado ficam em internal/viewer/win.
package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"time"

	"remoteit/internal/protocol"
)

// SessionCookie é o cookie de sessão do relay (o mesmo do console).
const SessionCookie = "remoteit_session"

// ErrUnauthorized: sem sessão, ou ela expirou. Faça login de novo.
var ErrUnauthorized = errors.New("client: faça login")

// User é o usuário logado.
type User struct {
	ID       int64  `json:"id"`
	Username string `json:"username"`
	Role     string `json:"role"`
}

// Machine é uma linha de GET /api/agents.
type Machine struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Owner    string `json:"owner"`
	Online   bool   `json:"online"`
	User     string `json:"user"`
	Version  string `json:"version"`
	Busy     bool   `json:"busy"`
	ViewedBy string `json:"viewedBy"`
	Legacy   bool   `json:"legacy"`
}

// API é o cliente HTTP do relay, com o cookie de sessão.
type API struct {
	Base *url.URL // https://dominio
	http *http.Client
}

// New cria o cliente para base (https://dominio ou http://host:porta).
func New(base string) (*API, error) {
	u, err := url.Parse(strings.TrimRight(base, "/"))
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		return nil, fmt.Errorf("client: endereço do relay inválido: %q", base)
	}
	jar, _ := cookiejar.New(nil)
	return &API{Base: u, http: &http.Client{
		Jar: jar, Timeout: 20 * time.Second,
		Transport: &http.Transport{Proxy: http.ProxyFromEnvironment},
	}}, nil
}

// Token devolve o cookie de sessão atual ("" sem login), para guardar entre
// execuções.
func (a *API) Token() string {
	for _, c := range a.http.Jar.Cookies(a.Base) {
		if c.Name == SessionCookie {
			return c.Value
		}
	}
	return ""
}

// SetToken restaura uma sessão guardada.
func (a *API) SetToken(token string) {
	a.http.Jar.SetCookies(a.Base, []*http.Cookie{{Name: SessionCookie, Value: token, Path: "/"}})
}

func (a *API) do(ctx context.Context, method, path string, in, out any) error {
	var body io.Reader
	if in != nil {
		b, _ := json.Marshal(in)
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, a.Base.String()+path, body)
	if err != nil {
		return err
	}
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := a.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized && path != "/api/login" {
		return ErrUnauthorized
	}
	if resp.StatusCode >= 300 {
		var e struct{ Error string }
		json.NewDecoder(io.LimitReader(resp.Body, 4096)).Decode(&e)
		if e.Error == "" {
			e.Error = resp.Status
		}
		return errors.New(e.Error)
	}
	if out != nil {
		return json.NewDecoder(resp.Body).Decode(out)
	}
	return nil
}

// Login abre a sessão (usuário ou e-mail).
func (a *API) Login(ctx context.Context, username, password string) (User, error) {
	var u User
	err := a.do(ctx, http.MethodPost, "/api/login", map[string]string{"username": username, "password": password}, &u)
	return u, err
}

// Me devolve o usuário da sessão atual; ErrUnauthorized sem sessão.
func (a *API) Me(ctx context.Context) (User, error) {
	var u User
	err := a.do(ctx, http.MethodGet, "/api/me", nil, &u)
	return u, err
}

// Logout encerra a sessão no relay.
func (a *API) Logout(ctx context.Context) error {
	return a.do(ctx, http.MethodPost, "/api/logout", nil, nil)
}

// Machines lista as máquinas que o usuário acessa.
func (a *API) Machines(ctx context.Context) ([]Machine, error) {
	var list []Machine
	err := a.do(ctx, http.MethodGet, "/api/agents", nil, &list)
	return list, err
}

// UpdateOffer pergunta ao relay a versão disponível do visualizador nativo.
// available = false se o relay não publicou nenhuma.
func (a *API) UpdateOffer(ctx context.Context) (offer protocol.UpdateOffer, available bool, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, a.Base.String()+"/api/viewer/update", nil)
	if err != nil {
		return offer, false, err
	}
	resp, err := a.http.Do(req)
	if err != nil {
		return offer, false, err
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusNoContent:
		return offer, false, nil
	case http.StatusUnauthorized:
		return offer, false, ErrUnauthorized
	case http.StatusOK:
		return offer, true, json.NewDecoder(io.LimitReader(resp.Body, 1<<16)).Decode(&offer)
	}
	return offer, false, fmt.Errorf("client: oferta de atualização respondeu %s", resp.Status)
}

// DownloadUpdate baixa o visualizador nativo publicado no relay.
func (a *API) DownloadUpdate(ctx context.Context) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, a.Base.String()+"/api/viewer/update.exe", nil)
	if err != nil {
		return nil, err
	}
	c := *a.http
	c.Timeout = 5 * time.Minute
	resp, err := c.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("client: download da atualização respondeu %s", resp.Status)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 64<<20))
}
