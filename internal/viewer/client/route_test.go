package client

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// relayPair sobe o "endereço público" e o "local" de um relay falso. A rota
// do público aponta para o local; instance é o que o local diz ser.
func relayPair(t *testing.T, instance string) (pub, local *httptest.Server, localCookie *atomic.Value) {
	t.Helper()
	localCookie = &atomic.Value{}
	localCookie.Store("")
	local = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if c := r.Header.Get("Cookie"); c != "" {
			localCookie.Store(c)
		}
		json.NewEncoder(w).Encode(map[string]string{"instance": instance})
	}))
	t.Cleanup(local.Close)
	pub = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/viewer/route" {
			http.NotFound(w, r)
			return
		}
		json.NewEncoder(w).Encode(map[string]string{"local": local.URL + "/", "instance": "relay-1"})
	}))
	t.Cleanup(pub.Close)
	return pub, local, localCookie
}

func TestLocalRouteUsesOnlyTheSameRelay(t *testing.T) {
	pub, local, cookie := relayPair(t, "relay-1")
	api, _ := New(pub.URL)
	api.SetToken("segredo")
	u := api.LocalRoute(context.Background())
	if u == nil || u.String() != strings.TrimRight(local.URL, "/") {
		t.Fatalf("rota local = %v, quero %s", u, local.URL)
	}
	if c := cookie.Load().(string); c != "" {
		t.Fatalf("a conferência não pode levar a sessão: %q", c)
	}

	other, _, _ := relayPair(t, "outro-relay") // o endereço local responde, mas é outro processo
	api2, _ := New(other.URL)
	if u := api2.LocalRoute(context.Background()); u != nil {
		t.Fatalf("instância diferente: não deveria usar %v", u)
	}
}

func TestLocalRouteAbsent(t *testing.T) {
	pub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent) // relay sem RELAY_LOCAL_URL
	}))
	defer pub.Close()
	api, _ := New(pub.URL)
	if u := api.LocalRoute(context.Background()); u != nil {
		t.Fatalf("sem rota configurada: %v", u)
	}
	dead := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{"local": "http://127.0.0.1:1", "instance": "x"})
	}))
	defer dead.Close()
	api2, _ := New(dead.URL)
	if u := api2.LocalRoute(context.Background()); u != nil {
		t.Fatalf("endereço local que não responde: %v", u)
	}
}
