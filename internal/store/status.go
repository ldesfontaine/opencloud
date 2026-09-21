package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/ldesfontaine/opencloud/internal/status"
)

// --- Composants ---

func (db *DB) InsertComponent(ctx context.Context, component status.Component) error {
	tx, err := db.sql.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin insert component: %w", err)
	}
	defer tx.Rollback()
	_, err = tx.ExecContext(ctx, `
		INSERT INTO status_components (id, name, position, created_at) VALUES (?, ?, ?, ?)`,
		component.ID, component.Name, component.Position, component.CreatedAt.Unix())
	if err != nil {
		return fmt.Errorf("insert component: %w", err)
	}
	if err := insertMembers(ctx, tx, component); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit insert component: %w", err)
	}
	return nil
}

// UpdateComponent réécrit le nom, l'ordre et les objets rattachés : les
// liens sont remplacés, pas rapprochés.
func (db *DB) UpdateComponent(ctx context.Context, component status.Component) error {
	tx, err := db.sql.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin update component: %w", err)
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `UPDATE status_components SET name = ?, position = ? WHERE id = ?`,
		component.Name, component.Position, component.ID)
	if err != nil {
		return fmt.Errorf("update component: %w", err)
	}
	if err := statusNotFoundIfNoRow(result); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM status_component_members WHERE component_id = ?`, component.ID); err != nil {
		return fmt.Errorf("clear members: %w", err)
	}
	if err := insertMembers(ctx, tx, component); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit update component: %w", err)
	}
	return nil
}

// insertMembers écrit chaque objet dans sa colonne ; un objet inconnu fait
// échouer la clé étrangère, et c'est un refus.
func insertMembers(ctx context.Context, tx *sql.Tx, component status.Component) error {
	for _, member := range component.Members {
		var column string
		switch member.Kind {
		case status.KindMachine:
			column = "machine_id"
		case status.KindService:
			column = "service_id"
		case status.KindHeartbeat:
			column = "heartbeat_id"
		case status.KindProbe:
			column = "probe_id"
		default:
			return status.ErrMemberInvalid
		}
		// column vient d'une liste fermée juste au-dessus : jamais une entrée.
		_, err := tx.ExecContext(ctx, `INSERT INTO status_component_members (component_id, `+column+`) VALUES (?, ?)`, component.ID, member.ID)
		if isConstraintViolation(err) {
			return status.ErrMemberInvalid
		}
		if err != nil {
			return fmt.Errorf("insert member: %w", err)
		}
	}
	return nil
}

// Une clé étrangère ou un index unique refusés : l'objet n'existe pas, ou
// est donné deux fois.
func isConstraintViolation(err error) bool {
	if err == nil {
		return false
	}
	var target interface{ Code() int }
	if errors.As(err, &target) {
		code := target.Code()
		return code == 787 || code == 1555 || code == 2067
	}
	return isUniqueViolation(err)
}

func (db *DB) ListComponents(ctx context.Context) ([]status.Component, error) {
	rows, err := db.sql.QueryContext(ctx, `SELECT id, name, position, created_at FROM status_components ORDER BY position, name, id`)
	if err != nil {
		return nil, fmt.Errorf("list components: %w", err)
	}
	defer rows.Close()
	components := []status.Component{}
	for rows.Next() {
		component, err := scanComponent(rows)
		if err != nil {
			return nil, err
		}
		components = append(components, component)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range components {
		if components[i].Members, err = db.listMembers(ctx, components[i].ID); err != nil {
			return nil, err
		}
	}
	return components, nil
}

func (db *DB) GetComponent(ctx context.Context, id string) (status.Component, error) {
	component, err := scanComponent(db.sql.QueryRowContext(ctx, `SELECT id, name, position, created_at FROM status_components WHERE id = ?`, id))
	if err != nil {
		return status.Component{}, err
	}
	if component.Members, err = db.listMembers(ctx, id); err != nil {
		return status.Component{}, err
	}
	return component, nil
}

func scanComponent(row scanner) (status.Component, error) {
	var component status.Component
	var createdAt int64
	err := row.Scan(&component.ID, &component.Name, &component.Position, &createdAt)
	if errors.Is(err, sql.ErrNoRows) {
		return status.Component{}, status.ErrNotFound
	}
	if err != nil {
		return status.Component{}, fmt.Errorf("scan component: %w", err)
	}
	component.CreatedAt = time.Unix(createdAt, 0)
	return component, nil
}

// listMembers rend les objets rattachés, genre et identifiant seulement :
// le nom et l'état viennent des objets eux-mêmes, lus par le composant.
func (db *DB) listMembers(ctx context.Context, componentID string) ([]status.Member, error) {
	rows, err := db.sql.QueryContext(ctx, `
		SELECT machine_id, service_id, heartbeat_id, probe_id FROM status_component_members
		WHERE component_id = ? ORDER BY rowid`, componentID)
	if err != nil {
		return nil, fmt.Errorf("list members: %w", err)
	}
	defer rows.Close()
	members := []status.Member{}
	for rows.Next() {
		var machineID, serviceID, heartbeatID, probeID sql.NullString
		if err := rows.Scan(&machineID, &serviceID, &heartbeatID, &probeID); err != nil {
			return nil, fmt.Errorf("scan member: %w", err)
		}
		switch {
		case machineID.Valid:
			members = append(members, status.Member{Kind: status.KindMachine, ID: machineID.String})
		case serviceID.Valid:
			members = append(members, status.Member{Kind: status.KindService, ID: serviceID.String})
		case heartbeatID.Valid:
			members = append(members, status.Member{Kind: status.KindHeartbeat, ID: heartbeatID.String})
		case probeID.Valid:
			members = append(members, status.Member{Kind: status.KindProbe, ID: probeID.String})
		}
	}
	return members, rows.Err()
}

func (db *DB) DeleteComponent(ctx context.Context, id string) error {
	result, err := db.sql.ExecContext(ctx, `DELETE FROM status_components WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete component: %w", err)
	}
	return statusNotFoundIfNoRow(result)
}

