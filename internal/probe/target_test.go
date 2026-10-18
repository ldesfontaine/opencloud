package probe_test

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"

	"github.com/ldesfontaine/opencloud/internal/probe"
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
// création, modification, suppression, pause.
func TestServiceLogs_NeverCarryATargetPassword(t *testing.T) {
	var logs bytes.Buffer
	fixture := newBenchLogging(t, slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})))
	created := fixture.create(t, "feed", "https://u:secret@h.example.org/")
	definition := definitionOf(created)
	definition.Target = "https://u:other-secret@h.example.org/status"
	updated := fixture.update(t, created.ID, definition)
	if err := fixture.service.Pause(context.Background(), updated.ID); err != nil {
		t.Fatal(err)
	}
	if err := fixture.service.Delete(context.Background(), updated.ID); err != nil {
		t.Fatal(err)
	}
	text := logs.String()
	if !strings.Contains(text, "probe created") || !strings.Contains(text, "u:…@h.example.org") {
		t.Fatalf("the target must still be logged, masked:\n%s", text)
	}
	if strings.Contains(text, "secret") {
		t.Fatalf("a log line carries the password:\n%s", text)
	}
}
