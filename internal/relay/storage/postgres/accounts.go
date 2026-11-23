package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"remoteit/internal/relay/identity"
)

// tokenIssueLock serializa a contagem dos limites com a emissão do link.
const tokenIssueLock = 7_412_990_002

// tokenRetention precisa ser maior que a validade de qualquer link e que a
// janela do limite diário.
const tokenRetention = 48 * time.Hour

var _ identity.AccountRepository = (*DB)(nil)

// UserByEmail busca um usuário pelo e-mail normalizado.
func (db *DB) UserByEmail(ctx context.Context, email string) (identity.User, error) {
	return scanUser(db.pool.QueryRow(ctx, `SELECT `+userCols+` FROM users WHERE email = $1`, email))
}

// IssueToken grava t se os limites permitirem e apaga os links antigos.
func (db *DB) IssueToken(ctx context.Context, t identity.Token, l identity.TokenLimits) (issued bool, err error) {
	err = pgx.BeginFunc(ctx, db.pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, tokenIssueLock); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `DELETE FROM account_tokens WHERE created_at < $1`, t.CreatedAt.Add(-tokenRetention)); err != nil {
			return err
		}
		var today, lastHour int
		if err := tx.QueryRow(ctx, `SELECT count(*), count(*) FILTER (WHERE email = $2 AND created_at > $3)
			FROM account_tokens WHERE kind = $1 AND created_at > $4`,
			t.Kind, t.Email, t.CreatedAt.Add(-time.Hour), t.CreatedAt.Add(-24*time.Hour)).Scan(&today, &lastHour); err != nil {
			return err
		}
		if today >= l.PerDay || lastHour >= l.PerEmailHour {
			return nil
		}
		if _, err := tx.Exec(ctx, `INSERT INTO account_tokens (token_hash, kind, email, user_id, created_at, expires_at)
			VALUES ($1, $2, $3, NULLIF($4::bigint, 0), $5, $6)`,
			t.Hash, t.Kind, t.Email, t.UserID, t.CreatedAt, t.ExpiresAt); err != nil {
			return err
		}
		issued = true
		return nil
	})
	return issued && err == nil, err
}

const tokenCols = `token_hash, kind, email, COALESCE(user_id, 0), created_at, expires_at`

func scanToken(row pgx.Row) (identity.Token, error) {
	var t identity.Token
	err := row.Scan(&t.Hash, &t.Kind, &t.Email, &t.UserID, &t.CreatedAt, &t.ExpiresAt)
	if errors.Is(err, pgx.ErrNoRows) {
		err = identity.ErrInvalidToken
	}
	return t, err
}

// TokenByHash devolve um link ainda válido, sem consumi-lo.
func (db *DB) TokenByHash(ctx context.Context, kind identity.TokenKind, hash []byte, now time.Time) (identity.Token, error) {
	return scanToken(db.pool.QueryRow(ctx, `SELECT `+tokenCols+` FROM account_tokens
		WHERE token_hash = $1 AND kind = $2 AND used_at IS NULL AND expires_at > $3`, hash, kind, now))
}

// consumeToken é atômico: de duas requisições com o mesmo link, só uma recebe
// a linha.
func consumeToken(ctx context.Context, tx pgx.Tx, kind identity.TokenKind, hash []byte, userID int64, now time.Time) (identity.Token, error) {
	return scanToken(tx.QueryRow(ctx, `UPDATE account_tokens SET used_at = $3
		WHERE token_hash = $1 AND kind = $2 AND used_at IS NULL AND expires_at > $3
		  AND ($4::bigint = 0 OR user_id = $4)
		RETURNING `+tokenCols, hash, kind, now, userID))
}

// CompleteSignup consome o link e cria a conta com o e-mail que está nele.
func (db *DB) CompleteSignup(ctx context.Context, hash []byte, now time.Time, u identity.User) (created identity.User, err error) {
	err = pgx.BeginFunc(ctx, db.pool, func(tx pgx.Tx) error {
		t, err := consumeToken(ctx, tx, identity.TokenSignup, hash, 0, now)
		if err != nil {
			return err
		}
		u.Email = t.Email
		if created, err = insertUser(ctx, tx, u); err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `UPDATE account_tokens SET used_at = $2 WHERE email = $1 AND kind = 'signup' AND used_at IS NULL`, t.Email, now)
		return err
	})
	return created, err
}

// ResetPassword consome o link, troca a senha e apaga as sessões da conta.
func (db *DB) ResetPassword(ctx context.Context, hash []byte, now time.Time, passwordHash string) (u identity.User, err error) {
	err = pgx.BeginFunc(ctx, db.pool, func(tx pgx.Tx) error {
		t, err := consumeToken(ctx, tx, identity.TokenReset, hash, 0, now)
		if err != nil {
			return err
		}
		if u, err = scanUser(tx.QueryRow(ctx, `UPDATE users SET password_hash = $2 WHERE id = $1 RETURNING `+userCols, t.UserID, passwordHash)); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `DELETE FROM sessions WHERE user_id = $1`, u.ID); err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `UPDATE account_tokens SET used_at = $2 WHERE user_id = $1 AND used_at IS NULL`, u.ID, now)
		return err
	})
	return u, err
}

// LinkEmail consome o link de userID e grava o e-mail, se a conta não tiver um.
func (db *DB) LinkEmail(ctx context.Context, hash []byte, userID int64, now time.Time) (u identity.User, err error) {
	err = pgx.BeginFunc(ctx, db.pool, func(tx pgx.Tx) error {
		t, err := consumeToken(ctx, tx, identity.TokenLink, hash, userID, now)
		if err != nil {
			return err
		}
		u, err = scanUser(tx.QueryRow(ctx, `UPDATE users SET email = $2 WHERE id = $1 AND email IS NULL RETURNING `+userCols, userID, t.Email))
		if errors.Is(err, identity.ErrNotFound) {
			return identity.ErrInvalidToken
		}
		return userConflict(err)
	})
	return u, err
}

// ApproveUser libera uma conta pendente.
func (db *DB) ApproveUser(ctx context.Context, id int64) (identity.User, error) {
	return scanUser(db.pool.QueryRow(ctx, `UPDATE users SET pending = false WHERE id = $1 AND pending RETURNING `+userCols, id))
}

// userConflict traduz violação de unicidade. E-mail repetido significa que
// outro link já foi usado, por isso vira link inválido.
func userConflict(err error) error {
	if pgErr, ok := errors.AsType[*pgconn.PgError](err); ok && pgErr.Code == "23505" {
		if pgErr.ConstraintName == "users_email_key" {
			return identity.ErrInvalidToken
		}
		return identity.ErrUserExists
	}
	return err
}
