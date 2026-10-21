package store

import (
	"context"
	"testing"
	"time"

	"github.com/ldesfontaine/opencloud/internal/machine"
)

// La migration 030 sépare la machine de son agent : chaque machine enrôlée
// reçoit l'agent qu'elle avait (clé, version, dernier signe de vie), la
// machine openCloud n'en reçoit aucun, le genre disparaît, et rien de ce qui
// vise une machine ne se perd dans la reconstruction.
func TestMigration030_MovesKeyVersionAndLastSeenToAgentsAndDropsTheKind(t *testing.T) {
	ctx := context.Background()
	db := openAtVersion(t, 29)
	exec(t, db, `INSERT INTO machines (id, name, kind, hostname, os, agent_version, enrolled_at, last_seen_at, created_at)
		VALUES ('local', 'opencloud', 'local', 'oc-host', 'Debian 12', 'v0.0.1', 5, 6, 1)`)
	exec(t, db, `INSERT INTO machines (id, name, kind, public_key, hostname, address, os, arch, agent_version, enrolled_at, last_seen_at, created_at)
		VALUES (?, 'vps-1', 'remote', zeroblob(32), 'vps-1', '51.15.20.114', 'Debian 12', 'amd64', 'v0.0.1', 10, 20, 2)`, m1)
	exec(t, db, `INSERT INTO machines (id, name, kind, public_key, enrolled_at, created_at) VALUES (?, 'vps-2', 'remote', zeroblob(32), 11, 3)`, m2)
	exec(t, db, `INSERT INTO enrollment_tokens (id, token_hash, token_prefix, name, created_at, expires_at, consumed_at, consumed_by)
		VALUES ('t1', 'h1', 'oc_aaaaaa', 'vps-1', 9, 99, 10, ?)`, m1)
	exec(t, db, `INSERT INTO enrollment_tokens (id, token_hash, token_prefix, name, machine_id, created_at, expires_at)
		VALUES ('t2', 'h2', 'oc_bbbbbb', 'vps-2', ?, 12, 9999999999)`, m2)
	exec(t, db, `INSERT INTO machine_samples (machine_id, sampled_at, cpu_percent, cpu_cores, load_1, mem_used, mem_total,
		swap_used, swap_total, disk_used, disk_total, net_rx_per_second, net_tx_per_second) VALUES (?, 1, 0, 1, 0, 0, 1, 0, 0, 0, 1, 0, 0)`, m1)
	exec(t, db, `INSERT INTO probes (id, name, kind, target, machine_id, status, interval_seconds, timeout_seconds,
		failure_threshold, recovery_threshold, created_at) VALUES ('p1', 'site', 'http', 'https://a.fr/', 'local', 'up', 60, 5, 3, 2, 1)`)

	migrateToTheEnd(t, db)

	if got := count(t, db, `SELECT COUNT(*) FROM pragma_table_info('machines') WHERE name IN ('kind', 'public_key', 'agent_version', 'enrolled_at', 'last_seen_at')`); got != 0 {
		t.Fatalf("%d columns moved to the agent are still on machines", got)
	}
	for table, want := range map[string]int{"machines": 3, "agents": 2, "enrollment_tokens": 2, "machine_samples": 1, "probes": 1} {
		if got := count(t, db, `SELECT COUNT(*) FROM `+table); got != want {
			t.Errorf("%s : %d lignes, %d attendues", table, got, want)
		}
	}
	local, err := db.GetMachine(ctx, machine.LocalID)
	if err != nil || local.Agent.Exists() || local.Hostname != "oc-host" || !local.CreatedAt.Equal(time.Unix(1, 0)) {
		t.Fatalf("the openCloud machine keeps its row, without an agent: %+v %v", local, err)
	}
	first, err := db.GetMachine(ctx, m1)
	if err != nil {
		t.Fatal(err)
	}
	agent := first.Agent
	if !agent.IsActive() || len(agent.PublicKey) != 32 || agent.Version != "v0.0.1" || agent.RootDir != "/srv/workspace" {
		t.Fatalf("agent of vps-1: %+v", agent)
	}
	if !agent.EnrolledAt.Equal(time.Unix(10, 0)) || !first.LastSeenAt.Equal(time.Unix(20, 0)) || first.Address != "51.15.20.114" {
		t.Fatalf("vps-1 lost its dates or address: %+v", first)
	}
	if len(agent.Requested) != 0 || len(agent.Capabilities) != 0 || first.Agent.CapabilitiesDiffer() {
		t.Fatalf("an agent from before the rights asks and reports nothing: %+v", agent)
	}
	if pending := count(t, db, `SELECT COUNT(*) FROM enrollment_tokens WHERE machine_id = ? AND consumed_at IS NULL AND agent_id IS NULL AND requested_capabilities = ''`, m2); pending != 1 {
		t.Fatal("the pending re-enroll token must survive, without rights")
	}
	for _, link := range []struct{ table, column, rule string }{
		{"agents", "machine_id", "CASCADE"},
		{"agent_capabilities", "agent_id", "CASCADE"},
		{"enrollment_tokens", "machine_id", "CASCADE"},
		{"enrollment_tokens", "agent_id", "SET NULL"},
		{"probes", "machine_id", "RESTRICT"},
	} {
		if rule := text(t, db, `SELECT on_delete FROM pragma_foreign_key_list(?) WHERE "from" = ?`, link.table, link.column); rule != link.rule {
			t.Errorf("%s.%s : %s, %s attendu", link.table, link.column, rule, link.rule)
		}
	}
	if dangling := count(t, db, `SELECT COUNT(*) FROM pragma_foreign_key_check`); dangling != 0 {
		t.Fatalf("%d dangling foreign keys", dangling)
	}
	// Un seul agent actif par machine : la base le tient.
	if _, err := db.sql.Exec(`INSERT INTO agents (id, machine_id, public_key, enrolled_at) VALUES ('agt_twin', ?, zeroblob(32), 30)`, m1); err == nil {
		t.Fatal("a second active agent on the same machine must be refused")
	}
}
