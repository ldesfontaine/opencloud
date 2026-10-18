package main

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/ldesfontaine/opencloud/internal/brand"
	"github.com/ldesfontaine/opencloud/internal/heartbeat"
	"github.com/ldesfontaine/opencloud/internal/seal"
	"github.com/ldesfontaine/opencloud/internal/store"
)

// openState ouvre le répertoire d'état et sa base, comme le font serve et mcp
// avant de demander la clé.
func openState(t *testing.T, stateDir string) (*os.Root, *store.DB) {
	t.Helper()
	root, err := openStateDir(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { root.Close() })
	db, err := store.Open(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return root, db
}

func capturedLogger() (*slog.Logger, *bytes.Buffer) {
	var journal bytes.Buffer
	return slog.New(slog.NewJSONHandler(&journal, nil)), &journal
}

// clearSealer « chiffre » en ne changeant rien : ce que la base contenait
// avant que les secrets ne soient chiffrés.
type clearSealer struct{}

func (clearSealer) Seal(plaintext string, _ seal.Scope) string { return plaintext }

func (clearSealer) Open(sealed string, _ seal.Scope) (string, error) { return sealed, nil }

// clearTokenHash remet token_hash à NULL en SQL brut, comme la migration 020
// le laisse sur les tâches d'avant. Le pilote est celui que le paquet store
// a enregistré, quel que soit son nom.
func clearTokenHash(t *testing.T, stateDir string) {
	t.Helper()
	driver := ""
	for _, candidate := range []string{"sqlite", "sqlite3"} {
		if slices.Contains(sql.Drivers(), candidate) {
			driver = candidate
		}
	}
	if driver == "" {
		t.Fatalf("no SQLite driver registered among %v", sql.Drivers())
	}
	handle, err := sql.Open(driver, "file:"+filepath.Join(stateDir, brand.DatabaseFile))
	if err != nil {
		t.Fatal(err)
	}
	defer handle.Close()
	if _, err := handle.Exec(`UPDATE heartbeats SET token_hash = NULL`); err != nil {
		t.Fatal(err)
	}
	var missing int
	if err := handle.QueryRow(`SELECT COUNT(*) FROM heartbeats WHERE token_hash IS NULL`).Scan(&missing); err != nil || missing != 1 {
		t.Fatalf("heartbeats without a hash: %d, %v", missing, err)
	}
}

func addHeartbeat(t *testing.T, db *store.DB, token string) {
	t.Helper()
	monitor := heartbeat.Heartbeat{ID: "job1", Token: token, Name: "backup", Status: heartbeat.StatusNew, Interval: time.Hour, CreatedAt: time.Unix(1_800_000_000, 0)}
	if err := db.InsertHeartbeat(context.Background(), monitor); err != nil {
		t.Fatal(err)
	}
}

func TestOpenSecrets_NoKeyOnAnEmptyBase_CreatesTheKeyInMode0600(t *testing.T) {
	_, stateDir := writeConfig(t)
	root, db := openState(t, stateDir)
	logger, journal := capturedLogger()

	if err := openSecrets(context.Background(), root, db, logger, true); err != nil {
		t.Fatal(err)
	}
	info, err := root.Stat(brand.SecretKeyFile)
	if err != nil {
		t.Fatalf("the key was not written: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v, want 0600", info.Mode().Perm())
	}
	if !strings.Contains(journal.String(), "secret key created") {
		t.Fatalf("journal: %s", journal)
	}
	addHeartbeat(t, db, "hb_after_start")
}

func TestOpenSecrets_NoKeyButSealedSecrets_RefusesAndWritesNoKey(t *testing.T) {
	_, stateDir := writeConfig(t)
	root, db := openState(t, stateDir)
	logger, _ := capturedLogger()
	if err := openSecrets(context.Background(), root, db, logger, true); err != nil {
		t.Fatal(err)
	}
	addHeartbeat(t, db, "hb_sealed")
	db.Close()
	if err := root.Remove(brand.SecretKeyFile); err != nil {
		t.Fatal(err)
	}

	again, err := store.Open(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	defer again.Close()
	err = openSecrets(context.Background(), root, again, logger, true)
	var refused *refusal
	if !errors.As(err, &refused) {
		t.Fatalf("got %v, want a refusal", err)
	}
	for _, want := range []string{brand.DatabaseFile, brand.SecretKeyFile, brand.Command + " secrets forget"} {
		if !strings.Contains(refused.message, want) {
			t.Errorf("the refusal does not say %q: %s", want, refused.message)
		}
	}
	if _, err := root.Stat(brand.SecretKeyFile); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a refused start must not write a key: %v", err)
	}
}

func TestOpenSecrets_McpWithoutKey_RefusesAndNeverCreatesIt(t *testing.T) {
	_, stateDir := writeConfig(t)
	root, db := openState(t, stateDir)
	logger, _ := capturedLogger()

	err := openSecrets(context.Background(), root, db, logger, false)
	var refused *refusal
	if !errors.As(err, &refused) || !strings.Contains(refused.message, "démarrez serve une fois") {
		t.Fatalf("got %v, want the mcp refusal", err)
	}
	if _, err := root.Stat(brand.SecretKeyFile); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("mcp must not write a key: %v", err)
	}
}

