package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"mime"
	"net/http"
	"strconv"
	"time"

	"remoteit/internal/relay/identity"
)

// SessionCookie é o nome do cookie de sessão do console.
const SessionCookie = "remoteit_session"

type ctxKey struct{}

func userFrom(ctx context.Context) identity.User {
	u, _ := ctx.Value(ctxKey{}).(identity.User)
	return u
}

// auth exige uma sessão válida e coloca o usuário no contexto da requisição.
func (s *server) auth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie(SessionCookie)
		if err != nil {
			writeError(w, http.StatusUnauthorized, "faça login")
			return
		}
		u, err := s.Identity.Authenticate(r.Context(), c.Value)
		if errors.Is(err, identity.ErrInvalidCredentials) {
			writeError(w, http.StatusUnauthorized, "sessão expirada")
			return
		}
		if err != nil {
			s.Logger.Error("autenticação", "err", err)
			writeError(w, http.StatusInternalServerError, "erro interno")
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), ctxKey{}, u)))
	})
}

// admin é auth + papel de administrador.
func (s *server) admin(next http.Handler) http.Handler {
	return s.auth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !userFrom(r.Context()).IsAdmin() {
			writeError(w, http.StatusForbidden, "apenas administradores")
			return
		}
		next.ServeHTTP(w, r)
	}))
}

// decodeJSON exige Content-Type JSON. Formulários de outros sites não podem
// enviar esse tipo sem preflight CORS, o que reforça a proteção do cookie
// SameSite contra CSRF.
func decodeJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	if mt, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type")); mt != "application/json" {
		writeError(w, http.StatusUnsupportedMediaType, "use application/json")
		return false
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(v); err != nil {
		writeError(w, http.StatusBadRequest, "JSON inválido")
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

type credentials struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

func (s *server) login(w http.ResponseWriter, r *http.Request) {
	var in credentials
	if !decodeJSON(w, r, &in) {
		return
	}
	token, u, err := s.Identity.Login(r.Context(), in.Username, in.Password)
	if errors.Is(err, identity.ErrInvalidCredentials) {
		writeError(w, http.StatusUnauthorized, "usuário ou senha inválidos")
		return
	}
	if errors.Is(err, identity.ErrPending) {
		writeError(w, http.StatusForbidden, "conta aguardando aprovação de um administrador")
		return
	}
	if err != nil {
		s.Logger.Error("login", "err", err)
		writeError(w, http.StatusInternalServerError, "erro interno")
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name: SessionCookie, Value: token, Path: "/",
		MaxAge: int(identity.SessionTTL / time.Second),
		// Secure quando o navegador está em HTTPS: direto (r.TLS) ou atrás de
		// proxy/túnel que termina o TLS (X-Forwarded-Proto).
		Secure:   r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https",
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
	})
	writeJSON(w, http.StatusOK, u)
}

func (s *server) logout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(SessionCookie); err == nil {
		s.Identity.Logout(r.Context(), c.Value)
	}
	http.SetCookie(w, &http.Cookie{Name: SessionCookie, Path: "/", MaxAge: -1, HttpOnly: true, SameSite: http.SameSiteStrictMode})
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) me(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, userFrom(r.Context()))
}

func (s *server) listAgents(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.Hub.List())
}

func (s *server) listUsers(w http.ResponseWriter, r *http.Request) {
	users, err := s.Identity.ListUsers(r.Context())
	if err != nil {
		s.Logger.Error("listar usuários", "err", err)
		writeError(w, http.StatusInternalServerError, "erro interno")
		return
	}
	writeJSON(w, http.StatusOK, users)
}

func (s *server) createUser(w http.ResponseWriter, r *http.Request) {
	var in struct {
		credentials
		Role identity.Role `json:"role"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	u, err := s.Identity.CreateUser(r.Context(), in.Username, in.Password, in.Role)
	switch {
	case errors.Is(err, identity.ErrInvalidInput):
		writeError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, identity.ErrUserExists):
		writeError(w, http.StatusConflict, "usuário já existe")
	case err != nil:
		s.Logger.Error("cadastrar usuário", "err", err)
		writeError(w, http.StatusInternalServerError, "erro interno")
	default:
		writeJSON(w, http.StatusCreated, u)
	}
}

func (s *server) deleteUser(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "id inválido")
		return
	}
	removed, err := s.Identity.DeleteUser(r.Context(), userFrom(r.Context()), id)
	switch {
	case errors.Is(err, identity.ErrSelfDelete):
		writeError(w, http.StatusBadRequest, "não é possível remover o próprio usuário")
	case errors.Is(err, identity.ErrNotFound):
		writeError(w, http.StatusNotFound, "usuário não encontrado")
	case err != nil:
		s.Logger.Error("remover usuário", "err", err)
		writeError(w, http.StatusInternalServerError, "erro interno")
	default:
		s.Hub.DisconnectUser(removed.Username)
		w.WriteHeader(http.StatusNoContent)
	}
}

func (s *server) listAudit(w http.ResponseWriter, r *http.Request) {
	entries, err := s.Audit.Recent(r.Context(), 200)
	if err != nil {
		s.Logger.Error("listar auditoria", "err", err)
		writeError(w, http.StatusInternalServerError, "erro interno")
		return
	}
	writeJSON(w, http.StatusOK, entries)
}
