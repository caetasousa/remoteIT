package httpapi

import (
	"encoding/json"
	"net/http"
	"os"

	"remoteit/internal/protocol"
	"remoteit/internal/relay/machines"
)

// Atualização automática (docs/adr/0011-atualizacao-automatica.md). O relay
// só repassa o que o build.sh publicou: a versão, o hash e a assinatura do
// update.json, e o .exe correspondente. Quem confere é o agente, com a chave
// pública embutida nele; o relay não tem a chave privada.

func validEdition(e string) bool {
	return e == machines.EditionPortable || e == machines.EditionService
}

// EditionViewer é o visualizador nativo do técnico no update.json.
const EditionViewer = "tecnico"

func (s *server) readManifest() (protocol.UpdateManifest, bool) {
	var m protocol.UpdateManifest
	if s.UpdateManifest == "" {
		return m, false
	}
	b, err := os.ReadFile(s.UpdateManifest)
	if err != nil || json.Unmarshal(b, &m) != nil || m.Version == "" {
		return m, false
	}
	return m, true
}

// updateOffer: GET /api/agent/update?mid=&edition= → a versão disponível da
// edição, com hash e assinatura. 204 se não há atualização publicada.
func (s *server) updateOffer(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	edition := q.Get("edition")
	if !validEdition(edition) {
		http.Error(w, "edição inválida", http.StatusBadRequest)
		return
	}
	if !s.authMachine(w, r, q.Get("mid")) {
		return
	}
	m, ok := s.readManifest()
	f, has := m.Files[edition]
	if !ok || !has {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, protocol.UpdateOffer{Version: m.Version, UpdateFile: f})
}

// updateDownload: GET /api/agent/update/{edition}?mid= → o .exe da edição,
// sem código gravado (a máquina já tem credencial).
func (s *server) updateDownload(w http.ResponseWriter, r *http.Request) {
	edition := r.PathValue("edition")
	if !validEdition(edition) {
		http.Error(w, "edição inválida", http.StatusBadRequest)
		return
	}
	if !s.authMachine(w, r, r.URL.Query().Get("mid")) {
		return
	}
	path, _ := s.exeFor(edition)
	w.Header().Set("Cache-Control", "no-store, private")
	w.Header().Set("Content-Type", "application/vnd.microsoft.portable-executable")
	http.ServeFile(w, r, path)
}

// viewerOffer: GET /api/viewer/update (técnico logado) → a versão disponível
// do visualizador nativo. 204 se não há.
func (s *server) viewerOffer(w http.ResponseWriter, r *http.Request) {
	m, ok := s.readManifest()
	f, has := m.Files[EditionViewer]
	if !ok || !has {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, protocol.UpdateOffer{Version: m.Version, UpdateFile: f})
}

// viewerExe serve o visualizador nativo (download pelo console e atualização).
func (s *server) viewerExe(w http.ResponseWriter, r *http.Request) {
	if s.ViewerExe == "" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Cache-Control", "no-store, private")
	w.Header().Set("Content-Type", "application/vnd.microsoft.portable-executable")
	w.Header().Set("Content-Disposition", `attachment; filename="remoteit-tecnico.exe"`)
	http.ServeFile(w, r, s.ViewerExe)
}