func TestOpenSecrets_McpWithoutKeyOnASealedBase_KeepsTheShortMessage(t *testing.T) {
	_, stateDir := writeConfig(t)
	root, db := openState(t, stateDir)
	logger, _ := capturedLogger()
	if err := openSecrets(context.Background(), root, db, logger, true); err != nil {
		t.Fatal(err)
	}
	addHeartbeat(t, db, "hb_sealed")
	if err := root.Remove(brand.SecretKeyFile); err != nil {
		t.Fatal(err)
	}

	err := openSecrets(context.Background(), root, db, logger, false)
	var refused *refusal
	if !errors.As(err, &refused) || !strings.Contains(refused.message, "démarrez serve une fois") || strings.Contains(refused.message, "forget") {
		t.Fatalf("got %v, want the short mcp refusal that names no destructive command", err)
	}
}

func TestOpenSecrets_SealsWhatAnOlderVersionLeftInTheClear(t *testing.T) {
	_, stateDir := writeConfig(t)
	root, db := openState(t, stateDir)
	logger, journal := capturedLogger()
	ctx := context.Background()
	// Une base d'avant le chiffrement : le jeton y est écrit tel quel.
	db.SetSealer(clearSealer{})
	addHeartbeat(t, db, "hb_clear_token")
	db.SetSealer(nil)
	// Après la migration, une tâche d'avant n'a pas encore d'empreinte.
	clearTokenHash(t, stateDir)

	if err := openSecrets(ctx, root, db, logger, true); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(journal.String(), "plaintext secrets sealed") {
		t.Fatalf("journal: %s", journal)
	}
	found, err := db.GetHeartbeatByToken(ctx, "hb_clear_token")
	if err != nil || found.ID != "job1" {
		t.Fatalf("%+v, %v", found, err)
	}
}

func TestOpenSecrets_RefusesAKeyOthersCanRead(t *testing.T) {
	_, stateDir := writeConfig(t)
	root, db := openState(t, stateDir)
	logger, _ := capturedLogger()
	if err := seal.Save(root, seal.Generate()); err != nil {
		t.Fatal(err)
	}
	if err := root.Chmod(brand.SecretKeyFile, 0o644); err != nil {
		t.Fatal(err)
	}

	err := openSecrets(context.Background(), root, db, logger, true)
	var refused *refusal
	if !errors.As(err, &refused) || !strings.Contains(refused.message, "chmod 600") {
		t.Fatalf("got %v, want a refusal that says how to fix the mode", err)
	}
}

func TestOpenSecrets_RefusesTheWrongKey(t *testing.T) {
	_, stateDir := writeConfig(t)
	root, db := openState(t, stateDir)
	logger, _ := capturedLogger()
	ctx := context.Background()
	if err := openSecrets(ctx, root, db, logger, true); err != nil {
		t.Fatal(err)
	}
	addHeartbeat(t, db, "hb_sealed")
	if err := seal.Save(root, seal.Generate()); err != nil {
		t.Fatal(err)
	}

	err := openSecrets(ctx, root, db, logger, true)
	var refused *refusal
	if !errors.As(err, &refused) || !strings.Contains(refused.message, brand.Command+" secrets forget") {
		t.Fatalf("got %v, want a refusal that names the way out", err)
	}
}

