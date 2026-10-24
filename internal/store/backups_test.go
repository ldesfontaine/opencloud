package store

import (
	"context"
	"database/sql"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/ldesfontaine/opencloud/internal/backup"
	"github.com/ldesfontaine/opencloud/internal/deletion"
	"github.com/ldesfontaine/opencloud/internal/machine"
)

var backupsNow = time.Unix(1_800_000_000, 0)

// Une seule paire : la seconde écriture remplace la première, sans ligne de
// plus.
func TestBackupKeyring_SavesOneRowAndReplaces(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	if _, err := db.GetBackupKeyring(ctx); !errors.Is(err, backup.ErrNotFound) {
		t.Fatalf("no key yet: %v", err)
	}
	first := backup.Keyring{Recipient: "age1premiere", Fingerprint: "0123456789abcdef", IdentitySealed: []byte{1, 2, 3, 0, 255}, CreatedAt: backupsNow}
	if err := db.SaveBackupKeyring(ctx, first); err != nil {
		t.Fatal(err)
	}
	got, err := db.GetBackupKeyring(ctx)
	if err != nil || !reflect.DeepEqual(got, first) {
		t.Fatalf("got %+v %v", got, err)
	}
	second := backup.Keyring{Recipient: "age1seconde", Fingerprint: "fedcba9876543210", IdentitySealed: []byte{9}, CreatedAt: backupsNow.Add(time.Hour)}
	if err := db.SaveBackupKeyring(ctx, second); err != nil {
		t.Fatal(err)
	}
	got, err = db.GetBackupKeyring(ctx)
	if err != nil || !reflect.DeepEqual(got, second) || count(t, db, `SELECT COUNT(*) FROM backup_keyring`) != 1 {
		t.Fatalf("got %+v %v", got, err)
	}
	if _, err := db.sql.Exec(`INSERT INTO backup_keyring (id, recipient, identity_sealed, fingerprint, created_at) VALUES (2, 'x', x'00', 'x', 1)`); err == nil {
		t.Fatal("a second row must be refused by the schema")
	}
}

func seedBackupPlan(t *testing.T, db *DB) backup.Destination {
	t.Helper()
	ctx := context.Background()
	destination := backup.Destination{ID: "bkd_1", Name: "store", MachineID: enrolledMachine(t, db, "vps"), Path: "/srv/backups", CreatedAt: backupsNow}
	if err := db.InsertBackupDestination(ctx, destination); err != nil {
		t.Fatal(err)
	}
	plan := backup.Plan{ID: "bkp_1", Subject: backup.SubjectDatabase, SourceKind: backup.SourceDatabase, DestinationID: destination.ID, Keep: 14, Schedule: backup.ScheduleDaily, CreatedAt: backupsNow, UpdatedAt: backupsNow}
	if err := db.SaveBackupPlan(ctx, plan); err != nil {
		t.Fatal(err)
	}
	return destination
}

