package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/ldesfontaine/opencloud/internal/auth"
)

const authStateSelect = `SELECT mode, username, password_hash, forward_header, enabled_at FROM auth_settings WHERE id = 1`

// GetAuthState rend l'état de l'accès à l'interface ; sans ligne, le mode
// est « none » : une installation neuve n'a pas encore d'identifiants.
func (db *DB) GetAuthState(ctx context.Context) (auth.State, error) {
	return scanAuthState(db.sql.QueryRowContext(ctx, authStateSelect))
}

func scanAuthState(row scanner) (auth.State, error) {
	var state auth.State
	var mode string
	var enabledAt sql.NullInt64
	err := row.Scan(&mode, &state.Username, &state.PasswordHash, &state.Header, &enabledAt)
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

// SaveAuthStateIf remplace la ligne unique, créée au premier appel, seulement
// si elle vaut encore expected : la lecture et l'écriture tiennent dans une
// même transaction, qui prend le verrou d'écriture dès son début (_txlock,
// voir openHandle). Rend faux, sans rien écrire, quand l'état a changé
// entre-temps.
func (db *DB) SaveAuthStateIf(ctx context.Context, expected, next auth.State, at time.Time) (bool, error) {
	tx, err := db.sql.BeginTx(ctx, nil)
	if err != nil {
		return false, fmt.Errorf("begin auth state update: %w", err)
	}
	defer tx.Rollback()

	current, err := scanAuthState(tx.QueryRowContext(ctx, authStateSelect))
	if err != nil {
		return false, err
	}
	if !expected.Same(current) {
		return false, nil
	}
	if err := writeAuthState(ctx, tx, next, at); err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("commit auth state update: %w", err)
	}
	return true, nil
}

func writeAuthState(ctx context.Context, to execer, state auth.State, at time.Time) error {
	_, err := to.ExecContext(ctx, `
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
