package store

import (
	"context"
	"slices"
	"testing"
)

// La 005 remplace les composants visés par leurs membres : un composant à
// deux membres donne deux cibles, un objet venu de deux composants n'en
// donne qu'une, un composant vide ne donne rien et l'incident garde son fil.
func TestMigration005_UnfoldsComponentsIntoTargets(t *testing.T) {
	ctx := context.Background()
	db := openEmpty(t)
	names, err := migrationFiles()
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range names {
		if version := migrationVersion(name); version < 5 {
			if err := db.apply(ctx, name, version); err != nil {
				t.Fatal(err)
			}
		}
	}

	exec(t, db, `INSERT INTO machines (id, name, created_at) VALUES ('m1', 'vps-1', 1), ('m2', 'vps-2', 1)`)
	exec(t, db, `INSERT INTO declared_services (id, machine_id, name, nature, origin, declared_by, created_at, updated_at)
		VALUES ('svc_1', 'm1', 'web', 'web', 'manual', 'operator', 1, 1)`)
	exec(t, db, `INSERT INTO status_pages (id, slug, created_at, updated_at) VALUES ('p1', 'statut', 1, 1)`)
	exec(t, db, `INSERT INTO status_components (id, page_id, name, created_at) VALUES ('c_two', 'p1', 'Deux', 1), ('c_shared', 'p1', 'Partagé', 1), ('c_empty', 'p1', 'Vide', 1)`)
	exec(t, db, `INSERT INTO status_component_members (component_id, machine_id) VALUES ('c_two', 'm1')`)
	exec(t, db, `INSERT INTO status_component_members (component_id, service_id) VALUES ('c_two', 'svc_1')`)
	exec(t, db, `INSERT INTO status_component_members (component_id, machine_id) VALUES ('c_shared', 'm1')`)
	exec(t, db, `INSERT INTO incidents (id, title, impact, status, created_at, updated_at) VALUES ('i_two', 'Panne', 'down', 'investigating', 1, 1), ('i_empty', 'Vide', 'down', 'investigating', 1, 1)`)
	exec(t, db, `INSERT INTO incident_updates (incident_id, status, message, created_at) VALUES ('i_empty', 'investigating', 'On regarde.', 1)`)
	exec(t, db, `INSERT INTO incident_components (incident_id, component_id) VALUES ('i_two', 'c_two'), ('i_two', 'c_shared'), ('i_empty', 'c_empty')`)

	if err := db.apply(ctx, "005_incident_targets.sql", 5); err != nil {
		t.Fatal(err)
	}

	got := rowIDs(t, db, `SELECT COALESCE(machine_id, service_id, heartbeat_id, probe_id) FROM incident_targets WHERE incident_id = 'i_two'`)
	slices.Sort(got)
	requireRowIDs(t, got, "m1", "svc_1")
	requireRowIDs(t, rowIDs(t, db, `SELECT incident_id FROM incident_targets WHERE incident_id = 'i_empty'`))
	if tables := count(t, db, `SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'incident_components'`); tables != 0 {
		t.Fatal("incident_components doit disparaître")
	}
	if kept := count(t, db, `SELECT COUNT(*) FROM incidents`) + count(t, db, `SELECT COUNT(*) FROM incident_updates`); kept != 3 {
		t.Fatalf("les incidents et leur fil restent : %d ligne(s)", kept)
	}
	exec(t, db, `INSERT INTO mutes (starts_at, ends_at, created_at) VALUES (1, 2, 1)`)
	if title := text(t, db, `SELECT public_title FROM mutes`); title != "" || count(t, db, `SELECT announce FROM mutes`) != 0 {
		t.Fatal("un silence existant n'est pas annoncé")
	}
}
