package httpapi

import (
	"testing"
	"time"
)

func TestRateLimit(t *testing.T) {
	now := time.Unix(1000, 0)
	l := newRateLimit(2, time.Hour)
	l.now = func() time.Time { return now }
	if !l.allow("a") || !l.allow("a") || l.allow("a") {
		t.Fatal("o limite de 2 não foi respeitado")
	}
	if !l.allow("b") {
		t.Fatal("um cliente bloqueou outro")
	}
	now = now.Add(time.Hour)
	if !l.allow("a") {
		t.Fatal("a janela não renovou")
	}
}

func TestClientKey(t *testing.T) {
	cases := map[string]string{
		"203.0.113.7:5123":        "203.0.113.7",
		"203.0.113.7":             "203.0.113.7",
		"[2001:db8:1:2:3::4]:443": "2001:db8:1:2::/64",
		"2001:db8:1:2:ffff::9":    "2001:db8:1:2::/64",
		"::ffff:203.0.113.7":      "203.0.113.7",
	}
	for in, want := range cases {
		if got := clientKey(in); got != want {
			t.Errorf("clientKey(%q) = %q, want %q", in, got, want)
		}
	}
}
