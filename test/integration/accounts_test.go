//go:build integration

package integration

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"

	"remoteit/internal/relay/httpapi"
	"remoteit/internal/relay/identity"
	"remoteit/internal/relay/mail"
	"remoteit/internal/relay/storage/postgres"
)

// mailpitImage deve acompanhar a versão do compose.e2e.yaml.
const mailpitImage = "axllent/mailpit:v1.34.0"

const adminEmail = "chefe@example.test"

var tokenRe = regexp.MustCompile(`#/([a-z-]+)\?token=([A-Za-z0-9_-]{43})`)

// mailbox é um servidor SMTP real (Mailpit) com API para ler o que chegou.
type mailbox struct {
	t    *testing.T
	api  string
	host string
	port int
}

var sharedMailpit struct {
	ctr  testcontainers.Container
	host string
	smtp int
	api  string
}

func newMailbox(t *testing.T) *mailbox {
	t.Helper()
	m := &sharedMailpit
	if m.ctr == nil {
		ctx := context.Background()
		c, err := testcontainers.Run(ctx, mailpitImage,
			testcontainers.WithExposedPorts("1025/tcp", "8025/tcp"),
			testcontainers.WithWaitStrategy(wait.ForHTTP("/readyz").WithPort("8025/tcp")))
		if err != nil {
			t.Fatal(err)
		}
		if m.host, err = c.Host(ctx); err != nil {
			t.Fatal(err)
		}
		smtp, err := c.MappedPort(ctx, "1025/tcp")
		if err != nil {
			t.Fatal(err)
		}
		api, err := c.MappedPort(ctx, "8025/tcp")
		if err != nil {
			t.Fatal(err)
		}
		if m.smtp, err = strconv.Atoi(smtp.Port()); err != nil {
			t.Fatal(err)
		}
		m.ctr, m.api = c, "http://"+m.host+":"+api.Port()
	}
	b := &mailbox{t: t, api: m.api, host: m.host, port: m.smtp}
	req, _ := http.NewRequest("DELETE", b.api+"/api/v1/messages", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	return b
}

func (b *mailbox) get(path string, out any) {
	b.t.Helper()
	resp, err := http.Get(b.api + path)
	if err != nil {
		b.t.Fatal(err)
	}
	defer resp.Body.Close()
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		b.t.Fatal(err)
	}
}

// messages devolve os IDs das mensagens para to, da mais nova para a mais velha.
func (b *mailbox) messages(to string) []string {
	b.t.Helper()
	var list struct {
		Messages []struct {
			ID string
			To []struct{ Address string }
		} `json:"messages"`
	}
	b.get("/api/v1/messages?limit=200", &list)
	var ids []string
	for _, m := range list.Messages {
		if len(m.To) == 1 && m.To[0].Address == to {
			ids = append(ids, m.ID)
		}
	}
	return ids
}

// wait espera a n-ésima mensagem para to e devolve o texto da mais nova.
func (b *mailbox) wait(to string, n int) string {
	b.t.Helper()
	var ids []string
	eventually(b.t, func() bool { ids = b.messages(to); return len(ids) >= n }, "mensagem %d para %s não chegou", n, to)
	var msg struct{ Text string }
	b.get("/api/v1/message/"+url.PathEscape(ids[0]), &msg)
	return msg.Text
}

// token espera a n-ésima mensagem e extrai o token, conferindo a rota do link.
func (b *mailbox) token(to, route string, n int) string {
	b.t.Helper()
	text := b.wait(to, n)
	m := tokenRe.FindStringSubmatch(text)
	if m == nil || m[1] != route {
		b.t.Fatalf("e-mail sem link %q: %q", route, text)
	}
	return m[2]
}

