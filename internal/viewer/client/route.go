package client

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// probeTimeout é quanto esperar o endereço local responder: ele está no
// mesmo PC ou na mesma rede, então demora quase nada ou não existe.
const probeTimeout = 400 * time.Millisecond

// LocalRoute pergunta ao relay (logado, pelo endereço configurado) se ele
// tem um endereço local (RELAY_LOCAL_URL) e confere se este PC chega nele.
// Devolve nil se não houver, se não chegar ou se do outro lado não for o
// mesmo relay.
//
// A conferência (GET /api/relay, que diz a identidade do processo) vai sem
// cookie: a sessão só é mandada ao endereço local depois de saber que ele é
// este relay. Com o relay no PC de casa atrás de um túnel, conectar por ali
// tira do caminho a volta pela internet (~90 ms medidos).
func (a *API) LocalRoute(ctx context.Context) *url.URL {
	var r struct{ Local, Instance string }
	if a.do(ctx, http.MethodGet, "/api/viewer/route", nil, &r) != nil || r.Local == "" || r.Instance == "" {
		return nil
	}
	u, err := url.Parse(strings.TrimRight(r.Local, "/"))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.Host == a.Base.Host {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String()+"/api/relay", nil)
	if err != nil {
		return nil
	}
	// direto, sem proxy: um proxy corporativo não alcança o localhost do técnico
	probe := &http.Client{Transport: &http.Transport{Proxy: nil}}
	resp, err := probe.Do(req)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()
	var who struct{ Instance string }
	if resp.StatusCode != http.StatusOK || json.NewDecoder(resp.Body).Decode(&who) != nil || who.Instance != r.Instance {
		return nil
	}
	return u
}
