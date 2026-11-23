package identity

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"
)

// SessionTTL é a validade de uma sessão de login.
const SessionTTL = 12 * time.Hour

var usernameRe = regexp.MustCompile(`^[a-z0-9._-]{3,32}$`)

// dummyHash é comparado quando o usuário não existe, para que o tempo de
// resposta não revele quais nomes de usuário estão cadastrados.
var dummyHash = sync.OnceValue(func() []byte {
	h, _ := bcrypt.GenerateFromPassword([]byte("remoteit-dummy-password"), bcrypt.DefaultCost)
	return h
})

// Service aplica as regras de identidade sobre um [Repository].
type Service struct {
	repo Repository
	now  func() time.Time
}

// NewService cria o serviço sobre repo.
func NewService(repo Repository) *Service {
	return &Service{repo: repo, now: time.Now}
}

// ValidateNewUser confere nome, senha e papel antes do cadastro.
//
// Nome: 3 a 32 caracteres entre a-z, 0-9, ".", "_" e "-". Senha: 8 a 72
// bytes (72 é o limite do bcrypt; além disso o resto seria ignorado).
func ValidateNewUser(username, password string, role Role) error {
	switch {
	case !usernameRe.MatchString(username):
		return fmt.Errorf("%w: nome deve ter 3-32 caracteres a-z 0-9 . _ -", ErrInvalidInput)
	case !role.Valid():
		return fmt.Errorf("%w: papel desconhecido %q", ErrInvalidInput, role)
	}
	return validatePassword(password)
}

func validatePassword(password string) error {
	if len(password) < 8 || len(password) > 72 {
		return fmt.Errorf("%w: senha deve ter entre 8 e 72 caracteres", ErrInvalidInput)
	}
	return nil
}

func hashPassword(password string) (string, error) {
	h, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	return string(h), err
}

func newToken() string {
	raw := make([]byte, 32)
	rand.Read(raw)
	return base64.RawURLEncoding.EncodeToString(raw)
}

// CreateUser valida os dados, gera o hash da senha e grava o usuário.
func (s *Service) CreateUser(ctx context.Context, username, password string, role Role) (User, error) {
	if err := ValidateNewUser(username, password, role); err != nil {
		return User{}, err
	}
	hash, err := hashPassword(password)
	if err != nil {
		return User{}, err
	}
	return s.repo.CreateUser(ctx, User{Username: username, Role: role, PasswordHash: hash})
}

// EnsureAdmin cria o administrador inicial quando ainda não há nenhum usuário.
// Com usuários já cadastrados não faz nada e devolve created=false: a senha
// inicial vinda do ambiente deixa de valer depois do primeiro uso.
func (s *Service) EnsureAdmin(ctx context.Context, username, password string) (created bool, err error) {
	n, err := s.repo.CountUsers(ctx)
	if err != nil || n > 0 {
		return false, err
	}
	if _, err := s.CreateUser(ctx, username, password, RoleAdmin); err != nil {
		return false, err
	}
	return true, nil
}

// Login confere as credenciais (login é o nome ou o e-mail) e abre uma sessão. Devolve o token, que deve
// ir para o navegador e nunca ser gravado em claro.
// Credenciais erradas devolvem sempre [ErrInvalidCredentials], exista ou não
// o usuário. [ErrPending] só aparece para quem acertou a senha.
func (s *Service) Login(ctx context.Context, login, password string) (string, User, error) {
	find := s.repo.UserByUsername
	if strings.Contains(login, "@") { // nomes de usuário não aceitam "@"
		email, err := NormalizeEmail(login)
		if err == nil {
			login, find = email, s.repo.UserByEmail
		}
	}
	u, err := find(ctx, login)
	if errors.Is(err, ErrNotFound) {
		bcrypt.CompareHashAndPassword(dummyHash(), []byte(password))
		return "", User{}, ErrInvalidCredentials
	}
	if err != nil {
		return "", User{}, err
	}
	if bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(password)) != nil {
		return "", User{}, ErrInvalidCredentials
	}
	if u.Pending {
		return "", User{}, ErrPending
	}

	token := newToken()
	now := s.now()
	s.repo.DeleteExpiredSessions(ctx, now)
	err = s.repo.CreateSession(ctx, Session{TokenHash: hashToken(token), UserID: u.ID, ExpiresAt: now.Add(SessionTTL)}, u.PasswordHash)
	if errors.Is(err, ErrNotFound) {
		return "", User{}, ErrInvalidCredentials
	}
	if err != nil {
		return "", User{}, err
	}
	return token, u, nil
}

// Authenticate devolve o dono de uma sessão válida. Sessões inexistentes ou
// vencidas devolvem [ErrInvalidCredentials].
func (s *Service) Authenticate(ctx context.Context, token string) (User, error) {
	if token == "" {
		return User{}, ErrInvalidCredentials
	}
	sess, err := s.repo.SessionByTokenHash(ctx, hashToken(token))
	if errors.Is(err, ErrNotFound) {
		return User{}, ErrInvalidCredentials
	}
	if err != nil {
		return User{}, err
	}
	if !s.now().Before(sess.ExpiresAt) {
		s.repo.DeleteSession(ctx, sess.TokenHash)
		return User{}, ErrInvalidCredentials
	}
	u, err := s.repo.UserByID(ctx, sess.UserID)
	if errors.Is(err, ErrNotFound) || (err == nil && u.Pending) {
		return User{}, ErrInvalidCredentials
	}
	return u, err
}

// ApproveUser libera uma conta pendente; devolve [ErrNotFound] se não houver.
func (s *Service) ApproveUser(ctx context.Context, id int64) (User, error) {
	return s.repo.ApproveUser(ctx, id)
}

// Logout encerra a sessão do token. Um token desconhecido não é erro.
func (s *Service) Logout(ctx context.Context, token string) error {
	return s.repo.DeleteSession(ctx, hashToken(token))
}

// ListUsers devolve todos os usuários.
func (s *Service) ListUsers(ctx context.Context) ([]User, error) {
	return s.repo.ListUsers(ctx)
}

// DeleteUser remove o usuário id e suas sessões e devolve quem foi removido,
// para quem chama derrubar as conexões abertas dele. Ninguém remove a si
// mesmo, o que evita ficar sem nenhum administrador por engano.
func (s *Service) DeleteUser(ctx context.Context, actor User, id int64) (User, error) {
	if actor.ID == id {
		return User{}, ErrSelfDelete
	}
	u, err := s.repo.UserByID(ctx, id)
	if err != nil {
		return User{}, err
	}
	return u, s.repo.DeleteUser(ctx, id)
}

func hashToken(token string) []byte {
	h := sha256.Sum256([]byte(token))
	return h[:]
}
