package store

import (
	"context"
	"errors"
	"os"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/ldesfontaine/opencloud/internal/refusal"
)

// Une migration au SQL invalide : la transaction échoue, comme une coupure au
// milieu, et le verrou doit rester.
var brokenMigration = fstest.MapFS{
	"001_accounts.sql": firstMigration["001_accounts.sql"],
	"002_broken.sql":   &fstest.MapFile{Data: []byte(`CREATE TABLE ;`)},
}

func lockExists(t *testing.T, root *os.Root) bool {
	t.Helper()
	_, err := root.Stat(MigrationLockFileName)
	if err == nil {
		return true
	}
	if errors.Is(err, os.ErrNotExist) {
		return false
	}
	t.Fatal(err)
	return false
}

func TestMigrationLock_Held_NamesThisProcessThenIsReleased(t *testing.T) {
	root := openTestRoot(t)

	lock, err := acquireMigrationLock(root)
	if err != nil {
		t.Fatal(err)
	}

	info, err := root.Stat(MigrationLockFileName)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != migrationLockFileMode {
		t.Fatalf("mode du verrou = %o, attendu %o", info.Mode().Perm(), migrationLockFileMode)
	}
	holder := readMigrationLock(root)
	if holder.pid != strconv.Itoa(os.Getpid()) {
		t.Fatalf("pid du verrou = %q, attendu celui de ce processus", holder.pid)
	}
	if holder.date == unknownLockDate || !strings.HasSuffix(holder.date, "Z") {
		t.Fatalf("date du verrou = %q, attendu une date ISO en UTC", holder.date)
	}

	if err := lock.release(); err != nil {
		t.Fatal(err)
	}
	if lockExists(t, root) {
		t.Fatal("le verrou rendu doit avoir disparu")
	}
}

func TestMigrationLock_AlreadyHeld_IsRefused(t *testing.T) {
	root := openTestRoot(t)
	if _, err := acquireMigrationLock(root); err != nil {
		t.Fatal(err)
	}

	_, err := acquireMigrationLock(root)

	var refused refusal.Refusal
	if !errors.As(err, &refused) {
		t.Fatalf("attendu un refus, reçu %v", err)
	}
}

func TestOpen_NothingPending_LeavesNoLock(t *testing.T) {
	root := openTestRoot(t)
	openTestStore(t, root, twoMigrations).Close()

	openTestStore(t, root, twoMigrations)

	if lockExists(t, root) {
		t.Fatal("rien à migrer : aucun verrou ne doit être posé")
	}
}

func TestOpen_MigrationApplied_ReleasesTheLock(t *testing.T) {
	root := openTestRoot(t)

	openTestStore(t, root, twoMigrations)

	if lockExists(t, root) {
		t.Fatal("le verrou doit être rendu après la dernière migration")
	}
}

func TestOpen_MigrationFails_KeepsTheLock(t *testing.T) {
	root := openTestRoot(t)

	_, err := Open(context.Background(), root, brokenMigration, quietLogger())

	if err == nil || !strings.Contains(err.Error(), "apply migration 002_broken.sql") {
		t.Fatalf("attendu l'échec de la migration, reçu %v", err)
	}
	if !lockExists(t, root) {
		t.Fatal("une migration échouée laisse son verrou : c'est ce qu'il doit dire")
	}
}

