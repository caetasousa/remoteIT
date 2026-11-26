package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRelayInfoIsPublicAndStable(t *testing.T) {
	s := &server{instance: newInstance()}
	get := func() string {
		rec := httptest.NewRecorder()
		s.relayInfo(rec, httptest.NewRequest(http.MethodGet, "/api/relay", nil))
		var v map[string]string
		if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &v) != nil {
			t.Fatalf("relayInfo: %d %s", rec.Code, rec.Body)
		}
		return v["instance"]
	}
	if a, b := get(), get(); len(a) != 32 || a != b {
		t.Fatalf("instância deveria ser fixa no processo e ter 128 bits: %q %q", a, b)
	}
	if newInstance() == s.instance {
		t.Fatal("cada processo sorteia a sua")
	}
}

func TestViewerRoute(t *testing.T) {
	s := &server{instance: "abc"}
	rec := httptest.NewRecorder()
	s.viewerRoute(rec, httptest.NewRequest(http.MethodGet, "/api/viewer/route", nil))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("sem RELAY_LOCAL_URL: %d, quero 204", rec.Code)
	}
	s.LocalURL = "http://localhost:3000"
	rec = httptest.NewRecorder()
	s.viewerRoute(rec, httptest.NewRequest(http.MethodGet, "/api/viewer/route", nil))
	var v map[string]string
	json.Unmarshal(rec.Body.Bytes(), &v)
	if rec.Code != http.StatusOK || v["local"] != s.LocalURL || v["instance"] != "abc" {
		t.Fatalf("rota: %d %v", rec.Code, v)
	}
}
