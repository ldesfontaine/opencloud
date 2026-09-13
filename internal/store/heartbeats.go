package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/ldesfontaine/opencloud/internal/heartbeat"
)

const heartbeatColumns = `h.id, h.token, h.name, h.machine_id, COALESCE(m.name, ''), h.status,
	h.interval_seconds, h.grace_seconds, h.last_ping_at, h.next_deadline_at, h.run_started_at,
	h.last_exit_code, h.last_duration_ms, h.created_at`

const heartbeatFrom = ` FROM heartbeats h LEFT JOIN machines m ON m.id = h.machine_id`

func (db *DB) InsertHeartbeat(ctx context.Context, h heartbeat.Heartbeat) error {
	_, err := db.sql.ExecContext(ctx, `
		INSERT INTO heartbeats (id, token, name, machine_id, status, interval_seconds, grace_seconds, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		h.ID, h.Token, h.Name, nullableString(h.MachineID), string(h.Status),
		int64(h.Interval.Seconds()), int64(h.Grace.Seconds()), h.CreatedAt.Unix())
	if err != nil {
		return fmt.Errorf("insert heartbeat: %w", err)
	}
	return nil
}

// ListHeartbeats rend les moniteurs par nom.
func (db *DB) ListHeartbeats(ctx context.Context) ([]heartbeat.Heartbeat, error) {
	return db.queryHeartbeats(ctx, `SELECT `+heartbeatColumns+heartbeatFrom+` ORDER BY h.name`)
}

func (db *DB) GetHeartbeat(ctx context.Context, id string) (heartbeat.Heartbeat, error) {
	row := db.sql.QueryRowContext(ctx, `SELECT `+heartbeatColumns+heartbeatFrom+` WHERE h.id = ?`, id)
	return scanHeartbeat(row)
}

func (db *DB) GetHeartbeatByToken(ctx context.Context, token string) (heartbeat.Heartbeat, error) {
	row := db.sql.QueryRowContext(ctx, `SELECT `+heartbeatColumns+heartbeatFrom+` WHERE h.token = ?`, token)
	return scanHeartbeat(row)
}

func (db *DB) DeleteHeartbeat(ctx context.Context, id string) error {
	result, err := db.sql.ExecContext(ctx, `DELETE FROM heartbeats WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete heartbeat: %w", err)
	}
	return heartbeatNotFoundIfNoRow(result)
}

// CountHeartbeats rend le nombre de moniteurs et ceux à traiter, en retard
// ou en échec, sans charger les lignes : la barre latérale le demande à
// chaque page.
func (db *DB) CountHeartbeats(ctx context.Context) (total, attention int, err error) {
	err = db.sql.QueryRowContext(ctx, `
		SELECT COUNT(*), COALESCE(SUM(status IN (?, ?)), 0) FROM heartbeats`,
		string(heartbeat.StatusLate), string(heartbeat.StatusFailed)).Scan(&total, &attention)
	if err != nil {
		return 0, 0, fmt.Errorf("count heartbeats: %w", err)
	}
	return total, attention, nil
}

// ListOverdueHeartbeats présélectionne les moniteurs dont l'échéance est
// passée ; seuls ceux qui en attendent une en ont.
func (db *DB) ListOverdueHeartbeats(ctx context.Context, now time.Time) ([]heartbeat.Heartbeat, error) {
	return db.queryHeartbeats(ctx,
		`SELECT `+heartbeatColumns+heartbeatFrom+` WHERE h.next_deadline_at IS NOT NULL AND h.next_deadline_at < ? ORDER BY h.name`,
		now.Unix())
}

// Transact prend le verrou d'écriture, relit le moniteur, laisse decide
// choisir, puis écrit le ping, l'exécution et l'état dans la même
// transaction : un ping est tout ou rien, et jamais calculé sur un état
// qu'un autre ping vient de changer.
func (db *DB) Transact(ctx context.Context, id string, decide heartbeat.Decision) error {
	tx, err := db.sql.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin transition: %w", err)
	}
	defer tx.Rollback()

	current, err := scanHeartbeat(tx.QueryRowContext(ctx, `SELECT `+heartbeatColumns+heartbeatFrom+` WHERE h.id = ?`, id))
	if err != nil {
		return err
	}
	transition := decide(current)
	if transition == nil {
		return nil
	}
	if err := applyTransition(ctx, tx, *transition); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit transition: %w", err)
	}
	return nil
}

