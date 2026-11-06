package store

import (
	"context"
	"testing"
)

// La 041 réécrit les clés de refus que les plans, les sauvegardes restic et
// leurs historiques ont gardées, et laisse les autres.
func TestMigration041_RewritesTheMachineRefusalKeysItsTablesKept(t *testing.T) {
	ctx := context.Background()
	db := openEmpty(t)
	names, err := migrationFiles()
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range names {
		if version := migrationVersion(name); version < 41 {
			if err := db.apply(ctx, name, version); err != nil {
				t.Fatal(err)
			}
		}
	}
	exec(t, db, `INSERT INTO machines (id, name, created_at) VALUES ('m1', 'vps', 1)`)
	exec(t, db, `INSERT INTO backup_destinations (id, name, machine_id, path, created_at) VALUES ('bkd_1', 'store', 'm1', '/srv/backups', 1)`)
	exec(t, db, `INSERT INTO backup_plans (id, subject, destination_id, schedule, keep, last_error, created_at, updated_at)
		VALUES ('bkp_1', 'database', 'bkd_1', 'daily', 14, 'backup.machine_offline', 5, 6)`)
	exec(t, db, `INSERT INTO backup_plans (id, subject, source_kind, source_machine_id, producer, selector, destination_id, schedule, keep, last_error, created_at, updated_at)
		VALUES ('bkp_2', 'vms', 'agent', 'm1', 'proxmox', 'all', 'bkd_1', 'daily', 14, 'backup.destination_agent_outdated', 5, 6)`)
	exec(t, db, `INSERT INTO backup_runs (subject, pass_at, source, started_at, finished_at, result, reason, destination_id, destination_name)
		VALUES ('database', 1, 'database', 1, 1, 'refused', 'backup.machine_unknown', 'bkd_1', 'store')`)
	exec(t, db, `INSERT INTO restic_targets (machine_id, requested_host, requested_port, requested_dir, requested_at) VALUES ('m1', 'h', 8000, '/srv', 1)`)
	exec(t, db, `INSERT INTO restic_backups (id, name, source_machine_id, scope, vmid, guest_type, target_machine_id, repo_user, created_at, last_reason)
		VALUES ('rb1', 'vm', 'm1', 'vm', 100, 'qemu', 'm1', 'u', 1, 'restic.machine_offline')`)
	exec(t, db, `INSERT INTO restic_runs (backup_id, run_id, finished_at, state, reason) VALUES ('rb1', 'r1', 1, 'refused', 'restic.machine_unknown')`)

	if err := db.apply(ctx, "041_refusal_keys.sql", 41); err != nil {
		t.Fatal(err)
	}

	checks := map[string][2]string{
		"plan":       {`SELECT last_error FROM backup_plans WHERE id = 'bkp_1'`, "machine.offline"},
		"plan other": {`SELECT last_error FROM backup_plans WHERE id = 'bkp_2'`, "backup.destination_agent_outdated"},
		"backup run": {`SELECT reason FROM backup_runs`, "machine.unknown"},
		"restic":     {`SELECT last_reason FROM restic_backups`, "machine.offline"},
		"restic run": {`SELECT reason FROM restic_runs`, "machine.unknown"},
	}
	for name, check := range checks {
		if got := text(t, db, check[0]); got != check[1] {
			t.Errorf("%s: %q, attendu %q", name, got, check[1])
		}
	}
}
