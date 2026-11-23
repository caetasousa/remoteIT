package identity

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
)

// TokenKind diz o que um link enviado por e-mail permite fazer. Um token de um
// tipo nunca vale para outro.
type TokenKind string

// Tipos de link.
const (
	TokenSignup TokenKind = "signup" // criar uma conta
	TokenReset  TokenKind = "reset"  // trocar a senha
	TokenLink   TokenKind = "link"   // associar e-mail a uma conta que não tem
)

// Token é um link de uso único, como fica guardado.
type Token struct {
	Hash      []byte
	Kind      TokenKind
	Email     string
	UserID    int64 // 0 no cadastro: a conta ainda não existe
	CreatedAt time.Time
	ExpiresAt time.Time
}

// TokenLimits limita a emissão de links de um mesmo tipo.
type TokenLimits struct {
	PerEmailHour int
	// PerDay protege a cota do provedor SMTP, que pode ser dividida com outros
	// sistemas. É por tipo para que uma enxurrada de cadastros não bloqueie a
	// recuperação de senha.
	PerDay int
}

type tokenPolicy struct {
	ttl    time.Duration
	limits TokenLimits
}

var tokenPolicies = map[TokenKind]tokenPolicy{
	TokenSignup: {24 * time.Hour, TokenLimits{PerEmailHour: 3, PerDay: 30}},
	TokenReset:  {time.Hour, TokenLimits{PerEmailHour: 3, PerDay: 30}},
	TokenLink:   {time.Hour, TokenLimits{PerEmailHour: 3, PerDay: 20}},
}

// NoticeKind é o assunto de um e-mail do sistema.
type NoticeKind string

// E-mails enviados pelo sistema.
const (
	NoticeSignup   NoticeKind = "signup"
	NoticeReset    NoticeKind = "reset"
	NoticeLink     NoticeKind = "link"
	NoticePending  NoticeKind = "pending"
	NoticeApproved NoticeKind = "approved"
)

// Notice é o conteúdo de um e-mail, sem apresentação.
type Notice struct {
	Kind     NoticeKind
	To       string
	Username string
	Email    string // em NoticePending, o e-mail de quem se cadastrou
	Token    string // em claro: só existe dentro do link enviado
}

// Mailer entrega um [Notice], implementado em internal/relay/mail.
type Mailer interface {
	Send(ctx context.Context, n Notice) error
}

// AccountRepository é a persistência de cadastro e recuperação.
//
// CompleteSignup, ResetPassword e LinkEmail consomem o token e gravam a
// mudança na mesma transação, devolvendo [ErrInvalidToken] quando ele não vale.
type AccountRepository interface {
	UserByEmail(ctx context.Context, email string) (User, error)
	IssueToken(ctx context.Context, t Token, l TokenLimits) (issued bool, err error)
	TokenByHash(ctx context.Context, kind TokenKind, hash []byte, now time.Time) (Token, error)
	CompleteSignup(ctx context.Context, hash []byte, now time.Time, u User) (User, error)
	ResetPassword(ctx context.Context, hash []byte, now time.Time, passwordHash string) (User, error)
	LinkEmail(ctx context.Context, hash []byte, userID int64, now time.Time) (User, error)
}

// Accounts cuida do que depende de e-mail: cadastro, recuperação de senha e
// vínculo de e-mail.
//
// Os pedidos só entram numa fila e são processados por [Accounts.Run]. Assim a
// resposta HTTP é igual, em conteúdo e em tempo, exista ou não o e-mail. A
// fila fica em memória: se o relay reiniciar, basta pedir o link de novo.
type Accounts struct {
	repo       AccountRepository
	mailer     Mailer
	adminEmail string
	logger     *slog.Logger
	now        func() time.Time
	jobs       chan job
}

type job struct {
	name string
	do   func(context.Context) error
}

const queueSize = 100

