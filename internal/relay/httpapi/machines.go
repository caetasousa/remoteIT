package httpapi

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"remoteit/internal/relay/agentbin"
	"remoteit/internal/relay/identity"
	"remoteit/internal/relay/machines"
)

// Limites por IP das rotas públicas do agente.
const (
	enrollsPerHour      = 120 // um escritório implantando dezenas de PCs de uma vez
	linkDownloadsPerDay = 200
)

func actorOf(u identity.User) machines.Actor { return machines.Actor{ID: u.ID, Admin: u.IsAdmin()} }

// machineView é uma máquina vista pelo console: o que está no banco mais o
// estado ao vivo do hub.
type machineView struct {
	machines.Machine
	Online    bool       `json:"online"`
	User      string     `json:"user,omitempty"` // usuário logado no Windows
	Version   string     `json:"version,omitempty"`
	Since     *time.Time `json:"since,omitempty"`
	Busy      bool       `json:"busy"`
	ViewedBy  string     `json:"viewedBy,omitempty"`
	Mine      bool       `json:"mine"`
	CanManage bool       `json:"canManage"`
}

func (s *server) listMachines(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r.Context())
	list, err := s.Machines.Visible(r.Context(), actorOf(u))
	if err != nil {
		s.Logger.Error("listar máquinas", "err", err)
		writeError(w, http.StatusInternalServerError, "erro interno")
		return
	}
	online := map[string]int{}
	live := s.Hub.List()
	for i, a := range live {
		online[a.ID] = i
	}
	out := make([]machineView, 0, len(list))
	for _, m := range list {
		v := machineView{Machine: m, Mine: m.OwnerID == u.ID, CanManage: u.IsAdmin() || m.OwnerID == u.ID}
		if i, ok := online[m.ID]; ok {
			a := live[i]
			v.Online, v.User, v.Version, v.Since, v.Busy, v.ViewedBy = true, a.User, a.Version, &a.Since, a.Busy, a.ViewedBy
		}
		out = append(out, v)
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *server) machineError(w http.ResponseWriter, op string, err error) {
	switch {
	case errors.Is(err, machines.ErrNotFound):
		writeError(w, http.StatusNotFound, "máquina não encontrada")
	case errors.Is(err, machines.ErrForbidden):
		writeError(w, http.StatusForbidden, "só o dono da máquina ou um administrador")
	case errors.Is(err, machines.ErrInvalidInput):
		writeError(w, http.StatusBadRequest, "dados inválidos")
	default:
		s.Logger.Error(op, "err", err)
		writeError(w, http.StatusInternalServerError, "erro interno")
	}
}

func (s *server) removeMachine(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := s.Machines.Remove(r.Context(), actorOf(userFrom(r.Context())), id); err != nil {
		s.machineError(w, "remover máquina", err)
		return
	}
	s.Hub.Kick(id)
	w.WriteHeader(http.StatusNoContent)
}

// ---------- links de instalação ----------

