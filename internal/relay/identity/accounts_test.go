package identity

import "testing"

func TestNormalizeEmail(t *testing.T) {
	cases := map[string]string{
		" Maria.Silva+ti@Example.COM ": "maria.silva+ti@example.com",
		"sem-arroba":                   "",
		"a@b":                          "",
		"a..b@example.com":             "",
		"a@example.com,b@example.com":  "",
		"a@example.com\r\nBcc: x@y.io": "",
		"Nome <a@example.com>":         "",
		"<script>@example.com":         "",
		"Kim@example.com":              "", // Kelvin: viraria "kim" no ToLower
	}
	for in, want := range cases {
		got, err := NormalizeEmail(in)
		if got != want || (err == nil) != (want != "") {
			t.Errorf("NormalizeEmail(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
}
