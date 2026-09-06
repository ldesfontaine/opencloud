package store

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/ldesfontaine/opencloud/internal/refusal"
	"github.com/ldesfontaine/opencloud/migrations"
)

var firstMigration = fstest.MapFS{
	"001_accounts.sql": &fstest.MapFile{Data: []byte(`CREATE TABLE accounts (id INTEGER PRIMARY KEY, username TEXT NOT NULL UNIQUE, password_hash TEXT NOT NULL, must_change_password INTEGER NOT NULL DEFAULT 0, created_at TEXT NOT NULL, updated_at TEXT NOT NULL);`)},
}

var twoMigrations = fstest.MapFS{
	"001_accounts.sql": firstMigration["001_accounts.sql"],
	"002_notes.sql":    &fstest.MapFile{Data: []byte(`CREATE TABLE notes (id INTEGER PRIMARY KEY);`)},
}

func openTestRoot(t *testing.T) *os.Root {
	t.Helper()
	root, err := os.OpenRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { root.Close() })
	return root
}

func quietLogger() *slog.Logger {
	return slog.New(slog.DiscardHandler)
}

func openTestStore(t *testing.T, root *os.Root, files fs.FS) *Store {
	t.Helper()
	store, err := Open(context.Background(), root, files, quietLogger())
	if err != nil {
		t.Fatalf("ouvrir le store : %v", err)
	}
	t.Cleanup(func() { store.Close() })
	return store
}

func appliedMigrations(t *testing.T, store *Store) []string {
	t.Helper()
	rows, err := store.db.Query(`SELECT name FROM schema_migrations ORDER BY version`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		names = append(names, name)
	}
	return names
}

func listBackups(t *testing.T, root *os.Root) []string {
	t.Helper()
	entries, err := fs.ReadDir(root.FS(), backupDirName)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	return names
}

func TestOpen_MissingDatabase_CreatesItAndAppliesMigrationsOnce(t *testing.T) {
	root := openTestRoot(t)

	store := openTestStore(t, root, twoMigrations)

	if got := appliedMigrations(t, store); len(got) != 2 {
		t.Fatalf("migrations appliquées = %v, attendu 2", got)
	}
	if backups := listBackups(t, root); len(backups) != 0 {
		t.Fatalf("une base neuve ne se sauvegarde pas, trouvé %v", backups)
	}
	info, err := root.Stat(DatabaseFileName)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != databaseFileMode {
		t.Fatalf("mode de la base = %o, attendu %o", info.Mode().Perm(), databaseFileMode)
	}
}

func TestOpen_Reopen_DoesNotReplayMigrations(t *testing.T) {
	root := openTestRoot(t)
	first := openTestStore(t, root, twoMigrations)
	first.Close()

	second := openTestStore(t, root, twoMigrations)

	if got := appliedMigrations(t, second); len(got) != 2 {
		t.Fatalf("migrations = %v, attendu toujours 2", got)
	}
	if backups := listBackups(t, root); len(backups) != 0 {
		t.Fatalf("rien à migrer, donc pas de sauvegarde, trouvé %v", backups)
	}
}

func TestOpen_PendingMigrationOnExistingDatabase_BacksUpFirst(t *testing.T) {
	root := openTestRoot(t)
	first := openTestStore(t, root, firstMigration)
	if _, err := first.CreateAccount(context.Background(), Account{Username: "admin", PasswordHash: "x"}); err != nil {
		t.Fatal(err)
	}
	first.Close()

	second := openTestStore(t, root, twoMigrations)

	if got := appliedMigrations(t, second); len(got) != 2 || got[1] != "002_notes.sql" {
		t.Fatalf("migrations = %v", got)
	}
	backups := listBackups(t, root)
	if len(backups) != 1 || !strings.HasSuffix(backups[0], "-before-002.db") {
		t.Fatalf("sauvegardes = %v, attendu une seule nommée d'après la migration", backups)
	}
	backupRoot, err := root.OpenRoot(backupDirName)
	if err != nil {
		t.Fatal(err)
	}
	defer backupRoot.Close()
	restored := openTestStore(t, backupRootAsState(t, backupRoot, backups[0]), firstMigration)
	count, err := restored.CountAccounts(context.Background())
	if err != nil || count != 1 {
		t.Fatalf("la sauvegarde doit contenir le compte : count=%d err=%v", count, err)
	}
}

