package store

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/ldesfontaine/opencloud/internal/auth"
)

func TestAuthState_HeaderRoundTripsAndModeNoneClearsIt(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	enabledAt := time.Unix(1_800_000_000, 0)
	forward := auth.State{Mode: auth.ModeForward, Header: "Remote-User", EnabledAt: enabledAt}
	if err := db.SaveAuthState(ctx, forward, enabledAt); err != nil {
		t.Fatal(err)
	}
	got, err := db.GetAuthState(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got.Mode != auth.ModeForward || got.Header != "Remote-User" || !got.EnabledAt.Equal(enabledAt) || got.Username != "" {
		t.Fatalf("read back %+v", got)
	}
	if err := db.SaveAuthState(ctx, auth.State{Mode: auth.ModeNone}, enabledAt); err != nil {
		t.Fatal(err)
	}
	got, err = db.GetAuthState(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got.Mode != auth.ModeNone || got.Header != "" || !got.EnabledAt.IsZero() {
		t.Fatalf("after disable %+v", got)
	}
}

// La migration 018 ajoute la colonne sans toucher à une installation déjà
// protégée en basic : l'identifiant et l'empreinte restent, l'en-tête est vide.
func TestMigration018_KeepsAnExistingBasicState(t *testing.T) {
	root, err := os.OpenRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	ctx := context.Background()
	db, err := Open(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		`ALTER TABLE auth_settings DROP COLUMN forward_header`,
		`DELETE FROM schema_migrations WHERE version = 18`,
		`INSERT INTO auth_settings (id, mode, username, password_hash, enabled_at, updated_at) VALUES (1, 'basic', 'lucas', 'empreinte', 1, 1)`,
	} {
		if _, err := db.sql.ExecContext(ctx, statement); err != nil {
			t.Fatalf("%s: %v", statement, err)
		}
	}
	db.Close()

	again, err := Open(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	defer again.Close()
	state, err := again.GetAuthState(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if state.Mode != auth.ModeBasic || state.Username != "lucas" || state.PasswordHash != "empreinte" || state.Header != "" {
		t.Fatalf("%+v", state)
	}
}
