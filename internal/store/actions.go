package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// ActionState est l'état du journal de transaction (05-execution.md).
type ActionState string

const (
	StatePrepared ActionState = "prepared" // journalisée, rien n'est encore parti
	StateRunning  ActionState = "running"  // l'unité tourne sur la machine
	StateApplied  ActionState = "applied"  // code 0
	StateFailed   ActionState = "failed"   // code 1, tuée, délai dépassé, suivi abandonné
	StateRefused  ActionState = "refused"  // code 2, ou refusée avant de partir
)

// Action est une ligne du journal de transaction. LaunchedAt et FinishedAt
// sont nuls tant que l'étape n'a pas eu lieu ; ExitCode est nil tant que
// personne n'a lu de code de retour — une action abandonnée n'en a jamais.
type Action struct {
	ID             string
	MachineID      string
	Kind           string
	Params         map[string]string
	State          ActionState
	ScriptDigest   string
	UnitName       string
	TimeoutSeconds int
	CreatedAt      time.Time
	LaunchedAt     time.Time
	FinishedAt     time.Time
	ExitCode       *int
	Result         string
	Note           string
	LastCursor     string
}

// ActionLine est une ligne de sortie du script, telle que stockée.
type ActionLine struct {
	Seq  int64
	At   time.Time
	Text string
}

const actionColumns = `id, machine_id, kind, params, state, script_digest, unit_name,
	timeout_seconds, created_at, launched_at, finished_at, exit_code, result, note, last_cursor`

func (s *Store) InsertAction(ctx context.Context, action Action) error {
	params, err := json.Marshal(action.Params)
	if err != nil {
		return fmt.Errorf("encode action params: %w", err)
	}
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO actions (id, machine_id, kind, params, state, script_digest, unit_name,
			timeout_seconds, created_at, result, note, last_cursor)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		action.ID, action.MachineID, action.Kind, string(params), string(action.State),
		action.ScriptDigest, action.UnitName, action.TimeoutSeconds,
		formatTime(action.CreatedAt), action.Result, action.Note, action.LastCursor)
	if err != nil {
		return fmt.Errorf("insert action: %w", err)
	}
	return nil
}

func (s *Store) Action(ctx context.Context, id string) (Action, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+actionColumns+` FROM actions WHERE id = ?`, id)
	action, err := scanAction(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Action{}, ErrNotFound
	}
	return action, err
}

// ActionsForMachine rend l'historique d'une machine, la plus récente d'abord.
func (s *Store) ActionsForMachine(ctx context.Context, machineID string, limit int) ([]Action, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+actionColumns+`
		FROM actions WHERE machine_id = ? ORDER BY created_at DESC, id DESC LIMIT ?`,
		machineID, limit)
	if err != nil {
		return nil, fmt.Errorf("list actions of machine: %w", err)
	}
	return collectActions(rows)
}

// PendingActions rend ce que la reprise doit rejouer : tout ce qui est
// préparé ou en cours, groupé par machine, dans l'ordre où c'est arrivé.
func (s *Store) PendingActions(ctx context.Context) ([]Action, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+actionColumns+`
		FROM actions WHERE state IN (?, ?) ORDER BY machine_id, created_at, id`,
		string(StatePrepared), string(StateRunning))
	if err != nil {
		return nil, fmt.Errorf("list pending actions: %w", err)
	}
	return collectActions(rows)
}

// MarkRunning note que l'unité tourne : c'est la deuxième ligne du journal.
func (s *Store) MarkRunning(ctx context.Context, id string, launchedAt time.Time) error {
	result, err := s.db.ExecContext(ctx, `
		UPDATE actions SET state = ?, launched_at = ? WHERE id = ?`,
		string(StateRunning), formatTime(launchedAt), id)
	if err != nil {
		return fmt.Errorf("mark action running: %w", err)
	}
	return oneRowAffected(result)
}

// Conclude ferme la ligne du journal. exitCode est nil quand personne n'a pu
// en lire un.
func (s *Store) Conclude(ctx context.Context, id string, state ActionState, exitCode *int, result, note string) error {
	var code any
	if exitCode != nil {
		code = *exitCode
	}
	updated, err := s.db.ExecContext(ctx, `
		UPDATE actions SET state = ?, finished_at = ?, exit_code = ?, result = ?, note = ?
		WHERE id = ?`,
		string(state), formatTime(time.Now().UTC()), code, result, note, id)
	if err != nil {
		return fmt.Errorf("conclude action: %w", err)
	}
	return oneRowAffected(updated)
}

