package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/ldesfontaine/opencloud/internal/service"
)

const serviceColumns = `id, machine_id, kind, name, group_name, container_id, image, image_id, state, exit_code,
	health, restart_count, ports, created_at, started_at, finished_at, first_seen_at, last_seen_at, archived_at`

// ListServices rend les fiches d'une machine, ou de toutes si machineID
// est vide, archivées comprises, par groupe puis par nom.
func (db *DB) ListServices(ctx context.Context, machineID string) ([]service.Service, error) {
	query := `SELECT ` + serviceColumns + ` FROM services`
	var args []any
	if machineID != "" {
		query += ` WHERE machine_id = ?`
		args = append(args, machineID)
	}
	rows, err := db.sql.QueryContext(ctx, query+` ORDER BY machine_id, group_name, name, id`, args...)
	if err != nil {
		return nil, fmt.Errorf("list services: %w", err)
	}
	defer rows.Close()
	services := []service.Service{}
	for rows.Next() {
		item, err := scanService(rows)
		if err != nil {
			return nil, err
		}
		services = append(services, item)
	}
	return services, rows.Err()
}

func (db *DB) GetService(ctx context.Context, id string) (service.Service, error) {
	row := db.sql.QueryRowContext(ctx, `SELECT `+serviceColumns+` FROM services WHERE id = ?`, id)
	item, err := scanService(row)
	if errors.Is(err, sql.ErrNoRows) {
		return service.Service{}, service.ErrNotFound
	}
	return item, err
}

// ApplyChanges écrit un rapport entier dans une transaction : fiches,
// archivage des absentes, transitions, échantillons, état du Docker.
func (db *DB) ApplyChanges(ctx context.Context, changes service.Changes) error {
	tx, err := db.sql.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin apply services: %w", err)
	}
	defer tx.Rollback()
	for _, item := range changes.Services {
		if err := upsertService(ctx, tx, item); err != nil {
			return err
		}
	}
	if changes.ArchiveMissing {
		if err := archiveMissing(ctx, tx, changes.MachineID, changes.Keep, changes.Now); err != nil {
			return err
		}
	}
	for _, transition := range changes.Transitions {
		if err := insertTransition(ctx, tx, transition); err != nil {
			return err
		}
	}
	for _, sample := range changes.Samples {
		if err := insertSample(ctx, tx, sample); err != nil {
			return err
		}
	}
	if changes.Engine != nil {
		if err := saveEngine(ctx, tx, *changes.Engine); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit apply services: %w", err)
	}
	return nil
}

func upsertService(ctx context.Context, tx *sql.Tx, item service.Service) error {
	ports, err := json.Marshal(item.Ports)
	if err != nil {
		return fmt.Errorf("encode ports: %w", err)
	}
	if item.Ports == nil {
		ports = []byte("[]")
	}
	_, err = tx.ExecContext(ctx, `
		INSERT INTO services (`+serviceColumns+`)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (id) DO UPDATE SET
			name = excluded.name, group_name = excluded.group_name, image = excluded.image,
			image_id = excluded.image_id, state = excluded.state, exit_code = excluded.exit_code,
			health = excluded.health, restart_count = excluded.restart_count, ports = excluded.ports,
			created_at = excluded.created_at, started_at = excluded.started_at, finished_at = excluded.finished_at,
			last_seen_at = excluded.last_seen_at, archived_at = excluded.archived_at`,
		item.ID, item.MachineID, string(item.Kind), item.Name, item.Group, item.ContainerID, item.Image, item.ImageID,
		string(item.State), item.ExitCode, string(item.Health), item.RestartCount, string(ports),
		item.CreatedAt.Unix(), nullableTime(item.StartedAt), nullableTime(item.FinishedAt),
		item.FirstSeenAt.Unix(), item.LastSeenAt.Unix(), nullableTime(item.ArchivedAt))
	if err != nil {
		return fmt.Errorf("upsert service %s: %w", item.ID, err)
	}
	return nil
}

