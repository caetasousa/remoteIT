// Package postgres implementa a persistência do relay em PostgreSQL:
// [identity.Repository], [identity.AccountRepository] e [audit.Store].
//
// É o único pacote do projeto que conhece SQL. As migrations ficam em
// migrations/*.sql, embutidas no binário e aplicadas em ordem por [DB.Migrate].
package postgres

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"sort"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"remoteit/internal/relay/audit"
	"remoteit/internal/relay/identity"
)

//go:embed migrations/*.sql
var migrations embed.FS

// migrationLock é a chave do advisory lock que impede duas instâncias de
// aplicarem migrations ao mesmo tempo.
const migrationLock = 7_412_990_001

// DB é o banco do relay.
type DB struct {
	pool *pgxpool.Pool
}

var (
	_ identity.Repository = (*DB)(nil)
	_ audit.Store         = (*DB)(nil)
)

// Open conecta ao banco descrito por url (formato postgres://...) e confere a
// conexão.
func Open(ctx context.Context, url string) (*DB, error) {
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		return nil, err
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("postgres: %w", err)
	}
	return &DB{pool: pool}, nil
}

// Close fecha o pool de conexões.
func (db *DB) Close() { db.pool.Close() }

// Ping confere se o banco responde. Usado pelo health check.
func (db *DB) Ping(ctx context.Context) error { return db.pool.Ping(ctx) }

// Migrate aplica, em ordem de nome de arquivo, as migrations ainda não
// registradas na tabela schema_migrations. Cada migration roda em sua própria
// transação: uma falha não deixa o schema pela metade.
func (db *DB) Migrate(ctx context.Context) error {
	conn, err := db.pool.Acquire(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()
	if _, err := conn.Exec(ctx, `SELECT pg_advisory_lock($1)`, migrationLock); err != nil {
		return err
	}
	defer conn.Exec(context.Background(), `SELECT pg_advisory_unlock($1)`, migrationLock)

	if _, err := conn.Exec(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		version TEXT PRIMARY KEY, applied_at TIMESTAMPTZ NOT NULL DEFAULT now())`); err != nil {
		return err
	}
	names, _ := fs.Glob(migrations, "migrations/*.sql")
	sort.Strings(names)
	for _, name := range names {
		var done bool
		if err := conn.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM schema_migrations WHERE version = $1)`, name).Scan(&done); err != nil {
			return err
		}
		if done {
			continue
		}
		sql, _ := migrations.ReadFile(name)
		err := pgx.BeginFunc(ctx, conn, func(tx pgx.Tx) error {
			if _, err := tx.Exec(ctx, string(sql)); err != nil {
				return err
			}
			_, err := tx.Exec(ctx, `INSERT INTO schema_migrations (version) VALUES ($1)`, name)
			return err
		})
		if err != nil {
			return fmt.Errorf("migration %s: %w", name, err)
		}
	}
	return nil
}

func notFound(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return identity.ErrNotFound
	}
	return err
}

// ---------- identity.Repository ----------

const userCols = `id, username, password_hash, role, created_at, COALESCE(email, ''), pending`

func scanUser(row pgx.Row) (identity.User, error) {
	var u identity.User
	err := row.Scan(&u.ID, &u.Username, &u.PasswordHash, &u.Role, &u.CreatedAt, &u.Email, &u.Pending)
	return u, notFound(err)
}

// CreateUser grava u e devolve o usuário com ID e data de criação.
func (db *DB) CreateUser(ctx context.Context, u identity.User) (identity.User, error) {
	return insertUser(ctx, db.pool, u)
}

// querier é o que pool e transação têm em comum.
type querier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

func insertUser(ctx context.Context, q querier, u identity.User) (identity.User, error) {
	created, err := scanUser(q.QueryRow(ctx, `INSERT INTO users (username, password_hash, role, email, pending)
		VALUES ($1, $2, $3, NULLIF($4, ''), $5) RETURNING `+userCols,
		u.Username, u.PasswordHash, u.Role, u.Email, u.Pending))
	if err != nil {
		return identity.User{}, userConflict(err)
	}
	return created, nil
}

// UserByUsername busca um usuário pelo nome.
func (db *DB) UserByUsername(ctx context.Context, username string) (identity.User, error) {
	return scanUser(db.pool.QueryRow(ctx, `SELECT `+userCols+` FROM users WHERE username = $1`, username))
}

// UserByID busca um usuário pelo ID.
func (db *DB) UserByID(ctx context.Context, id int64) (identity.User, error) {
	return scanUser(db.pool.QueryRow(ctx, `SELECT `+userCols+` FROM users WHERE id = $1`, id))
}