// AppendLine stocke une ligne de sortie et avance le curseur de reprise dans
// la même transaction : une coupure ne laisse jamais un curseur en avance sur
// ce qui est stocké.
func (s *Store) AppendLine(ctx context.Context, id string, at time.Time, text, cursor string) (ActionLine, error) {
	transaction, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return ActionLine{}, fmt.Errorf("begin append line: %w", err)
	}
	defer transaction.Rollback()

	var lastSeq sql.NullInt64
	err = transaction.QueryRowContext(ctx,
		`SELECT MAX(seq) FROM action_lines WHERE action_id = ?`, id).Scan(&lastSeq)
	if err != nil {
		return ActionLine{}, fmt.Errorf("read last line sequence: %w", err)
	}

	line := ActionLine{Seq: lastSeq.Int64 + 1, At: at.UTC(), Text: text}
	_, err = transaction.ExecContext(ctx,
		`INSERT INTO action_lines (action_id, seq, at, text) VALUES (?, ?, ?, ?)`,
		id, line.Seq, formatTime(line.At), line.Text)
	if err != nil {
		return ActionLine{}, fmt.Errorf("insert action line: %w", err)
	}

	updated, err := transaction.ExecContext(ctx,
		`UPDATE actions SET last_cursor = ? WHERE id = ?`, cursor, id)
	if err != nil {
		return ActionLine{}, fmt.Errorf("update action cursor: %w", err)
	}
	if err := oneRowAffected(updated); err != nil {
		return ActionLine{}, err
	}

	if err := transaction.Commit(); err != nil {
		return ActionLine{}, fmt.Errorf("commit append line: %w", err)
	}
	return line, nil
}

// Lines rend la sortie stockée après afterSeq ; 0 rend tout.
func (s *Store) Lines(ctx context.Context, id string, afterSeq int64) ([]ActionLine, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT seq, at, text FROM action_lines WHERE action_id = ? AND seq > ? ORDER BY seq`,
		id, afterSeq)
	if err != nil {
		return nil, fmt.Errorf("list action lines: %w", err)
	}
	defer rows.Close()

	var lines []ActionLine
	for rows.Next() {
		var line ActionLine
		var at string
		if err := rows.Scan(&line.Seq, &at, &line.Text); err != nil {
			return nil, fmt.Errorf("scan action line: %w", err)
		}
		line.At, err = parseTime(at)
		if err != nil {
			return nil, err
		}
		lines = append(lines, line)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list action lines: %w", err)
	}
	return lines, nil
}

func collectActions(rows *sql.Rows) ([]Action, error) {
	defer rows.Close()

	var actions []Action
	for rows.Next() {
		action, err := scanAction(rows)
		if err != nil {
			return nil, err
		}
		actions = append(actions, action)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list actions: %w", err)
	}
	return actions, nil
}

func scanAction(row scanner) (Action, error) {
	var action Action
	var params, state, createdAt string
	var launchedAt, finishedAt sql.NullString
	var exitCode sql.NullInt64

	err := row.Scan(&action.ID, &action.MachineID, &action.Kind, &params, &state,
		&action.ScriptDigest, &action.UnitName, &action.TimeoutSeconds, &createdAt,
		&launchedAt, &finishedAt, &exitCode, &action.Result, &action.Note, &action.LastCursor)
	if errors.Is(err, sql.ErrNoRows) {
		return Action{}, err
	}
	if err != nil {
		return Action{}, fmt.Errorf("scan action: %w", err)
	}

	if err := json.Unmarshal([]byte(params), &action.Params); err != nil {
		return Action{}, fmt.Errorf("decode action params: %w", err)
	}
	action.State = ActionState(state)

	action.CreatedAt, err = parseTime(createdAt)
	if err != nil {
		return Action{}, err
	}
	action.LaunchedAt, err = parseOptionalTime(launchedAt)
	if err != nil {
		return Action{}, err
	}
	action.FinishedAt, err = parseOptionalTime(finishedAt)
	if err != nil {
		return Action{}, err
	}
	if exitCode.Valid {
		code := int(exitCode.Int64)
		action.ExitCode = &code
	}
	return action, nil
}

func parseOptionalTime(value sql.NullString) (time.Time, error) {
	if !value.Valid || value.String == "" {
		return time.Time{}, nil
	}
	return parseTime(value.String)
}

func formatTime(value time.Time) string {
	return value.UTC().Format(time.RFC3339)
}
