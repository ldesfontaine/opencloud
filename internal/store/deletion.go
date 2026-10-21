package store

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/ldesfontaine/opencloud/internal/deletion"
)

// planQuerier est satisfait par *sql.DB et *sql.Tx : un plan se lit seul
// (l'écran le montre) ou dans la transaction de la suppression qu'il juge.
type planQuerier interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// planQuery est une ligne du plan et la requête qui la lit, dont le seul
// paramètre est l'objet supprimé : (id, nom) pour ce qui retient, un COUNT
// pour ce qui part ou se détache. Les fenêtres pour taire partent avec leur
// objet par la clé étrangère ; l'écran les appelle « silences ».
type planQuery struct {
	kind  deletion.Kind
	query string
}

// componentsShowing liste les composants de statut dont un membre vise
// l'objet par la colonne donnée, une constante de ce fichier.
func componentsShowing(column string) string {
	return `SELECT id, name FROM status_components
		WHERE id IN (SELECT component_id FROM status_component_members WHERE ` + column + ` = ?)
		ORDER BY position, name, id`
}

const probesWatchingService = `SELECT id, name FROM probes WHERE service_id = ? ORDER BY name, id`

// MachineDeletionPlan : la machine est retenue par ce qui y est déclaré ou
// la vise ; elle emporte ce qu'elle a observé et mesuré.
func (db *DB) MachineDeletionPlan(ctx context.Context, id string) (deletion.Plan, error) {
	return machineDeletionPlan(ctx, db.sql, id)
}

func machineDeletionPlan(ctx context.Context, db planQuerier, id string) (deletion.Plan, error) {
	return readPlan(ctx, db, id,
		[]planQuery{
			{deletion.KindDeclaredServices, `SELECT id, name FROM declared_services WHERE machine_id = ? ORDER BY name, id`},
			{deletion.KindProbes, `SELECT id, name FROM probes WHERE machine_id = ? ORDER BY name, id`},
			{deletion.KindHeartbeats, `SELECT id, name FROM heartbeats WHERE machine_id = ? ORDER BY name, id`},
			{deletion.KindComponents, componentsShowing("machine_id")},
		},
		[]planQuery{
			{deletion.KindEnrollmentTokens, `SELECT COUNT(*) FROM enrollment_tokens WHERE machine_id = ?`},
			{deletion.KindMachineSamples, `SELECT COUNT(*) FROM (
				SELECT machine_id FROM machine_samples
				UNION ALL SELECT machine_id FROM machine_samples_hourly
				UNION ALL SELECT machine_id FROM machine_samples_daily) WHERE machine_id = ?`},
			{deletion.KindMachineNetworks, `SELECT COUNT(*) FROM machine_networks WHERE machine_id = ?`},
			{deletion.KindContainers, `SELECT COUNT(*) FROM containers WHERE machine_id = ?`},
			{deletion.KindContainerEvents, `SELECT COUNT(*) FROM container_events
				WHERE container_id IN (SELECT id FROM containers WHERE machine_id = ?)`},
			{deletion.KindImageChecks, `SELECT COUNT(*) FROM image_checks WHERE machine_id = ?`},
			{deletion.KindIgnoredKeys, `SELECT COUNT(*) FROM ignored_keys WHERE machine_id = ?`},
			// Celles de la machine, et celles de ses conteneurs, qui partent avec eux.
			{deletion.KindSilences, `SELECT COUNT(*) FROM mutes, (SELECT ? AS id) AS gone
				WHERE mutes.machine_id = gone.id OR mutes.container_id IN (SELECT id FROM containers WHERE machine_id = gone.id)`},
		},
		[]planQuery{
			{deletion.KindAlerts, `SELECT COUNT(*) FROM alerts WHERE machine_id = ?`},
		})
}

// DeclaredServiceDeletionPlan : le service est retenu par ce qui le surveille
// ou l'affiche ; il emporte ses unités, ses conteneurs redeviennent non
// déclarés. Rien ne touche la machine.
func (db *DB) DeclaredServiceDeletionPlan(ctx context.Context, id string) (deletion.Plan, error) {
	return declaredServiceDeletionPlan(ctx, db.sql, id)
}

func declaredServiceDeletionPlan(ctx context.Context, db planQuerier, id string) (deletion.Plan, error) {
	return readPlan(ctx, db, id,
		[]planQuery{
			{deletion.KindProbes, probesWatchingService},
			{deletion.KindComponents, componentsShowing("service_id")},
		},
		[]planQuery{
			{deletion.KindUnits, `SELECT COUNT(*) FROM service_units WHERE service_id = ?`},
			{deletion.KindSilences, `SELECT COUNT(*) FROM mutes WHERE service_id = ?`},
		},
		[]planQuery{
			{deletion.KindProjectLinks, `SELECT COUNT(*) FROM project_services WHERE service_id = ?`},
			{deletion.KindContainers, `SELECT COUNT(*) FROM containers
				WHERE unit_id IN (SELECT id FROM service_units WHERE service_id = ?)`},
			{deletion.KindAlerts, `SELECT COUNT(*) FROM alerts WHERE service_id = ?`},
		})
}

