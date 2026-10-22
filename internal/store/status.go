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
	return db.inTx(ctx, "insert component", func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `
		INSERT INTO status_components (id, page_id, name, position, created_at) VALUES (?, ?, ?, ?, ?)`,
			component.ID, component.PageID, component.Name, component.Position, component.CreatedAt.Unix())
		if isConstraintViolation(err) {
			return status.ErrPageInvalid
		}
		if err != nil {
			return fmt.Errorf("insert component: %w", err)
		}
		if err := insertMembers(ctx, tx, component); err != nil {
			return err
		}
		return nil
	})
}

// UpdateComponent réécrit le nom, l'ordre et les objets rattachés : les
// liens sont remplacés, pas rapprochés.
func (db *DB) UpdateComponent(ctx context.Context, component status.Component) error {
	return db.inTx(ctx, "update component", func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, `UPDATE status_components SET name = ?, position = ? WHERE id = ?`,
			component.Name, component.Position, component.ID)
		if err != nil {
			return fmt.Errorf("update component: %w", err)
		}
		if err := notFoundIfNoRow(result, status.ErrNotFound); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM status_component_members WHERE component_id = ?`, component.ID); err != nil {
			return fmt.Errorf("clear members: %w", err)
		}
		if err := insertMembers(ctx, tx, component); err != nil {
			return err
		}
		return nil
	})
}

// Une requête par genre d'objet : quatre colonnes, quatre instructions
// entières, jamais une colonne collée à une requête.
var memberInserts = map[status.MemberKind]string{
	status.KindMachine:   `INSERT INTO status_component_members (component_id, machine_id) VALUES (?, ?)`,
	status.KindService:   `INSERT INTO status_component_members (component_id, service_id) VALUES (?, ?)`,
	status.KindHeartbeat: `INSERT INTO status_component_members (component_id, heartbeat_id) VALUES (?, ?)`,
	status.KindProbe:     `INSERT INTO status_component_members (component_id, probe_id) VALUES (?, ?)`,
}

// insertMembers écrit chaque objet dans sa colonne ; un objet inconnu fait
// échouer la clé étrangère, et c'est un refus.
func insertMembers(ctx context.Context, tx *sql.Tx, component status.Component) error {
	for _, member := range component.Members {
		insert, ok := memberInserts[member.Kind]
		if !ok {
			return status.ErrMemberInvalid
		}
		_, err := tx.ExecContext(ctx, insert, component.ID, member.ID)
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

const componentColumns = `id, page_id, name, position, created_at`

// ListComponents rend les composants d'une page, ou de toutes quand pageID
// est vide ; les membres se lisent sur le même filtre.
func (db *DB) ListComponents(ctx context.Context, pageID string) ([]status.Component, error) {
	var where, members string
	var args []any
	if pageID != "" {
		where = ` WHERE page_id = ?`
		members = `WHERE component_id IN (SELECT id FROM status_components WHERE page_id = ?)`
		args = append(args, pageID)
	}
	query := `SELECT ` + componentColumns + ` FROM status_components` + where + ` ORDER BY position, name, id` // #nosec G202 -- la clause est une constante du package, les valeurs sont liées.
	rows, err := db.sql.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list components: %w", err)
	}
	components, err := collect(rows, scanComponent)
	if err != nil {
		return nil, err
	}
	if err := db.attachMembers(ctx, components, members, args); err != nil {
		return nil, err
	}
	return components, nil
}

func (db *DB) GetComponent(ctx context.Context, id string) (status.Component, error) {
	component, err := scanComponent(db.sql.QueryRowContext(ctx, `SELECT `+componentColumns+` FROM status_components WHERE id = ?`, id))
	if err != nil {
		return status.Component{}, err
	}
	components := []status.Component{component}
	if err := db.attachMembers(ctx, components, `WHERE component_id = ?`, []any{id}); err != nil {
		return status.Component{}, err
	}
	return components[0], nil
}

func scanComponent(row scanner) (status.Component, error) {
	var component status.Component
	var createdAt int64
	err := row.Scan(&component.ID, &component.PageID, &component.Name, &component.Position, &createdAt)
	if errors.Is(err, sql.ErrNoRows) {
		return status.Component{}, status.ErrNotFound
	}
	if err != nil {
		return status.Component{}, fmt.Errorf("scan component: %w", err)
	}
	component.CreatedAt = time.Unix(createdAt, 0)
	return component, nil
}

// attachMembers lit en une requête les objets rattachés aux composants que
// la clause désigne et les pose sur chacun, genre et identifiant seulement :
// le nom et l'état viennent des objets eux-mêmes, lus par le composant.
// Sans membre, la liste est vide, jamais nil.
func (db *DB) attachMembers(ctx context.Context, components []status.Component, where string, args []any) error {
	if len(components) == 0 {
		return nil
	}
	query := `SELECT component_id, machine_id, service_id, heartbeat_id, probe_id FROM status_component_members ` + where + ` ORDER BY rowid` // #nosec G202 -- la clause est une constante du package, les valeurs sont liées.
	rows, err := db.sql.QueryContext(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("list members: %w", err)
	}
	defer rows.Close()
	byComponent := map[string][]status.Member{}
	for rows.Next() {
		var componentID string
		var machineID, serviceID, heartbeatID, probeID sql.NullString
		if err := rows.Scan(&componentID, &machineID, &serviceID, &heartbeatID, &probeID); err != nil {
			return fmt.Errorf("scan member: %w", err)
		}
		member, ok := memberOf(machineID, serviceID, heartbeatID, probeID)
		if ok {
			byComponent[componentID] = append(byComponent[componentID], member)
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for i := range components {
		components[i].Members = byComponent[components[i].ID]
		if components[i].Members == nil {
			components[i].Members = []status.Member{}
		}
	}
	return nil
}

// memberOf lit le genre d'un membre dans la colonne qui est remplie.
func memberOf(machineID, serviceID, heartbeatID, probeID sql.NullString) (status.Member, bool) {
	switch {
	case machineID.Valid:
		return status.Member{Kind: status.KindMachine, ID: machineID.String}, true
	case serviceID.Valid:
		return status.Member{Kind: status.KindService, ID: serviceID.String}, true
	case heartbeatID.Valid:
		return status.Member{Kind: status.KindHeartbeat, ID: heartbeatID.String}, true
	case probeID.Valid:
		return status.Member{Kind: status.KindProbe, ID: probeID.String}, true
	}
	return status.Member{}, false
}

func (db *DB) DeleteComponent(ctx context.Context, id string) error {
	result, err := db.sql.ExecContext(ctx, `DELETE FROM status_components WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete component: %w", err)
	}
	return notFoundIfNoRow(result, status.ErrNotFound)
}

