package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/ldesfontaine/opencloud/internal/container"
	"github.com/ldesfontaine/opencloud/internal/directory"
	"github.com/ldesfontaine/opencloud/internal/update"
)

const imageCheckColumns = `machine_id, image, checked_at, outcome, local_digest, remote_digest, newer_tag, newer_digest, kind`

// ApplyImageChecks écrase le constat de chaque (machine, image), en une
// transaction.
func (db *DB) ApplyImageChecks(ctx context.Context, checks []update.Check) error {
	return db.inTx(ctx, "apply image checks", func(tx *sql.Tx) error {
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
		return nil
	})
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
	return collect(rows, scanImageCheck)
}

func scanImageCheck(row scanner) (update.Check, error) {
	var check update.Check
	var checkedAt int64
	var outcome, kind string
	if err := row.Scan(&check.MachineID, &check.Image, &checkedAt, &outcome, &check.LocalDigest, &check.RemoteDigest, &check.NewerTag, &check.NewerDigest, &kind); err != nil {
		return update.Check{}, err
	}
	check.CheckedAt = time.Unix(checkedAt, 0).UTC()
	check.Outcome, check.Kind = update.Outcome(outcome), update.Kind(kind)
	return check, nil
}

// CountContainerUpdates compte les conteneurs vivants dont l'image a un
// constat plus récent (update.Check.HasUpdate : un type posé) et dont
// l'unité veut le voir ; sans unité, la fiche suit « watch ».
func (db *DB) CountContainerUpdates(ctx context.Context) (int, error) {
	var count int
	err := db.sql.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM containers c
		JOIN image_checks ic ON ic.machine_id = c.machine_id AND ic.image = c.image
		WHERE c.archived_at IS NULL AND ic.kind != ''
		AND COALESCE((SELECT u.update_policy FROM service_units u WHERE u.id = c.unit_id), 'watch') = 'watch'`).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("count container updates: %w", err)
	}
	return count, nil
}

func (db *DB) PurgeImageChecks(ctx context.Context, before time.Time) error {
	if _, err := db.sql.ExecContext(ctx, `DELETE FROM image_checks WHERE checked_at < ?`, before.Unix()); err != nil {
		return fmt.Errorf("purge image checks: %w", err)
	}
	return nil
}

// SetUnitUpdatePolicy écrit la politique de mise à jour d'une unité de
// l'annuaire ; tous ses conteneurs la lisent.
func (db *DB) SetUnitUpdatePolicy(ctx context.Context, unitID string, policy container.UpdatePolicy) error {
	result, err := db.sql.ExecContext(ctx, `UPDATE service_units SET update_policy = ? WHERE id = ?`, string(policy), unitID)
	if err != nil {
		return fmt.Errorf("set update policy of unit %s: %w", unitID, err)
	}
	return notFoundIfNoRow(result, directory.ErrUnitNotFound)
}