// archiveMissing archive les fiches vivantes de la machine que
// l'inventaire ne nomme plus.
func archiveMissing(ctx context.Context, tx *sql.Tx, machineID string, keep []string, now time.Time) error {
	args := []any{now.Unix(), machineID}
	query := `UPDATE services SET archived_at = ? WHERE machine_id = ? AND archived_at IS NULL`
	if len(keep) > 0 {
		query += ` AND id NOT IN (?` + strings.Repeat(", ?", len(keep)-1) + `)` // #nosec G202 -- des « ? » répétés, les valeurs sont liées.
		for _, id := range keep {
			args = append(args, id)
		}
	}
	if _, err := tx.ExecContext(ctx, query, args...); err != nil {
		return fmt.Errorf("archive missing services: %w", err)
	}
	return nil
}

func insertTransition(ctx context.Context, tx *sql.Tx, t service.Transition) error {
	_, err := tx.ExecContext(ctx, `
		INSERT INTO service_transitions (service_id, at, action, previous_state, new_state, previous_health, new_health, exit_code, replayed, snippet)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		t.ServiceID, t.At.Unix(), t.Action, string(t.PreviousState), string(t.NewState),
		string(t.PreviousHealth), string(t.NewHealth), nullableInt(t.ExitCode), boolToInt(t.Replayed), t.Snippet)
	if err != nil {
		return fmt.Errorf("insert transition: %w", err)
	}
	return nil
}

// Un échantillon déjà là, rejoué après une coupure, est ignoré.
func insertSample(ctx context.Context, tx *sql.Tx, sample service.Sample) error {
	_, err := tx.ExecContext(ctx, `
		INSERT OR IGNORE INTO service_samples (service_id, sampled_at, cpu_percent, mem_used, mem_limit)
		VALUES (?, ?, ?, ?, ?)`,
		sample.ServiceID, sample.SampledAt.Unix(), sample.CPUPercent, sample.MemUsed, sample.MemLimit)
	if err != nil {
		return fmt.Errorf("insert service sample: %w", err)
	}
	return nil
}

func saveEngine(ctx context.Context, tx *sql.Tx, engine service.Engine) error {
	_, err := tx.ExecContext(ctx, `
		INSERT INTO machine_engines (machine_id, present, reason, version, api_version, checked_at)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT (machine_id) DO UPDATE SET present = excluded.present, reason = excluded.reason,
			version = excluded.version, api_version = excluded.api_version, checked_at = excluded.checked_at`,
		engine.MachineID, boolToInt(engine.Present), string(engine.Reason), engine.Version, engine.APIVersion, engine.CheckedAt.Unix())
	if err != nil {
		return fmt.Errorf("save engine: %w", err)
	}
	return nil
}

// ListTransitions rend les plus récentes d'abord.
func (db *DB) ListTransitions(ctx context.Context, serviceID string, limit int) ([]service.Transition, error) {
	rows, err := db.sql.QueryContext(ctx, `
		SELECT id, service_id, at, action, previous_state, new_state, previous_health, new_health, exit_code, replayed, snippet
		FROM service_transitions WHERE service_id = ? ORDER BY at DESC, id DESC LIMIT ?`, serviceID, limit)
	if err != nil {
		return nil, fmt.Errorf("list transitions: %w", err)
	}
	defer rows.Close()
	transitions := []service.Transition{}
	for rows.Next() {
		var t service.Transition
		var at int64
		var exitCode sql.NullInt64
		var replayed int
		if err := rows.Scan(&t.ID, &t.ServiceID, &at, &t.Action, &t.PreviousState, &t.NewState, &t.PreviousHealth, &t.NewHealth, &exitCode, &replayed, &t.Snippet); err != nil {
			return nil, err
		}
		t.At = time.Unix(at, 0).UTC()
		t.ExitCode = intPointer(exitCode)
		t.Replayed = replayed != 0
		transitions = append(transitions, t)
	}
	return transitions, rows.Err()
}

// LatestServiceSamples rend le dernier échantillon de chaque service.
func (db *DB) LatestServiceSamples(ctx context.Context) ([]service.Sample, error) {
	rows, err := db.sql.QueryContext(ctx, `
		SELECT service_id, sampled_at, cpu_percent, mem_used, mem_limit FROM service_samples
		WHERE (service_id, sampled_at) IN (SELECT service_id, MAX(sampled_at) FROM service_samples GROUP BY service_id)
		ORDER BY service_id`)
	if err != nil {
		return nil, fmt.Errorf("list latest service samples: %w", err)
	}
	defer rows.Close()
	samples := []service.Sample{}
	for rows.Next() {
		var sample service.Sample
		var sampledAt int64
		if err := rows.Scan(&sample.ServiceID, &sampledAt, &sample.CPUPercent, &sample.MemUsed, &sample.MemLimit); err != nil {
			return nil, err
		}
		sample.SampledAt = time.Unix(sampledAt, 0).UTC()
		samples = append(samples, sample)
	}
	return samples, rows.Err()
}

func (db *DB) ListEngines(ctx context.Context) ([]service.Engine, error) {
	rows, err := db.sql.QueryContext(ctx, `SELECT machine_id, present, reason, version, api_version, checked_at FROM machine_engines ORDER BY machine_id`)
	if err != nil {
		return nil, fmt.Errorf("list engines: %w", err)
	}
	defer rows.Close()
	engines := []service.Engine{}
	for rows.Next() {
		var engine service.Engine
		var present int
		var checkedAt int64
		if err := rows.Scan(&engine.MachineID, &present, &engine.Reason, &engine.Version, &engine.APIVersion, &checkedAt); err != nil {
			return nil, err
		}
		engine.Present = present != 0
		engine.CheckedAt = time.Unix(checkedAt, 0).UTC()
		engines = append(engines, engine)
	}
	return engines, rows.Err()
}

// PurgeServices efface les transitions trop vieilles, les fiches archivées
// depuis trop longtemps (leurs transitions et échantillons partent avec),
// et le brut des mesures.
func (db *DB) PurgeServices(ctx context.Context, transitionsBefore, archivedBefore, samplesBefore time.Time) error {
	tx, err := db.sql.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin purge services: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM service_transitions WHERE at < ?`, transitionsBefore.Unix()); err != nil {
		return fmt.Errorf("purge transitions: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM services WHERE archived_at IS NOT NULL AND archived_at < ?`, archivedBefore.Unix()); err != nil {
		return fmt.Errorf("purge archived services: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM service_samples WHERE sampled_at < ?`, samplesBefore.Unix()); err != nil {
		return fmt.Errorf("purge service samples: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit purge services: %w", err)
	}
	return nil
}

func scanService(row scanner) (service.Service, error) {
	var item service.Service
	var kind, state, health, ports string
	var createdAt, firstSeen, lastSeen int64
	var startedAt, finishedAt, archivedAt sql.NullInt64
	err := row.Scan(&item.ID, &item.MachineID, &kind, &item.Name, &item.Group, &item.ContainerID, &item.Image, &item.ImageID,
		&state, &item.ExitCode, &health, &item.RestartCount, &ports, &createdAt, &startedAt, &finishedAt, &firstSeen, &lastSeen, &archivedAt)
	if err != nil {
		return service.Service{}, err
	}
	item.Kind, item.State, item.Health = service.Kind(kind), service.State(state), service.Health(health)
	if err := json.Unmarshal([]byte(ports), &item.Ports); err != nil {
		return service.Service{}, fmt.Errorf("decode ports of %s: %w", item.ID, err)
	}
	item.CreatedAt = time.Unix(createdAt, 0).UTC()
	item.FirstSeenAt = time.Unix(firstSeen, 0).UTC()
	item.LastSeenAt = time.Unix(lastSeen, 0).UTC()
	item.StartedAt, item.FinishedAt, item.ArchivedAt = timeOf(startedAt).UTC(), timeOf(finishedAt).UTC(), timeOf(archivedAt).UTC()
	return item, nil
}

func boolToInt(value bool) int {
	if value {
		return 1
	}
	return 0
}
