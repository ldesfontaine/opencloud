package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/ldesfontaine/opencloud/internal/directory"
)

// ListIgnoredKeys rend les clés ignorées d'une machine, de toutes si
// machineID est vide, par machine, genre et valeur.
func (db *DB) ListIgnoredKeys(ctx context.Context, machineID string) ([]directory.IgnoredKey, error) {
	query := `SELECT machine_id, kind, value, created_at FROM ignored_keys`
	var args []any
	if machineID != "" {
		query += ` WHERE machine_id = ?`
		args = append(args, machineID)
	}
	rows, err := db.sql.QueryContext(ctx, query+` ORDER BY machine_id, kind, value`, args...)
	if err != nil {
		return nil, fmt.Errorf("list ignored keys: %w", err)
	}
	return collect(rows, scanIgnoredKey)
}

func scanIgnoredKey(row scanner) (directory.IgnoredKey, error) {
	var key directory.IgnoredKey
	var kind string
	var createdAt int64
	if err := row.Scan(&key.MachineID, &kind, &key.Value, &createdAt); err != nil {
		return directory.IgnoredKey{}, err
	}
	key.Kind = directory.IgnoreKind(kind)
	key.CreatedAt = time.Unix(createdAt, 0)
	return key, nil
}

// InsertIgnoredKey est idempotent : dire deux fois qu'une clé est ignorée
// garde la première date. Lire la machine avant d'écrire donne le refus
// précis, là où une clé étrangère violée ne dirait rien.
func (db *DB) InsertIgnoredKey(ctx context.Context, key directory.IgnoredKey) error {
	return db.inTx(ctx, "insert ignored key", func(tx *sql.Tx) error {
		var machines int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM machines WHERE id = ?`, key.MachineID).Scan(&machines); err != nil {
			return fmt.Errorf("find ignored key machine: %w", err)
		}
		if machines == 0 {
			return directory.ErrMachineInvalid
		}
		_, err := tx.ExecContext(ctx,
			`INSERT OR IGNORE INTO ignored_keys (machine_id, kind, value, created_at) VALUES (?, ?, ?, ?)`,
			key.MachineID, string(key.Kind), key.Value, key.CreatedAt.Unix())
		if err != nil {
			return fmt.Errorf("insert ignored key: %w", err)
		}
		return nil
	})
}

func (db *DB) DeleteIgnoredKey(ctx context.Context, key directory.IgnoredKey) error {
	result, err := db.sql.ExecContext(ctx,
		`DELETE FROM ignored_keys WHERE machine_id = ? AND kind = ? AND value = ?`,
		key.MachineID, string(key.Kind), key.Value)
	if err != nil {
		return fmt.Errorf("delete ignored key: %w", err)
	}
	return notFoundIfNoRow(result, directory.ErrIgnoredNotFound)
}