func TestSecretsForget_StartsAgainWithANewKeyAndNewPingTokens(t *testing.T) {
	configPath, stateDir := writeConfig(t)
	root, db := openState(t, stateDir)
	logger, _ := capturedLogger()
	ctx := context.Background()
	if err := openSecrets(ctx, root, db, logger, true); err != nil {
		t.Fatal(err)
	}
	addHeartbeat(t, db, "hb_before_loss")
	oldKey, err := seal.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	db.Close()
	if err := root.Remove(brand.SecretKeyFile); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	if err := secretsCommand([]string{"forget", "-config", configPath}, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "jetons de ping renouvelés : 1") {
		t.Fatalf("output: %q", out.String())
	}

	again, err := store.Open(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	defer again.Close()
	if err := openSecrets(ctx, root, again, logger, true); err != nil {
		t.Fatalf("the next start must work: %v", err)
	}
	newKey, err := seal.Load(root)
	if err != nil || newKey.ID() == oldKey.ID() {
		t.Fatalf("the key was not replaced: %v", err)
	}
	if _, err := again.GetHeartbeatByToken(ctx, "hb_before_loss"); !errors.Is(err, heartbeat.ErrNotFound) {
		t.Fatalf("old ping token: got %v, want ErrNotFound", err)
	}
	monitors, err := again.ListHeartbeats(ctx)
	if err != nil || len(monitors) != 1 || monitors[0].Token == "" {
		t.Fatalf("%+v, %v", monitors, err)
	}

	// Une fois fait, il n'y a plus rien à oublier : la clé neuve ouvre tout.
	err = secretsCommand([]string{"forget", "-config", configPath}, &bytes.Buffer{})
	var refused *refusal
	if !errors.As(err, &refused) || !strings.Contains(refused.message, "rien à oublier") {
		t.Fatalf("second run: got %v, want a refusal that says there is nothing to forget", err)
	}
}

func TestSecretsForget_RefusesWhileTheKeyStillOpensEverything(t *testing.T) {
	configPath, stateDir := writeConfig(t)
	root, db := openState(t, stateDir)
	logger, _ := capturedLogger()
	ctx := context.Background()
	if err := openSecrets(ctx, root, db, logger, true); err != nil {
		t.Fatal(err)
	}
	addHeartbeat(t, db, "hb_still_readable")
	keyBefore, err := root.ReadFile(brand.SecretKeyFile)
	if err != nil {
		t.Fatal(err)
	}
	db.Close()

	var out bytes.Buffer
	err = secretsCommand([]string{"forget", "-config", configPath}, &out)
	var refused *refusal
	if !errors.As(err, &refused) || !strings.Contains(refused.message, "rien à oublier") {
		t.Fatalf("got %v, want a refusal that says there is nothing to forget", err)
	}
	keyAfter, err := root.ReadFile(brand.SecretKeyFile)
	if err != nil || !bytes.Equal(keyBefore, keyAfter) {
		t.Fatalf("a refused command must leave the key alone: %v", err)
	}
	entries, err := fs.ReadDir(root.FS(), ".")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.Contains(entry.Name(), "forgotten") {
			t.Fatalf("a refused command must not set the key aside: %s", entry.Name())
		}
	}
	again, err := store.Open(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	defer again.Close()
	again.SetSealer(mustLoad(t, root))
	if _, err := again.GetHeartbeatByToken(ctx, "hb_still_readable"); err != nil {
		t.Fatalf("the ping token must still be there: %v", err)
	}
}

func TestSecretsForget_KeepsTheWrongKeyAsideInsteadOfOverwritingIt(t *testing.T) {
	configPath, stateDir := writeConfig(t)
	root, db := openState(t, stateDir)
	logger, _ := capturedLogger()
	ctx := context.Background()
	if err := openSecrets(ctx, root, db, logger, true); err != nil {
		t.Fatal(err)
	}
	addHeartbeat(t, db, "hb_sealed")
	db.Close()
	// Une clé intacte, mais qui n'est pas celle de la base : une restauration ratée.
	wrong := seal.Generate()
	if err := seal.Save(root, wrong); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	if err := secretsCommand([]string{"forget", "-config", configPath}, &out); err != nil {
		t.Fatal(err)
	}
	entries, err := fs.ReadDir(root.FS(), ".")
	if err != nil {
		t.Fatal(err)
	}
	var keptName string
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), brand.SecretKeyFile+".forgotten-") {
			keptName = entry.Name()
		}
	}
	if keptName == "" {
		t.Fatalf("the old key was not set aside: %v", entries)
	}
	if !strings.Contains(out.String(), keptName) {
		t.Fatalf("the output does not name the kept key: %q", out.String())
	}
	kept, err := root.ReadFile(keptName)
	if err != nil || !strings.Contains(string(kept), wrong.ID()) {
		t.Fatalf("the kept file is not the old key: %q, %v", kept, err)
	}
	current := mustLoad(t, root)
	if current.ID() == wrong.ID() {
		t.Fatal("the key was not replaced")
	}
}

func mustLoad(t *testing.T, root *os.Root) *seal.Keyring {
	t.Helper()
	keyring, err := seal.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	return keyring
}

func TestSecretsCommand_RefusesAMissingOrUnknownSubcommand(t *testing.T) {
	for _, args := range [][]string{nil, {"rotate"}} {
		err := secretsCommand(args, &bytes.Buffer{})
		var refused *refusal
		if !errors.As(err, &refused) {
			t.Errorf("%v: got %v, want a refusal", args, err)
		}
	}
}
