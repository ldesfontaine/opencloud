package store

import (
	"context"
	"testing"
	"time"

	"github.com/ldesfontaine/opencloud/internal/restic"
)

// La 034 sur une base qui a déjà des liens restic, une rétention notée et une
// sauvegarde (à jour de toutes les autres migrations, comme une installation
// qui suit dev) : les liens restent tels quels et se relisent sans
// vérification, les colonnes refusent NULL pour les mots et les comptes, et un
// essai raté n'efface pas la dernière réussite.
func TestMigration034_KeepsTheLinksAndStartsWithoutVerification(t *testing.T) {
	ctx := context.Background()
	db := openEmpty(t)
	names, err := migrationFiles()
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range names {
		if version := migrationVersion(name); version != 34 {
			if err := db.apply(ctx, name, version); err != nil {
				t.Fatal(err)
			}
		}
	}
	exec(t, db, `INSERT INTO machines (id, name, hostname, created_at) VALUES ('vps', 'vps', 'vps', 1), ('nas', 'nas', 'nas', 1)`)
	exec(t, db, `INSERT INTO restic_targets (machine_id, requested_host, requested_port, requested_dir, requested_at) VALUES ('nas', '192.0.2.10', 8000, '/srv/restic', 1)`)
	exec(t, db, `INSERT INTO restic_links (id, source_machine_id, target_machine_id, rest_password, repo_password, created_at, retain_at, retain_run_id, retain_state, retain_reason)
		VALUES ('l1', 'vps', 'nas', 'sealed-rest', 'sealed-repo', 5, 7, 'run-retain', 'succeeded', '')`)

	if err := db.apply(ctx, "034_restic_verify.sql", 34); err != nil {
		t.Fatal(err)
	}

	if got := text(t, db, `SELECT retain_state || '|' || retain_run_id || '|' || created_at || '|' || verify_state || '|' || verify_run_id || '|' || verify_reason || '|' || verify_snapshots || '|' || verify_files || '|' || verify_bytes FROM restic_links WHERE id = 'l1'`); got != "succeeded|run-retain|5||||0|0|0" {
		t.Errorf("le lien a changé : %q", got)
	}
	if _, err := db.sql.Exec(`UPDATE restic_links SET verify_state = 'inconnu'`); err == nil {
		t.Error("verify_state accepte un état hors de la liste")
	}
	for _, column := range []string{"verify_state", "verify_run_id", "verify_reason", "verify_snapshots", "verify_files", "verify_bytes"} {
		if _, err := db.sql.Exec(`UPDATE restic_links SET ` + column + ` = NULL`); err == nil {
			t.Errorf("%s accepte NULL", column)
		}
	}
	links, err := db.ListResticLinks(ctx)
	if err != nil || len(links) != 1 || links[0].Verify.State != "" || !links[0].Verify.At.IsZero() || !links[0].Verify.SuccessAt.IsZero() || links[0].Retain.State != "succeeded" {
		t.Fatalf("liens relus : %+v %v", links, err)
	}

	good := restic.VerifyOutcome{RunID: "v1", State: restic.StateSucceeded, At: time.Unix(10, 0), Snapshots: 2, Files: 4, Bytes: 4096}
	if err := db.NoteResticVerify(ctx, "l1", good); err != nil {
		t.Fatal(err)
	}
	bad := restic.VerifyOutcome{RunID: "v2", State: restic.StateFailed, Reason: "check_failed", At: time.Unix(20, 0)}
	if err := db.NoteResticVerify(ctx, "l1", bad); err != nil {
		t.Fatal(err)
	}
	links, err = db.ListResticLinks(ctx)
	if err != nil {
		t.Fatal(err)
	}
	verify := links[0].Verify
	if verify.RunID != "v2" || verify.State != restic.StateFailed || verify.Reason != "check_failed" || verify.At.Unix() != 20 || verify.SuccessAt.Unix() != 10 || verify.Files != 0 {
		t.Errorf("l'essai raté a effacé la dernière réussite : %+v", verify)
	}
	found, err := db.FindResticLinkByVerifyRun(ctx, "nas", "v2")
	if err != nil || found.ID != "l1" {
		t.Errorf("lien retrouvé par son run : %+v %v", found, err)
	}
	if _, err := db.FindResticLinkByVerifyRun(ctx, "vps", "v2"); err == nil {
		t.Error("un run ne se retrouve que par la cible à qui il a été envoyé")
	}
}
