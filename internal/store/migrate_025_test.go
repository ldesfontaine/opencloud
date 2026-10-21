package store

import (
	"strings"
	"testing"
)

// La migration 025 renomme le conteneur observé, rien d'autre : les lignes,
// leurs liens et leurs index suivent sous le nouveau nom.

func TestMigration025_RenamesContainersAndKeepsTheirRows(t *testing.T) {
	db := openAtVersion(t, 24)
	seedMachines(t, db)
	for _, id := range []string{"c1", "c2"} {
		exec(t, db, `
			INSERT INTO services (id, machine_id, kind, name, container_id, image, state, ports, created_at, first_seen_at, last_seen_at)
			VALUES (?, ?, 'container', ?, ?, 'nginx:1.27', 'running', '[]', 1, 1, 1)`, id, m1, "nom-"+id, id)
	}
	exec(t, db, `INSERT INTO service_transitions (service_id, at, action, new_state) VALUES ('c1', 10, 'start', 'running')`)
	exec(t, db, `INSERT INTO service_transitions (service_id, at, action, new_state) VALUES ('c1', 20, 'die', 'exited')`)
	exec(t, db, `INSERT INTO service_samples (service_id, sampled_at, cpu_percent, mem_used, mem_limit) VALUES ('c1', 30, 1.5, 100, 200)`)
	exec(t, db, `INSERT INTO service_networks (service_id, network_id, name, ip) VALUES ('c1', 'n1', 'blog_default', '172.20.0.2')`)
	seedAlert(t, db, "service_down", "service", "c1", "", "c1")

	migrateToTheEnd(t, db)

	for table, want := range map[string]int{"containers": 2, "container_events": 2, "container_samples": 1, "container_networks": 1} {
		if got := count(t, db, `SELECT COUNT(*) FROM `+table); got != want {
			t.Errorf("%s : %d lignes, %d attendues", table, got, want)
		}
	}
	for _, old := range []string{"services", "service_transitions", "service_samples", "service_networks"} {
		if got := count(t, db, `SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?`, old); got != 0 {
			t.Errorf("la table %s existe encore", old)
		}
	}
	if got := count(t, db, `SELECT COUNT(*) FROM container_events WHERE container_id = 'c1'`); got != 2 {
		t.Errorf("les événements de c1 ont perdu leur lien : %d", got)
	}
	// Le genre « service » de l'alerte devient « container » à la 032.
	if got := count(t, db, `SELECT COUNT(*) FROM alerts WHERE container_id = 'c1' AND object_kind = 'container'`); got != 1 {
		t.Errorf("l'alerte de c1 a perdu son lien ou son genre d'objet : %d", got)
	}
	if violations := count(t, db, `SELECT COUNT(*) FROM pragma_foreign_key_check`); violations != 0 {
		t.Fatalf("%d clés étrangères pendantes", violations)
	}
	// Les clés tiennent sous le nouveau nom : supprimer une fiche emporte son historique.
	exec(t, db, `PRAGMA foreign_keys = ON`)
	exec(t, db, `DELETE FROM containers WHERE id = 'c1'`)
	for _, table := range []string{"container_events", "container_samples", "container_networks"} {
		if got := count(t, db, `SELECT COUNT(*) FROM `+table); got != 0 {
			t.Errorf("%s : %d lignes survivent à leur conteneur", table, got)
		}
	}
}

func TestMigration025_IndexesTakeTheNewNames(t *testing.T) {
	db := openAtVersion(t, 24)
	migrateToTheEnd(t, db)

	for _, name := range []string{
		"containers_machine", "containers_archived", "containers_unit",
		"container_events_container", "container_events_at",
		"container_samples_sampled", "container_networks_network",
	} {
		if got := count(t, db, `SELECT COUNT(*) FROM sqlite_master WHERE type = 'index' AND name = ?`, name); got != 1 {
			t.Errorf("l'index %s manque", name)
		}
	}
	if got := count(t, db, `SELECT COUNT(*) FROM sqlite_master WHERE sql LIKE '%service_transitions%' OR sql LIKE '%service_samples%' OR sql LIKE '%service_networks%' OR name LIKE 'service_transitions%' OR name LIKE 'service_samples%' OR name LIKE 'service_networks%' OR name IN ('services_machine', 'services_archived', 'services_unit')`); got != 0 {
		t.Errorf("%d objets portent encore l'ancien nom", got)
	}
	if !strings.Contains(text(t, db, `SELECT sql FROM sqlite_master WHERE name = 'container_events'`), "REFERENCES \"containers\"") &&
		!strings.Contains(text(t, db, `SELECT sql FROM sqlite_master WHERE name = 'container_events'`), "REFERENCES containers") {
		t.Error("container_events ne référence pas containers")
	}
}