func applyTransition(ctx context.Context, tx *sql.Tx, t heartbeat.Transition) error {
	if t.Ping != nil {
		if err := insertPing(ctx, tx, *t.Ping); err != nil {
			return err
		}
	}
	if t.CloseRun != nil {
		if err := closeCurrentRun(ctx, tx, *t.CloseRun); err != nil {
			return err
		}
	}
	if t.NewRun != nil {
		if err := insertRun(ctx, tx, *t.NewRun); err != nil {
			return err
		}
	}
	return updateHeartbeatState(ctx, tx, t.Heartbeat)
}

func insertPing(ctx context.Context, tx *sql.Tx, p heartbeat.Ping) error {
	_, err := tx.ExecContext(ctx, `
		INSERT INTO heartbeat_pings (heartbeat_id, kind, exit_code, source, method, payload, received_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		p.HeartbeatID, string(p.Kind), nullableInt(p.ExitCode), p.Source, p.Method, p.Payload, p.ReceivedAt.Unix())
	if err != nil {
		return fmt.Errorf("insert ping: %w", err)
	}
	return nil
}

// closeCurrentRun ferme l'exécution encore ouverte du moniteur ; l'index
// unique garantit qu'il n'y en a jamais plus d'une.
func closeCurrentRun(ctx context.Context, tx *sql.Tx, run heartbeat.Run) error {
	_, err := tx.ExecContext(ctx, `
		UPDATE heartbeat_runs SET completed_at = ?, duration_ms = ?, exit_code = ?, outcome = ?, payload = ?
		WHERE heartbeat_id = ? AND outcome = ?`,
		run.CompletedAt.Unix(), nullableMillis(run.Duration), nullableInt(run.ExitCode), string(run.Outcome), run.Payload,
		run.HeartbeatID, string(heartbeat.OutcomeInProgress))
	if err != nil {
		return fmt.Errorf("close run: %w", err)
	}
	return nil
}

func insertRun(ctx context.Context, tx *sql.Tx, run heartbeat.Run) error {
	_, err := tx.ExecContext(ctx, `
		INSERT INTO heartbeat_runs (heartbeat_id, started_at, completed_at, duration_ms, exit_code, outcome, payload)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		run.HeartbeatID, nullableTime(run.StartedAt), nullableTime(run.CompletedAt), nullableMillis(run.Duration),
		nullableInt(run.ExitCode), string(run.Outcome), run.Payload)
	if err != nil {
		return fmt.Errorf("insert run: %w", err)
	}
	return nil
}

func updateHeartbeatState(ctx context.Context, tx *sql.Tx, h heartbeat.Heartbeat) error {
	result, err := tx.ExecContext(ctx, `
		UPDATE heartbeats SET status = ?, last_ping_at = ?, next_deadline_at = ?, run_started_at = ?,
			last_exit_code = ?, last_duration_ms = ?
		WHERE id = ?`,
		string(h.Status), nullableTime(h.LastPingAt), nullableTime(h.NextDeadlineAt), nullableTime(h.RunStartedAt),
		nullableInt(h.LastExitCode), nullableMillis(h.LastDuration), h.ID)
	if err != nil {
		return fmt.Errorf("update heartbeat state: %w", err)
	}
	return heartbeatNotFoundIfNoRow(result)
}

// ListPings rend les derniers pings, le plus récent d'abord.
func (db *DB) ListPings(ctx context.Context, heartbeatID string, limit int) ([]heartbeat.Ping, error) {
	rows, err := db.sql.QueryContext(ctx, `
		SELECT id, heartbeat_id, kind, exit_code, source, method, payload, received_at
		FROM heartbeat_pings WHERE heartbeat_id = ? ORDER BY received_at DESC, id DESC LIMIT ?`, heartbeatID, limit)
	if err != nil {
		return nil, fmt.Errorf("list pings: %w", err)
	}
	defer rows.Close()
	var pings []heartbeat.Ping
	for rows.Next() {
		var p heartbeat.Ping
		var kind string
		var exitCode sql.NullInt64
		var receivedAt int64
		if err := rows.Scan(&p.ID, &p.HeartbeatID, &kind, &exitCode, &p.Source, &p.Method, &p.Payload, &receivedAt); err != nil {
			return nil, err
		}
		p.Kind = heartbeat.Kind(kind)
		p.ExitCode = intPointer(exitCode)
		p.ReceivedAt = time.Unix(receivedAt, 0)
		pings = append(pings, p)
	}
	return pings, rows.Err()
}

