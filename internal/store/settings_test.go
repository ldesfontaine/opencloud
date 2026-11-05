package store

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/ldesfontaine/opencloud/internal/lang"
	"github.com/ldesfontaine/opencloud/internal/settings"
)

func TestLoadSettings_FreshDatabase_GivesEmptySettings(t *testing.T) {
	current, err := openTestDB(t).LoadSettings(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if current != settings.Defaults() {
		t.Fatalf("got %+v", current)
	}
}

func TestUpdateSettings_ThenLoad_RoundTripsEveryKey(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	want := settings.Settings{
		Language:            lang.English,
		DangerReminderHours: 24,
		MailHost:            "smtp.example.org", MailPort: 465, MailTLS: "tls", MailUsername: "alerts", MailPassword: "s3cret", MailFrom: "oc@example.org",
		LifecycleDisabled: true,
	}
	written, err := db.UpdateSettings(ctx, func(current *settings.Settings) { *current = want })
	if err != nil {
		t.Fatal(err)
	}
	if written != want {
		t.Fatalf("written %+v, want %+v", written, want)
	}
	loaded, err := db.LoadSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if loaded != want {
		t.Fatalf("loaded %+v, want %+v", loaded, want)
	}
}

// La fin de support est activée sur une base neuve, et reste coupée une fois
// que l'opérateur l'a coupée, même quand un autre réglage est écrit ensuite.
func TestUpdateSettings_LifecycleStaysOffUntilTurnedBackOn(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	fresh, err := db.LoadSettings(ctx)
	if err != nil || !fresh.LifecycleEnabled() {
		t.Fatalf("fresh: %+v, %v", fresh, err)
	}
	if _, err := db.UpdateSettings(ctx, func(current *settings.Settings) { current.LifecycleDisabled = true }); err != nil {
		t.Fatal(err)
	}
	if _, err := db.UpdateSettings(ctx, func(current *settings.Settings) { current.Language = lang.English }); err != nil {
		t.Fatal(err)
	}
	if still, err := db.LoadSettings(ctx); err != nil || still.LifecycleEnabled() {
		t.Fatalf("after another setting: %+v, %v", still, err)
	}
	if _, err := db.UpdateSettings(ctx, func(current *settings.Settings) { current.LifecycleDisabled = false }); err != nil {
		t.Fatal(err)
	}
	if on, err := db.LoadSettings(ctx); err != nil || !on.LifecycleEnabled() {
		t.Fatalf("turned back on: %+v, %v", on, err)
	}
}

func TestUpdateSettings_KeepsTheKeysItDoesNotTouch(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	if _, err := db.UpdateSettings(ctx, func(current *settings.Settings) { current.MailFrom = "oc@example.org" }); err != nil {
		t.Fatal(err)
	}
	written, err := db.UpdateSettings(ctx, func(current *settings.Settings) { current.Language = lang.English })
	if err != nil {
		t.Fatal(err)
	}
	if written.MailFrom != "oc@example.org" || written.Language != lang.English {
		t.Fatalf("got %+v", written)
	}
}

// La langue de l'interface et l'expéditeur du relais changés en même temps :
// chacun réécrit la ligne entière, aucun ne perd la clé de l'autre.
func TestUpdateSettings_TwoConcurrentWriters_BothValuesSurvive(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	const rounds = 50
	var writers sync.WaitGroup
	errs := make(chan error, 2*rounds)
	writers.Go(func() {
		for round := range rounds {
			from := fmt.Sprintf("oc%d@example.org", round)
			_, err := db.UpdateSettings(ctx, func(current *settings.Settings) { current.MailFrom = from })
			errs <- err
		}
	})
	writers.Go(func() {
		for round := range rounds {
			code := lang.French
			if round%2 == 1 {
				code = lang.English
			}
			_, err := db.UpdateSettings(ctx, func(current *settings.Settings) { current.Language = code })
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

	current, err := db.LoadSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	want := settings.Settings{Language: lang.English, DangerReminderHours: 12, MailFrom: fmt.Sprintf("oc%d@example.org", rounds-1)}
	if current != want {
		t.Fatalf("got %+v, want %+v", current, want)
	}
}

// Le rappel est activé sur une installation neuve : écrire un autre réglage
// en premier, quand la ligne n'existe pas encore, ne le coupe pas ; seul un
// zéro écrit à dessein le coupe.
func TestUpdateSettings_TheFirstWriteKeepsTheReminderOn_ZeroTurnsItOff(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	written, err := db.UpdateSettings(ctx, func(current *settings.Settings) { current.Language = lang.English })
	if err != nil || written.DangerReminderHours != 12 {
		t.Fatalf("first write: %+v, %v", written, err)
	}
	loaded, err := db.LoadSettings(ctx)
	if err != nil || loaded.DangerReminderHours != 12 {
		t.Fatalf("after the first write: %+v, %v", loaded, err)
	}
	if _, err := db.UpdateSettings(ctx, func(current *settings.Settings) { current.DangerReminderHours = 0 }); err != nil {
		t.Fatal(err)
	}
	if off, err := db.LoadSettings(ctx); err != nil || off.DangerReminder() != 0 {
		t.Fatalf("turned off: %+v, %v", off, err)
	}
}
