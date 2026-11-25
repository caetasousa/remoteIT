// Package machines cuida de quem é dono de cada máquina e de quem pode
// acessá-la.
//
// Regras deste contexto:
//
//   - Toda máquina nova entra por um código de cadastro, e o dono dela é o
//     técnico que gerou o código (o .exe baixado já leva o código gravado,
//     veja internal/relay/agentbin).
//   - No cadastro, a máquina recebe um ID aleatório e uma credencial só dela.
//     O banco guarda o SHA-256 da credencial; outra máquina não consegue se
//     passar por ela, nem usando o mesmo nome de computador.
//   - Acessam e gerenciam (remover) a máquina: o dono e os administradores.
//     Não há compartilhamento: cada técnico atende as máquinas dos links e
//     downloads dele.
//   - Remover revoga: a credencial deixa de valer e o agente não volta a
//     se cadastrar sozinho com a mesma credencial.
//   - Agentes antigos, com a chave compartilhada (sem cadastro), são
//     "legados": aparecem só para administradores, até as máquinas serem
//     reinstaladas.
//   - Um link de instalação pode não ter prazo nem limite de máquinas: o
//     técnico manda o mesmo link a todos os clientes e o revoga quando quiser.
//
// Este pacote não importa os outros contextos: recebe só o ID do usuário e
// se ele é administrador ([Actor]).
package machines

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base32"
	"errors"
	"strings"
	"time"
)

// Edições do agente que um código de cadastro pode entregar.
const (
	EditionPortable = "portatil" // suporte-ti.exe: só executa
	EditionService  = "servico"  // suporte-ti-servico.exe: instala o serviço
)

// Tipos de código de cadastro.
const (
	KindDownload = "download" // gerado a cada download pelo console
	KindLink     = "link"     // link de instalação criado pelo técnico
)

// Limites dos códigos.
const (
	DownloadTTL  = 30 * 24 * time.Hour // o mesmo .exe pode ser levado a vários PCs
	DownloadUses = 50
	MinLinkTTL   = time.Hour
	MaxLinkTTL   = 7 * 24 * time.Hour
	MaxLinkUses  = 100
	// NoExpiry e UnlimitedUses são o que um link "sem prazo" e "sem limite"
	// guarda no banco: valores que na prática nunca são alcançados, para as
	// consultas continuarem simples (expires_at > agora, uses < max_uses).
	NoExpiry       = 100 * 365 * 24 * time.Hour
	UnlimitedUses  = 1_000_000
	machineIDBytes = 10
	codeBytes      = 20 // 32 caracteres em base32
)

// IDPrefix começa o ID de toda máquina cadastrada. Nomes de computador não
// têm "_", então um agente legado (que usa o nome como ID) nunca se confunde
// com uma máquina cadastrada.
const IDPrefix = "m_"

// Erros do contexto.
var (
	ErrNotFound     = errors.New("machines: máquina não encontrada")
	ErrForbidden    = errors.New("machines: sem permissão para esta máquina")
	ErrInvalidCode  = errors.New("machines: código de cadastro inválido, vencido ou esgotado")
	ErrUnauthorized = errors.New("machines: credencial da máquina inválida")
	ErrRevoked      = errors.New("machines: máquina removida")
	ErrInvalidInput = errors.New("machines: dados inválidos")
)

// Actor é quem está pedindo a operação.
type Actor struct {
	ID    int64
	Admin bool
}

// Machine é um computador cadastrado (ou legado).
type Machine struct {
	ID         string     `json:"id"`
	Name       string     `json:"name"`              // nome do computador
	OwnerID    int64      `json:"ownerId,omitempty"` // 0: sem dono (legado ou dono removido)
	Owner      string     `json:"owner,omitempty"`
	Legacy     bool       `json:"legacy"`
	CreatedAt  time.Time  `json:"createdAt"`
	LastSeenAt *time.Time `json:"lastSeenAt,omitempty"`
}

// Code é um código de cadastro (o código em si só existe no .exe ou no
// link; o banco guarda o hash).
type Code struct {
	ID        int64      `json:"id"`
	UserID    int64      `json:"-"`
	Kind      string     `json:"kind"`
	Edition   string     `json:"edition"`
	MaxUses   int        `json:"maxUses"`
	Uses      int        `json:"uses"`
	CreatedAt time.Time  `json:"createdAt"`
	ExpiresAt time.Time  `json:"expiresAt"`
	RevokedAt *time.Time `json:"revokedAt,omitempty"`
}

