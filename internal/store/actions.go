package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/ldesfontaine/opencloud/internal/action"
	"github.com/ldesfontaine/opencloud/internal/agentwire"
	"github.com/ldesfontaine/opencloud/internal/machine"
)

var _ action.Store = (*DB)(nil)

// InsertRun prend le rang suivant de l'agent et écrit le run, dans la même
// transaction : deux ordres simultanés n'ont jamais le même rang, et un
// agent révoqué entre-temps n'en reçoit aucun.
func (db *DB) InsertRun(ctx context.Context, run action.Run) (action.Run, error) {
	err := db.inTx(ctx, "insert run", func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, `
			UPDATE agents SET last_order_seq = last_order_seq + 1
			WHERE id = ? AND machine_id = ? AND revoked_at IS NULL`, run.AgentID, run.MachineID)
		if err != nil {
			return fmt.Errorf("take order seq: %w", err)
		}
		if err := notFoundIfNoRow(result, machine.ErrNoAgent); err != nil {
			return err
		}
		if err := tx.QueryRowContext(ctx, `SELECT last_order_seq FROM agents WHERE id = ?`, run.AgentID).Scan(&run.Seq); err != nil {
			return fmt.Errorf("read order seq: %w", err)
		}
		_, err = tx.ExecContext(ctx, `
			INSERT INTO action_runs (id, action, params, machine_id, machine_name, agent_id, state,
				not_after, seq, requested_by, requested_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			run.ID, string(run.Kind), paramsText(run.Params), run.MachineID, run.MachineName, run.AgentID,
			string(run.State), run.NotAfter.Unix(), run.Seq, run.RequestedBy, run.RequestedAt.Unix())
		if err != nil {
			return fmt.Errorf("insert run: %w", err)
		}
		return nil
	})
	return run, err
}

// paramsText : des paramètres absents s'écrivent comme un objet vide.
func paramsText(params []byte) string {
	if len(params) == 0 {
		return "{}"
	}
	return string(params)
}

// SetRunState pose un état que le serveur décide.
func (db *DB) SetRunState(ctx context.Context, id string, state action.State, reason string, finishedAt time.Time) error {
	result, err := db.sql.ExecContext(ctx, `UPDATE action_runs SET state = ?, refusal_reason = ?, finished_at = ? WHERE id = ?`,
		string(state), reason, nullableTime(finishedAt), id)
	if err != nil {
		return fmt.Errorf("set run state: %w", err)
	}
	return notFoundIfNoRow(result, sql.ErrNoRows)
}

// ApplyRunResult fait avancer un run de cette machine, sa sortie à la suite
// de celle déjà gardée. Un run fini ne bouge plus ; un run accepté ne revient
// pas à accepté.
func (db *DB) ApplyRunResult(ctx context.Context, machineID string, result action.Result, at time.Time) (action.Run, bool, error) {
	changed := false
	var run action.Run
	err := db.inTx(ctx, "apply run result", func(tx *sql.Tx) error {
		var current, kind, agentID string
		err := tx.QueryRowContext(ctx, `
			SELECT state, action, machine_name, COALESCE(agent_id, '') FROM action_runs WHERE id = ? AND machine_id = ?`,
			result.RunID, machineID).Scan(&current, &kind, &run.MachineName, &agentID)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("read run: %w", err)
		}
		run.ID, run.Kind, run.MachineID, run.AgentID = result.RunID, agentwire.Kind(kind), machineID, agentID
		state := action.State(current)
		if state.IsFinished() || (state == action.StateAccepted && result.State == action.StateAccepted) {
			return nil
		}
		finishedAt := time.Time{}
		if result.State.IsFinished() {
			finishedAt = at
		}
		_, err = tx.ExecContext(ctx, `UPDATE action_runs SET state = ?, refusal_reason = ?, finished_at = ? WHERE id = ?`,
			string(result.State), result.Reason, nullableTime(finishedAt), result.RunID)
		if err != nil {
			return fmt.Errorf("update run: %w", err)
		}
		if err := appendRunLogs(ctx, tx, result.RunID, result.Output, at); err != nil {
			return err
		}
		run.State, run.Reason, run.FinishedAt = result.State, result.Reason, finishedAt
		changed = true
		return nil
	})
	return run, changed, err
}

// appendRunLogs ajoute la sortie à la suite, dans la limite d'une sortie par
// run : ce qui dépasse est perdu, la fin d'abord.
func appendRunLogs(ctx context.Context, tx *sql.Tx, runID string, lines []action.LogLine, at time.Time) error {
	if len(lines) == 0 {
		return nil
	}
	var last int
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(seq), 0) FROM run_logs WHERE run_id = ?`, runID).Scan(&last); err != nil {
		return fmt.Errorf("read run log seq: %w", err)
	}
	room := max(agentwire.MaxOutputLines-last, 0)
	for index, line := range lines[:min(len(lines), room)] {
		_, err := tx.ExecContext(ctx, `INSERT INTO run_logs (run_id, seq, at, stream, line) VALUES (?, ?, ?, ?, ?)`,
			runID, last+index+1, at.Unix(), line.Stream, line.Line)
		if err != nil {
			return fmt.Errorf("insert run log: %w", err)
		}
	}
	return nil
}