// Le plan s'écrit par son sujet : refaire le plan change sa destination et
// son nombre gardé, pas son histoire. La tentative s'écrit à part ; un ordre
// absent efface le lien vers le dernier run.
func TestSaveBackupPlan_UpsertsBySubjectAndKeepsItsHistory(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	destination := seedBackupPlan(t, db)
	machineName := text(t, db, `SELECT name FROM machines WHERE id = ?`, destination.MachineID)

	listed, err := db.ListBackupDestinations(ctx)
	if err != nil || len(listed) != 1 || listed[0].MachineName != machineName || !listed[0].CreatedAt.Equal(backupsNow) {
		t.Fatalf("listed %+v %v", listed, err)
	}
	exec(t, db, `INSERT INTO action_runs (id, action, machine_name, state, not_after, seq, requested_by, requested_at)
		VALUES ('run1', 'backup.receive', 'vps', 'sent', 1, 1, 'operator:lucas', 1)`)
	if err := db.NoteBackupAttempt(ctx, backup.SubjectDatabase, backupsNow.Add(time.Hour), "", "run1"); err != nil {
		t.Fatal(err)
	}
	other := backup.Destination{ID: "bkd_2", Name: "autre", MachineID: destination.MachineID, Path: "/srv/b", CreatedAt: backupsNow}
	if err := db.InsertBackupDestination(ctx, other); err != nil {
		t.Fatal(err)
	}
	changed := backup.Plan{ID: "bkp_ignored", Subject: backup.SubjectDatabase, SourceKind: backup.SourceDatabase, DestinationID: "bkd_2", Keep: 30, Schedule: backup.ScheduleDaily,
		CreatedAt: backupsNow.Add(48 * time.Hour), UpdatedAt: backupsNow.Add(48 * time.Hour)}
	if err := db.SaveBackupPlan(ctx, changed); err != nil {
		t.Fatal(err)
	}
	plan, err := db.GetBackupPlan(ctx, backup.SubjectDatabase)
	if err != nil || plan.ID != "bkp_1" || plan.DestinationID != "bkd_2" || plan.Keep != 30 || plan.LastRunID != "run1" ||
		!plan.LastAttemptAt.Equal(backupsNow.Add(time.Hour)) || !plan.CreatedAt.Equal(backupsNow) || !plan.UpdatedAt.Equal(backupsNow.Add(48*time.Hour)) {
		t.Fatalf("plan %+v %v", plan, err)
	}
	if err := db.NoteBackupAttempt(ctx, backup.SubjectDatabase, backupsNow.Add(2*time.Hour), "backup.machine_offline", ""); err != nil {
		t.Fatal(err)
	}
	plan, _ = db.GetBackupPlan(ctx, backup.SubjectDatabase)
	if plan.LastError != "backup.machine_offline" || plan.LastRunID != "" {
		t.Fatalf("plan %+v", plan)
	}
	if err := db.DeleteBackupPlan(ctx, backup.SubjectDatabase); err != nil {
		t.Fatal(err)
	}
	if err := db.DeleteBackupPlan(ctx, backup.SubjectDatabase); !errors.Is(err, backup.ErrNotFound) {
		t.Fatalf("twice: %v", err)
	}
	if err := db.NoteBackupAttempt(ctx, backup.SubjectDatabase, backupsNow, "", ""); !errors.Is(err, backup.ErrNotFound) {
		t.Fatalf("without a plan: %v", err)
	}
}

// La purge des runs ne défait pas le plan : le lien se vide (set null).
func TestBackupPlan_SurvivesThePurgeOfItsLastRun(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	seedBackupPlan(t, db)
	exec(t, db, `INSERT INTO action_runs (id, action, machine_name, state, not_after, seq, requested_by, requested_at)
		VALUES ('run1', 'backup.receive', 'vps', 'succeeded', 1, 1, 'operator:lucas', 1)`)
	if err := db.NoteBackupAttempt(ctx, backup.SubjectDatabase, backupsNow, "", "run1"); err != nil {
		t.Fatal(err)
	}
	if err := db.PurgeRuns(ctx, backupsNow); err != nil {
		t.Fatal(err)
	}
	plan, err := db.GetBackupPlan(ctx, backup.SubjectDatabase)
	if err != nil || plan.LastRunID != "" || plan.LastAttemptAt.IsZero() {
		t.Fatalf("plan %+v %v", plan, err)
	}
}

