package settings_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"

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
	if saved := load(t, db); saved != (settings.Settings{}) {
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
	if saved := load(t, db); saved != (settings.Settings{}) {
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

func TestSetDiskThresholds_Invalid_IsRefused_AndTheDefaultsStay(t *testing.T) {
	service, _ := newService(t)
	ctx := context.Background()
	err := service.SetDiskThresholds(ctx, alert.DiskThresholds{Attention: 90, Danger: 80})
	if !errors.Is(err, alert.ErrDiskThresholdsInvalid) {
		t.Fatalf("got %v", err)
	}
	if thresholds, err := service.DiskThresholds(ctx); err != nil || thresholds != alert.DefaultDiskThresholds() {
		t.Fatalf("defaults: %+v, %v", thresholds, err)
	}
	set := alert.DiskThresholds{Attention: 70, Danger: 85}
	if err := service.SetDiskThresholds(ctx, set); err != nil {
		t.Fatal(err)
	}
	if thresholds := service.CurrentDiskThresholds(); thresholds != set {
		t.Fatalf("set: %+v", thresholds)
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
	if err := service.SetDiskThresholds(ctx, alert.DiskThresholds{Attention: 70, Danger: 85}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.SetMailRelay(ctx, settings.MailRelayChange{Relay: mail.Relay{Host: "smtp.example.org", TLS: mail.TLSStartTLS, From: "oc@example.org"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.SetLifecycleEnabled(ctx, false); err != nil {
		t.Fatal(err)
	}
	want := settings.Settings{
		Language:             lang.English,
		DiskAttentionPercent: 70, DiskDangerPercent: 85,
		MailHost: "smtp.example.org", MailPort: 587, MailTLS: "starttls", MailFrom: "oc@example.org",
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
	if thresholds := service.CurrentDiskThresholds(); thresholds != alert.DefaultDiskThresholds() {
		t.Errorf("disks: %+v", thresholds)
	}
	if service.CurrentLifecycleEnabled() {
		t.Error("lifecycle must be off when the setting cannot be read")
	}
}