// ExpireRuns pose expired sur ce qui attend encore son agent après
// not_after, et rend les machines touchées.
func (db *DB) ExpireRuns(ctx context.Context, now time.Time) ([]string, error) {
	var machines []string
	err := db.inTx(ctx, "expire runs", func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `
			SELECT DISTINCT COALESCE(machine_id, '') FROM action_runs
			WHERE state IN ('queued', 'sent') AND not_after < ?`, now.Unix())
		if err != nil {
			return fmt.Errorf("list expiring runs: %w", err)
		}
		machines, err = collect(rows, scanString)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `
			UPDATE action_runs SET state = 'expired', finished_at = ?
			WHERE state IN ('queued', 'sent') AND not_after < ?`, now.Unix(), now.Unix())
		if err != nil {
			return fmt.Errorf("expire runs: %w", err)
		}
		return nil
	})
	return machines, err
}

// TimeoutRuns pose failed, timeout sur ce que l'agent a accepté avant
// acceptedBefore sans le finir, et rend les machines touchées.
func (db *DB) TimeoutRuns(ctx context.Context, acceptedBefore, now time.Time) ([]string, error) {
	var machines []string
	err := db.inTx(ctx, "time out runs", func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `
			SELECT DISTINCT COALESCE(machine_id, '') FROM action_runs
			WHERE state = 'accepted' AND requested_at < ?`, acceptedBefore.Unix())
		if err != nil {
			return fmt.Errorf("list timing out runs: %w", err)
		}
		machines, err = collect(rows, scanString)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `
			UPDATE action_runs SET state = 'failed', refusal_reason = ?, finished_at = ?
			WHERE state = 'accepted' AND requested_at < ?`, action.ReasonTimeout, now.Unix(), acceptedBefore.Unix())
		if err != nil {
			return fmt.Errorf("time out runs: %w", err)
		}
		return nil
	})
	return machines, err
}

func scanString(row scanner) (string, error) {
	var value string
	err := row.Scan(&value)
	return value, err
}

// PurgeRuns retire les runs demandés avant before, leur sortie avec eux.
func (db *DB) PurgeRuns(ctx context.Context, before time.Time) error {
	if _, err := db.sql.ExecContext(ctx, `DELETE FROM action_runs WHERE requested_at < ?`, before.Unix()); err != nil {
		return fmt.Errorf("purge runs: %w", err)
	}
	return nil
}

// ListActionRuns rend les runs, d'une machine ou de toutes, du plus récent
// au plus ancien, avec leur sortie.
func (db *DB) ListActionRuns(ctx context.Context, query action.Query) ([]action.Run, error) {
	rows, err := db.sql.QueryContext(ctx, `
		SELECT id, action, params, COALESCE(machine_id, ''), machine_name, COALESCE(agent_id, ''), state,
			refusal_reason, not_after, seq, requested_by, requested_at, finished_at
		FROM action_runs
		WHERE (? = '' OR machine_id = ?)
		ORDER BY requested_at DESC, id DESC
		LIMIT ?`, query.MachineID, query.MachineID, query.Limit)
	if err != nil {
		return nil, fmt.Errorf("list runs: %w", err)
	}
	runs, err := collect(rows, scanActionRun)
	if err != nil {
		return nil, err
	}
	if err := attachRunLogs(ctx, db.sql, runs); err != nil {
		return nil, err
	}
	return runs, nil
}

// GetActionRun rend un run et sa sortie ; sql.ErrNoRows, enveloppé, s'il
// n'existe plus (la purge des runs l'a emporté).
func (db *DB) GetActionRun(ctx context.Context, id string) (action.Run, error) {
	run, err := scanActionRun(db.sql.QueryRowContext(ctx, `
		SELECT id, action, params, COALESCE(machine_id, ''), machine_name, COALESCE(agent_id, ''), state,
			refusal_reason, not_after, seq, requested_by, requested_at, finished_at
		FROM action_runs WHERE id = ?`, id))
	if err != nil {
		return action.Run{}, fmt.Errorf("get run: %w", err)
	}
	runs := []action.Run{run}
	if err := attachRunLogs(ctx, db.sql, runs); err != nil {
		return action.Run{}, err
	}
	return runs[0], nil
}

func scanActionRun(row scanner) (action.Run, error) {
	var run action.Run
	var kind, params, state string
	var notAfter, requestedAt int64
	var finishedAt sql.NullInt64
	err := row.Scan(&run.ID, &kind, &params, &run.MachineID, &run.MachineName, &run.AgentID, &state,
		&run.Reason, &notAfter, &run.Seq, &run.RequestedBy, &requestedAt, &finishedAt)
	if err != nil {
		return action.Run{}, err
	}
	run.Kind, run.Params, run.State = agentwire.Kind(kind), []byte(params), action.State(state)
	run.NotAfter, run.RequestedAt, run.FinishedAt = time.Unix(notAfter, 0), time.Unix(requestedAt, 0), timeOf(finishedAt)
	return run, nil
}

// attachRunLogs pose la sortie de chaque run, en une requête pour la liste.
func attachRunLogs(ctx context.Context, q querier, runs []action.Run) error {
	if len(runs) == 0 {
		return nil
	}
	ids := make([]any, 0, len(runs))
	byID := make(map[string]int, len(runs))
	for index, run := range runs {
		ids = append(ids, run.ID)
		byID[run.ID] = index
		runs[index].Output = []action.LogLine{}
	}
	rows, err := q.QueryContext(ctx, `
		SELECT run_id, at, stream, line FROM run_logs
		WHERE run_id IN (`+strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",")+`)
		ORDER BY run_id, seq`, ids...)
	if err != nil {
		return fmt.Errorf("list run logs: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var runID string
		var at int64
		var line action.LogLine
		if err := rows.Scan(&runID, &at, &line.Stream, &line.Line); err != nil {
			return err
		}
		line.At = time.Unix(at, 0)
		index := byID[runID]
		runs[index].Output = append(runs[index].Output, line)
	}
	return rows.Err()
}