// CountComponents compte les composants d'une page : le plafond vaut par
// page.
func (db *DB) CountComponents(ctx context.Context, pageID string) (int, error) {
	var count int
	if err := db.sql.QueryRowContext(ctx, `SELECT COUNT(*) FROM status_components WHERE page_id = ?`, pageID).Scan(&count); err != nil {
		return 0, fmt.Errorf("count components: %w", err)
	}
	return count, nil
}

// --- Incidents ---

const incidentColumns = `id, title, impact, status, starts_at, ends_at, created_at, updated_at, resolved_at`

// InsertIncident écrit l'incident, ses cibles et la première entrée de son
// fil d'un coup.
func (db *DB) InsertIncident(ctx context.Context, incident status.Incident) error {
	return db.inTx(ctx, "insert incident", func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `
		INSERT INTO incidents (`+incidentColumns+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			incident.ID, incident.Title, string(incident.Impact), string(incident.Status),
			nullableTime(incident.StartsAt), nullableTime(incident.EndsAt),
			incident.CreatedAt.Unix(), incident.UpdatedAt.Unix(), nullableTime(incident.ResolvedAt))
		if err != nil {
			return fmt.Errorf("insert incident: %w", err)
		}
		if err := insertIncidentTargets(ctx, tx, incident); err != nil {
			return err
		}
		for _, update := range incident.Updates {
			if err := insertUpdate(ctx, tx, incident.ID, update); err != nil {
				return err
			}
		}
		return nil
	})
}