func (db *DB) CountComponents(ctx context.Context) (int, error) {
	var count int
	if err := db.sql.QueryRowContext(ctx, `SELECT COUNT(*) FROM status_components`).Scan(&count); err != nil {
		return 0, fmt.Errorf("count components: %w", err)
	}
	return count, nil
}

// --- Incidents ---

const incidentColumns = `id, title, impact, status, starts_at, ends_at, created_at, updated_at, resolved_at`

// InsertIncident écrit l'incident, ses composants et la première entrée de
// son fil d'un coup.
func (db *DB) InsertIncident(ctx context.Context, incident status.Incident) error {
	tx, err := db.sql.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin insert incident: %w", err)
	}
	defer tx.Rollback()
	_, err = tx.ExecContext(ctx, `
		INSERT INTO incidents (`+incidentColumns+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		incident.ID, incident.Title, string(incident.Impact), string(incident.Status),
		nullableTime(incident.StartsAt), nullableTime(incident.EndsAt),
		incident.CreatedAt.Unix(), incident.UpdatedAt.Unix(), nullableTime(incident.ResolvedAt))
	if err != nil {
		return fmt.Errorf("insert incident: %w", err)
	}
	if err := insertIncidentComponents(ctx, tx, incident); err != nil {
		return err
	}
	for _, update := range incident.Updates {
		if err := insertUpdate(ctx, tx, incident.ID, update); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit insert incident: %w", err)
	}
	return nil
}

