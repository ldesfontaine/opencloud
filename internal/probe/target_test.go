package probe_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/ldesfontaine/opencloud/internal/machine"
	"github.com/ldesfontaine/opencloud/internal/probe"
	"github.com/ldesfontaine/opencloud/internal/store/storetest"
)

func TestRedactTarget_HidesThePasswordOnly(t *testing.T) {
	cases := map[string]string{
		"https://u:secret@h.example.org/path?q=1": "https://u:…@h.example.org/path?q=1",
		"https://u:secret@h/":                     "https://u:…@h/",
		"https://h.example.org/status":            "https://h.example.org/status",
		"https://u@h.example.org/":                "https://u@h.example.org/",
		"mail.example.org:993":                    "mail.example.org:993",
	}
	for target, want := range cases {
		if got := probe.RedactTarget(target); got != want {
			t.Errorf("RedactTarget(%q) = %q, want %q", target, got, want)
		}
	}
	if probe.TargetPassword("https://u:secret@h/") != "secret" || probe.TargetPassword("https://h/") != "" {
		t.Error("TargetPassword reads the password")
	}
}

// Aucune ligne de journal des sondes ne porte le mot de passe d'une cible :
// celle d'une sonde d'avant le refus, mise en pause au démarrage, modifiée,
// supprimée.
func TestServiceLogs_NeverCarryATargetPassword(t *testing.T) {
	var logs bytes.Buffer
	fixture := newBenchLogging(t, slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})))
	legacy := storetest.InsertProbe(t, fixture.db, "00000000000000a1", probe.Definition{
		Name: "feed", Kind: probe.KindHTTP, Target: "https://u:secret@h.example.org/", MachineID: machine.LocalID,
	}, testNow)
	if err := fixture.service.PauseTargetsWithCredentials(context.Background()); err != nil {
		t.Fatal(err)
	}
	definition := definitionOf(legacy)
	definition.Target = "https://u:other-secret@h.example.org/status"
	if _, err := fixture.service.Update(context.Background(), legacy.ID, definition); !errors.Is(err, probe.ErrTargetCredentials) {
		t.Fatalf("attendu %v, obtenu %v", probe.ErrTargetCredentials, err)
	}
	definition.Target = "https://h.example.org/status"
	updated := fixture.update(t, legacy.ID, definition)
	if err := fixture.service.Delete(context.Background(), updated.ID); err != nil {
		t.Fatal(err)
	}
	text := logs.String()
	if !strings.Contains(text, "its target carries credentials") || !strings.Contains(text, "name=feed") || !strings.Contains(text, "u:…@h.example.org") {
		t.Fatalf("the paused probe must be logged, target masked:\n%s", text)
	}
	if strings.Contains(text, "secret") {
		t.Fatalf("a log line carries the password:\n%s", text)
	}
}
