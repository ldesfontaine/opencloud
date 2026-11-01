package store

import (
	"context"
	"testing"
)

// La 029 sur une base qui a déjà une machine, un service binaire et des clés
// SSH : rien ne change pour eux, les deux tables naissent vides (aucune
// machine n'a rapporté d'unité), et la suppression d'une machine les emporte.
func TestMigration029_AddsTheUnitTablesWithoutTouchingWhatExists(t *testing.T) {
	ctx := context.Background()
	db := openEmpty(t)
	names, err := migrationFiles()
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range names {
		if version := migrationVersion(name); version < 29 {
			if err := db.apply(ctx, name, version); err != nil {
				t.Fatal(err)
			}
		}
	}
	exec(t, db, `INSERT INTO machines (id, name, created_at) VALUES ('m_vps', 'vps-lyon', 1)`)
	exec(t, db, `INSERT INTO declared_services (id, machine_id, name, nature, origin, declared_by, created_at, updated_at)
		VALUES ('svc_forgejo', 'm_vps', 'forgejo', 'application', 'binary', 'operator', 1, 1)`)
	exec(t, db, `INSERT INTO service_units (id, service_id, name, kind, systemd_unit, ports, position)
		VALUES ('unit_forgejo', 'svc_forgejo', 'forgejo', 'binary', 'forgejo.service', '["3000"]', 0)`)
	exec(t, db, `INSERT INTO ssh_reports (machine_id, truncated, reported_at) VALUES ('m_vps', 0, 5)`)

	if err := db.apply(ctx, "029_systemd_units.sql", 29); err != nil {
		t.Fatal(err)
	}

	if got := text(t, db, `SELECT s.name || '|' || u.systemd_unit || '|' || u.ports FROM declared_services s JOIN service_units u ON u.service_id = s.id`); got != "forgejo|forgejo.service|[\"3000\"]" {
		t.Errorf("le service binaire a changé : %q", got)
	}
	if got := count(t, db, `SELECT COUNT(*) FROM ssh_reports`); got != 1 {
		t.Errorf("%d relevé(s) SSH, 1 attendu", got)
	}
	for _, table := range []string{"systemd_reports", "systemd_units"} {
		if got := count(t, db, `SELECT COUNT(*) FROM `+table); got != 0 {
			t.Errorf("%s : %d ligne(s) à la naissance, 0 attendue", table, got)
		}
	}
	exec(t, db, `INSERT INTO machines (id, name, created_at) VALUES ('m_bare', 'bare', 1)`)
	exec(t, db, `INSERT INTO systemd_reports (machine_id, available, reason, truncated, reported_at) VALUES ('m_bare', 1, '', 0, 9)`)
	exec(t, db, `INSERT INTO systemd_units (machine_id, name, load_state, active_state, sub_state) VALUES ('m_bare', 'forgejo.service', 'loaded', 'active', 'running')`)
	if _, err := db.sql.Exec(`UPDATE systemd_reports SET reason = 'raison inconnue' WHERE machine_id = 'm_bare'`); err == nil {
		t.Error("une raison hors liste doit être refusée par la base")
	}
	exec(t, db, `DELETE FROM machines WHERE id = 'm_bare'`)
	if got := count(t, db, `SELECT COUNT(*) FROM systemd_units`) + count(t, db, `SELECT COUNT(*) FROM systemd_reports`); got != 0 {
		t.Errorf("%d ligne(s) survivent à la machine", got)
	}
}
