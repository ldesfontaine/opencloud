package settings

import (
	"os"
	"testing"

	"github.com/ldesfontaine/opencloud/internal/lang"
)

func newStore(t *testing.T) *Store {
	t.Helper()
	root, err := os.OpenRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { root.Close() })
	return New(root)
}

func TestLoad_MissingFile_GivesEmptySettings(t *testing.T) {
	settings, err := newStore(t).Load()
	if err != nil {
		t.Fatal(err)
	}
	if settings != (Settings{}) {
		t.Fatalf("got %+v", settings)
	}
}

func TestSave_ThenLoad_RoundTrips(t *testing.T) {
	store := newStore(t)
	if err := store.Save(Settings{Language: lang.English}); err != nil {
		t.Fatal(err)
	}
	settings, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if settings.Language != lang.English {
		t.Fatalf("got %+v", settings)
	}
}

func TestLoad_BadFile_IsAnError(t *testing.T) {
	store := newStore(t)
	if err := store.root.WriteFile(fileName, []byte("language = \n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Load(); err == nil {
		t.Fatal("want a parse error")
	}
}
