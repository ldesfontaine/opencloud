package store

import (
	"context"
	"testing"
	"time"

	"github.com/ldesfontaine/opencloud/internal/agentwire"
	"github.com/ldesfontaine/opencloud/internal/restic"
)

// La 033 sur une base qui a déjà des sauvegardes restic et leur historique
// (à jour de la 032 et de la 035, comme une installation qui suit dev) : les
// lignes restent telles quelles avec « [] » pour parties, se relisent sans
// partie, la colonne refuse NULL, et un passage suivant écrit ses parties.
func TestMigration033_KeepsTheBackupsAndStartsWithoutParts(t *testing.T) {
	ctx := context.Background()
	db := openEmpty(t)
	names, err := migrationFiles()
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range names {
		if version := migrationVersion(name); version != 33 {
			if err := db.apply(ctx, name, version); err != nil {
				t.Fatal(err)
			}
		}
	}
	exec(t, db, `INSERT INTO machines (id, name, hostname, created_at) VALUES ('pve-host', 'pve', 'pve', 1), ('nas-host', 'nas', 'nas', 1)`)
	exec(t, db, `INSERT INTO restic_targets (machine_id, requested_host, requested_port, requested_dir, requested_at) VALUES ('nas-host', '192.0.2.10', 8000, '/srv/restic', 1)`)
	exec(t, db, `INSERT INTO restic_backups (id, name, source_machine_id, scope, vmid, guest_type, target_machine_id, repo_user, keep_daily,
		created_at, last_run_id, last_state, last_path, last_path_reason, last_bytes_added, last_finished_at, last_success_at)
		VALUES ('rb1', 'docker1', 'pve-host', 'vm', 101, 'qemu', 'nas-host', 'pve-host', 14, 1, 'run1', 'succeeded', 'direct', 'reachable', 42, 2, 2)`)
	exec(t, db, `INSERT INTO restic_runs (backup_id, run_id, finished_at, state, path, path_reason, bytes_added) VALUES ('rb1', 'run1', 2, 'succeeded', 'direct', 'reachable', 42)`)

	if err := db.apply(ctx, "033_restic_parts.sql", 33); err != nil {
		t.Fatal(err)
	}

	if got := text(t, db, `SELECT last_parts || '|' || last_state || '|' || last_path || '|' || last_bytes_added FROM restic_backups WHERE id = 'rb1'`); got != "[]|succeeded|direct|42" {
		t.Errorf("la sauvegarde a changé : %q", got)
	}
	if got := text(t, db, `SELECT parts || '|' || run_id || '|' || bytes_added FROM restic_runs WHERE backup_id = 'rb1'`); got != "[]|run1|42" {
		t.Errorf("l'historique a changé : %q", got)
	}
	if _, err := db.sql.Exec(`UPDATE restic_runs SET parts = NULL`); err == nil {
		t.Error("la colonne accepte NULL")
	}
	stored, err := db.GetResticBackup(ctx, "rb1")
	if err != nil || len(stored.Last.Report.Parts) != 0 || stored.LastSuccessAt.Unix() != 2 {
		t.Fatalf("sauvegarde relue : %+v %v", stored, err)
	}

	// Le passage suivant écrit ses parties, la sauvegarde et l'historique les relisent.
	parts := []agentwire.ResticPartReport{
		{Kind: agentwire.ResticPartFolder, State: agentwire.ResticPartOK, BytesAdded: 3},
		{Kind: agentwire.ResticPartDatabase, Name: "nextcloud-db", State: agentwire.ResticPartFailed, Reason: agentwire.ReasonDumpFailed},
	}
	outcome := restic.Outcome{RunID: "run2", State: restic.StateFailed, FinishedAt: time.Unix(3, 0), Report: agentwire.ResticRunReport{
		Path: agentwire.ResticPathDirect, Reason: agentwire.ResticReachable, BytesAdded: 3, Parts: parts,
	}}
	exec(t, db, `UPDATE restic_backups SET last_run_id = 'run2', last_state = 'sent' WHERE id = 'rb1'`)
	if err := db.RecordResticOutcome(ctx, "rb1", outcome); err != nil {
		t.Fatal(err)
	}
	stored, err = db.GetResticBackup(ctx, "rb1")
	if err != nil || len(stored.Last.Report.Parts) != 2 || stored.Last.Report.Parts[1] != parts[1] || stored.LastSuccessAt.Unix() != 2 {
		t.Fatalf("parties relues : %+v %v", stored, err)
	}
	runs, err := db.ListRecentResticRuns(ctx, 7)
	if err != nil || len(runs["rb1"]) != 2 || len(runs["rb1"][0].Parts) != 2 || runs["rb1"][0].Parts[1].Reason != agentwire.ReasonDumpFailed || len(runs["rb1"][1].Parts) != 0 {
		t.Fatalf("historique relu : %+v %v", runs, err)
	}
}
