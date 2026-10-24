package main

import (
	"bytes"
	"context"
	"errors"
	"io/fs"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/ldesfontaine/opencloud/internal/brand"
	"github.com/ldesfontaine/opencloud/internal/heartbeat"
	"github.com/ldesfontaine/opencloud/internal/seal"
	"github.com/ldesfontaine/opencloud/internal/store"
)

// openState ouvre le répertoire d'état et sa base, comme le fait serve
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

// replaceKey pose une autre clé à la place de celle du répertoire d'état,
// comme une restauration ratée.
func replaceKey(t *testing.T, root *os.Root, keyring *seal.Keyring) {
	t.Helper()
	if err := root.Remove(brand.SecretKeyFile); err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
	if err := seal.Create(root, keyring); err != nil {
		t.Fatal(err)
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

	if err := openSecrets(context.Background(), root, db, logger); err != nil {
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
	if err := openSecrets(context.Background(), root, db, logger); err != nil {
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
	err = openSecrets(context.Background(), root, again, logger)
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

func TestOpenSecrets_RefusesAKeyOthersCanRead(t *testing.T) {
	_, stateDir := writeConfig(t)
	root, db := openState(t, stateDir)
	logger, _ := capturedLogger()
	replaceKey(t, root, seal.Generate())
	if err := root.Chmod(brand.SecretKeyFile, 0o644); err != nil {
		t.Fatal(err)
	}

	err := openSecrets(context.Background(), root, db, logger)
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
	if err := openSecrets(ctx, root, db, logger); err != nil {
		t.Fatal(err)
	}
	addHeartbeat(t, db, "hb_sealed")
	replaceKey(t, root, seal.Generate())

	err := openSecrets(ctx, root, db, logger)
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
	if err := openSecrets(ctx, root, db, logger); err != nil {
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
	if !strings.Contains(out.String(), "jetons de ping renouvelés : 1") || !strings.Contains(out.String(), "jetons Proxmox effacés : 0") {
		t.Fatalf("output: %q", out.String())
	}

	again, err := store.Open(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	defer again.Close()
	if err := openSecrets(ctx, root, again, logger); err != nil {
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
	if err := openSecrets(ctx, root, db, logger); err != nil {
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
	if err := openSecrets(ctx, root, db, logger); err != nil {
		t.Fatal(err)
	}
	addHeartbeat(t, db, "hb_sealed")
	db.Close()
	// Une clé intacte, mais qui n'est pas celle de la base : une restauration ratée.
	wrong := seal.Generate()
	replaceKey(t, root, wrong)

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
	for _, args := range [][]string{nil, {"renew"}} {
		err := secretsCommand(args, &bytes.Buffer{})
		var refused *refusal
		if !errors.As(err, &refused) {
			t.Errorf("%v: got %v, want a refusal", args, err)
		}
	}
}
