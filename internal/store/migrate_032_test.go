package store

import (
	"testing"
)

// seedBefore032 pose, au schéma d'avant la 032, une machine, un service
// déclaré d'une unité, un conteneur rattaché et un qui ne l'est pas, deux
// sondes (l'une surveille le service), une tâche, et une maintenance en
// cours sur un composant qui les montre.
func seedBefore032(t *testing.T, db *DB) {
	t.Helper()
	exec(t, db, `INSERT INTO machines (id, name, kind, public_key, enrolled_at, created_at) VALUES (?, 'vps-1', 'remote', zeroblob(32), 1, 1)`, m1)
	exec(t, db, `INSERT INTO declared_services (id, machine_id, name, nature, origin, compose_project, declared_by, created_at, updated_at)
		VALUES ('svc_a', ?, 'shop', 'application', 'compose', 'shop', 'operator', 1, 1)`, m1)
	exec(t, db, `INSERT INTO service_units (id, service_id, name, kind) VALUES ('unit_a', 'svc_a', 'app', 'container')`)
	exec(t, db, `INSERT INTO containers (id, machine_id, kind, name, container_id, state, created_at, first_seen_at, last_seen_at, unit_id)
		VALUES ('c_app', ?, 'container', 'shop-app-1', 'aaa', 'exited', 1, 1, 1, 'unit_a'),
		       ('c_alone', ?, 'container', 'stranger', 'fff', 'exited', 1, 1, 1, NULL)`, m1, m1)
	exec(t, db, `INSERT INTO probes (id, name, kind, target, machine_id, service_id, status, interval_seconds, timeout_seconds,
		failure_threshold, recovery_threshold, created_at)
		VALUES ('p_shop', 'boutique', 'http', 'https://a.fr/', ?, 'svc_a', 'down', 60, 5, 3, 2, 1),
		       ('p_other', 'autre', 'tcp', '127.0.0.1:22', ?, NULL, 'down', 60, 5, 3, 2, 1)`, m1, m1)
	exec(t, db, `INSERT INTO heartbeats (id, token, name, machine_id, status, interval_seconds, grace_seconds, created_at)
		VALUES ('h1', 'hb_x', 'sauvegarde', ?, 'late', 3600, 60, 1)`, m1)
	exec(t, db, `INSERT INTO status_components (id, name, created_at) VALUES ('cmp1', 'Boutique', 1), ('cmp2', 'Nuage', 1)`)
	exec(t, db, `INSERT INTO status_component_members (component_id, machine_id) VALUES ('cmp1', ?), ('cmp2', ?)`, m1, m1)
	exec(t, db, `INSERT INTO status_component_members (component_id, service_id) VALUES ('cmp1', 'svc_a')`)
	exec(t, db, `INSERT INTO status_component_members (component_id, probe_id) VALUES ('cmp1', 'p_shop')`)
	exec(t, db, `INSERT INTO status_component_members (component_id, heartbeat_id) VALUES ('cmp1', 'h1')`)
	exec(t, db, `INSERT INTO incidents (id, title, impact, status, starts_at, ends_at, created_at, updated_at, resolved_at) VALUES
		('i_now', 'Migration de la base', 'maintenance', 'in_progress', 100, 200, 50, 100, NULL),
		('i_done', 'Ancienne', 'maintenance', 'resolved', 10, 20, 5, 20, 20),
		('i_down', 'Panne', 'down', 'investigating', NULL, NULL, 5, 5, NULL)`)
	exec(t, db, `INSERT INTO incident_components (incident_id, component_id) VALUES
		('i_now', 'cmp1'), ('i_now', 'cmp2'), ('i_done', 'cmp1'), ('i_down', 'cmp1')`)
}