func newAccountsEnv(t *testing.T) (*env, *mailbox) {
	t.Helper()
	box := newMailbox(t)
	sender, err := mail.New(mail.Config{
		Host: box.host, Port: box.port, From: "remoteit@example.test", FromName: "RemoteIT",
		PublicURL: "https://console.example.test",
	})
	if err != nil {
		t.Fatal(err)
	}
	db, err := postgres.Open(t.Context(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	accounts, err := identity.NewAccounts(db, sender, adminEmail, logger)
	if err != nil {
		t.Fatal(err)
	}
	e := newEnv(t, func(c *httpapi.Config) { c.Accounts = accounts })
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); accounts.Run(ctx) }()
	t.Cleanup(func() { cancel(); <-done })
	return e, box
}

type body = map[string]string

func expect(t *testing.T, what string, got, want int) {
	t.Helper()
	if got != want {
		t.Fatalf("%s: status %d, want %d", what, got, want)
	}
}

func (e *env) signup(box *mailbox, email, user, pass string) identity.User {
	e.t.Helper()
	c := e.anonymous()
	n := len(box.messages(email))
	expect(e.t, "pedir cadastro", c.do("POST", "/api/account/signup", body{"email": email}, nil), 202)
	token := box.token(email, "criar-conta", n+1)
	var u identity.User
	expect(e.t, "concluir cadastro", c.do("POST", "/api/account/signup/complete", body{"token": token, "username": user, "password": pass}, &u), 201)
	return u
}

func TestSignupNeedsApproval(t *testing.T) {
	e, box := newAccountsEnv(t)
	u := e.signup(box, "maria@example.test", "maria", "senha-maria")
	if !u.Pending || u.Role != identity.RoleTechnician || u.Email != "maria@example.test" {
		t.Fatalf("conta = %+v", u)
	}
	anon := e.anonymous()
	creds := body{"username": "maria", "password": "senha-maria"}
	expect(t, "login pendente", anon.do("POST", "/api/login", creds, nil), 403)
	expect(t, "pendente com senha errada", anon.do("POST", "/api/login", body{"username": "maria", "password": "errada-123"}, nil), 401)
	for _, path := range []string{"/api/me", "/api/agents"} {
		expect(t, "pendente em "+path, anon.do("GET", path, nil, nil), 401)
	}
	if text := box.wait(adminEmail, 1); !strings.Contains(text, "maria@example.test") {
		t.Errorf("aviso ao admin = %q", text)
	}

	approve := "/api/users/" + strconv.FormatInt(u.ID, 10) + "/approve"
	expect(t, "aprovar sem login", anon.do("POST", approve, nil, nil), 401)
	admin := e.login(adminUser, adminPass)
	expect(t, "aprovar", admin.do("POST", approve, nil, nil), 200)
	expect(t, "aprovar de novo", admin.do("POST", approve, nil, nil), 404)
	box.wait("maria@example.test", 2)

	maria := e.login("maria", "senha-maria")
	expect(t, "técnico não aprova", maria.do("POST", approve, nil, nil), 403)
	e.login("Maria@Example.Test", "senha-maria")
}

func TestAdminEmailBootstrapsFirstAdmin(t *testing.T) {
	e, box := newAccountsEnv(t)
	execSQL(t, `DELETE FROM users`)
	u := e.signup(box, adminEmail, "chefe", "senha-do-chefe")
	if u.Pending || u.Role != identity.RoleAdmin {
		t.Fatalf("conta = %+v, want admin ativo", u)
	}
	expect(t, "admin lista usuários", e.login("chefe", "senha-do-chefe").do("GET", "/api/users", nil, nil), 200)
}

// Quem pede o cadastro não escolhe senha, papel, aprovação nem e-mail final.
func TestSignupIgnoresInjectedFields(t *testing.T) {
	e, box := newAccountsEnv(t)
	c := e.anonymous()
	expect(t, "pedir", c.do("POST", "/api/account/signup", body{"email": "eva@example.test", "password": "do-atacante"}, nil), 202)
	token := box.token("eva@example.test", "criar-conta", 1)
	var u identity.User
	in := map[string]any{"token": token, "username": "eva", "password": "senha-da-eva", "role": "admin", "pending": false, "email": adminEmail}
	expect(t, "concluir", c.do("POST", "/api/account/signup/complete", in, &u), 201)
	if !u.Pending || u.Role != identity.RoleTechnician || u.Email != "eva@example.test" {
		t.Fatalf("conta = %+v", u)
	}
}

