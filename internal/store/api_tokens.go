package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/ldesfontaine/opencloud/internal/apitoken"
)

var _ apitoken.Store = (*DB)(nil)

const apiTokenColumns = `id, token_hash, token_prefix, scope, name, created_at, expires_at, last_used_at` // #nosec G101 -- des noms de colonnes.

func (db *DB) InsertAPIToken(ctx context.Context, token apitoken.Token) error {
	_, err := db.sql.ExecContext(ctx, `
		INSERT INTO api_tokens (id, token_hash, token_prefix, scope, name, created_at, expires_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		token.ID, token.Hash, token.Prefix, string(token.Scope), token.Name, token.CreatedAt.Unix(), nullableTime(token.ExpiresAt))
	if err != nil {
		return fmt.Errorf("insert api token: %w", err)
	}
	return nil
}

func (db *DB) GetAPIToken(ctx context.Context, hash string) (apitoken.Token, error) {
	token, err := scanAPIToken(db.sql.QueryRowContext(ctx, `SELECT `+apiTokenColumns+` FROM api_tokens WHERE token_hash = ?`, hash))
	if errors.Is(err, sql.ErrNoRows) {
		return apitoken.Token{}, apitoken.ErrNotFound
	}
	if err != nil {
		return apitoken.Token{}, fmt.Errorf("get api token: %w", err)
	}
	return token, nil
}

// ListAPITokens rend les jetons, les plus récents d'abord.
func (db *DB) ListAPITokens(ctx context.Context) ([]apitoken.Token, error) {
	rows, err := db.sql.QueryContext(ctx, `SELECT `+apiTokenColumns+` FROM api_tokens ORDER BY created_at DESC, id`)
	if err != nil {
		return nil, fmt.Errorf("list api tokens: %w", err)
	}
	return collect(rows, scanAPIToken)
}

func (db *DB) TouchAPIToken(ctx context.Context, id string, at time.Time) error {
	if _, err := db.sql.ExecContext(ctx, `UPDATE api_tokens SET last_used_at = ? WHERE id = ?`, at.Unix(), id); err != nil {
		return fmt.Errorf("touch api token: %w", err)
	}
	return nil
}

func (db *DB) DeleteAPIToken(ctx context.Context, id string) error {
	result, err := db.sql.ExecContext(ctx, `DELETE FROM api_tokens WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete api token: %w", err)
	}
	return notFoundIfNoRow(result, apitoken.ErrNotFound)
}

// DeleteAllAPITokens efface tout jeton vivant et dit combien. Un jeton déjà
// échu ne compte pas : il ne servait plus.
func (db *DB) DeleteAllAPITokens(ctx context.Context, now time.Time) (int, error) {
	var revoked int
	err := db.inTx(ctx, "delete api tokens", func(tx *sql.Tx) error {
		err := tx.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM api_tokens WHERE expires_at IS NULL OR expires_at > ?`, now.Unix()).Scan(&revoked)
		if err != nil {
			return fmt.Errorf("count api tokens: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM api_tokens`); err != nil {
			return fmt.Errorf("delete api tokens: %w", err)
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return revoked, nil
}

// PurgeExpiredAPITokens efface les jetons échus.
func (db *DB) PurgeExpiredAPITokens(ctx context.Context, before time.Time) error {
	if _, err := db.sql.ExecContext(ctx, `DELETE FROM api_tokens WHERE expires_at IS NOT NULL AND expires_at <= ?`, before.Unix()); err != nil {
		return fmt.Errorf("purge api tokens: %w", err)
	}
	return nil
}

func scanAPIToken(row scanner) (apitoken.Token, error) {
	var token apitoken.Token
	var scope string
	var createdAt int64
	var expiresAt, lastUsedAt sql.NullInt64
	err := row.Scan(&token.ID, &token.Hash, &token.Prefix, &scope, &token.Name, &createdAt, &expiresAt, &lastUsedAt)
	if err != nil {
		return apitoken.Token{}, err
	}
	token.Scope = apitoken.Scope(scope)
	token.CreatedAt = time.Unix(createdAt, 0)
	token.ExpiresAt = timeOf(expiresAt)
	token.LastUsedAt = timeOf(lastUsedAt)
	return token, nil
}