func TestOpen_BackupFails_KeepsTheLock(t *testing.T) {
	root := openTestRoot(t)
	openTestStore(t, root, firstMigration).Close()
	// Un fichier à la place du dossier de sauvegardes : impossible d'y écrire.
	if err := root.WriteFile(backupDirName, []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := Open(context.Background(), root, twoMigrations, quietLogger())

	if err == nil || !strings.Contains(err.Error(), "backup before migration") {
		t.Fatalf("attendu un échec de sauvegarde, reçu %v", err)
	}
	if !lockExists(t, root) {
		t.Fatal("une sauvegarde échouée laisse le verrou : la migration n'a pas eu lieu")
	}
}

func TestOpen_LockLeftBehind_RefusesAndNamesPidDateAndPath(t *testing.T) {
	root := openTestRoot(t)
	if _, err := Open(context.Background(), root, brokenMigration, quietLogger()); err == nil {
		t.Fatal("la migration invalide devait échouer")
	}
	left := readMigrationLock(root)

	_, err := Open(context.Background(), root, firstMigration, quietLogger())

	var refused refusal.Refusal
	if !errors.As(err, &refused) {
		t.Fatalf("attendu un refus, reçu %v", err)
	}
	for _, word := range []string{left.pid, left.date, "migration est en cours ou a été interrompue"} {
		if !strings.Contains(refused.Cause, word) {
			t.Fatalf("la cause doit dire %q :\n%s", word, refused.Cause)
		}
	}
	for _, word := range []string{root.Name() + "/" + MigrationLockFileName, "pgrep", "transaction"} {
		if !strings.Contains(refused.Remedy, word) {
			t.Fatalf("le remède doit dire %q :\n%s", word, refused.Remedy)
		}
	}
}

func TestOpen_LockLeftBehind_NeitherBacksUpNorMigrates(t *testing.T) {
	root := openTestRoot(t)
	openTestStore(t, root, firstMigration).Close()
	if err := root.WriteFile(MigrationLockFileName, []byte("42\n2026-01-02T03:04:05Z\n"), migrationLockFileMode); err != nil {
		t.Fatal(err)
	}

	_, err := Open(context.Background(), root, twoMigrations, quietLogger())

	var refused refusal.Refusal
	if !errors.As(err, &refused) {
		t.Fatalf("attendu un refus, reçu %v", err)
	}
	if !strings.Contains(refused.Cause, "42") || !strings.Contains(refused.Cause, "2026-01-02T03:04:05Z") {
		t.Fatalf("le refus doit relire le verrou :\n%s", refused.Cause)
	}
	if backups := listBackups(t, root); len(backups) != 0 {
		t.Fatalf("un refus ne sauvegarde rien, trouvé %v", backups)
	}
	if err := root.Remove(MigrationLockFileName); err != nil {
		t.Fatal(err)
	}
	check := openTestStore(t, root, firstMigration)
	if got := appliedMigrations(t, check); len(got) != 1 {
		t.Fatalf("la migration ne devait pas passer, appliquées = %v", got)
	}
}

func TestOpen_LockRemovedByHand_MigratesAgain(t *testing.T) {
	root := openTestRoot(t)
	if _, err := Open(context.Background(), root, brokenMigration, quietLogger()); err == nil {
		t.Fatal("la migration invalide devait échouer")
	}
	if err := root.Remove(MigrationLockFileName); err != nil {
		t.Fatal(err)
	}

	store := openTestStore(t, root, twoMigrations)

	if got := appliedMigrations(t, store); len(got) != 2 {
		t.Fatalf("migrations appliquées = %v, attendu 2 une fois le verrou retiré", got)
	}
	if lockExists(t, root) {
		t.Fatal("le verrou doit être rendu après la dernière migration")
	}
}

func TestOpen_EmptyLock_StillRefuses(t *testing.T) {
	root := openTestRoot(t)
	if err := root.WriteFile(MigrationLockFileName, nil, migrationLockFileMode); err != nil {
		t.Fatal(err)
	}

	_, err := Open(context.Background(), root, firstMigration, quietLogger())

	var refused refusal.Refusal
	if !errors.As(err, &refused) {
		t.Fatalf("attendu un refus, reçu %v", err)
	}
	if !strings.Contains(refused.Cause, unknownLockPid) || !strings.Contains(refused.Cause, unknownLockDate) {
		t.Fatalf("un verrou vide reste un verrou :\n%s", refused.Cause)
	}
}

func TestCheckMigrationLock_NoStateDirectoryOrNoLock_SaysNothing(t *testing.T) {
	root := openTestRoot(t)

	if err := CheckMigrationLock(root.Name() + "/absent"); err != nil {
		t.Fatalf("un répertoire d'état absent n'est pas un verrou : %v", err)
	}
	if err := CheckMigrationLock(root.Name()); err != nil {
		t.Fatalf("pas de verrou, pas de refus : %v", err)
	}
}