func TestDeleteBackupDestination_RefusedWhileAPlanUsesIt(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	destination := seedBackupPlan(t, db)
	if err := db.DeleteBackupDestination(ctx, destination.ID); !errors.Is(err, backup.ErrDestinationInUse) {
		t.Fatalf("got %v", err)
	}
	if err := db.DeleteBackupDestination(ctx, "bkd_inconnue"); !errors.Is(err, backup.ErrNotFound) {
		t.Fatalf("unknown: %v", err)
	}
	if err := db.DeleteBackupPlan(ctx, backup.SubjectDatabase); err != nil {
		t.Fatal(err)
	}
	if err := db.DeleteBackupDestination(ctx, destination.ID); err != nil {
		t.Fatal(err)
	}
	if got, _ := db.CountBackupDestinations(ctx); got != 0 {
		t.Fatalf("%d destinations left", got)
	}
	twin := backup.Destination{ID: "bkd_2", Name: "store", MachineID: destination.MachineID, Path: "/a", CreatedAt: backupsNow}
	if err := db.InsertBackupDestination(ctx, twin); err != nil {
		t.Fatal(err)
	}
	twin.ID = "bkd_3"
	if err := db.InsertBackupDestination(ctx, twin); !errors.Is(err, backup.ErrNameTaken) {
		t.Fatalf("same name: %v", err)
	}
	if _, err := db.GetBackupDestination(ctx, "bkd_inconnue"); !errors.Is(err, backup.ErrNotFound) {
		t.Fatalf("unknown get: %v", err)
	}
}

// La machine qui reçoit les sauvegardes est retenue par son plan de
// suppression, nommée destination par destination, et le refus a sa clé.
func TestDeleteMachine_IsHeldBackByItsBackupDestination(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	destination := seedBackupPlan(t, db)
	err := db.DeleteMachine(ctx, destination.MachineID)
	var refused *deletion.RefusedError
	if !errors.Is(err, machine.ErrHasBackupDestinations) || !errors.As(err, &refused) {
		t.Fatalf("got %v", err)
	}
	want := []deletion.Blocker{{Kind: deletion.KindBackupDestinations, Objects: []deletion.Object{{ID: "bkd_1", Name: "store"}}}}
	if !reflect.DeepEqual(refused.Plan.Blockers, want) {
		t.Fatalf("blockers %+v", refused.Plan.Blockers)
	}
	if err := db.DeleteBackupPlan(ctx, backup.SubjectDatabase); err != nil {
		t.Fatal(err)
	}
	if err := db.DeleteBackupDestination(ctx, destination.ID); err != nil {
		t.Fatal(err)
	}
	if err := db.DeleteMachine(ctx, destination.MachineID); err != nil {
		t.Fatalf("released: %v", err)
	}
}

// L'instantané est une base entière, lisible seule, qui a la même matière
// que la vivante au moment de la copie.
func TestSnapshotDatabase_WritesAConsistentCopy(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	machineID := enrolledMachine(t, db, "vps")
	dir := t.TempDir()
	target := filepath.Join(dir, "snapshot.db")
	if err := db.SnapshotDatabase(ctx, target); err != nil {
		t.Fatal(err)
	}
	if err := db.SnapshotDatabase(ctx, target); err == nil {
		t.Fatal("VACUUM INTO must refuse a file that exists")
	}
	// Un fichier vide créé d'avance est accepté, et garde ses droits : c'est
	// ainsi que la sauvegarde obtient un instantané en 0600.
	private := filepath.Join(dir, "private.db")
	if err := os.WriteFile(private, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := db.SnapshotDatabase(ctx, private); err != nil {
		t.Fatalf("an empty file: %v", err)
	}
	if info, err := os.Stat(private); err != nil || info.Mode().Perm() != 0o600 || info.Size() == 0 {
		t.Fatalf("snapshot %v %v", info, err)
	}
	copied, err := sql.Open("sqlite", "file:"+target+"?"+url.Values{"mode": {"ro"}}.Encode())
	if err != nil {
		t.Fatal(err)
	}
	defer copied.Close()
	var integrity string
	if err := copied.QueryRow(`PRAGMA integrity_check`).Scan(&integrity); err != nil || integrity != "ok" {
		t.Fatalf("integrity_check = %q %v", integrity, err)
	}
	var name string
	if err := copied.QueryRow(`SELECT name FROM machines WHERE id = ?`, machineID).Scan(&name); err != nil || name != "vps" {
		t.Fatalf("the copy lost the machine: %q %v", name, err)
	}
}