// La 032 pose l'alerte sur l'annuaire : le genre « service » devient
// « container », le service déclaré se lit par l'unité du conteneur ou par
// la sonde ; les silences deviennent des fenêtres, et une maintenance pas
// encore résolue en donne une par membre de ses composants. Les livraisons
// et les incidents restent.
func TestMigration032_AlertsOnTheDirectory_MutesFromSilencesAndMaintenances(t *testing.T) {
	db := openAtVersion(t, 31)
	seedBefore032(t, db)
	exec(t, db, `INSERT INTO alerts (id, kind, severity, status, silenced, object_kind, object_id, object_name, machine_id, container_id, probe_id,
		opened_at, updated_at) VALUES
		(1, 'service_down', 'danger', 'open', 1, 'service', 'c_app', 'shop-app-1', ?, 'c_app', NULL, 1, 1),
		(2, 'service_down', 'danger', 'open', 0, 'service', 'c_alone', 'stranger', ?, 'c_alone', NULL, 1, 1),
		(3, 'service_down', 'danger', 'resolved', 0, 'service', 'c_gone', 'disparu', ?, NULL, NULL, 1, 1),
		(4, 'probe_down', 'danger', 'open', 0, 'probe', 'p_shop', 'boutique', ?, NULL, 'p_shop', 1, 1),
		(5, 'probe_down', 'danger', 'open', 0, 'probe', 'p_other', 'autre', ?, NULL, 'p_other', 1, 1),
		(6, 'disk_full', 'attention', 'open', 0, 'volume', ?, '/data', ?, NULL, NULL, 1, 1)`,
		m1, m1, m1, m1, m1, m1+":/data", m1)
	exec(t, db, `INSERT INTO alert_channels (id, name, url, format, min_severity, created_at) VALUES (1, 'ops', '', 'json', 'attention', 1)`)
	exec(t, db, `INSERT INTO alert_deliveries (alert_id, channel_id, event, status, attempts, reason, code, created_at, updated_at)
		VALUES (2, 1, 'opened', 'failed', 3, 'status', 500, 1, 1)`)
	exec(t, db, `INSERT INTO alert_silences (id, kind, object_kind, object_id, object_name, reason, starts_at, ends_at, created_at) VALUES
		(7, '', 'machine', ?, 'vps-1', 'déménagement', 100, 200, 90),
		(8, 'service_down', 'service', 'c_alone', 'stranger', '', 100, 200, 90),
		(9, '', 'volume', ?, '/data', '', 100, 200, 90),
		(10, '', 'probe', 'p_disparue', 'site', '', 100, 200, 90),
		(11, 'job_late', '', '', '', 'cron revu', 100, 200, 90)`, m1, m1+":/data")

	migrateToTheEnd(t, db)

	alerts := map[string]string{
		"1": "container|c_app|svc_a|c_app",
		"2": "container|c_alone||c_alone",
		"3": "container|c_gone||",
		"4": "probe|p_shop|svc_a|",
		"5": "probe|p_other||",
		"6": "volume|" + m1 + ":/data||",
	}
	for id, want := range alerts {
		got := text(t, db, `SELECT object_kind || '|' || object_id || '|' || COALESCE(service_id, '') || '|' || COALESCE(container_id, '')
			FROM alerts WHERE id = ?`, id)
		if got != want {
			t.Errorf("alerte %s : %q, attendu %q", id, got, want)
		}
	}
	if got := count(t, db, `SELECT COUNT(*) FROM pragma_table_info('alerts') WHERE name = 'silenced'`); got != 0 {
		t.Error("la colonne silenced doit partir : taire se juge à chaque envoi")
	}
	if got := text(t, db, `SELECT status || '|' || attempts || '|' || COALESCE(next_attempt_at, 'null') FROM alert_deliveries`); got != "failed|3|null" {
		t.Errorf("livraison : %q", got)
	}

	mutes := map[string]bool{}
	rows, err := db.sql.Query(`SELECT COALESCE(alert_kind, '*') || '|' || COALESCE(machine_id, service_id, container_id, probe_id, heartbeat_id, 'tout')
		|| '|' || reason || '|' || starts_at || '-' || ends_at FROM mutes`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var line string
		if err := rows.Scan(&line); err != nil {
			t.Fatal(err)
		}
		mutes[line] = true
	}
	rows.Close()
	want := []string{
		// Les silences : la sonde disparue ne passe pas, le volume tait le disque de sa machine.
		"*|" + m1 + "|déménagement|100-200",
		"service_down|c_alone||100-200",
		"disk_full|" + m1 + "||100-200",
		"job_late|tout|cron revu|100-200",
		// La maintenance en cours : un membre, une fenêtre, la machine une seule fois.
		"*|" + m1 + "|Migration de la base|100-200",
		"*|svc_a|Migration de la base|100-200",
		"*|p_shop|Migration de la base|100-200",
		"*|h1|Migration de la base|100-200",
	}
	if len(mutes) != len(want) || count(t, db, `SELECT COUNT(*) FROM mutes`) != len(want) {
		t.Errorf("fenêtres : %v", mutes)
	}
	for _, line := range want {
		if !mutes[line] {
			t.Errorf("fenêtre manquante : %s (%v)", line, mutes)
		}
	}
	if got := count(t, db, `SELECT COUNT(*) FROM mutes WHERE id IN (7, 8, 9, 11)`); got != 4 {
		t.Errorf("les silences gardent leur identifiant : %d", got)
	}
	if got := count(t, db, `SELECT COUNT(*) FROM sqlite_master WHERE name = 'alert_silences'`); got != 0 {
		t.Error("alert_silences doit partir")
	}
	if got := count(t, db, `SELECT COUNT(*) FROM incidents`); got != 3 {
		t.Errorf("les incidents restent, pour le public : %d", got)
	}
	for _, link := range []struct{ table, column, rule string }{
		{"alerts", "service_id", "SET NULL"}, {"alerts", "container_id", "SET NULL"},
		{"mutes", "machine_id", "CASCADE"}, {"mutes", "service_id", "CASCADE"}, {"mutes", "container_id", "CASCADE"},
		{"mutes", "probe_id", "CASCADE"}, {"mutes", "heartbeat_id", "CASCADE"},
	} {
		if rule := text(t, db, `SELECT on_delete FROM pragma_foreign_key_list(?) WHERE "from" = ?`, link.table, link.column); rule != link.rule {
			t.Errorf("%s.%s : %s, %s attendu", link.table, link.column, rule, link.rule)
		}
	}
}