// UpdateIncident réécrit ce que l'opérateur corrige : le titre, la
// fenêtre, les composants. Le statut et le fil passent par Transition.
func (db *DB) UpdateIncident(ctx context.Context, incident status.Incident) error {
	tx, err := db.sql.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin update incident: %w", err)
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `
		UPDATE incidents SET title = ?, starts_at = ?, ends_at = ?, updated_at = ? WHERE id = ?`,
		incident.Title, nullableTime(incident.StartsAt), nullableTime(incident.EndsAt), incident.UpdatedAt.Unix(), incident.ID)
	if err != nil {
		return fmt.Errorf("update incident: %w", err)
	}
	if err := statusNotFoundIfNoRow(result); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM incident_components WHERE incident_id = ?`, incident.ID); err != nil {
		return fmt.Errorf("clear incident components: %w", err)
	}
	if err := insertIncidentComponents(ctx, tx, incident); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit update incident: %w", err)
	}
	return nil
}

func insertIncidentComponents(ctx context.Context, tx *sql.Tx, incident status.Incident) error {
	for _, component := range incident.Components {
		_, err := tx.ExecContext(ctx, `INSERT INTO incident_components (incident_id, component_id) VALUES (?, ?)`, incident.ID, component.ID)
		if isConstraintViolation(err) {
			return status.ErrComponentInvalid
		}
		if err != nil {
			return fmt.Errorf("insert incident component: %w", err)
		}
	}
	return nil
}

func insertUpdate(ctx context.Context, tx *sql.Tx, incidentID string, update status.Update) error {
	_, err := tx.ExecContext(ctx, `
		INSERT INTO incident_updates (incident_id, status, message, created_at) VALUES (?, ?, ?, ?)`,
		incidentID, string(update.Status), update.Message, update.CreatedAt.Unix())
	if err != nil {
		return fmt.Errorf("insert incident update: %w", err)
	}
	return nil
}

// Transition écrit le statut et l'entrée du fil dans la même transaction.
func (db *DB) Transition(ctx context.Context, incident status.Incident, update status.Update) error {
	tx, err := db.sql.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin transition: %w", err)
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `UPDATE incidents SET status = ?, updated_at = ?, resolved_at = ? WHERE id = ?`,
		string(incident.Status), incident.UpdatedAt.Unix(), nullableTime(incident.ResolvedAt), incident.ID)
	if err != nil {
		return fmt.Errorf("update incident status: %w", err)
	}
	if err := statusNotFoundIfNoRow(result); err != nil {
		return err
	}
	if err := insertUpdate(ctx, tx, incident.ID, update); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit transition: %w", err)
	}
	return nil
}

// ListIncidents rend les incidents ouverts et ceux résolus depuis
// l'instant donné, les plus récents d'abord — à date égale, le dernier
// écrit, pour que deux incidents ouverts dans la même seconde gardent un
// ordre ; zéro rend tout.
func (db *DB) ListIncidents(ctx context.Context, resolvedSince time.Time) ([]status.Incident, error) {
	query := `SELECT ` + incidentColumns + ` FROM incidents`
	var args []any
	if !resolvedSince.IsZero() {
		query += ` WHERE status != 'resolved' OR resolved_at >= ?`
		args = append(args, resolvedSince.Unix())
	}
	rows, err := db.sql.QueryContext(ctx, query+` ORDER BY created_at DESC, rowid DESC`, args...)
	if err != nil {
		return nil, fmt.Errorf("list incidents: %w", err)
	}
	defer rows.Close()
	incidents := []status.Incident{}
	for rows.Next() {
		incident, err := scanIncident(rows)
		if err != nil {
			return nil, err
		}
		incidents = append(incidents, incident)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range incidents {
		if err := db.attachIncident(ctx, &incidents[i]); err != nil {
			return nil, err
		}
	}
	return incidents, nil
}

func (db *DB) GetIncident(ctx context.Context, id string) (status.Incident, error) {
	incident, err := scanIncident(db.sql.QueryRowContext(ctx, `SELECT `+incidentColumns+` FROM incidents WHERE id = ?`, id))
	if err != nil {
		return status.Incident{}, err
	}
	if err := db.attachIncident(ctx, &incident); err != nil {
		return status.Incident{}, err
	}
	return incident, nil
}

func scanIncident(row scanner) (status.Incident, error) {
	var incident status.Incident
	var impact, state string
	var startsAt, endsAt, resolvedAt sql.NullInt64
	var createdAt, updatedAt int64
	err := row.Scan(&incident.ID, &incident.Title, &impact, &state, &startsAt, &endsAt, &createdAt, &updatedAt, &resolvedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return status.Incident{}, status.ErrNotFound
	}
	if err != nil {
		return status.Incident{}, fmt.Errorf("scan incident: %w", err)
	}
	incident.Impact = status.Impact(impact)
	incident.Status = status.IncidentStatus(state)
	incident.StartsAt = timeOf(startsAt)
	incident.EndsAt = timeOf(endsAt)
	incident.CreatedAt = time.Unix(createdAt, 0)
	incident.UpdatedAt = time.Unix(updatedAt, 0)
	incident.ResolvedAt = timeOf(resolvedAt)
	return incident, nil
}

// attachIncident lit les composants touchés, avec leur nom, et le fil du
// plus récent au plus ancien.
func (db *DB) attachIncident(ctx context.Context, incident *status.Incident) error {
	rows, err := db.sql.QueryContext(ctx, `
		SELECT c.id, c.name FROM incident_components ic JOIN status_components c ON c.id = ic.component_id
		WHERE ic.incident_id = ? ORDER BY c.position, c.name, c.id`, incident.ID)
	if err != nil {
		return fmt.Errorf("list incident components: %w", err)
	}
	defer rows.Close()
	incident.Components = []status.ComponentRef{}
	for rows.Next() {
		var ref status.ComponentRef
		if err := rows.Scan(&ref.ID, &ref.Name); err != nil {
			return fmt.Errorf("scan incident component: %w", err)
		}
		incident.Components = append(incident.Components, ref)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	updates, err := db.sql.QueryContext(ctx, `
		SELECT id, status, message, created_at FROM incident_updates WHERE incident_id = ? ORDER BY created_at DESC, id DESC`, incident.ID)
	if err != nil {
		return fmt.Errorf("list incident updates: %w", err)
	}
	defer updates.Close()
	incident.Updates = []status.Update{}
	for updates.Next() {
		var update status.Update
		var state string
		var createdAt int64
		if err := updates.Scan(&update.ID, &state, &update.Message, &createdAt); err != nil {
			return fmt.Errorf("scan incident update: %w", err)
		}
		update.Status = status.IncidentStatus(state)
		update.CreatedAt = time.Unix(createdAt, 0)
		incident.Updates = append(incident.Updates, update)
	}
	return updates.Err()
}

func (db *DB) DeleteIncident(ctx context.Context, id string) error {
	result, err := db.sql.ExecContext(ctx, `DELETE FROM incidents WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete incident: %w", err)
	}
	return statusNotFoundIfNoRow(result)
}

func (db *DB) CountOpenIncidents(ctx context.Context) (int, error) {
	var count int
	if err := db.sql.QueryRowContext(ctx, `SELECT COUNT(*) FROM incidents WHERE status != 'resolved'`).Scan(&count); err != nil {
		return 0, fmt.Errorf("count open incidents: %w", err)
	}
	return count, nil
}

// PurgeIncidents efface les incidents résolus avant l'instant donné ; le
// fil et les rattachements partent en cascade.
func (db *DB) PurgeIncidents(ctx context.Context, resolvedBefore time.Time) error {
	if _, err := db.sql.ExecContext(ctx, `DELETE FROM incidents WHERE resolved_at IS NOT NULL AND resolved_at < ?`, resolvedBefore.Unix()); err != nil {
		return fmt.Errorf("purge incidents: %w", err)
	}
	return nil
}

func statusNotFoundIfNoRow(result sql.Result) error {
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return status.ErrNotFound
	}
	return nil
}
