package store

import (
	"context"
	"testing"
	"time"

	"github.com/ldesfontaine/opencloud/internal/heartbeat"
	"github.com/ldesfontaine/opencloud/internal/status"
)

// La migration 028 ne change que la règle de suppression de cinq clés
// étrangères : les lignes, l'ordre des membres d'un composant et le jeton
// chiffré d'une tâche (lié à son identifiant) traversent la reconstruction.
func TestMigration028_RestrictsTheLinksAndKeepsTheRows(t *testing.T) {
	ctx := context.Background()
	db := openAtVersion(t, 27)
	seedMachines(t, db)
	exec(t, db, `INSERT INTO probes (id, name, kind, target, machine_id, status, interval_seconds, timeout_seconds,
		failure_threshold, recovery_threshold, created_at) VALUES ('p1', 'site', 'http', 'https://a.fr/', ?, 'up', 60, 5, 3, 2, 1)`, m1)
	exec(t, db, `INSERT INTO probe_results (probe_id, checked_at, outcome, duration_ms) VALUES ('p1', 1, 'up', 5), ('p1', 2, 'up', 5)`)
	exec(t, db, `INSERT INTO probe_days (probe_id, day, total, success, degraded, duration_ms) VALUES ('p1', 1, 2, 2, 0, 10)`)
	job := heartbeat.Heartbeat{ID: "h1", Token: "hb_secret", Name: "sauvegarde", MachineID: m1, Status: heartbeat.StatusNew, Interval: time.Hour, CreatedAt: time.Unix(1, 0)}
	if err := db.InsertHeartbeat(ctx, job); err != nil {
		t.Fatal(err)
	}
	exec(t, db, `INSERT INTO heartbeat_pings (heartbeat_id, kind, received_at) VALUES ('h1', 'finish', 1)`)
	exec(t, db, `INSERT INTO status_components (id, name, created_at) VALUES ('c1', 'Nuage', 1)`)
	// L'ordre saisi : sonde, machine, tâche.
	exec(t, db, `INSERT INTO status_component_members (component_id, probe_id) VALUES ('c1', 'p1')`)
	exec(t, db, `INSERT INTO status_component_members (component_id, machine_id) VALUES ('c1', ?)`, m1)
	exec(t, db, `INSERT INTO status_component_members (component_id, heartbeat_id) VALUES ('c1', 'h1')`)

	migrateToTheEnd(t, db)

	for table, want := range map[string]int{"probes": 1, "probe_results": 2, "probe_days": 1, "heartbeats": 1, "heartbeat_pings": 1, "status_component_members": 3} {
		if got := count(t, db, `SELECT COUNT(*) FROM `+table); got != want {
			t.Errorf("%s : %d lignes, %d attendues", table, got, want)
		}
	}
	component, err := db.GetComponent(ctx, "c1")
	if err != nil {
		t.Fatal(err)
	}
	order := []status.MemberKind{status.KindProbe, status.KindMachine, status.KindHeartbeat}
	for index, member := range component.Members {
		if member.Kind != order[index] {
			t.Fatalf("l'ordre des membres a changé : %+v", component.Members)
		}
	}
	if kept, err := db.GetHeartbeat(ctx, "h1"); err != nil || kept.Token != "hb_secret" || kept.MachineID != m1 {
		t.Fatalf("le jeton chiffré doit traverser la reconstruction : %+v %v", kept, err)
	}
	for _, link := range []struct{ table, column string }{
		{"probes", "machine_id"}, {"heartbeats", "machine_id"},
		{"status_component_members", "machine_id"}, {"status_component_members", "heartbeat_id"}, {"status_component_members", "probe_id"},
	} {
		rule := text(t, db, `SELECT on_delete FROM pragma_foreign_key_list(?) WHERE "from" = ?`, link.table, link.column)
		if rule != "RESTRICT" {
			t.Errorf("%s.%s : %s, RESTRICT attendu", link.table, link.column, rule)
		}
	}
	if _, err := db.sql.Exec(`DELETE FROM machines WHERE id = ?`, m1); err == nil {
		t.Fatal("la machine qui lance une sonde ne doit plus partir par un DELETE brut")
	}
}