func TestOpen_Backup_IsAsPrivateAsTheDatabase(t *testing.T) {
	root := openTestRoot(t)
	first := openTestStore(t, root, firstMigration)
	first.Close()

	openTestStore(t, root, twoMigrations)

	backups := listBackups(t, root)
	if len(backups) != 1 {
		t.Fatalf("sauvegardes = %v, attendu une seule", backups)
	}
	info, err := root.Stat(backupDirName + "/" + backups[0])
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != databaseFileMode {
		t.Fatalf("mode de la sauvegarde = %o, attendu %o", info.Mode().Perm(), databaseFileMode)
	}
}

// Copie la sauvegarde dans un répertoire d'état neuf pour l'ouvrir comme une base.
func backupRootAsState(t *testing.T, backupRoot *os.Root, backupName string) *os.Root {
	t.Helper()
	content, err := backupRoot.ReadFile(backupName)
	if err != nil {
		t.Fatal(err)
	}
	stateRoot := openTestRoot(t)
	if err := stateRoot.WriteFile(DatabaseFileName, content, databaseFileMode); err != nil {
		t.Fatal(err)
	}
	return stateRoot
}

func TestOpen_OneMigrationAfterAnother_KeepsOnlyTheLastBackups(t *testing.T) {
	root := openTestRoot(t)
	files := fstest.MapFS{"001_accounts.sql": firstMigration["001_accounts.sql"]}
	openTestStore(t, root, files).Close()

	// Une sauvegarde par migration : cinq migrations, cinq sauvegardes prises.
	const lastVersion = 6
	for version := 2; version <= lastVersion; version++ {
		files[fmt.Sprintf("%03d_notes.sql", version)] = &fstest.MapFile{
			Data: []byte(fmt.Sprintf("CREATE TABLE notes_%d (id INTEGER PRIMARY KEY);", version)),
		}
		openTestStore(t, root, files).Close()
	}

	backups := listBackups(t, root)
	if len(backups) != keptMigrationBackups {
		t.Fatalf("sauvegardes = %v, attendu les %d dernières", backups, keptMigrationBackups)
	}
	// fs.ReadDir trie par nom, et le nom commence par l'horodatage.
	for index, backup := range backups {
		want := fmt.Sprintf("-before-%03d.db", lastVersion-keptMigrationBackups+1+index)
		if !strings.HasSuffix(backup, want) {
			t.Fatalf("sauvegarde %s, attendu une qui finit par %s", backup, want)
		}
	}
}

func TestOpen_SchemaNewerThanTheBinary_RefusesToStart(t *testing.T) {
	root := openTestRoot(t)
	openTestStore(t, root, twoMigrations).Close()

	// Le binaire remis en arrière ne connaît que la première migration.
	_, err := Open(context.Background(), root, firstMigration, quietLogger())

	var refused refusal.Refusal
	if !errors.As(err, &refused) {
		t.Fatalf("attendu un refus, reçu %v", err)
	}
	for _, word := range []string{"002_notes.sql", "plus récente", "before-002.db"} {
		if !strings.Contains(refused.Error(), word) {
			t.Fatalf("le refus doit dire %q :\n%s", word, refused.Error())
		}
	}
	if backups := listBackups(t, root); len(backups) != 0 {
		t.Fatalf("un refus ne sauvegarde ni ne migre rien, trouvé %v", backups)
	}
}