// ListRuns rend les dernières exécutions, la plus récente d'abord.
func (db *DB) ListRuns(ctx context.Context, heartbeatID string, limit int) ([]heartbeat.Run, error) {
	rows, err := db.sql.QueryContext(ctx, `
		SELECT id, heartbeat_id, started_at, completed_at, duration_ms, exit_code, outcome, payload
		FROM heartbeat_runs WHERE heartbeat_id = ? ORDER BY id DESC LIMIT ?`, heartbeatID, limit)
	if err != nil {
		return nil, fmt.Errorf("list runs: %w", err)
	}
	defer rows.Close()
	var runs []heartbeat.Run
	for rows.Next() {
		var r heartbeat.Run
		var outcome string
		var startedAt, completedAt, durationMs, exitCode sql.NullInt64
		if err := rows.Scan(&r.ID, &r.HeartbeatID, &startedAt, &completedAt, &durationMs, &exitCode, &outcome, &r.Payload); err != nil {
			return nil, err
		}
		r.Outcome = heartbeat.Outcome(outcome)
		r.StartedAt = timeOf(startedAt)
		r.CompletedAt = timeOf(completedAt)
		r.Duration = durationPointer(durationMs)
		r.ExitCode = intPointer(exitCode)
		runs = append(runs, r)
	}
	return runs, rows.Err()
}

// PurgeHistory efface le brut et les exécutions trop anciens ; une
// exécution ouverte se juge à son début.
func (db *DB) PurgeHistory(ctx context.Context, pingsBefore, runsBefore time.Time) error {
	tx, err := db.sql.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin purge: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM heartbeat_pings WHERE received_at < ?`, pingsBefore.Unix()); err != nil {
		return fmt.Errorf("purge pings: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM heartbeat_runs WHERE COALESCE(completed_at, started_at) < ?`, runsBefore.Unix()); err != nil {
		return fmt.Errorf("purge runs: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit purge: %w", err)
	}
	return nil
}

func (db *DB) queryHeartbeats(ctx context.Context, query string, args ...any) ([]heartbeat.Heartbeat, error) {
	rows, err := db.sql.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list heartbeats: %w", err)
	}
	defer rows.Close()
	var heartbeats []heartbeat.Heartbeat
	for rows.Next() {
		h, err := scanHeartbeat(rows)
		if err != nil {
			return nil, err
		}
		heartbeats = append(heartbeats, h)
	}
	return heartbeats, rows.Err()
}

func scanHeartbeat(row scanner) (heartbeat.Heartbeat, error) {
	var h heartbeat.Heartbeat
	var machineID sql.NullString
	var status string
	var intervalSeconds, graceSeconds, createdAt int64
	var lastPingAt, nextDeadlineAt, runStartedAt, lastExitCode, lastDurationMs sql.NullInt64
	err := row.Scan(&h.ID, &h.Token, &h.Name, &machineID, &h.MachineName, &status,
		&intervalSeconds, &graceSeconds, &lastPingAt, &nextDeadlineAt, &runStartedAt,
		&lastExitCode, &lastDurationMs, &createdAt)
	if errors.Is(err, sql.ErrNoRows) {
		return heartbeat.Heartbeat{}, heartbeat.ErrNotFound
	}
	if err != nil {
		return heartbeat.Heartbeat{}, err
	}
	h.MachineID = machineID.String
	h.Status = heartbeat.Status(status)
	h.Interval = time.Duration(intervalSeconds) * time.Second
	h.Grace = time.Duration(graceSeconds) * time.Second
	h.LastPingAt = timeOf(lastPingAt)
	h.NextDeadlineAt = timeOf(nextDeadlineAt)
	h.RunStartedAt = timeOf(runStartedAt)
	h.LastExitCode = intPointer(lastExitCode)
	h.LastDuration = durationPointer(lastDurationMs)
	h.CreatedAt = time.Unix(createdAt, 0)
	return h, nil
}

func heartbeatNotFoundIfNoRow(result sql.Result) error {
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return heartbeat.ErrNotFound
	}
	return nil
}
