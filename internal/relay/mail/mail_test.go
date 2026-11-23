package mail

import "testing"

func TestConfigFromEnv(t *testing.T) {
	base := func() map[string]string {
		return map[string]string{"SMTP_HOST": "smtp.test", "EMAIL_REMETENTE": "a@example.test", "RELAY_PUBLIC_URL": "https://r.test"}
	}
	cases := []struct {
		name        string
		set         map[string]string
		enabled, ok bool
	}{
		{"completa", nil, true, true},
		{"sem host desliga", map[string]string{"SMTP_HOST": ""}, false, true},
		{"sem URL pública", map[string]string{"RELAY_PUBLIC_URL": ""}, false, false},
		{"URL com caminho", map[string]string{"RELAY_PUBLIC_URL": "https://r.test/x"}, false, false},
		{"sem remetente", map[string]string{"EMAIL_REMETENTE": ""}, false, false},
		{"porta inválida", map[string]string{"SMTP_PORT": "abc"}, false, false},
		{"usuário sem senha", map[string]string{"SMTP_USER": "u"}, false, false},
		{"senha sem TLS", map[string]string{"SMTP_USER": "u", "SMTP_PASSWORD": "p", "SMTP_STARTTLS": "false"}, false, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			env := base()
			for k, v := range c.set {
				env[k] = v
			}
			_, enabled, err := ConfigFromEnv(func(k string) string { return env[k] })
			if enabled != c.enabled || (err == nil) != c.ok {
				t.Errorf("enabled=%v err=%v", enabled, err)
			}
		})
	}
}
