package machine

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"time"
)

// ErrCodeRejected é a recusa definitiva do código pelo relay (vencido,
// esgotado, revogado ou inválido): tentar de novo não adianta.
var ErrCodeRejected = errors.New("machine: código de cadastro recusado pelo relay")

var client = &http.Client{
	Timeout:   20 * time.Second,
	Transport: &http.Transport{Proxy: http.ProxyFromEnvironment},
}

// Enroll troca o código por uma credencial em POST /api/agent/enroll.
// relayURL é a URL do WebSocket (wss://dominio); a chamada vai por HTTPS no
// mesmo host.
func Enroll(ctx context.Context, relayURL, code, name string) (Credential, error) {
	u, err := url.Parse(relayURL)
	if err != nil {
		return Credential{}, err
	}
	switch u.Scheme {
	case "wss":
		u.Scheme = "https"
	case "ws":
		u.Scheme = "http"
	}
	u.Path, u.RawQuery = "/api/agent/enroll", ""
	body, _ := json.Marshal(map[string]string{"code": code, "name": name})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), bytes.NewReader(body))
	if err != nil {
		return Credential{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return Credential{}, err
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusCreated:
		var out struct{ ID, Secret string }
		if err := json.NewDecoder(resp.Body).Decode(&out); err != nil || out.ID == "" || out.Secret == "" {
			return Credential{}, fmt.Errorf("machine: resposta de cadastro inválida: %v", err)
		}
		return Credential{MachineID: out.ID, Secret: out.Secret}, nil
	case resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusBadRequest:
		return Credential{}, ErrCodeRejected
	default:
		return Credential{}, fmt.Errorf("machine: cadastro respondeu HTTP %d", resp.StatusCode)
	}
}
