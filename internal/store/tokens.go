package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/ldesfontaine/opencloud/internal/machine"
)

const tokenColumns = `id, token_hash, token_prefix, name, machine_id, created_at, expires_at, consumed_at, consumed_by` // #nosec G101 -- des noms de colonnes.

func (db *DB) InsertToken(ctx context.Context, t machine.Token) error {
	_, err := db.sql.ExecContext(ctx, `
		INSERT INTO enrollment_tokens (id, token_hash, token_prefix, name, machine_id, created_at, expires_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		t.ID, t.Hash, t.Prefix, t.Name, nullableString(t.MachineID), t.CreatedAt.Unix(), t.ExpiresAt.Unix())
	if err != nil {
		return fmt.Errorf("insert token: %w", err)
	}
	return nil
}

// ListPendingTokens rend les jetons encore valables et non consommés, les
// plus récents d'abord.
func (db *DB) ListPendingTokens(ctx context.Context, now time.Time) ([]machine.Token, error) {
	rows, err := db.sql.QueryContext(ctx, `
		SELECT `+tokenColumns+` FROM enrollment_tokens
		WHERE consumed_at IS NULL AND expires_at > ?
		ORDER BY created_at DESC`, now.Unix())
	if err != nil {
		return nil, fmt.Errorf("list pending tokens: %w", err)
	}
	defer rows.Close()
	var tokens []machine.Token
	for rows.Next() {
		t, err := scanToken(rows)
		if err != nil {
			return nil, err
		}
		tokens = append(tokens, t)
	}
	return tokens, rows.Err()
}

// DeleteToken annule un jeton non consommé ; un jeton déjà servi reste, pour
// l'historique.
func (db *DB) DeleteToken(ctx context.Context, id string) error {
	result, err := db.sql.ExecContext(ctx,
		`DELETE FROM enrollment_tokens WHERE id = ? AND consumed_at IS NULL`, id)
	if err != nil {
		return fmt.Errorf("delete token: %w", err)
	}
	if err := notFoundIfNoRow(result); err != nil {
		return machine.ErrTokenNotFound
	}
	return nil
}

// PurgeExpiredTokens efface les jetons périmés jamais consommés.
func (db *DB) PurgeExpiredTokens(ctx context.Context, before time.Time) error {
	_, err := db.sql.ExecContext(ctx,
		`DELETE FROM enrollment_tokens WHERE consumed_at IS NULL AND expires_at <= ?`, before.Unix())
	if err != nil {
		return fmt.Errorf("purge expired tokens: %w", err)
	}
	return nil
}

func scanToken(row scanner) (machine.Token, error) {
	var t machine.Token
	var machineID, consumedBy sql.NullString
	var createdAt, expiresAt int64
	var consumedAt sql.NullInt64
	err := row.Scan(&t.ID, &t.Hash, &t.Prefix, &t.Name, &machineID, &createdAt, &expiresAt, &consumedAt, &consumedBy)
	if err != nil {
		return machine.Token{}, err
	}
	t.MachineID = machineID.String
	t.ConsumedBy = consumedBy.String
	t.CreatedAt = time.Unix(createdAt, 0)
	t.ExpiresAt = time.Unix(expiresAt, 0)
	if consumedAt.Valid {
		t.ConsumedAt = time.Unix(consumedAt.Int64, 0)
	}
	return t, nil
}
