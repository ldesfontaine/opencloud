package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/ldesfontaine/opencloud/internal/manifest"
)

// storedChange fige en base le format d'un changement du plan : le renommer
// ou le réordonner dans le code ne change pas ce que le journal a écrit. Le
// plan ne porte que des valeurs de l'annuaire, jamais un secret.
type storedChange struct {
	Action  string        `json:"action"`
	Kind    string        `json:"kind"`
	ID      string        `json:"id,omitempty"`
	Name    string        `json:"name"`
	Fields  []storedField `json:"fields,omitempty"`
	Notes   []string      `json:"notes,omitempty"`
	Carries []storedCarry `json:"carries,omitempty"`
}

type storedField struct {
	Field  string `json:"field"`
	Before string `json:"before"`
	After  string `json:"after"`
}

type storedCarry struct {
	What  string `json:"what"`
	Count int    `json:"count"`
}

func encodeChanges(changes []manifest.Change) (string, error) {
	stored := make([]storedChange, 0, len(changes))
	for _, change := range changes {
		item := storedChange{Action: string(change.Action), Kind: string(change.Kind), ID: change.ID, Name: change.Name}
		for _, field := range change.Fields {
			item.Fields = append(item.Fields, storedField{Field: field.Field, Before: field.Before, After: field.After})
		}
		for _, note := range change.Notes {
			item.Notes = append(item.Notes, string(note))
		}
		for _, carry := range change.Carries {
			item.Carries = append(item.Carries, storedCarry{What: carry.What, Count: carry.Count})
		}
		stored = append(stored, item)
	}
	encoded, err := json.Marshal(stored)
	if err != nil {
		return "", fmt.Errorf("encode apply plan: %w", err)
	}
	return string(encoded), nil
}

func decodeChanges(text string) ([]manifest.Change, error) {
	var stored []storedChange
	if err := json.Unmarshal([]byte(text), &stored); err != nil {
		return nil, fmt.Errorf("decode apply plan: %w", err)
	}
	changes := make([]manifest.Change, 0, len(stored))
	for _, item := range stored {
		change := manifest.Change{Action: manifest.Action(item.Action), Kind: manifest.Kind(item.Kind), ID: item.ID, Name: item.Name}
		for _, field := range item.Fields {
			change.Fields = append(change.Fields, manifest.FieldChange{Field: field.Field, Before: field.Before, After: field.After})
		}
		for _, note := range item.Notes {
			change.Notes = append(change.Notes, manifest.Note(note))
		}
		for _, carry := range item.Carries {
			change.Carries = append(change.Carries, manifest.Carry{What: carry.What, Count: carry.Count})
		}
		changes = append(changes, change)
	}
	return changes, nil
}