// ListUsers devolve todos os usuários em ordem alfabética.
func (db *DB) ListUsers(ctx context.Context) ([]identity.User, error) {
	rows, err := db.pool.Query(ctx, `SELECT `+userCols+` FROM users ORDER BY username`)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (identity.User, error) { return scanUser(r) })
}

// DeleteUser remove o usuário; as sessões dele saem junto (ON DELETE CASCADE).
func (db *DB) DeleteUser(ctx context.Context, id int64) error {
	tag, err := db.pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, id)
	if err == nil && tag.RowsAffected() == 0 {
		return identity.ErrNotFound
	}
	return err
}

// CountUsers devolve quantos usuários existem.
func (db *DB) CountUsers(ctx context.Context) (int, error) {
	var n int
	err := db.pool.QueryRow(ctx, `SELECT count(*) FROM users`).Scan(&n)
	return n, err
}

// CreateSession grava uma sessão se a senha ainda for passwordHash. O FOR
// SHARE espera uma troca de senha em andamento terminar antes de decidir.
func (db *DB) CreateSession(ctx context.Context, s identity.Session, passwordHash string) error {
	tag, err := db.pool.Exec(ctx, `INSERT INTO sessions (token_hash, user_id, expires_at)
		SELECT $1, id, $3 FROM users WHERE id = $2 AND password_hash = $4 FOR SHARE`,
		s.TokenHash, s.UserID, s.ExpiresAt, passwordHash)
	if err == nil && tag.RowsAffected() == 0 {
		return identity.ErrNotFound
	}
	return err
}

// SessionByTokenHash busca uma sessão pelo hash do token.
func (db *DB) SessionByTokenHash(ctx context.Context, hash []byte) (identity.Session, error) {
	s := identity.Session{TokenHash: hash}
	err := db.pool.QueryRow(ctx, `SELECT user_id, expires_at FROM sessions WHERE token_hash = $1`, hash).
		Scan(&s.UserID, &s.ExpiresAt)
	return s, notFound(err)
}

// DeleteSession remove a sessão, se existir.
func (db *DB) DeleteSession(ctx context.Context, hash []byte) error {
	_, err := db.pool.Exec(ctx, `DELETE FROM sessions WHERE token_hash = $1`, hash)
	return err
}

// DeleteExpiredSessions remove as sessões vencidas até now.
func (db *DB) DeleteExpiredSessions(ctx context.Context, now time.Time) error {
	_, err := db.pool.Exec(ctx, `DELETE FROM sessions WHERE expires_at <= $1`, now)
	return err
}

// ---------- audit.Store ----------

// StartAccess grava o início de um acesso e devolve o ID do registro.
func (db *DB) StartAccess(ctx context.Context, e audit.Entry) (int64, error) {
	var id int64
	err := db.pool.QueryRow(ctx, `INSERT INTO access_log
		(user_id, username, agent_id, host, remote_addr, started_at)
		VALUES (NULLIF($1, 0), $2, $3, $4, $5, $6) RETURNING id`,
		e.UserID, e.Username, e.AgentID, e.Host, e.RemoteAddr, e.StartedAt).Scan(&id)
	return id, err
}

// EndAccess grava o fim do acesso id.
func (db *DB) EndAccess(ctx context.Context, id int64, at time.Time) error {
	_, err := db.pool.Exec(ctx, `UPDATE access_log SET ended_at = $2 WHERE id = $1`, id, at)
	return err
}

// CloseOpen encerra todos os acessos sem fim registrado.
func (db *DB) CloseOpen(ctx context.Context, at time.Time) (int64, error) {
	tag, err := db.pool.Exec(ctx, `UPDATE access_log SET ended_at = $1 WHERE ended_at IS NULL`, at)
	return tag.RowsAffected(), err
}

// ListAccesses devolve os últimos limit acessos.
func (db *DB) ListAccesses(ctx context.Context, limit int) ([]audit.Entry, error) {
	rows, err := db.pool.Query(ctx, `SELECT id, COALESCE(user_id, 0), username, agent_id, host,
		remote_addr, started_at, ended_at FROM access_log ORDER BY started_at DESC, id DESC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (audit.Entry, error) {
		var e audit.Entry
		err := r.Scan(&e.ID, &e.UserID, &e.Username, &e.AgentID, &e.Host, &e.RemoteAddr, &e.StartedAt, &e.EndedAt)
		return e, err
	})
}