// Repository é a persistência, implementada em internal/relay/storage/postgres.
type Repository interface {
	CreateCode(ctx context.Context, c Code, hash []byte) (Code, error)
	CodeByHash(ctx context.Context, hash []byte, now time.Time) (Code, error)
	ListCodes(ctx context.Context, userID int64, kind string, now time.Time) ([]Code, error)
	// RevokeCode revoga o código id se ele pertence a userID (ou a qualquer
	// um, com admin); ErrNotFound caso contrário.
	RevokeCode(ctx context.Context, id, userID int64, admin bool, now time.Time) error
	// Enroll consome um uso do código (válido, não revogado, com usos
	// sobrando) e cria a máquina com o dono do código, numa transação.
	// ErrInvalidCode se o código não serve mais.
	Enroll(ctx context.Context, codeHash []byte, now time.Time, m Machine, credHash []byte) (Machine, error)
	// Credential devolve o hash da credencial e se a máquina foi removida.
	Credential(ctx context.Context, id string) (hash []byte, revoked bool, err error)
	// UpsertLegacy registra um agente legado; revoked indica que um admin o
	// removeu e ele não deve mais ser aceito.
	UpsertLegacy(ctx context.Context, id, name string, now time.Time) (revoked bool, err error)
	Touch(ctx context.Context, id, name string, now time.Time) error
	// Visible lista as máquinas não removidas de userID (todas, com admin).
	Visible(ctx context.Context, userID int64, admin bool) ([]Machine, error)
	// Get devolve a máquina não removida; ErrNotFound caso contrário.
	Get(ctx context.Context, id string) (Machine, error)
	Revoke(ctx context.Context, id string, now time.Time) error
}

// Service aplica as regras sobre um [Repository].
type Service struct {
	repo Repository
	now  func() time.Time
}

// New cria o serviço de máquinas.
func New(repo Repository) *Service { return &Service{repo: repo, now: time.Now} }

var b32 = base32.StdEncoding.WithPadding(base32.NoPadding)

func random(n int) string {
	b := make([]byte, n)
	rand.Read(b)
	return strings.ToLower(b32.EncodeToString(b))
}

func hash(s string) []byte {
	h := sha256.Sum256([]byte(s))
	return h[:]
}

func validEdition(e string) bool { return e == EditionPortable || e == EditionService }

// NewDownloadCode cria o código gravado num .exe baixado pelo console: vale
// 30 dias e até 50 máquinas, porque o técnico costuma levar o mesmo arquivo
// a vários PCs.
func (s *Service) NewDownloadCode(ctx context.Context, a Actor, edition string) (string, Code, error) {
	return s.newCode(ctx, a, KindDownload, edition, DownloadTTL, DownloadUses)
}

// NewLink cria um link de instalação, com validade e número de máquinas
// escolhidos pelo técnico. ttl 0 = sem prazo; uses 0 = sem limite.
func (s *Service) NewLink(ctx context.Context, a Actor, edition string, ttl time.Duration, uses int) (string, Code, error) {
	switch {
	case ttl == 0:
		ttl = NoExpiry
	case ttl < MinLinkTTL || ttl > MaxLinkTTL:
		return "", Code{}, ErrInvalidInput
	}
	switch {
	case uses == 0:
		uses = UnlimitedUses
	case uses < 1 || uses > MaxLinkUses:
		return "", Code{}, ErrInvalidInput
	}
	return s.newCode(ctx, a, KindLink, edition, ttl, uses)
}

func (s *Service) newCode(ctx context.Context, a Actor, kind, edition string, ttl time.Duration, uses int) (string, Code, error) {
	if !validEdition(edition) || a.ID == 0 {
		return "", Code{}, ErrInvalidInput
	}
	code := random(codeBytes)
	now := s.now()
	c, err := s.repo.CreateCode(ctx, Code{
		UserID: a.ID, Kind: kind, Edition: edition, MaxUses: uses,
		CreatedAt: now, ExpiresAt: now.Add(ttl),
	}, hash(code))
	return code, c, err
}