var emailRe = regexp.MustCompile(`^[a-z0-9_%+-]+(\.[a-z0-9_%+-]+)*@([a-z0-9]([a-z0-9-]*[a-z0-9])?\.)+[a-z]{2,}$`)

// NormalizeEmail valida o endereço e o devolve em minúsculas, a forma gravada.
func NormalizeEmail(s string) (string, error) {
	s = strings.TrimSpace(s)
	// confere ASCII antes do ToLower, que transforma alguns caracteres Unicode
	// em letras ASCII (o "K" de Kelvin vira "k") e faria dois endereços colidirem
	for _, r := range s {
		if r > 127 {
			return "", fmt.Errorf("%w: e-mail inválido", ErrInvalidInput)
		}
	}
	s = strings.ToLower(s)
	if len(s) > 254 || !emailRe.MatchString(s) {
		return "", fmt.Errorf("%w: e-mail inválido", ErrInvalidInput)
	}
	return s, nil
}

// NewAccounts cria o serviço. Quem confirmar a caixa adminEmail no cadastro já
// entra como administrador aprovado; vazio desativa essa regra.
func NewAccounts(repo AccountRepository, mailer Mailer, adminEmail string, logger *slog.Logger) (*Accounts, error) {
	if adminEmail != "" {
		var err error
		if adminEmail, err = NormalizeEmail(adminEmail); err != nil {
			return nil, fmt.Errorf("e-mail do administrador: %w", err)
		}
	}
	return &Accounts{
		repo: repo, mailer: mailer, adminEmail: adminEmail, logger: logger,
		now: time.Now, jobs: make(chan job, queueSize),
	}, nil
}

// Run processa a fila até ctx ser cancelado.
func (a *Accounts) Run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case j := <-a.jobs:
			if err := j.do(ctx); err != nil {
				a.logger.Error("e-mail de conta", "job", j.name, "err", err)
			}
		}
	}
}

func (a *Accounts) enqueue(name string, do func(context.Context) error) error {
	select {
	case a.jobs <- job{name, do}:
		return nil
	default:
		return ErrBusy
	}
}

// RequestSignup pede o link de cadastro. Se o e-mail já tem conta, o que chega
// à caixa é um link de recuperação; quem fez o pedido não fica sabendo.
//
// Nenhuma senha é pedida aqui: quem define a senha é quem abre o link. Senão,
// alguém poderia cadastrar o e-mail de outra pessoa com uma senha própria e
// esperar que ela clicasse em "confirmar".
func (a *Accounts) RequestSignup(email string) error {
	email, err := NormalizeEmail(email)
	if err != nil {
		return err
	}
	return a.enqueue("signup", func(ctx context.Context) error {
		u, err := a.repo.UserByEmail(ctx, email)
		switch {
		case errors.Is(err, ErrNotFound):
			return a.sendLink(ctx, TokenSignup, email, User{})
		case err != nil:
			return err
		}
		return a.sendLink(ctx, TokenReset, email, u)
	})
}

// RequestReset pede o link de nova senha. E-mail sem conta não recebe nada.
func (a *Accounts) RequestReset(email string) error {
	email, err := NormalizeEmail(email)
	if err != nil {
		return err
	}
	return a.enqueue("reset", func(ctx context.Context) error {
		u, err := a.repo.UserByEmail(ctx, email)
		if errors.Is(err, ErrNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		return a.sendLink(ctx, TokenReset, email, u)
	})
}

// RequestLink pede o link que associa um e-mail a u. Exige a senha atual: uma
// sessão esquecida aberta não pode virar controle permanente da conta. Pelo
// mesmo motivo, um e-mail já associado não é trocado por aqui.
func (a *Accounts) RequestLink(u User, password, email string) error {
	email, err := NormalizeEmail(email)
	if err != nil {
		return err
	}
	if u.Email != "" {
		return ErrEmailSet
	}
	if bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(password)) != nil {
		return ErrInvalidCredentials
	}
	return a.enqueue("link", func(ctx context.Context) error {
		_, err := a.repo.UserByEmail(ctx, email)
		if err == nil {
			return nil // e-mail de outra conta: não revela e não envia
		}
		if !errors.Is(err, ErrNotFound) {
			return err
		}
		return a.sendLink(ctx, TokenLink, email, u)
	})
}

