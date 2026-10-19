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
	if swapped, err := db.SaveAuthStateIf(ctx, auth.State{Mode: auth.ModeNone}, forward, enabledAt); err != nil || !swapped {
		t.Fatal(swapped, err)
	}
	got, err := db.GetAuthState(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got.Mode != auth.ModeForward || got.Header != "Remote-User" || !got.EnabledAt.Equal(enabledAt) || got.Username != "" {
		t.Fatalf("read back %+v", got)
	}
	if swapped, err := db.SaveAuthStateIf(ctx, got, auth.State{Mode: auth.ModeNone}, enabledAt); err != nil || !swapped {
		t.Fatal(swapped, err)
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

// La comparaison-échange : l'écriture ne passe que si la ligne vaut encore
// l'état attendu, et de deux écritures parties du même état, une seule gagne.
func TestSaveAuthStateIf_WritesOnlyFromTheExpectedState(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	at := time.Unix(1_800_000_000, 0)
	none := auth.State{Mode: auth.ModeNone}
	basic := auth.State{Mode: auth.ModeBasic, Username: "lucas", PasswordHash: "empreinte", EnabledAt: at}
	other := auth.State{Mode: auth.ModeBasic, Username: "autre", PasswordHash: "autre", EnabledAt: at}

	// Sans ligne, l'état stocké est « none ».
	swapped, err := db.SaveAuthStateIf(ctx, none, basic, at)
	if err != nil || !swapped {
		t.Fatalf("first write: %v %v", swapped, err)
	}
	swapped, err = db.SaveAuthStateIf(ctx, none, other, at)
	if err != nil || swapped {
		t.Fatalf("second write from the same state: %v %v", swapped, err)
	}
	got, err := db.GetAuthState(ctx)
	if err != nil || !got.Same(basic) {
		t.Fatalf("stored %+v %v, want %+v", got, err, basic)
	}
	swapped, err = db.SaveAuthStateIf(ctx, basic, none, at)
	if err != nil || !swapped {
		t.Fatalf("write from the stored state: %v %v", swapped, err)
	}
	if got, _ := db.GetAuthState(ctx); got.Mode != auth.ModeNone || got.PasswordHash != "" {
		t.Fatalf("after the swap %+v", got)
	}
}
