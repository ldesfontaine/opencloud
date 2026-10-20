package settings_test

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"testing"

	"github.com/ldesfontaine/opencloud/internal/lang"
	"github.com/ldesfontaine/opencloud/internal/settings"
	"github.com/ldesfontaine/opencloud/internal/store/storetest"
)

// L'ancien fichier, tel qu'une installation d'avant la base l'a écrit.
const legacyFile = `language = "en"
status_title = "Statut"
status_announcement = "Maintenance jeudi"
status_language = "fr"
disk_attention_percent = 70
disk_danger_percent = 85
mail_host = "smtp.example.org"
mail_port = 587
mail_tls = "starttls"
mail_username = "alerts"
mail_password = "s3cret"
mail_from = "oc@example.org"
lifecycle_disabled = true
`

func assertNoLegacyFile(t *testing.T, root *os.Root) {
	t.Helper()
	if _, err := root.Stat("settings.toml"); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("settings.toml still there: %v", err)
	}
}

func TestImportLegacyFile_MovesEveryKeyIntoTheDatabase_ThenRemovesTheFile(t *testing.T) {
	root, db := storetest.OpenWithStateDir(t)
	if err := root.WriteFile("settings.toml", []byte(legacyFile), 0o600); err != nil {
		t.Fatal(err)
	}
	outcome, err := settings.ImportLegacyFile(context.Background(), root, db)
	if err != nil || outcome != settings.Imported {
		t.Fatalf("outcome %v, %v", outcome, err)
	}
	assertNoLegacyFile(t, root)

	current, err := db.LoadSettings(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := settings.Settings{
		Language: lang.English, StatusTitle: "Statut", StatusAnnouncement: "Maintenance jeudi", StatusLanguage: lang.French,
		DiskAttentionPercent: 70, DiskDangerPercent: 85,
		MailHost: "smtp.example.org", MailPort: 587, MailTLS: "starttls", MailUsername: "alerts", MailPassword: "s3cret", MailFrom: "oc@example.org",
		LifecycleDisabled: true,
	}
	if current != want {
		t.Fatalf("got %+v, want %+v", current, want)
	}
}

func TestImportLegacyFile_NoFile_ChangesNothing(t *testing.T) {
	root, db := storetest.OpenWithStateDir(t)
	outcome, err := settings.ImportLegacyFile(context.Background(), root, db)
	if err != nil || outcome != settings.NoLegacyFile {
		t.Fatalf("outcome %v, %v", outcome, err)
	}
	current, err := db.LoadSettings(context.Background())
	if err != nil || current != (settings.Settings{}) {
		t.Fatalf("got %+v, %v", current, err)
	}
}

// Un démarrage interrompu entre la reprise et l'effacement : au suivant, la
// base l'emporte et le fichier part, sans écraser ce qui a été réglé depuis.
func TestImportLegacyFile_DatabaseAlreadySet_Wins(t *testing.T) {
	root, db := storetest.OpenWithStateDir(t)
	ctx := context.Background()
	if _, err := db.UpdateSettings(ctx, func(current *settings.Settings) { current.Language = lang.French }); err != nil {
		t.Fatal(err)
	}
	if err := root.WriteFile("settings.toml", []byte(legacyFile), 0o600); err != nil {
		t.Fatal(err)
	}
	outcome, err := settings.ImportLegacyFile(ctx, root, db)
	if err != nil || outcome != settings.AlreadyInDatabase {
		t.Fatalf("outcome %v, %v", outcome, err)
	}
	assertNoLegacyFile(t, root)
	current, err := db.LoadSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if current != (settings.Settings{Language: lang.French}) {
		t.Fatalf("got %+v", current)
	}
}

// Un fichier illisible arrête la reprise et reste en place : l'opérateur le
// corrige, rien n'est perdu.
func TestImportLegacyFile_BadFile_IsAnError_AndStays(t *testing.T) {
	root, db := storetest.OpenWithStateDir(t)
	if err := root.WriteFile("settings.toml", []byte("language = \n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := settings.ImportLegacyFile(context.Background(), root, db); err == nil {
		t.Fatal("want a parse error")
	}
	if _, err := root.Stat("settings.toml"); err != nil {
		t.Fatalf("the file must stay: %v", err)
	}
	current, err := db.LoadSettings(context.Background())
	if err != nil || current != (settings.Settings{}) {
		t.Fatalf("got %+v, %v", current, err)
	}
}

// Le temporaire d'une écriture interrompue de l'ancien code porte aussi le
// mot de passe du relais : il part avec le fichier.
func TestImportLegacyFile_RemovesALeftoverTempFile(t *testing.T) {
	root, db := storetest.OpenWithStateDir(t)
	for _, name := range []string{"settings.toml", "settings.toml.tmp"} {
		if err := root.WriteFile(name, []byte(legacyFile), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := settings.ImportLegacyFile(context.Background(), root, db); err != nil {
		t.Fatal(err)
	}
	assertNoLegacyFile(t, root)
	if _, err := root.Stat("settings.toml.tmp"); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("settings.toml.tmp still there: %v", err)
	}
}

// Un fichier qui ne s'efface pas n'arrête rien : les réglages sont en base,
// l'erreur le dit, et le démarrage suivant retentera.
func TestImportLegacyFile_FileThatCannotBeRemoved_KeepsTheImport(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignore les droits du dossier")
	}
	root, db := storetest.OpenWithStateDir(t)
	if err := root.WriteFile("settings.toml", []byte(legacyFile), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(root.Name(), 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(root.Name(), 0o700) })
	outcome, err := settings.ImportLegacyFile(context.Background(), root, db)
	if !errors.Is(err, settings.ErrLegacyFileKept) || outcome != settings.Imported {
		t.Fatalf("outcome %v, %v", outcome, err)
	}
	current, err := db.LoadSettings(context.Background())
	if err != nil || current.MailPassword != "s3cret" {
		t.Fatalf("got %+v, %v", current, err)
	}
}