func (a *Accounts) sendLink(ctx context.Context, kind TokenKind, email string, u User) error {
	policy := tokenPolicies[kind]
	token, now := newToken(), a.now()
	issued, err := a.repo.IssueToken(ctx, Token{
		Hash: hashToken(token), Kind: kind, Email: email, UserID: u.ID,
		CreatedAt: now, ExpiresAt: now.Add(policy.ttl),
	}, policy.limits)
	if err != nil {
		return err
	}
	if !issued {
		a.logger.Warn("limite de e-mails atingido", "kind", kind)
		return nil
	}
	return a.mailer.Send(ctx, Notice{Kind: NoticeKind(kind), To: email, Username: u.Username, Token: token})
}

// CompleteSignup cria a conta de quem abriu o link. Ela fica pendente até um
// administrador aprovar, exceto a do e-mail de administrador configurado.
func (a *Accounts) CompleteSignup(ctx context.Context, token, username, password string) (User, error) {
	if err := ValidateNewUser(username, password, RoleTechnician); err != nil {
		return User{}, err
	}
	hash, now := hashToken(token), a.now()
	// confere o token antes do bcrypt, que é caro e a rota é pública
	t, err := a.repo.TokenByHash(ctx, TokenSignup, hash, now)
	if err != nil {
		return User{}, err
	}
	u := User{Username: username, Email: t.Email, Role: RoleTechnician, Pending: true}
	if t.Email == a.adminEmail {
		u.Role, u.Pending = RoleAdmin, false
	}
	if u.PasswordHash, err = hashPassword(password); err != nil {
		return User{}, err
	}
	if u, err = a.repo.CompleteSignup(ctx, hash, now, u); err != nil {
		return User{}, err
	}
	if u.Pending && a.adminEmail != "" {
		a.notify(Notice{Kind: NoticePending, To: a.adminEmail, Username: u.Username, Email: u.Email})
	}
	return u, nil
}

// ResetPassword troca a senha, invalida os outros links e encerra as sessões.
// Não altera a aprovação: conta pendente continua pendente.
func (a *Accounts) ResetPassword(ctx context.Context, token, password string) (User, error) {
	if err := validatePassword(password); err != nil {
		return User{}, err
	}
	hash, now := hashToken(token), a.now()
	if _, err := a.repo.TokenByHash(ctx, TokenReset, hash, now); err != nil {
		return User{}, err
	}
	pw, err := hashPassword(password)
	if err != nil {
		return User{}, err
	}
	return a.repo.ResetPassword(ctx, hash, now, pw)
}

// ConfirmLink associa o e-mail. O token só vale na sessão de quem o pediu:
// senão alguém ligaria o e-mail de outra pessoa à própria conta se ela clicasse.
func (a *Accounts) ConfirmLink(ctx context.Context, u User, token string) (User, error) {
	return a.repo.LinkEmail(ctx, hashToken(token), u.ID, a.now())
}

// NotifyApproved avisa o usuário de que a conta foi aprovada.
func (a *Accounts) NotifyApproved(u User) {
	if u.Email != "" {
		a.notify(Notice{Kind: NoticeApproved, To: u.Email, Username: u.Username})
	}
}

// notify é para avisos: a operação já foi gravada, então falha só vai ao log.
func (a *Accounts) notify(n Notice) {
	send := func(ctx context.Context) error { return a.mailer.Send(ctx, n) }
	if err := a.enqueue(string(n.Kind), send); err != nil {
		a.logger.Warn("aviso descartado", "kind", n.Kind, "err", err)
	}
}