// ProjectDeletionPlan : un projet ne retient rien et n'emporte rien ; ses
// services restent, seuls les liens partent.
func (db *DB) ProjectDeletionPlan(ctx context.Context, id string) (deletion.Plan, error) {
	return readPlan(ctx, db.sql, id, nil, nil,
		[]planQuery{
			{deletion.KindProjectLinks, `SELECT COUNT(*) FROM project_services WHERE project_id = ?`},
		})
}

// ProbeDeletionPlan : la sonde est retenue par une page publique qui
// l'affiche ; elle emporte son historique.
func (db *DB) ProbeDeletionPlan(ctx context.Context, id string) (deletion.Plan, error) {
	return probeDeletionPlan(ctx, db.sql, id)
}

func probeDeletionPlan(ctx context.Context, db planQuerier, id string) (deletion.Plan, error) {
	return readPlan(ctx, db, id,
		[]planQuery{
			{deletion.KindComponents, componentsShowing("probe_id")},
		},
		[]planQuery{
			{deletion.KindProbeResults, `SELECT COUNT(*) FROM probe_results WHERE probe_id = ?`},
			{deletion.KindProbeDays, `SELECT COUNT(*) FROM probe_days WHERE probe_id = ?`},
			{deletion.KindSilences, `SELECT COUNT(*) FROM mutes WHERE probe_id = ?`},
		},
		[]planQuery{
			{deletion.KindAlerts, `SELECT COUNT(*) FROM alerts WHERE probe_id = ?`},
		})
}

// HeartbeatDeletionPlan : la tâche est retenue par une page publique qui
// l'affiche ; elle emporte son historique.
func (db *DB) HeartbeatDeletionPlan(ctx context.Context, id string) (deletion.Plan, error) {
	return heartbeatDeletionPlan(ctx, db.sql, id)
}

func heartbeatDeletionPlan(ctx context.Context, db planQuerier, id string) (deletion.Plan, error) {
	return readPlan(ctx, db, id,
		[]planQuery{
			{deletion.KindComponents, componentsShowing("heartbeat_id")},
		},
		[]planQuery{
			{deletion.KindHeartbeatPings, `SELECT COUNT(*) FROM heartbeat_pings WHERE heartbeat_id = ?`},
			{deletion.KindHeartbeatRuns, `SELECT COUNT(*) FROM heartbeat_runs WHERE heartbeat_id = ?`},
			{deletion.KindSilences, `SELECT COUNT(*) FROM mutes WHERE heartbeat_id = ?`},
		},
		[]planQuery{
			{deletion.KindAlerts, `SELECT COUNT(*) FROM alerts WHERE heartbeat_id = ?`},
		})
}

// ComponentDeletionPlan : un composant ne retient rien ; il emporte ses
// membres, les incidents qui le citaient restent.
func (db *DB) ComponentDeletionPlan(ctx context.Context, id string) (deletion.Plan, error) {
	return readPlan(ctx, db.sql, id, nil,
		[]planQuery{
			{deletion.KindMembers, `SELECT COUNT(*) FROM status_component_members WHERE component_id = ?`},
		},
		[]planQuery{
			{deletion.KindIncidentLinks, `SELECT COUNT(*) FROM incident_components WHERE component_id = ?`},
		})
}

// readPlan lit un plan : ce qui retient, ce qui part, ce qui se détache. Un
// genre vide n'y figure pas.
func readPlan(ctx context.Context, db planQuerier, id string, blockers, removes, detaches []planQuery) (deletion.Plan, error) {
	var plan deletion.Plan
	for _, blocker := range blockers {
		objects, err := blockingObjects(ctx, db, blocker.query, id)
		if err != nil {
			return deletion.Plan{}, fmt.Errorf("list %s: %w", blocker.kind, err)
		}
		plan.Block(blocker.kind, objects)
	}
	for _, removed := range removes {
		count, err := countFor(ctx, db, removed.query, id)
		if err != nil {
			return deletion.Plan{}, fmt.Errorf("count %s: %w", removed.kind, err)
		}
		plan.Remove(removed.kind, count)
	}
	for _, detached := range detaches {
		count, err := countFor(ctx, db, detached.query, id)
		if err != nil {
			return deletion.Plan{}, fmt.Errorf("count %s: %w", detached.kind, err)
		}
		plan.Detach(detached.kind, count)
	}
	return plan, nil
}

func blockingObjects(ctx context.Context, db planQuerier, query, id string) ([]deletion.Object, error) {
	rows, err := db.QueryContext(ctx, query, id)
	if err != nil {
		return nil, err
	}
	return collect(rows, scanDeletionObject)
}

func scanDeletionObject(row scanner) (deletion.Object, error) {
	var object deletion.Object
	if err := row.Scan(&object.ID, &object.Name); err != nil {
		return deletion.Object{}, err
	}
	return object, nil
}

func countFor(ctx context.Context, db planQuerier, query, id string) (int, error) {
	var count int
	if err := db.QueryRowContext(ctx, query, id).Scan(&count); err != nil {
		return 0, err
	}
	return count, nil
}
