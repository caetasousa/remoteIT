package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"remoteit/internal/relay/machines"
)

var _ machines.Repository = (*DB)(nil)

func machineNotFound(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return machines.ErrNotFound
	}
	return err
}

const codeCols = `id, user_id, kind, edition, max_uses, uses, created_at, expires_at, revoked_at`

func scanCode(row pgx.Row) (machines.Code, error) {
	var c machines.Code
	err := row.Scan(&c.ID, &c.UserID, &c.Kind, &c.Edition, &c.MaxUses, &c.Uses, &c.CreatedAt, &c.ExpiresAt, &c.RevokedAt)
	return c, machineNotFound(err)
}

// CreateCode grava um código de cadastro.
func (db *DB) CreateCode(ctx context.Context, c machines.Code, hash []byte) (machines.Code, error) {
	return scanCode(db.pool.QueryRow(ctx, `INSERT INTO enrollment_codes
		(code_hash, user_id, kind, edition, max_uses, created_at, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7) RETURNING `+codeCols,
		hash, c.UserID, c.Kind, c.Edition, c.MaxUses, c.CreatedAt, c.ExpiresAt))
}

// validCode é a condição de um código que ainda cadastra máquinas.
const validCode = `revoked_at IS NULL AND expires_at > $2 AND uses < max_uses`

// CodeByHash devolve um código ainda válido, sem consumi-lo.
func (db *DB) CodeByHash(ctx context.Context, hash []byte, now time.Time) (machines.Code, error) {
	return scanCode(db.pool.QueryRow(ctx, `SELECT `+codeCols+` FROM enrollment_codes
		WHERE code_hash = $1 AND `+validCode, hash, now))
}

// ListCodes lista os códigos válidos de userID do tipo kind, do mais novo
// para o mais antigo.
func (db *DB) ListCodes(ctx context.Context, userID int64, kind string, now time.Time) ([]machines.Code, error) {
	rows, err := db.pool.Query(ctx, `SELECT `+codeCols+` FROM enrollment_codes
		WHERE user_id = $1 AND kind = $3 AND revoked_at IS NULL AND expires_at > $2 AND uses < max_uses
		ORDER BY created_at DESC LIMIT 100`, userID, now, kind)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (machines.Code, error) { return scanCode(r) })
}

// RevokeCode revoga o código id de userID (qualquer um, se admin).
func (db *DB) RevokeCode(ctx context.Context, id, userID int64, admin bool, now time.Time) error {
	tag, err := db.pool.Exec(ctx, `UPDATE enrollment_codes SET revoked_at = $3
		WHERE id = $1 AND (user_id = $2 OR $4) AND revoked_at IS NULL`, id, userID, now, admin)
	if err == nil && tag.RowsAffected() == 0 {
		err = machines.ErrNotFound
	}
	return err
}

// Enroll consome um uso do código e cria a máquina, atomicamente: dois
// agentes com o último uso disponível não passam os dois.
func (db *DB) Enroll(ctx context.Context, codeHash []byte, now time.Time, m machines.Machine, credHash []byte) (created machines.Machine, err error) {
	err = pgx.BeginFunc(ctx, db.pool, func(tx pgx.Tx) error {
		var codeID, ownerID int64
		err := tx.QueryRow(ctx, `UPDATE enrollment_codes SET uses = uses + 1
			WHERE code_hash = $1 AND `+validCode+` RETURNING id, user_id`, codeHash, now).Scan(&codeID, &ownerID)
		if errors.Is(err, pgx.ErrNoRows) {
			return machines.ErrInvalidCode
		}
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO machines (id, name, owner_id, credential_hash, enrolled_with, created_at, last_seen_at)
			VALUES ($1, $2, $3, $4, $5, $6, $6)`, m.ID, m.Name, ownerID, credHash, codeID, now); err != nil {
			return err
		}
		created, err = getMachine(ctx, tx, m.ID)
		return err
	})
	return created, err
}

// Credential devolve o hash da credencial de uma máquina cadastrada.
func (db *DB) Credential(ctx context.Context, id string) (hash []byte, revoked bool, err error) {
	err = db.pool.QueryRow(ctx, `SELECT credential_hash, revoked_at IS NOT NULL FROM machines
		WHERE id = $1 AND credential_hash IS NOT NULL`, id).Scan(&hash, &revoked)
	return hash, revoked, machineNotFound(err)
}

// UpsertLegacy registra um agente legado e informa se ele foi removido.
func (db *DB) UpsertLegacy(ctx context.Context, id, name string, now time.Time) (revoked bool, err error) {
	err = db.pool.QueryRow(ctx, `INSERT INTO machines (id, name, created_at, last_seen_at) VALUES ($1, $2, $3, $3)
		ON CONFLICT (id) DO UPDATE SET name = EXCLUDED.name, last_seen_at = EXCLUDED.last_seen_at
		WHERE machines.credential_hash IS NULL
		RETURNING revoked_at IS NOT NULL`, id, name, now).Scan(&revoked)
	if errors.Is(err, pgx.ErrNoRows) {
		// o ID existe como máquina cadastrada: um legado não pode tomá-lo
		return true, nil
	}
	return revoked, err
}

// Touch atualiza o nome e o "visto por último".
func (db *DB) Touch(ctx context.Context, id, name string, now time.Time) error {
	_, err := db.pool.Exec(ctx, `UPDATE machines SET name = $2, last_seen_at = $3 WHERE id = $1`, id, name, now)
	return err
}

// machineSelect devolve as máquinas não removidas com o nome do dono.
const machineSelect = `SELECT m.id, m.name, COALESCE(m.owner_id, 0), COALESCE(o.username, ''),
	m.credential_hash IS NULL, m.created_at, m.last_seen_at
	FROM machines m LEFT JOIN users o ON o.id = m.owner_id
	WHERE m.revoked_at IS NULL`

func scanMachine(row pgx.Row) (machines.Machine, error) {
	var m machines.Machine
	err := row.Scan(&m.ID, &m.Name, &m.OwnerID, &m.Owner, &m.Legacy, &m.CreatedAt, &m.LastSeenAt)
	return m, machineNotFound(err)
}

func getMachine(ctx context.Context, q querier, id string) (machines.Machine, error) {
	return scanMachine(q.QueryRow(ctx, machineSelect+` AND m.id = $1`, id))
}

// Get devolve a máquina id, se não foi removida.
func (db *DB) Get(ctx context.Context, id string) (machines.Machine, error) {
	return getMachine(ctx, db.pool, id)
}

// Visible lista as máquinas de userID (todas, para admin).
func (db *DB) Visible(ctx context.Context, userID int64, admin bool) ([]machines.Machine, error) {
	rows, err := db.pool.Query(ctx, machineSelect+` AND ($2 OR m.owner_id = $1)
		ORDER BY lower(m.name), m.id`, userID, admin)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (machines.Machine, error) { return scanMachine(r) })
}

// Revoke marca a máquina como removida.
func (db *DB) Revoke(ctx context.Context, id string, now time.Time) error {
	tag, err := db.pool.Exec(ctx, `UPDATE machines SET revoked_at = $2 WHERE id = $1 AND revoked_at IS NULL`, id, now)
	if err == nil && tag.RowsAffected() == 0 {
		err = machines.ErrNotFound
	}
	return err
}