// Lookup confere um código sem consumi-lo (para servir o .exe de um link).
func (s *Service) Lookup(ctx context.Context, code string) (Code, error) {
	c, err := s.repo.CodeByHash(ctx, hash(code), s.now())
	if errors.Is(err, ErrNotFound) {
		return Code{}, ErrInvalidCode
	}
	return c, err
}

// Links lista os links de instalação ainda válidos do técnico.
func (s *Service) Links(ctx context.Context, a Actor) ([]Code, error) {
	return s.repo.ListCodes(ctx, a.ID, KindLink, s.now())
}

// RevokeLink invalida um link de instalação do técnico (admin: de qualquer um).
func (s *Service) RevokeLink(ctx context.Context, a Actor, id int64) error {
	return s.repo.RevokeCode(ctx, id, a.ID, a.Admin, s.now())
}

// Enroll cadastra uma máquina com o código e devolve a máquina e a
// credencial em claro, que só o agente guarda.
func (s *Service) Enroll(ctx context.Context, code, name string) (Machine, string, error) {
	name = strings.TrimSpace(name)
	if code == "" || name == "" || len(name) > 64 || len(code) > 64 {
		return Machine{}, "", ErrInvalidInput
	}
	secret := random(32)
	m, err := s.repo.Enroll(ctx, hash(code), s.now(), Machine{
		ID: IDPrefix + random(machineIDBytes), Name: name, CreatedAt: s.now(),
	}, hash(secret))
	if err != nil {
		return Machine{}, "", err
	}
	return m, secret, nil
}

// Authenticate confere a credencial de uma máquina. ErrUnauthorized se a
// máquina não existe ou a credencial não confere (o agente pode se cadastrar
// de novo); ErrRevoked se ela foi removida (não deve voltar sozinha).
func (s *Service) Authenticate(ctx context.Context, id, secret string) error {
	if !strings.HasPrefix(id, IDPrefix) || secret == "" {
		return ErrUnauthorized
	}
	h, revoked, err := s.repo.Credential(ctx, id)
	if errors.Is(err, ErrNotFound) {
		return ErrUnauthorized
	}
	if err != nil {
		return err
	}
	if subtle.ConstantTimeCompare(h, hash(secret)) != 1 {
		return ErrUnauthorized
	}
	if revoked {
		return ErrRevoked
	}
	return nil
}

// ValidLegacyID informa se id pode ser usado por um agente legado: não pode
// se confundir com o ID de uma máquina cadastrada.
func ValidLegacyID(id string) bool {
	return id != "" && len(id) <= 64 && !strings.HasPrefix(id, IDPrefix)
}

// SeenLegacy registra (ou atualiza) um agente legado que conectou.
// ErrRevoked se um admin o removeu: a conexão deve ser recusada.
func (s *Service) SeenLegacy(ctx context.Context, id, name string) error {
	revoked, err := s.repo.UpsertLegacy(ctx, id, name, s.now())
	if err == nil && revoked {
		return ErrRevoked
	}
	return err
}

// Seen atualiza o nome e o "visto por último" de uma máquina cadastrada.
func (s *Service) Seen(ctx context.Context, id, name string) error {
	return s.repo.Touch(ctx, id, name, s.now())
}

// Visible lista as máquinas que o ator pode acessar.
func (s *Service) Visible(ctx context.Context, a Actor) ([]Machine, error) {
	return s.repo.Visible(ctx, a.ID, a.Admin)
}

// CanAccess informa se o ator pode ver e controlar a máquina id.
func (s *Service) CanAccess(ctx context.Context, a Actor, id string) (bool, error) {
	m, err := s.repo.Get(ctx, id)
	if errors.Is(err, ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return a.Admin || (m.OwnerID != 0 && m.OwnerID == a.ID), nil
}

// manageable devolve a máquina se o ator pode gerenciá-la (dono ou admin).
func (s *Service) manageable(ctx context.Context, a Actor, id string) (Machine, error) {
	m, err := s.repo.Get(ctx, id)
	if err != nil {
		return Machine{}, err
	}
	if !a.Admin && (m.OwnerID == 0 || m.OwnerID != a.ID) {
		return Machine{}, ErrForbidden
	}
	return m, nil
}

// Remove revoga a máquina: some das listas e a credencial deixa de valer.
func (s *Service) Remove(ctx context.Context, a Actor, id string) error {
	if _, err := s.manageable(ctx, a, id); err != nil {
		return err
	}
	return s.repo.Revoke(ctx, id, s.now())
}