func (s *server) createLink(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Edition string `json:"edition"`
		Hours   int    `json:"hours"`
		Uses    int    `json:"uses"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	code, c, err := s.Machines.NewLink(r.Context(), actorOf(userFrom(r.Context())), in.Edition,
		time.Duration(in.Hours)*time.Hour, in.Uses)
	if err != nil {
		s.machineError(w, "criar link", err)
		return
	}
	// só o caminho: o console monta a URL com a própria origem, e o relay
	// não precisa confiar no cabeçalho Host
	writeJSON(w, http.StatusCreated, struct {
		machines.Code
		Path string `json:"path"`
	}{c, "/i/" + code})
}

func (s *server) listLinks(w http.ResponseWriter, r *http.Request) {
	list, err := s.Machines.Links(r.Context(), actorOf(userFrom(r.Context())))
	if err != nil {
		s.machineError(w, "listar links", err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *server) revokeLink(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "id inválido")
		return
	}
	if err := s.Machines.RevokeLink(r.Context(), actorOf(userFrom(r.Context())), id); err != nil {
		s.machineError(w, "revogar link", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ---------- download do agente ----------

// exeFor devolve o caminho do .exe da edição e o nome que o navegador vê.
func (s *server) exeFor(edition string) (path, name string) {
	if edition == machines.EditionService {
		return s.AgentServiceExe, "suporte-ti-servico.exe"
	}
	return s.AgentExe, "suporte-ti.exe"
}

// stamped devolve o .exe da edição com code gravado no espaço reservado, e o
// nome com que ele é servido. status é o código HTTP do erro, se houver.
func (s *server) stamped(edition, code string) (exe []byte, name string, status int, err error) {
	path, name := s.exeFor(edition)
	raw, err := os.ReadFile(path)
	if err != nil {
		s.Logger.Error("ler agente", "path", path, "err", err)
		return nil, "", http.StatusServiceUnavailable, errors.New("agente indisponível no servidor")
	}
	out, err := agentbin.Stamp(raw, code)
	if err != nil {
		s.Logger.Error("gravar código no agente: refaça o build.sh", "path", path, "err", err)
		return nil, "", http.StatusServiceUnavailable, errors.New("agente do servidor desatualizado")
	}
	return out, name, 0, nil
}

// serveStamped envia o .exe da edição com code gravado no espaço reservado;
// asZip o embrulha num .zip (veja zip.go).
func (s *server) serveStamped(w http.ResponseWriter, edition, code string, asZip bool) {
	out, name, status, err := s.stamped(edition, code)
	if err != nil {
		writeError(w, status, err.Error())
		return
	}
	s.writeDownload(w, name, out, asZip)
}

// download serve o .exe da edição marcado com um código novo do técnico
// logado: as máquinas em que ele rodar ficam no nome desse técnico.
func (s *server) download(edition string, asZip bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		code, _, err := s.Machines.NewDownloadCode(r.Context(), actorOf(userFrom(r.Context())), edition)
		if err != nil {
			s.machineError(w, "código de download", err)
			return
		}
		s.serveStamped(w, edition, code, asZip)
	})
}

// linkCode confere o limite por IP e o código do link; ok = false se já
// respondeu (limite, código inválido ou erro).
func (s *server) linkCode(w http.ResponseWriter, r *http.Request) (string, machines.Code, bool) {
	if !s.linkLimit.allow(clientKey(s.clientAddr(r))) {
		writeError(w, http.StatusTooManyRequests, "muitas tentativas; tente mais tarde")
		return "", machines.Code{}, false
	}
	code := r.PathValue("code")
	c, err := s.Machines.Lookup(r.Context(), code)
	if errors.Is(err, machines.ErrInvalidCode) {
		http.Error(w, "Link de instalação inválido, vencido ou já usado. Peça um novo ao técnico.", http.StatusNotFound)
		return "", machines.Code{}, false
	}
	if err != nil {
		s.Logger.Error("link de instalação", "err", err)
		http.Error(w, "erro interno", http.StatusInternalServerError)
		return "", machines.Code{}, false
	}
	return code, c, true
}

// installLink é público: quem tem o link baixa o .exe já marcado com o
// código do link, sem precisar de conta. No navegador vem num .zip; o .exe
// cru fica em /i/{code}/exe, para o script do PowerShell.
func (s *server) installLink(asZip bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if code, c, ok := s.linkCode(w, r); ok {
			s.serveStamped(w, c.Edition, code, asZip)
		}
	}
}

// installScript é público: o mesmo link, como script PowerShell para
// "irm <link>/ps | iex". Instala sem passar pelo navegador (sem aviso de
// download, sem "(1)" no nome, sem cache): o script baixa o .exe marcado,
// confere o SHA-256 calculado aqui e instala. Veja installps.go.
func (s *server) installScript(w http.ResponseWriter, r *http.Request) {
	code, c, ok := s.linkCode(w, r)
	if !ok {
		return
	}
	exe, name, status, err := s.stamped(c.Edition, code)
	if err != nil {
		writeError(w, status, err.Error())
		return
	}
	sum := sha256.Sum256(exe)
	script, err := installPS(installPSData{
		ExeURL:  s.publicBase(r) + "/i/" + code + "/exe",
		SHA256:  hex.EncodeToString(sum[:]),
		Name:    name,
		Service: c.Edition == machines.EditionService,
	})
	if err != nil {
		s.Logger.Error("script de instalação", "err", err)
		http.Error(w, "erro interno", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store, private")
	io.WriteString(w, script)
}

// publicBase é a origem pública do relay (RELAY_PUBLIC_URL). Sem ela, vem da
// requisição: o script só baixa do mesmo lugar de onde ele próprio veio.
func (s *server) publicBase(r *http.Request) string {
	if s.PublicURL != "" {
		return strings.TrimRight(s.PublicURL, "/")
	}
	scheme := "http" // localhost e testes, sem TLS na frente
	if r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https" {
		scheme = "https" // o cloudflared manda X-Forwarded-Proto
	}
	return scheme + "://" + r.Host
}

// ---------- cadastro do agente ----------

func (s *server) enrollAgent(w http.ResponseWriter, r *http.Request) {
	if !s.enrollLimit.allow(clientKey(s.clientAddr(r))) {
		writeError(w, http.StatusTooManyRequests, "muitas tentativas")
		return
	}
	var in struct {
		Code string `json:"code"`
		Name string `json:"name"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	m, secret, err := s.Machines.Enroll(r.Context(), in.Code, truncate(in.Name))
	switch {
	case errors.Is(err, machines.ErrInvalidCode):
		writeError(w, http.StatusForbidden, "código de cadastro inválido, vencido ou esgotado")
	case errors.Is(err, machines.ErrInvalidInput):
		writeError(w, http.StatusBadRequest, "dados inválidos")
	case err != nil:
		s.Logger.Error("cadastrar máquina", "err", err)
		writeError(w, http.StatusInternalServerError, "erro interno")
	default:
		s.Logger.Info("máquina cadastrada", "id", m.ID, "name", m.Name, "owner", m.Owner)
		writeJSON(w, http.StatusCreated, map[string]string{"id": m.ID, "secret": secret})
	}
}
