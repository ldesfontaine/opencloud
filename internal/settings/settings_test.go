package settings

import (
	"fmt"
	"os"
	"sync"
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

func TestUpdate_ThenLoad_RoundTrips(t *testing.T) {
	store := newStore(t)
	if _, err := store.Update(func(current *Settings) { current.Language = lang.English }); err != nil {
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

func TestUpdate_KeepsTheKeysItDoesNotTouch(t *testing.T) {
	store := newStore(t)
	if _, err := store.Update(func(current *Settings) { current.StatusTitle = "Statut" }); err != nil {
		t.Fatal(err)
	}
	written, err := store.Update(func(current *Settings) { current.Language = lang.English })
	if err != nil {
		t.Fatal(err)
	}
	if written.StatusTitle != "Statut" || written.Language != lang.English {
		t.Fatalf("got %+v", written)
	}
}

// Le scénario de la langue de l'interface et de la page de statut changées
// en même temps : chacun réécrit le fichier entier, aucun ne perd l'autre.
func TestUpdate_TwoConcurrentWriters_BothValuesSurvive(t *testing.T) {
	store := newStore(t)
	const rounds = 50
	var writers sync.WaitGroup
	errs := make(chan error, 2*rounds)
	writers.Go(func() {
		for round := range rounds {
			title := fmt.Sprintf("titre %d", round)
			_, err := store.Update(func(current *Settings) { current.StatusTitle = title })
			errs <- err
		}
	})
	writers.Go(func() {
		for round := range rounds {
			code := lang.French
			if round%2 == 1 {
				code = lang.English
			}
			_, err := store.Update(func(current *Settings) { current.Language = code })
			errs <- err
		}
	})
	writers.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}

	settings, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	want := Settings{Language: lang.English, StatusTitle: fmt.Sprintf("titre %d", rounds-1)}
	if settings != want {
		t.Fatalf("got %+v, want %+v", settings, want)
	}
}