func TestSignupTokenRules(t *testing.T) {
	e, box := newAccountsEnv(t)
	c := e.anonymous()
	for range 2 {
		expect(t, "pedir", c.do("POST", "/api/account/signup", body{"email": "lia@example.test"}, nil), 202)
	}
	newer := box.token("lia@example.test", "criar-conta", 2)

	// nome em uso não gasta o link
	in := body{"token": newer, "username": adminUser, "password": "senha-da-lia"}
	expect(t, "nome em uso", c.do("POST", "/api/account/signup/complete", in, nil), 409)
	expect(t, "link de outro tipo", c.do("POST", "/api/account/reset/complete", body{"token": newer, "password": "senha-da-lia"}, nil), 400)
	in["username"] = "lia"
	expect(t, "concluir", c.do("POST", "/api/account/signup/complete", in, nil), 201)
	in["username"] = "lia2"
	expect(t, "reusar", c.do("POST", "/api/account/signup/complete", in, nil), 400)

	expect(t, "pedir", c.do("POST", "/api/account/signup", body{"email": "rui@example.test"}, nil), 202)
	expired := box.token("rui@example.test", "criar-conta", 1)
	execSQL(t, `UPDATE account_tokens SET expires_at = now() - interval '1 second'`)
	expect(t, "vencido", c.do("POST", "/api/account/signup/complete", body{"token": expired, "username": "rui", "password": "senha-do-rui"}, nil), 400)
}

func TestTokenIsSingleUseUnderConcurrency(t *testing.T) {
	e, box := newAccountsEnv(t)
	expect(t, "pedir", e.anonymous().do("POST", "/api/account/signup", body{"email": "duo@example.test"}, nil), 202)
	token := box.token("duo@example.test", "criar-conta", 1)

	const n = 6
	status := make([]int, n)
	var wg sync.WaitGroup
	for i := range n {
		wg.Go(func() {
			status[i] = e.anonymous().do("POST", "/api/account/signup/complete",
				body{"token": token, "username": "duo" + strconv.Itoa(i), "password": "senha-do-duo"}, nil)
		})
	}
	wg.Wait()
	created := 0
	for _, s := range status {
		switch s {
		case 201:
			created++
		case 400:
		default:
			t.Errorf("status inesperado %d", s)
		}
	}
	if created != 1 {
		t.Fatalf("%d contas criadas com um link, want 1", created)
	}
}

func TestResetPassword(t *testing.T) {
	e, box := newAccountsEnv(t)
	u := e.signup(box, "ana@example.test", "ana", "senha-antiga")
	c := e.anonymous()
	expect(t, "pedir", c.do("POST", "/api/account/reset", body{"email": "ANA@example.test"}, nil), 202)
	first := box.token("ana@example.test", "nova-senha", 2)

	// recuperar a senha não pula a aprovação
	expect(t, "redefinir pendente", c.do("POST", "/api/account/reset/complete", body{"token": first, "password": "senha-dois"}, nil), 204)
	expect(t, "continua pendente", c.do("POST", "/api/login", body{"username": "ana", "password": "senha-dois"}, nil), 403)

	admin := e.login(adminUser, adminPass)
	expect(t, "aprovar", admin.do("POST", "/api/users/"+strconv.FormatInt(u.ID, 10)+"/approve", nil, nil), 200)
	box.wait("ana@example.test", 3)
	ana := e.login("ana", "senha-dois")
	pc, ag := ana.ownMachine()
	v, _, err := ana.viewer(pc)
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()
	readText(t, ag, "start", 2*time.Second)

	expect(t, "pedir", c.do("POST", "/api/account/reset", body{"email": "ana@example.test"}, nil), 202)
	token := box.token("ana@example.test", "nova-senha", 4)
	expect(t, "senha curta", c.do("POST", "/api/account/reset/complete", body{"token": token, "password": "curta"}, nil), 400)
	expect(t, "redefinir", c.do("POST", "/api/account/reset/complete", body{"token": token, "password": "senha-tres"}, nil), 204)

	readText(t, ag, "stop", 2*time.Second) // a tela remota aberta caiu
	expect(t, "sessão antiga", ana.do("GET", "/api/me", nil, nil), 401)
	expect(t, "senha antiga", c.do("POST", "/api/login", body{"username": "ana", "password": "senha-dois"}, nil), 401)
	expect(t, "reusar link", c.do("POST", "/api/account/reset/complete", body{"token": token, "password": "senha-quatro"}, nil), 400)
	e.login("ana", "senha-tres")
}