// InsertApply ouvre la ligne d'un passage confirmé, avec son plan.
func (db *DB) InsertApply(ctx context.Context, apply manifest.Apply) error {
	plan, err := encodeChanges(apply.Changes)
	if err != nil {
		return err
	}
	_, err = db.sql.ExecContext(ctx, `
		INSERT INTO applies (id, at, finished_at, actor_kind, actor_name, file_hash, file_name, prune, plan_hash, plan, state,
			created, updated, deleted, unchanged, done, error, error_object)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		apply.ID, apply.At.Unix(), nullableTime(apply.FinishedAt), string(apply.Actor.Kind), apply.Actor.Name,
		apply.FileHash, apply.FileName, apply.Prune, apply.PlanHash, plan, string(apply.State),
		apply.Created, apply.Updated, apply.Deleted, apply.Unchanged, apply.Done, apply.Error, apply.ErrorObject)
	if err != nil {
		return fmt.Errorf("insert apply: %w", err)
	}
	return nil
}

// SetApplyProgress dit combien de changements sont écrits.
func (db *DB) SetApplyProgress(ctx context.Context, id string, done int) error {
	result, err := db.sql.ExecContext(ctx, `UPDATE applies SET done = ? WHERE id = ?`, done, id)
	if err != nil {
		return fmt.Errorf("set apply progress: %w", err)
	}
	return applyNotFoundIfNoRow(result)
}

// FinishApply ferme la ligne : l'état final, la date, le compte et l'arrêt.
func (db *DB) FinishApply(ctx context.Context, id string, end manifest.ApplyEnd) error {
	result, err := db.sql.ExecContext(ctx, `
		UPDATE applies SET state = ?, finished_at = ?, done = ?, error = ?, error_object = ? WHERE id = ?`,
		string(end.State), end.FinishedAt.Unix(), end.Done, end.Error, end.ErrorObject, id)
	if err != nil {
		return fmt.Errorf("finish apply: %w", err)
	}
	return applyNotFoundIfNoRow(result)
}

func applyNotFoundIfNoRow(result sql.Result) error {
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return manifest.ErrApplyNotFound
	}
	return nil
}

const applyColumns = `id, at, finished_at, actor_kind, actor_name, file_hash, file_name, prune, plan_hash, state,
	created, updated, deleted, unchanged, done, error, error_object`

// ListApplies rend les passages du plus récent au plus ancien, sans leur plan.
func (db *DB) ListApplies(ctx context.Context, limit int) ([]manifest.Apply, error) {
	rows, err := db.sql.QueryContext(ctx, `SELECT `+applyColumns+` FROM applies ORDER BY at DESC, rowid DESC LIMIT ?`, limit)
	if err != nil {
		return nil, fmt.Errorf("list applies: %w", err)
	}
	defer rows.Close()
	applies := []manifest.Apply{}
	for rows.Next() {
		apply, err := scanApply(rows)
		if err != nil {
			return nil, err
		}
		applies = append(applies, apply)
	}
	return applies, rows.Err()
}

// GetApply rend un passage avec son plan.
func (db *DB) GetApply(ctx context.Context, id string) (manifest.Apply, error) {
	row := db.sql.QueryRowContext(ctx, `SELECT `+applyColumns+`, plan FROM applies WHERE id = ?`, id)
	var plan string
	apply, err := scanApply(row, &plan)
	if errors.Is(err, sql.ErrNoRows) {
		return manifest.Apply{}, manifest.ErrApplyNotFound
	}
	if err != nil {
		return manifest.Apply{}, err
	}
	if apply.Changes, err = decodeChanges(plan); err != nil {
		return manifest.Apply{}, err
	}
	return apply, nil
}

func scanApply(row scanner, extra ...any) (manifest.Apply, error) {
	var apply manifest.Apply
	var at int64
	var finishedAt sql.NullInt64
	var actorKind, state string
	destinations := []any{
		&apply.ID, &at, &finishedAt, &actorKind, &apply.Actor.Name, &apply.FileHash, &apply.FileName, &apply.Prune,
		&apply.PlanHash, &state, &apply.Created, &apply.Updated, &apply.Deleted, &apply.Unchanged, &apply.Done,
		&apply.Error, &apply.ErrorObject,
	}
	if err := row.Scan(append(destinations, extra...)...); err != nil {
		return manifest.Apply{}, err
	}
	apply.At = time.Unix(at, 0)
	apply.FinishedAt = timeOf(finishedAt)
	apply.Actor.Kind = manifest.ActorKind(actorKind)
	apply.State = manifest.ApplyState(state)
	return apply, nil
}

// FailInterruptedApplies passe en échec les lignes restées « confirmed » : un
// arrêt brutal a coupé l'écriture, plus rien ne la finira. Rend leur nombre.
func (db *DB) FailInterruptedApplies(ctx context.Context, finishedAt time.Time, key string) (int, error) {
	result, err := db.sql.ExecContext(ctx, `
		UPDATE applies SET state = ?, finished_at = ?, error = ? WHERE state = ?`,
		string(manifest.ApplyFailed), finishedAt.Unix(), key, string(manifest.ApplyConfirmed))
	if err != nil {
		return 0, fmt.Errorf("fail interrupted applies: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return 0, err
	}
	return int(affected), nil
}