// UpdateIncident réécrit ce que l'opérateur corrige : le titre, la
// fenêtre, les cibles. Le statut et le fil passent par Transition.
func (db *DB) UpdateIncident(ctx context.Context, incident status.Incident) error {
	return db.inTx(ctx, "update incident", func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, `
		UPDATE incidents SET title = ?, starts_at = ?, ends_at = ?, updated_at = ? WHERE id = ?`,
			incident.Title, nullableTime(incident.StartsAt), nullableTime(incident.EndsAt), incident.UpdatedAt.Unix(), incident.ID)
		if err != nil {
			return fmt.Errorf("update incident: %w", err)
		}
		if err := notFoundIfNoRow(result, status.ErrNotFound); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM incident_targets WHERE incident_id = ?`, incident.ID); err != nil {
			return fmt.Errorf("clear incident targets: %w", err)
		}
		if err := insertIncidentTargets(ctx, tx, incident); err != nil {
			return err
		}
		return nil
	})
}

// Une requête par genre d'objet visé, comme pour les membres d'un composant.
var targetInserts = map[status.MemberKind]string{
	status.KindMachine:   `INSERT INTO incident_targets (incident_id, machine_id) VALUES (?, ?)`,
	status.KindService:   `INSERT INTO incident_targets (incident_id, service_id) VALUES (?, ?)`,
	status.KindHeartbeat: `INSERT INTO incident_targets (incident_id, heartbeat_id) VALUES (?, ?)`,
	status.KindProbe:     `INSERT INTO incident_targets (incident_id, probe_id) VALUES (?, ?)`,
}

// insertIncidentTargets écrit chaque objet visé dans sa colonne ; un objet
// inconnu ou donné deux fois est un refus.
func insertIncidentTargets(ctx context.Context, tx *sql.Tx, incident status.Incident) error {
	for _, target := range incident.Targets {
		insert, ok := targetInserts[target.Kind]
		if !ok {
			return status.ErrTargetInvalid
		}
		_, err := tx.ExecContext(ctx, insert, incident.ID, target.ID)
		if isConstraintViolation(err) {
			return status.ErrTargetInvalid
		}
		if err != nil {
			return fmt.Errorf("insert incident target: %w", err)
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
	return db.inTx(ctx, "transition", func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, `UPDATE incidents SET status = ?, updated_at = ?, resolved_at = ? WHERE id = ?`,
			string(incident.Status), incident.UpdatedAt.Unix(), nullableTime(incident.ResolvedAt), incident.ID)
		if err != nil {
			return fmt.Errorf("update incident status: %w", err)
		}
		if err := notFoundIfNoRow(result, status.ErrNotFound); err != nil {
			return err
		}
		if err := insertUpdate(ctx, tx, incident.ID, update); err != nil {
			return err
		}
		return nil
	})
}

// ListIncidents rend les incidents ouverts et ceux résolus depuis
// l'instant donné, les plus récents d'abord — à date égale, le dernier
// écrit, pour que deux incidents ouverts dans la même seconde gardent un
// ordre ; zéro rend tout.
func (db *DB) ListIncidents(ctx context.Context, resolvedSince time.Time) ([]status.Incident, error) {
	var where string
	var args []any
	if !resolvedSince.IsZero() {
		where = ` WHERE status != 'resolved' OR resolved_at >= ?`
		args = append(args, resolvedSince.Unix())
	}
	query := `SELECT ` + incidentColumns + ` FROM incidents` + where + ` ORDER BY created_at DESC, rowid DESC` // #nosec G202 -- la clause est une constante du package, les valeurs sont liées.
	rows, err := db.sql.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list incidents: %w", err)
	}
	incidents, err := collect(rows, scanIncident)
	if err != nil {
		return nil, err
	}
	// Les parties se lisent sur le même filtre que la liste, jamais une
	// requête par incident.
	var scope string
	if where != "" {
		scope = `WHERE incident_id IN (SELECT id FROM incidents` + where + `)` // #nosec G202 -- la clause est une constante du package, les valeurs sont liées.
	}
	if err := db.attachIncidentParts(ctx, incidents, scope, args); err != nil {
		return nil, err
	}
	return incidents, nil
}

func (db *DB) GetIncident(ctx context.Context, id string) (status.Incident, error) {
	incident, err := scanIncident(db.sql.QueryRowContext(ctx, `SELECT `+incidentColumns+` FROM incidents WHERE id = ?`, id))
	if err != nil {
		return status.Incident{}, err
	}
	incidents := []status.Incident{incident}
	if err := db.attachIncidentParts(ctx, incidents, `WHERE incident_id = ?`, []any{id}); err != nil {
		return status.Incident{}, err
	}
	return incidents[0], nil
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

// attachIncidentParts remplit les objets visés, genre et identifiant
// seulement (le nom et les composants touchés viennent du service), et le
// fil du plus récent au plus ancien, des incidents que la clause désigne :
// deux lectures pour la liste entière, jamais deux par incident. Sans
// partie, les listes sont vides, jamais nil.
func (db *DB) attachIncidentParts(ctx context.Context, incidents []status.Incident, where string, args []any) error {
	if len(incidents) == 0 {
		return nil
	}
	position := make(map[string]int, len(incidents))
	for index, incident := range incidents {
		position[incident.ID] = index
		incidents[index].Targets = []status.Target{}
		incidents[index].Components = []status.ComponentRef{}
		incidents[index].Updates = []status.Update{}
	}
	targetsQuery := `SELECT incident_id, machine_id, service_id, heartbeat_id, probe_id FROM incident_targets ` + where + ` ORDER BY rowid` // #nosec G202 -- la clause est une constante du package, les valeurs sont liées.
	targets, err := db.sql.QueryContext(ctx, targetsQuery, args...)
	if err != nil {
		return fmt.Errorf("list incident targets: %w", err)
	}
	found, err := collect(targets, scanIncidentTarget)
	if err != nil {
		return err
	}
	for _, target := range found {
		if index, ok := position[target.incidentID]; ok {
			incidents[index].Targets = append(incidents[index].Targets, target.Target)
		}
	}
	updatesQuery := `SELECT incident_id, id, status, message, created_at FROM incident_updates ` + where + ` ORDER BY created_at DESC, id DESC` // #nosec G202 -- la clause est une constante du package, les valeurs sont liées.
	rows, err := db.sql.QueryContext(ctx, updatesQuery, args...)
	if err != nil {
		return fmt.Errorf("list incident updates: %w", err)
	}
	updates, err := collect(rows, scanIncidentUpdate)
	if err != nil {
		return err
	}
	for _, update := range updates {
		if index, ok := position[update.incidentID]; ok {
			incidents[index].Updates = append(incidents[index].Updates, update.Update)
		}
	}
	return nil
}

// incidentTarget et incidentUpdate portent l'incident de chaque ligne, le
// temps de la regrouper.
type incidentTarget struct {
	incidentID string
	status.Target
}

type incidentUpdate struct {
	incidentID string
	status.Update
}

func scanIncidentTarget(row scanner) (incidentTarget, error) {
	var found incidentTarget
	var machineID, serviceID, heartbeatID, probeID sql.NullString
	if err := row.Scan(&found.incidentID, &machineID, &serviceID, &heartbeatID, &probeID); err != nil {
		return incidentTarget{}, fmt.Errorf("scan incident target: %w", err)
	}
	member, _ := memberOf(machineID, serviceID, heartbeatID, probeID)
	found.Kind, found.ID = member.Kind, member.ID
	return found, nil
}

func scanIncidentUpdate(row scanner) (incidentUpdate, error) {
	var update incidentUpdate
	var state string
	var createdAt int64
	if err := row.Scan(&update.incidentID, &update.ID, &state, &update.Message, &createdAt); err != nil {
		return incidentUpdate{}, fmt.Errorf("scan incident update: %w", err)
	}
	update.Status = status.IncidentStatus(state)
	update.CreatedAt = time.Unix(createdAt, 0)
	return update, nil
}

func (db *DB) DeleteIncident(ctx context.Context, id string) error {
	result, err := db.sql.ExecContext(ctx, `DELETE FROM incidents WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete incident: %w", err)
	}
	return notFoundIfNoRow(result, status.ErrNotFound)
}

func (db *DB) CountOpenIncidents(ctx context.Context) (int, error) {
	var count int
	if err := db.sql.QueryRowContext(ctx, `SELECT COUNT(*) FROM incidents WHERE status != 'resolved'`).Scan(&count); err != nil {
		return 0, fmt.Errorf("count open incidents: %w", err)
	}
	return count, nil
}

// PurgeIncidents efface les incidents résolus avant l'instant donné ; le
// fil et les cibles partent en cascade.
func (db *DB) PurgeIncidents(ctx context.Context, resolvedBefore time.Time) error {
	if _, err := db.sql.ExecContext(ctx, `DELETE FROM incidents WHERE resolved_at IS NOT NULL AND resolved_at < ?`, resolvedBefore.Unix()); err != nil {
		return fmt.Errorf("purge incidents: %w", err)
	}
	return nil
}