func TestSessionNeedsCurrentPassword(t *testing.T) {
	e := newEnv(t)
	u, err := e.db.UserByUsername(t.Context(), adminUser)
	if err != nil {
		t.Fatal(err)
	}
	s := identity.Session{TokenHash: []byte("h"), UserID: u.ID, ExpiresAt: time.Now().Add(time.Hour)}
	if err := e.db.CreateSession(t.Context(), s, "hash-de-antes-da-troca"); err != identity.ErrNotFound {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestRequestsDoNotRevealAccounts(t *testing.T) {
	e, box := newAccountsEnv(t)
	e.signup(box, "bia@example.test", "bia", "senha-da-bia")
	c := e.anonymous()
	for _, path := range []string{"/api/account/reset", "/api/account/signup"} {
		known := c.do("POST", path, body{"email": "bia@example.test"}, nil)
		unknown := c.do("POST", path, body{"email": "ninguem@example.test"}, nil)
		if known != 202 || unknown != 202 {
			t.Errorf("%s: com conta %d, sem conta %d; want 202 nos dois", path, known, unknown)
		}
	}
	// a fila é serial: este cadastro é o último pedido, então os outros já rodaram
	box.token("ninguem@example.test", "criar-conta", 1)
	box.token("bia@example.test", "nova-senha", 3)
	if n := len(box.messages("ninguem@example.test")); n != 1 {
		t.Errorf("sem conta recebeu %d mensagens, want só o cadastro", n)
	}
}

func TestLinkEmailToExistingAccount(t *testing.T) {
	e, box := newAccountsEnv(t)
	admin := e.login(adminUser, adminPass)
	expect(t, "criar outro", admin.do("POST", "/api/users", body{"username": "joao", "password": "senha-joao", "role": "technician"}, nil), 201)
	joao := e.login("joao", "senha-joao")

	const addr = "admin@example.test"
	expect(t, "sem login", e.anonymous().do("POST", "/api/account/email", body{"email": addr, "password": adminPass}, nil), 401)
	expect(t, "senha errada", admin.do("POST", "/api/account/email", body{"email": addr, "password": "errada-123"}, nil), 403)
	expect(t, "senha errada mantém a sessão", admin.do("GET", "/api/me", nil, nil), 200)
	expect(t, "pedir", admin.do("POST", "/api/account/email", body{"email": addr, "password": adminPass}, nil), 202)
	token := box.token(addr, "confirmar-email", 1)

	expect(t, "confirmar sem login", e.anonymous().do("POST", "/api/account/email/complete", body{"token": token}, nil), 401)
	expect(t, "confirmar em outra conta", joao.do("POST", "/api/account/email/complete", body{"token": token}, nil), 400)
	var u identity.User
	expect(t, "confirmar", admin.do("POST", "/api/account/email/complete", body{"token": token}, &u), 200)
	if u.Email != addr {
		t.Fatalf("email = %q", u.Email)
	}
	expect(t, "não troca", admin.do("POST", "/api/account/email", body{"email": "outro@example.test", "password": adminPass}, nil), 409)
	e.login(addr, adminPass)
}

func TestEmailLimits(t *testing.T) {
	t.Run("por destinatário", func(t *testing.T) {
		e, box := newAccountsEnv(t)
		c := e.anonymous()
		for range 5 {
			expect(t, "pedir", c.do("POST", "/api/account/signup", body{"email": "alvo@example.test"}, nil), 202)
		}
		expect(t, "marcador", c.do("POST", "/api/account/signup", body{"email": "fim@example.test"}, nil), 202)
		box.wait("fim@example.test", 1)
		if n := len(box.messages("alvo@example.test")); n != 3 {
			t.Errorf("%d mensagens, want 3", n)
		}
	})
	t.Run("por IP", func(t *testing.T) {
		e, _ := newAccountsEnv(t)
		c := e.anonymous()
		for range 20 {
			expect(t, "dentro do limite", c.do("POST", "/api/account/reset", body{"email": "x@example.test"}, nil), 202)
		}
		expect(t, "acima do limite", c.do("POST", "/api/account/reset", body{"email": "x@example.test"}, nil), 429)
	})
	t.Run("por dia e por tipo", func(t *testing.T) {
		e := newEnv(t)
		now, limits := time.Now(), identity.TokenLimits{PerEmailHour: 3, PerDay: 2}
		for i, want := range []bool{true, true, false} {
			n := strconv.Itoa(i)
			got, err := e.db.IssueToken(t.Context(), identity.Token{Hash: []byte(n), Kind: identity.TokenSignup,
				Email: n + "@example.test", CreatedAt: now, ExpiresAt: now.Add(time.Hour)}, limits)
			if err != nil || got != want {
				t.Fatalf("emissão %d = %v, %v; want %v", i, got, err, want)
			}
		}
		u, err := e.db.UserByUsername(t.Context(), adminUser)
		if err != nil {
			t.Fatal(err)
		}
		got, err := e.db.IssueToken(t.Context(), identity.Token{Hash: []byte("r"), Kind: identity.TokenReset,
			Email: "r@example.test", UserID: u.ID, CreatedAt: now, ExpiresAt: now.Add(time.Hour)}, limits)
		if err != nil || !got {
			t.Fatalf("cadastros esgotados bloquearam a recuperação: %v, %v", got, err)
		}
	})
}

func TestAccountsDisabledWithoutSMTP(t *testing.T) {
	e := newEnv(t)
	var cfg struct{ Accounts bool }
	c := e.anonymous()
	expect(t, "config", c.do("GET", "/api/account/config", nil, &cfg), 200)
	if cfg.Accounts {
		t.Error("accounts = true sem SMTP")
	}
	expect(t, "cadastro", c.do("POST", "/api/account/signup", body{"email": "a@example.test"}, nil), 503)
}

// É o que acontece no deploy: um banco na 0001, com usuários e sessões.
func TestAccountsMigrationKeepsExistingUsers(t *testing.T) {
	ctx := t.Context()
	execSQL(t, `DROP DATABASE IF EXISTS upgrade_test`)
	execSQL(t, `CREATE DATABASE upgrade_test`)
	t.Cleanup(func() { execSQL(t, `DROP DATABASE upgrade_test WITH (FORCE)`) })
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	u.Path = "/upgrade_test"
	conn, err := pgx.Connect(ctx, u.String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(context.Background())
	for _, sql := range []string{
		readMigration(t, "0001_init.sql"),
		`CREATE TABLE schema_migrations (version TEXT PRIMARY KEY, applied_at TIMESTAMPTZ NOT NULL DEFAULT now())`,
		`INSERT INTO schema_migrations (version) VALUES ('migrations/0001_init.sql')`,
		`INSERT INTO users (username, password_hash, role) VALUES ('antigo', 'hash', 'admin')`,
		`INSERT INTO sessions (token_hash, user_id, expires_at) VALUES ('\x01', 1, now() + interval '1 hour')`,
	} {
		if _, err := conn.Exec(ctx, sql); err != nil {
			t.Fatal(err)
		}
	}
	db, err := postgres.Open(ctx, u.String())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	got, err := db.UserByUsername(ctx, "antigo")
	if err != nil || got.Pending || got.Email != "" || got.Role != identity.RoleAdmin {
		t.Fatalf("usuário migrado = %+v, %v", got, err)
	}
	if _, err := db.SessionByTokenHash(ctx, []byte{1}); err != nil {
		t.Fatalf("sessão perdida: %v", err)
	}
}
