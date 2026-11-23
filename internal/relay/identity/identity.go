// Package identity cuida dos usuários do console: cadastro, papéis, login,
// sessões e, com [Accounts], criação e recuperação de conta por e-mail.
//
// As regras ficam em [Service]; a persistência fica atrás da interface
// [Repository], implementada em internal/relay/storage/postgres. Assim este
// pacote não conhece SQL.
//
// Sessões usam um token aleatório de 256 bits entregue ao navegador em um
// cookie. O banco guarda só o SHA-256 do token: um vazamento do banco não
// permite sequestrar sessões.
package identity

import (
	"context"
	"errors"
	"time"
)

// Role é o papel de um usuário.
type Role string

// Papéis disponíveis.
const (
	RoleAdmin      Role = "admin"      // gerencia usuários e vê a auditoria
	RoleTechnician Role = "technician" // só acessa máquinas
)

// Valid informa se r é um papel conhecido.
func (r Role) Valid() bool { return r == RoleAdmin || r == RoleTechnician }

// User é um usuário do console.
type User struct {
	ID           int64     `json:"id"`
	Username     string    `json:"username"`
	Email        string    `json:"email,omitempty"` // confirmado por link; vazio se não tem
	Role         Role      `json:"role"`
	Pending      bool      `json:"pending"` // aguarda aprovação: não entra
	CreatedAt    time.Time `json:"createdAt"`
	PasswordHash string    `json:"-"`
}

// IsAdmin informa se o usuário tem o papel de administrador.
func (u User) IsAdmin() bool { return u.Role == RoleAdmin }

// Session liga o hash de um token de sessão a um usuário.
type Session struct {
	TokenHash []byte
	UserID    int64
	ExpiresAt time.Time
}

// Erros devolvidos por [Service] e pelas implementações de [Repository].
var (
	ErrNotFound           = errors.New("identity: não encontrado")
	ErrUserExists         = errors.New("identity: usuário já existe")
	ErrInvalidCredentials = errors.New("identity: usuário ou senha inválidos")
	ErrInvalidInput       = errors.New("identity: dados inválidos")
	ErrPending            = errors.New("identity: conta aguardando aprovação")
	ErrInvalidToken       = errors.New("identity: link inválido, vencido ou já usado")
	ErrEmailSet           = errors.New("identity: a conta já tem e-mail")
	ErrBusy               = errors.New("identity: fila de e-mails cheia")
	ErrSelfDelete         = errors.New("identity: não é possível remover o próprio usuário")
)

// Repository é a persistência de usuários e sessões.
//
// Implementações devem devolver [ErrNotFound] quando o registro não existe e
// [ErrUserExists] ao criar um usuário com nome repetido.
type Repository interface {
	CreateUser(ctx context.Context, u User) (User, error)
	UserByUsername(ctx context.Context, username string) (User, error)
	UserByID(ctx context.Context, id int64) (User, error)
	ListUsers(ctx context.Context) ([]User, error)
	DeleteUser(ctx context.Context, id int64) error
	CountUsers(ctx context.Context) (int, error)
	ApproveUser(ctx context.Context, id int64) (User, error)
	UserByEmail(ctx context.Context, email string) (User, error)

	// CreateSession só grava se a senha ainda for passwordHash; senão devolve
	// ErrNotFound. É o que impede um login em andamento de sobreviver a uma
	// troca de senha.
	CreateSession(ctx context.Context, s Session, passwordHash string) error
	SessionByTokenHash(ctx context.Context, hash []byte) (Session, error)
	DeleteSession(ctx context.Context, hash []byte) error
	DeleteExpiredSessions(ctx context.Context, now time.Time) error
}
