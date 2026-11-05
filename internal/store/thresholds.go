package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/ldesfontaine/opencloud/internal/alert"
)

// ListThresholds rend le réglage global et, pour une machine, ses
// surcharges ; machineID vide, le seul global.
func (db *DB) ListThresholds(ctx context.Context, machineID string) ([]alert.Threshold, error) {
	rows, err := db.sql.QueryContext(ctx, `
		SELECT COALESCE(machine_id, ''), resource, attention_percent, danger_percent, window_seconds
		FROM alert_thresholds
		WHERE machine_id IS NULL OR machine_id = ?
		ORDER BY resource`, machineID)
	if err != nil {
		return nil, fmt.Errorf("list thresholds: %w", err)
	}
	return collect(rows, scanThreshold)
}

func scanThreshold(row scanner) (alert.Threshold, error) {
	var threshold alert.Threshold
	var windowSeconds int64
	err := row.Scan(&threshold.MachineID, &threshold.Resource, &threshold.Attention, &threshold.Danger, &windowSeconds)
	if err != nil {
		return alert.Threshold{}, fmt.Errorf("scan threshold: %w", err)
	}
	threshold.Window = time.Duration(windowSeconds) * time.Second
	return threshold, nil
}

// UpsertThreshold remplace la règle de cette ressource pour ce périmètre :
// deux index uniques partiels, un pour le global et un par machine.
func (db *DB) UpsertThreshold(ctx context.Context, threshold alert.Threshold) error {
	err := db.inTx(ctx, "threshold upsert", func(tx *sql.Tx) error {
		if err := deleteThreshold(ctx, tx, threshold.MachineID, threshold.Resource); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `
			INSERT INTO alert_thresholds (machine_id, resource, attention_percent, danger_percent, window_seconds)
			VALUES (?, ?, ?, ?, ?)`,
			nullableString(threshold.MachineID), string(threshold.Resource), threshold.Attention, threshold.Danger,
			int64(threshold.Window/time.Second))
		if err != nil {
			return fmt.Errorf("insert threshold: %w", err)
		}
		return nil
	})
	return err
}

func (db *DB) DeleteThreshold(ctx context.Context, machineID string, resource alert.Resource) error {
	return db.inTx(ctx, "threshold delete", func(tx *sql.Tx) error {
		return deleteThreshold(ctx, tx, machineID, resource)
	})
}

func deleteThreshold(ctx context.Context, tx *sql.Tx, machineID string, resource alert.Resource) error {
	_, err := tx.ExecContext(ctx, `
		DELETE FROM alert_thresholds WHERE resource = ? AND machine_id IS ?`,
		string(resource), nullableString(machineID))
	if err != nil {
		return fmt.Errorf("delete threshold: %w", err)
	}
	return nil
}

// AverageUsage rend la moyenne des lectures brutes de [from, to] : la
// mémoire est moyennée en pourcentage de chaque lecture, pas en octets,
// pour qu'un total qui change ne fausse pas la moyenne.
func (db *DB) AverageUsage(ctx context.Context, machineID string, from, to time.Time) (alert.UsageAverage, error) {
	var average alert.UsageAverage
	var first int64
	err := db.sql.QueryRowContext(ctx, `
		SELECT COUNT(*), COALESCE(MIN(sampled_at), 0), COALESCE(AVG(cpu_percent), 0),
			COALESCE(AVG(CASE WHEN mem_total > 0 THEN mem_used * 100.0 / mem_total END), 0)
		FROM machine_samples
		WHERE machine_id = ? AND sampled_at >= ? AND sampled_at <= ?`,
		machineID, from.Unix(), to.Unix()).Scan(&average.Samples, &first, &average.CPUPercent, &average.MemoryPercent)
	if err != nil {
		return alert.UsageAverage{}, fmt.Errorf("average usage: %w", err)
	}
	if first != 0 {
		average.First = time.Unix(first, 0)
	}
	return average, nil
}
