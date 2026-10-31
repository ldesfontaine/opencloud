package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/ldesfontaine/opencloud/internal/backup"
)

// RecordBackupRun écrit la ligne et purge, dans la même transaction, ce que le
// plan garde au-delà de backup.MaxRunsPerPlan.
func (db *DB) RecordBackupRun(ctx context.Context, record backup.RunRecord) error {
	return db.inTx(ctx, "record backup run", func(tx *sql.Tx) error {
		var size any
		if record.SizeBytes != nil {
			size = *record.SizeBytes
		}
		_, err := tx.ExecContext(ctx, `
			INSERT INTO backup_runs (subject, pass_at, source, started_at, finished_at, result, reason, size_bytes,
				destination_id, destination_name, action_run_id)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			record.Subject, record.PassAt.Unix(), record.Source, record.StartedAt.Unix(), record.FinishedAt.Unix(),
			string(record.Result), record.Reason, size, record.DestinationID, record.DestinationName, record.ActionRunID)
		if err != nil {
			return fmt.Errorf("insert backup run: %w", err)
		}
		_, err = tx.ExecContext(ctx, `
			DELETE FROM backup_runs WHERE subject = ? AND id NOT IN (
				SELECT id FROM backup_runs WHERE subject = ? ORDER BY id DESC LIMIT ?)`,
			record.Subject, record.Subject, backup.MaxRunsPerPlan)
		if err != nil {
			return fmt.Errorf("purge backup runs: %w", err)
		}
		return nil
	})
}

// ListBackupRuns rend l'historique, les plus récentes d'abord ; un plan ou une
// source vide ne filtre pas.
func (db *DB) ListBackupRuns(ctx context.Context, query backup.RunQuery) ([]backup.RunRecord, error) {
	limit := query.Limit
	if limit <= 0 {
		limit = backup.DefaultRunsLimit
	}
	rows, err := db.sql.QueryContext(ctx, `
		SELECT id, subject, pass_at, source, started_at, finished_at, result, reason, size_bytes,
			destination_id, destination_name, action_run_id
		FROM backup_runs
		WHERE (? = '' OR subject = ?) AND (? = '' OR source = ?)
		ORDER BY id DESC LIMIT ?`,
		query.Subject, query.Subject, query.Source, query.Source, limit)
	if err != nil {
		return nil, fmt.Errorf("list backup runs: %w", err)
	}
	return collect(rows, scanBackupRun)
}

func scanBackupRun(row scanner) (backup.RunRecord, error) {
	var record backup.RunRecord
	var passAt, startedAt, finishedAt int64
	var size sql.NullInt64
	var result string
	err := row.Scan(&record.ID, &record.Subject, &passAt, &record.Source, &startedAt, &finishedAt, &result, &record.Reason,
		&size, &record.DestinationID, &record.DestinationName, &record.ActionRunID)
	record.PassAt, record.StartedAt, record.FinishedAt = time.Unix(passAt, 0), time.Unix(startedAt, 0), time.Unix(finishedAt, 0)
	record.Result = backup.Result(result)
	if size.Valid {
		record.SizeBytes = &size.Int64
	}
	return record, err
}

// LastPassCounts compte, par plan, les cibles reçues et en échec de sa dernière
// tentative. Une ligne sans source (le plan entier refusé) ne compte pas : ce
// sont des cibles.
func (db *DB) LastPassCounts(ctx context.Context) (map[string]backup.PassCounts, error) {
	rows, err := db.sql.QueryContext(ctx, `
		SELECT r.subject, COALESCE(SUM(r.result = 'ok'), 0), COALESCE(SUM(r.result <> 'ok'), 0)
		FROM backup_runs r JOIN backup_plans p ON p.subject = r.subject AND r.pass_at = p.last_attempt_at
		WHERE r.source <> ''
		GROUP BY r.subject`)
	if err != nil {
		return nil, fmt.Errorf("count last backup passes: %w", err)
	}
	type counted struct {
		subject string
		counts  backup.PassCounts
	}
	list, err := collect(rows, func(row scanner) (counted, error) {
		var item counted
		err := row.Scan(&item.subject, &item.counts.OK, &item.counts.Failed)
		return item, err
	})
	if err != nil {
		return nil, fmt.Errorf("scan last backup passes: %w", err)
	}
	counts := make(map[string]backup.PassCounts, len(list))
	for _, item := range list {
		counts[item.subject] = item.counts
	}
	return counts, nil
}
