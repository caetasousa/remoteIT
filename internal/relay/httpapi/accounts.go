package httpapi

import (
	"errors"
	"net/http"
	"strconv"

	"remoteit/internal/relay/identity"
)

// accountRoute responde 503 quando o relay está sem SMTP.
func (s *server) accountRoute(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.Accounts == nil {
			writeError(w, http.StatusServiceUnavailable, "cadastro e recuperação por e-mail não estão configurados")
			return
		}
		next(w, r)
	}
}

// emailRoute é accountRoute + limite por IP, para o que dispara e-mail.
func (s *server) emailRoute(next http.HandlerFunc) http.HandlerFunc {
	return s.accountRoute(func(w http.ResponseWriter, r *http.Request) {
		if !s.emailLimit.allow(clientKey(s.clientAddr(r))) {
			writeError(w, http.StatusTooManyRequests, "muitas tentativas; tente de novo em uma hora")
			return
		}
		next(w, r)
	})
}

func (s *server) accountError(w http.ResponseWriter, op string, err error) {
	switch {
	case errors.Is(err, identity.ErrInvalidInput):
		writeError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, identity.ErrInvalidToken):
		writeError(w, http.StatusBadRequest, "link inválido, vencido ou já usado")
	case errors.Is(err, identity.ErrInvalidCredentials):
		// 403 e não 401: senha errada aqui não encerra a sessão do console
		writeError(w, http.StatusForbidden, "senha incorreta")
	case errors.Is(err, identity.ErrUserExists):
		writeError(w, http.StatusConflict, "usuário já existe")
	case errors.Is(err, identity.ErrEmailSet):
		writeError(w, http.StatusConflict, "a conta já tem e-mail")
	case errors.Is(err, identity.ErrBusy):
		writeError(w, http.StatusServiceUnavailable, "muitos pedidos agora; tente em instantes")
	default:
		s.Logger.Error(op, "err", err)
		writeError(w, http.StatusInternalServerError, "erro interno")
	}
}

func (s *server) accountConfig(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]bool{"accounts": s.Accounts != nil})
}

func (s *server) requestSignup(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Email string `json:"email"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	if err := s.Accounts.RequestSignup(in.Email); err != nil {
		s.accountError(w, "pedir cadastro", err)
		return
	}
	w.WriteHeader(http.StatusAccepted)
}

func (s *server) requestReset(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Email string `json:"email"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	if err := s.Accounts.RequestReset(in.Email); err != nil {
		s.accountError(w, "pedir recuperação", err)
		return
	}
	w.WriteHeader(http.StatusAccepted)
}

func (s *server) requestLink(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	if err := s.Accounts.RequestLink(userFrom(r.Context()), in.Password, in.Email); err != nil {
		s.accountError(w, "pedir vínculo de e-mail", err)
		return
	}
	w.WriteHeader(http.StatusAccepted)
}

func (s *server) completeSignup(w http.ResponseWriter, r *http.Request) {
	var in struct {
		credentials
		Token string `json:"token"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	u, err := s.Accounts.CompleteSignup(r.Context(), in.Token, in.Username, in.Password)
	if err != nil {
		s.accountError(w, "concluir cadastro", err)
		return
	}
	writeJSON(w, http.StatusCreated, u)
}

func (s *server) resetPassword(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Token    string `json:"token"`
		Password string `json:"password"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	u, err := s.Accounts.ResetPassword(r.Context(), in.Token, in.Password)
	if err != nil {
		s.accountError(w, "redefinir senha", err)
		return
	}
	// apagar a sessão não fecha um WebSocket já aberto: quem tinha a senha
	// antiga continuaria controlando a máquina
	s.Hub.DisconnectUser(u.Username)
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) confirmLink(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Token string `json:"token"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	u, err := s.Accounts.ConfirmLink(r.Context(), userFrom(r.Context()), in.Token)
	if err != nil {
		s.accountError(w, "confirmar e-mail", err)
		return
	}
	writeJSON(w, http.StatusOK, u)
}

// approveUser funciona mesmo sem SMTP: só o aviso por e-mail depende dele.
func (s *server) approveUser(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "id inválido")
		return
	}
	u, err := s.Identity.ApproveUser(r.Context(), id)
	if errors.Is(err, identity.ErrNotFound) {
		writeError(w, http.StatusNotFound, "conta pendente não encontrada")
		return
	}
	if err != nil {
		s.accountError(w, "aprovar usuário", err)
		return
	}
	if s.Accounts != nil {
		s.Accounts.NotifyApproved(u)
	}
	writeJSON(w, http.StatusOK, u)
}
