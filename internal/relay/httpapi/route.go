package httpapi

import (
	"crypto/rand"
	"encoding/hex"
	"net/http"
)

// Rota local do visualizador.
//
// Com o relay num PC de casa atrás de um túnel, o técnico sentado nesse mesmo
// PC (ou na mesma rede) mandava o vídeo dar a volta pela internet: sair de
// casa até a Cloudflare e voltar pelo túnel, ~90 ms a mais por quadro. O
// visualizador pergunta (logado, pelo endereço público) se há um endereço
// local, confere em /api/relay que do outro lado está este mesmo processo e,
// estando, abre a tela por ali. A conferência vem antes de qualquer cookie:
// a sessão nunca é mandada a um serviço que não seja este relay.

// newInstance sorteia a identidade do processo do relay.
func newInstance() string {
	var b [16]byte
	rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// relayInfo (público) diz quem é este relay. Não revela nada além de um
// número sorteado a cada início.
func (s *server) relayInfo(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"instance": s.instance})
}

// viewerRoute (logado) devolve o endereço local do relay, se configurado.
func (s *server) viewerRoute(w http.ResponseWriter, r *http.Request) {
	if s.LocalURL == "" {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"local": s.LocalURL, "instance": s.instance})
}
