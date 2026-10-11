package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/ldesfontaine/opencloud/internal/auth"
)

// GetAuthState rend l'état de l'accès à l'interface ; sans ligne, le mode
// est « none » : une installation neuve n'a pas encore d'identifiants.
func (db *DB) GetAuthState(ctx context.Context) (auth.State, error) {
	var state auth.State
	var mode string
	var enabledAt sql.NullInt64
	err := db.sql.QueryRowContext(ctx,
		`SELECT mode, username, password_hash, forward_header, enabled_at FROM auth_settings WHERE id = 1`,
	).Scan(&mode, &state.Username, &state.PasswordHash, &state.Header, &enabledAt)
	if errors.Is(err, sql.ErrNoRows) {
		return auth.State{Mode: auth.ModeNone}, nil
	}
	if err != nil {
		return auth.State{}, fmt.Errorf("get auth state: %w", err)
	}
	state.Mode = auth.Mode(mode)
	if enabledAt.Valid {
		state.EnabledAt = time.Unix(enabledAt.Int64, 0)
	}
	return state, nil
}

// SaveAuthState remplace la ligne unique, créée au premier appel.
func (db *DB) SaveAuthState(ctx context.Context, state auth.State, at time.Time) error {
	_, err := db.sql.ExecContext(ctx, `
		INSERT INTO auth_settings (id, mode, username, password_hash, forward_header, enabled_at, updated_at)
		VALUES (1, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (id) DO UPDATE SET
			mode = excluded.mode,
			username = excluded.username,
			password_hash = excluded.password_hash,
			forward_header = excluded.forward_header,
			enabled_at = excluded.enabled_at,
			updated_at = excluded.updated_at`,
		string(state.Mode), state.Username, state.PasswordHash, state.Header, nullableTime(state.EnabledAt), at.Unix())
	if err != nil {
		return fmt.Errorf("save auth state: %w", err)
	}
	return nil
}
