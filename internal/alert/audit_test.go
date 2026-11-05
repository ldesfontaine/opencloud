package alert_test

import (
	"context"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/ldesfontaine/opencloud/internal/alert"
	"github.com/ldesfontaine/opencloud/internal/audit"
	"github.com/ldesfontaine/opencloud/internal/machine"
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

// Régler un seuil, global ou d'une machine, puis retirer la surcharge, va au
// journal sous l'acteur du contexte, avec la ressource et les valeurs ; un
// refus n'y laisse rien.
func TestThresholdGestures_AreJournaled_AndARefusalLeavesNothing(t *testing.T) {
	f := newFixture(t)
	f.SetAuditor(audit.New(f.db, slog.New(slog.NewTextHandler(os.Stderr, nil))))
	ctx := audit.WithActor(context.Background(), audit.Operator("lucas"))
	if err := f.SetRule(ctx, "", alert.Rule{Resource: alert.ResourceCPU, Attention: 80, Danger: 90, Window: 10 * time.Minute}); err != nil {
		t.Fatal(err)
	}
	if err := f.SetRule(ctx, machine.LocalID, alert.Rule{Resource: alert.ResourceDisk, Attention: 90, Danger: 98}); err != nil {
		t.Fatal(err)
	}
	if err := f.ClearRule(ctx, machine.LocalID, alert.ResourceDisk); err != nil {
		t.Fatal(err)
	}
	if err := f.SetRule(ctx, "", alert.Rule{Resource: alert.ResourceDisk, Attention: 20, Danger: 98}); err == nil {
		t.Fatal("a refused rule must be refused")
	}
	events, err := f.db.ListAuditEvents(context.Background(), audit.Query{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 3 {
		t.Fatalf("three gestures, one refusal: %+v", events)
	}
	// Du plus récent au plus ancien : retrait, surcharge, réglage global.
	cleared, own, global := events[0], events[1], events[2]
	for _, event := range events {
		if event.Action != audit.SettingsUpdate || event.Actor != audit.Operator("lucas") || event.ObjectID != "thresholds" {
			t.Errorf("event %+v", event)
		}
	}
	if global.Details["resource"] != "cpu" || global.Details["attention_percent"] != "80" || global.Details["danger_percent"] != "90" ||
		global.Details["window_minutes"] != "10" || global.Details["machine_id"] != "" {
		t.Errorf("global: %+v", global.Details)
	}
	if own.Details["resource"] != "disk" || own.Details["machine_id"] != machine.LocalID || own.Details["machine_name"] != "opencloud" || own.Details["attention_percent"] != "90" {
		t.Errorf("override: %+v", own.Details)
	}
	if cleared.Details["cleared"] != "true" || cleared.Details["machine_id"] != machine.LocalID || cleared.Details["resource"] != "disk" {
		t.Errorf("cleared: %+v", cleared.Details)
	}
}
