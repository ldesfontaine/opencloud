package alert_test

import (
	"context"
	"log/slog"
	"os"
	"strings"
	"testing"

	"github.com/ldesfontaine/opencloud/internal/alert"
	"github.com/ldesfontaine/opencloud/internal/audit"
)

// Créer puis modifier un canal va au journal sous l'acteur du contexte :
// l'URL et le secret n'y sont jamais, seul leur changement l'est.
func TestChannelGestures_AreJournaledWithoutTheURLNorTheSecret(t *testing.T) {
	f := newFixture(t)
	f.SetAuditor(audit.New(f.db, slog.New(slog.NewTextHandler(os.Stderr, nil))))
	ctx := audit.WithActor(context.Background(), audit.Operator("lucas"))
	definition := alert.ChannelDefinition{
		Name: "salon", URL: "https://discord.com/api/webhooks/1/x", Format: alert.FormatDiscord, Secret: "s3cret",
		MinSeverity: alert.SeverityAttention, Enabled: true,
	}
	created, err := f.CreateChannel(ctx, definition)
	if err != nil {
		t.Fatal(err)
	}
	definition.URL = "https://discord.com/api/webhooks/2/y"
	if _, err := f.UpdateChannel(ctx, created.ID, definition); err != nil {
		t.Fatal(err)
	}
	if err := f.DeleteChannel(ctx, created.ID); err != nil {
		t.Fatal(err)
	}
	events, err := f.db.ListAuditEvents(context.Background(), audit.Query{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 3 {
		t.Fatalf("events: %+v", events)
	}
	for _, event := range events {
		if event.Actor != audit.Operator("lucas") || event.ObjectName != "salon" {
			t.Errorf("%s: %+v", event.Action, event)
		}
		for name, value := range event.Details {
			if strings.Contains(value, "webhooks") || strings.Contains(value, "s3cret") || name == "url" || name == "secret" {
				t.Errorf("%s leaks %s=%s", event.Action, name, value)
			}
		}
	}
	updated := events[1]
	if updated.Action != audit.ChannelUpdate || updated.Details["url_changed"] != "true" || updated.Details["secret_changed"] != "true" {
		t.Fatalf("update: %+v", updated)
	}
}