func TestOpen_BackupFails_MigrationDoesNotHappen(t *testing.T) {
	root := openTestRoot(t)
	first := openTestStore(t, root, firstMigration)
	first.Close()
	// Un fichier à la place du dossier de sauvegardes : impossible d'y écrire.
	if err := root.WriteFile(backupDirName, []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := Open(context.Background(), root, twoMigrations, quietLogger())

	if err == nil || !strings.Contains(err.Error(), "backup before migration") {
		t.Fatalf("attendu un échec de sauvegarde, reçu %v", err)
	}
	check := openTestStore(t, root, firstMigration)
	if got := appliedMigrations(t, check); len(got) != 1 {
		t.Fatalf("la migration ne devait pas passer, appliquées = %v", got)
	}
}

func TestOpen_MalformedMigrationName_IsRefused(t *testing.T) {
	root := openTestRoot(t)
	bad := fstest.MapFS{"accounts.sql": &fstest.MapFile{Data: []byte(`SELECT 1;`)}}

	_, err := Open(context.Background(), root, bad, quietLogger())

	if err == nil || !strings.Contains(err.Error(), "NNN_subject.sql") {
		t.Fatalf("attendu un refus du nom, reçu %v", err)
	}
}

func TestOpen_EmbeddedMigrations_ApplyCleanly(t *testing.T) {
	root := openTestRoot(t)

	store := openTestStore(t, root, migrations.Files)

	got := appliedMigrations(t, store)
	if len(got) < 2 || got[0] != "001_accounts.sql" || got[1] != "002_sessions.sql" {
		t.Fatalf("migrations embarquées = %v", got)
	}
}

func TestAccounts_CreateFindUpdate(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t, openTestRoot(t), migrations.Files)

	id, err := store.CreateAccount(ctx, Account{Username: "admin", PasswordHash: "hash", MustChangePassword: true})
	if err != nil {
		t.Fatal(err)
	}
	found, err := store.FindAccountByUsername(ctx, "admin")
	if err != nil {
		t.Fatal(err)
	}
	if found.ID != id || !found.MustChangePassword || found.PasswordHash != "hash" {
		t.Fatalf("compte relu = %+v", found)
	}
	if found.CreatedAt.IsZero() {
		t.Fatal("created_at doit être relu")
	}

	if err := store.UpdateAccountPassword(ctx, id, "newhash", false); err != nil {
		t.Fatal(err)
	}
	updated, _ := store.FindAccountByID(ctx, id)
	if updated.PasswordHash != "newhash" || updated.MustChangePassword {
		t.Fatalf("compte mis à jour = %+v", updated)
	}

	if _, err := store.FindAccountByUsername(ctx, "nobody"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("attendu ErrNotFound, reçu %v", err)
	}
	if err := store.UpdateAccountPassword(ctx, 999, "x", false); !errors.Is(err, ErrNotFound) {
		t.Fatalf("attendu ErrNotFound, reçu %v", err)
	}
}

func TestSessions_CreateFindDeleteExpire(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t, openTestRoot(t), migrations.Files)
	accountID, err := store.CreateAccount(ctx, Account{Username: "admin", PasswordHash: "hash"})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)

	live := Session{TokenHash: "live", AccountID: accountID, CreatedAt: now, ExpiresAt: now.Add(time.Hour)}
	stale := Session{TokenHash: "stale", AccountID: accountID, CreatedAt: now, ExpiresAt: now.Add(-time.Hour)}
	for _, session := range []Session{live, stale} {
		if err := store.CreateSession(ctx, session); err != nil {
			t.Fatal(err)
		}
	}

	found, err := store.FindSession(ctx, "live")
	if err != nil {
		t.Fatal(err)
	}
	if !found.ExpiresAt.Equal(live.ExpiresAt) || found.AccountID != accountID {
		t.Fatalf("session relue = %+v", found)
	}

	if err := store.DeleteExpiredSessions(ctx, now); err != nil {
		t.Fatal(err)
	}
	if _, err := store.FindSession(ctx, "stale"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("la session périmée doit avoir disparu, reçu %v", err)
	}
	if _, err := store.FindSession(ctx, "live"); err != nil {
		t.Fatalf("la session vivante doit rester : %v", err)
	}

	if err := store.DeleteSessionsOfAccount(ctx, accountID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.FindSession(ctx, "live"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("toutes les sessions du compte doivent être fermées, reçu %v", err)
	}
}
