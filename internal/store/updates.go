package store

import (
	"context"
	"fmt"
	"time"

	"github.com/ldesfontaine/opencloud/internal/service"
	"github.com/ldesfontaine/opencloud/internal/update"
)

const imageCheckColumns = `machine_id, image, checked_at, outcome, local_digest, remote_digest, newer_tag, newer_digest, kind`

// ApplyImageChecks écrase le constat de chaque (machine, image), en une
// transaction.
func (db *DB) ApplyImageChecks(ctx context.Context, checks []update.Check) error {
	tx, err := db.sql.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin apply image checks: %w", err)
	}
	defer tx.Rollback()
	for _, check := range checks {
		_, err := tx.ExecContext(ctx, `
			INSERT INTO image_checks (`+imageCheckColumns+`)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT (machine_id, image) DO UPDATE SET
				checked_at = excluded.checked_at, outcome = excluded.outcome,
				local_digest = excluded.local_digest, remote_digest = excluded.remote_digest,
				newer_tag = excluded.newer_tag, newer_digest = excluded.newer_digest, kind = excluded.kind`,
			check.MachineID, check.Image, check.CheckedAt.Unix(), string(check.Outcome),
			check.LocalDigest, check.RemoteDigest, check.NewerTag, check.NewerDigest, string(check.Kind))
		if err != nil {
			return fmt.Errorf("upsert image check %s: %w", check.Image, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit apply image checks: %w", err)
	}
	return nil
}

// ListImageChecks rend les constats d'une machine, ou de toutes si
// machineID est vide.
func (db *DB) ListImageChecks(ctx context.Context, machineID string) ([]update.Check, error) {
	query := `SELECT ` + imageCheckColumns + ` FROM image_checks`
	var args []any
	if machineID != "" {
		query += ` WHERE machine_id = ?`
		args = append(args, machineID)
	}
	rows, err := db.sql.QueryContext(ctx, query+` ORDER BY machine_id, image`, args...)
	if err != nil {
		return nil, fmt.Errorf("list image checks: %w", err)
	}
	defer rows.Close()
	checks := []update.Check{}
	for rows.Next() {
		var check update.Check
		var checkedAt int64
		var outcome, kind string
		if err := rows.Scan(&check.MachineID, &check.Image, &checkedAt, &outcome, &check.LocalDigest, &check.RemoteDigest, &check.NewerTag, &check.NewerDigest, &kind); err != nil {
			return nil, err
		}
		check.CheckedAt = time.Unix(checkedAt, 0).UTC()
		check.Outcome, check.Kind = update.Outcome(outcome), update.Kind(kind)
		checks = append(checks, check)
	}
	return checks, rows.Err()
}

func (db *DB) PurgeImageChecks(ctx context.Context, before time.Time) error {
	if _, err := db.sql.ExecContext(ctx, `DELETE FROM image_checks WHERE checked_at < ?`, before.Unix()); err != nil {
		return fmt.Errorf("purge image checks: %w", err)
	}
	return nil
}

func (db *DB) SetServiceUpdatePolicy(ctx context.Context, id string, policy service.UpdatePolicy) error {
	result, err := db.sql.ExecContext(ctx, `UPDATE services SET update_policy = ? WHERE id = ?`, string(policy), id)
	if err != nil {
		return fmt.Errorf("set update policy of %s: %w", id, err)
	}
	if affected, _ := result.RowsAffected(); affected == 0 {
		return service.ErrNotFound
	}
	return nil
}
