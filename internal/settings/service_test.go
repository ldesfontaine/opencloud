package settings_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/ldesfontaine/opencloud/internal/alert"
	"github.com/ldesfontaine/opencloud/internal/lang"
	"github.com/ldesfontaine/opencloud/internal/mail"
	"github.com/ldesfontaine/opencloud/internal/refusal"
	"github.com/ldesfontaine/opencloud/internal/settings"
	"github.com/ldesfontaine/opencloud/internal/store"
	"github.com/ldesfontaine/opencloud/internal/store/storetest"
)

func newService(t *testing.T) (*settings.Service, *store.DB) {
	t.Helper()
	db := storetest.Open(t)
	return settings.New(db, slog.New(slog.NewTextHandler(io.Discard, nil))), db
}

func load(t *testing.T, db *store.DB) settings.Settings {
	t.Helper()
	current, err := db.LoadSettings(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return current
}

func TestSetMailRelay_NoPort_FollowsTheTLSMode(t *testing.T) {
	service, _ := newService(t)
	ctx := context.Background()
	for mode, want := range map[mail.TLSMode]int{mail.TLSImplicit: 465, mail.TLSStartTLS: 587, mail.TLSNone: 587} {
		relay, err := service.SetMailRelay(ctx, settings.MailRelayChange{Relay: mail.Relay{
			Host: " 10.0.0.25 ", TLS: mode, From: " oc@example.org ",
		}})
		if err != nil {
			t.Fatalf("%s: %v", mode, err)
		}
		if relay.Port != want || relay.Host != "10.0.0.25" || relay.From != "oc@example.org" {
			t.Fatalf("%s: got %+v", mode, relay)
		}
	}
}

// Le mot de passe gardé est celui de la base au moment d'écrire, pas celui
// lu plus tôt : un autre processus a pu le changer entre-temps.
func TestSetMailRelay_EmptyPassword_KeepsTheOneInTheDatabase(t *testing.T) {
	service, db := newService(t)
	ctx := context.Background()
	typed := mail.Relay{Host: "smtp.example.org", Port: 587, TLS: mail.TLSStartTLS, Username: "alerts", Password: "s3cret", From: "oc@example.org"}
	if _, err := service.SetMailRelay(ctx, settings.MailRelayChange{Relay: typed}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.UpdateSettings(ctx, func(current *settings.Settings) { current.MailPassword = "rotated-elsewhere" }); err != nil {
		t.Fatal(err)
	}
	typed.Password = ""
	relay, err := service.SetMailRelay(ctx, settings.MailRelayChange{Relay: typed})
	if err != nil || relay.Password != "rotated-elsewhere" {
		t.Fatalf("kept: %+v, %v", relay, err)
	}
	if relay, err := service.SetMailRelay(ctx, settings.MailRelayChange{Relay: typed, ClearPassword: true}); err != nil || relay.Password != "" {
		t.Fatalf("cleared: %+v, %v", relay, err)
	}
	if saved := load(t, db); saved.MailPassword != "" || saved.MailHost != "smtp.example.org" {
		t.Fatalf("saved: %+v", saved)
	}
}

func TestSetMailRelay_Invalid_IsRefusedAndWritesNothing(t *testing.T) {
	service, db := newService(t)
	_, err := service.SetMailRelay(context.Background(), settings.MailRelayChange{Relay: mail.Relay{Host: "smtp.example.org", Port: 70000, TLS: mail.TLSStartTLS}})
	if !errors.Is(err, mail.ErrPortInvalid) {
		t.Fatalf("got %v", err)
	}
	if key, ok := refusal.KeyOf(err); !ok || key != "settings.mail_port_invalid" {
		t.Fatalf("key %q %v", key, ok)
	}
	if saved := load(t, db); saved != settings.Defaults() {
		t.Fatalf("saved: %+v", saved)
	}
}

// Une saisie refusée laisse le relais en place tel quel, mot de passe
// compris.
func TestSetMailRelay_InvalidOverAnExistingRelay_LeavesTheRowAsItWas(t *testing.T) {
	service, db := newService(t)
	ctx := context.Background()
	typed := mail.Relay{Host: "smtp.example.org", TLS: mail.TLSStartTLS, Username: "alerts", Password: "s3cret", From: "oc@example.org"}
	if _, err := service.SetMailRelay(ctx, settings.MailRelayChange{Relay: typed}); err != nil {
		t.Fatal(err)
	}
	before := load(t, db)
	_, err := service.SetMailRelay(ctx, settings.MailRelayChange{Relay: mail.Relay{Host: "smtp.example.org", TLS: "ssl", From: "oc@example.org"}, ClearPassword: true})
	if !errors.Is(err, mail.ErrTLSModeInvalid) {
		t.Fatalf("got %v", err)
	}
	if after := load(t, db); after != before {
		t.Fatalf("got %+v, want %+v", after, before)
	}
}

// Un hôte vide retire le relais sans rien valider : tout s'efface, le mot
// de passe compris.
func TestSetMailRelay_NoHost_ClearsEverything(t *testing.T) {
	service, db := newService(t)
	ctx := context.Background()
	typed := mail.Relay{Host: "smtp.example.org", TLS: mail.TLSStartTLS, Password: "s3cret", From: "oc@example.org"}
	if _, err := service.SetMailRelay(ctx, settings.MailRelayChange{Relay: typed}); err != nil {
		t.Fatal(err)
	}
	relay, err := service.SetMailRelay(ctx, settings.MailRelayChange{Relay: mail.Relay{Host: "  ", Port: 70000}})
	if err != nil || relay.Configured() {
		t.Fatalf("got %+v, %v", relay, err)
	}
	if saved := load(t, db); saved.MailHost != "" || saved.MailPassword != "" || saved.MailFrom != "" || saved.MailPort != 0 {
		t.Fatalf("saved: %+v", saved)
	}
}

func TestSetLifecycleEnabled_TellsWhenItIsTurnedOn(t *testing.T) {
	service, _ := newService(t)
	ctx := context.Background()
	steps := []struct {
		enabled, isTurnedOn bool
	}{
		// Une installation neuve l'a déjà : rien n'est rallumé.
		{enabled: true, isTurnedOn: false},
		{enabled: false, isTurnedOn: false},
		{enabled: false, isTurnedOn: false},
		{enabled: true, isTurnedOn: true},
		{enabled: true, isTurnedOn: false},
	}
	for index, step := range steps {
		isTurnedOn, err := service.SetLifecycleEnabled(ctx, step.enabled)
		if err != nil || isTurnedOn != step.isTurnedOn {
			t.Fatalf("step %d: turned on %v, %v", index, isTurnedOn, err)
		}
		enabled, err := service.LifecycleEnabled(ctx)
		if err != nil || enabled != step.enabled {
			t.Fatalf("step %d: enabled %v, %v", index, enabled, err)
		}
	}
}

func TestSetLanguage_UnknownCode_IsRefused(t *testing.T) {
	service, db := newService(t)
	ctx := context.Background()
	err := service.SetLanguage(ctx, "de")
	if !errors.Is(err, settings.ErrLanguageUnknown) {
		t.Fatalf("got %v", err)
	}
	if key, ok := refusal.KeyOf(err); !ok || key != "error.unknown_language" {
		t.Fatalf("key %q %v", key, ok)
	}
	if saved := load(t, db); saved != settings.Defaults() {
		t.Fatalf("saved: %+v", saved)
	}
}

func TestLanguage_NothingChosen_IsTheDefault_ThenTheChoice(t *testing.T) {
	service, _ := newService(t)
	ctx := context.Background()
	if code, err := service.Language(ctx); err != nil || code != lang.Default {
		t.Fatalf("default: %q, %v", code, err)
	}
	if err := service.SetLanguage(ctx, lang.English); err != nil {
		t.Fatal(err)
	}
	if code, err := service.Language(ctx); err != nil || code != lang.English {
		t.Fatalf("chosen: %q, %v", code, err)
	}
	if code := service.CurrentLanguage(); code != lang.English {
		t.Fatalf("current: %q", code)
	}
}

func TestSetDangerReminderHours_OutOfBounds_IsRefused_AndTheDefaultStays(t *testing.T) {
	service, _ := newService(t)
	ctx := context.Background()
	for _, hours := range []int{-1, alert.MaxReminderHours + 1} {
		if err := service.SetDangerReminderHours(ctx, hours); !errors.Is(err, settings.ErrReminderInvalid) {
			t.Fatalf("%d h: %v", hours, err)
		}
	}
	if hours, err := service.DangerReminderHours(ctx); err != nil || hours != alert.DefaultReminderHours {
		t.Fatalf("default: %d, %v", hours, err)
	}
	if err := service.SetDangerReminderHours(ctx, 6); err != nil {
		t.Fatal(err)
	}
	if delay := service.CurrentDangerReminder(); delay != 6*time.Hour {
		t.Fatalf("set: %v", delay)
	}
	// Zéro coupe le rappel : ce n'est pas « rien de réglé ».
	if err := service.SetDangerReminderHours(ctx, 0); err != nil {
		t.Fatal(err)
	}
	if delay := service.CurrentDangerReminder(); delay != 0 {
		t.Fatalf("turned off: %v", delay)
	}
}

// Chaque geste ne touche que ses clés : la langue, les seuils, le relais et
// la fin de support écrits l'un après l'autre survivent tous.
func TestGestures_KeepTheOtherKeys(t *testing.T) {
	service, db := newService(t)
	ctx := context.Background()
	if err := service.SetLanguage(ctx, lang.English); err != nil {
		t.Fatal(err)
	}
	if err := service.SetDangerReminderHours(ctx, 24); err != nil {
		t.Fatal(err)
	}
	if _, err := service.SetMailRelay(ctx, settings.MailRelayChange{Relay: mail.Relay{Host: "smtp.example.org", TLS: mail.TLSStartTLS, From: "oc@example.org"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.SetLifecycleEnabled(ctx, false); err != nil {
		t.Fatal(err)
	}
	want := settings.Settings{
		Language:            lang.English,
		DangerReminderHours: 24,
		MailHost:            "smtp.example.org", MailPort: 587, MailTLS: "starttls", MailFrom: "oc@example.org",
		LifecycleDisabled: true,
	}
	if saved := load(t, db); saved != want {
		t.Fatalf("got %+v, want %+v", saved, want)
	}
}

// Une base qui ne répond pas : les lectures au moment de servir rendent
// leur valeur de repli.
type failingStore struct{}

var errDatabaseDown = errors.New("database down")

func (failingStore) LoadSettings(context.Context) (settings.Settings, error) {
	return settings.Settings{}, errDatabaseDown
}

func (failingStore) UpdateSettings(context.Context, func(*settings.Settings)) (settings.Settings, error) {
	return settings.Settings{}, errDatabaseDown
}

func TestCurrentReads_DatabaseDown_FallBack(t *testing.T) {
	service := settings.New(failingStore{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if code := service.CurrentLanguage(); code != lang.Default {
		t.Errorf("language: %q", code)
	}
	if relay := service.CurrentMailRelay(); relay != (mail.Relay{}) {
		t.Errorf("relay: %+v", relay)
	}
	if delay := service.CurrentDangerReminder(); delay != alert.DefaultReminderHours*time.Hour {
		t.Errorf("reminder: %v", delay)
	}
	if service.CurrentLifecycleEnabled() {
		t.Error("lifecycle must be off when the setting cannot be read")
	}
}
