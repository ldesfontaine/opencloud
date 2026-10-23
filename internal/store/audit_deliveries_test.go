package store

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/ldesfontaine/opencloud/internal/alert"
	"github.com/ldesfontaine/opencloud/internal/audit"
)

func insertAuditChannel(t *testing.T, db *DB, name string, enabled, receivesAudit bool) int64 {
	t.Helper()
	id, err := db.InsertChannel(context.Background(), alert.Channel{
		Name: name, URL: "https://hooks.example.org/" + name, Format: alert.FormatJSON,
		MinSeverity: alert.SeverityAttention, ReceivesAudit: receivesAudit, Enabled: enabled,
		CreatedAt: time.Unix(1_800_000_000, 0),
	})
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func TestInsertAuditEvent_ReservesOneDeliveryPerReceivingChannel(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	receiving := insertAuditChannel(t, db, "recoit", true, true)
	insertAuditChannel(t, db, "desactive", false, true)
	insertAuditChannel(t, db, "alertes-seules", true, false)

	at := time.Date(2026, 10, 21, 9, 0, 0, 0, time.UTC)
	id, err := db.InsertAuditEvent(ctx, auditEvent(audit.ObjectProbe, audit.ProbePause, "p1", at))
	if err != nil || id == 0 {
		t.Fatalf("insert: %d, %v", id, err)
	}
	deliveries, err := db.ListAuditDeliveries(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if len(deliveries) != 1 {
		t.Fatalf("%d livraisons, 1 attendue : %+v", len(deliveries), deliveries)
	}
	got := deliveries[0]
	if got.ChannelID != receiving || got.AuditID != id || got.Event != alert.EventAudit ||
		got.Status != alert.DeliveryPending || got.Attempts != 0 || !got.CreatedAt.Equal(at) || !got.NextAttemptAt.IsZero() {
		t.Errorf("livraison réservée : %+v", got)
	}
	pending, err := db.ListPendingAuditDeliveries(ctx)
	if err != nil || len(pending) != 1 {
		t.Fatalf("en attente : %v, %v", pending, err)
	}
}

func TestPurgeAuditEvents_TakesTheirDeliveriesAlong(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	insertAuditChannel(t, db, "recoit", true, true)
	now := time.Date(2026, 10, 21, 9, 0, 0, 0, time.UTC)
	old, err := db.InsertAuditEvent(ctx, auditEvent(audit.ObjectProbe, audit.ProbeDelete, "old", now.Add(-audit.Retention-time.Hour)))
	if err != nil {
		t.Fatal(err)
	}
	kept, err := db.InsertAuditEvent(ctx, auditEvent(audit.ObjectProbe, audit.ProbeDelete, "kept", now.Add(-time.Hour)))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.PurgeAuditEvents(ctx, now.Add(-audit.Retention)); err != nil {
		t.Fatal(err)
	}
	if gone, _ := db.ListAuditDeliveries(ctx, old); len(gone) != 0 {
		t.Errorf("la livraison de l'événement purgé reste : %+v", gone)
	}
	if left, _ := db.ListAuditDeliveries(ctx, kept); len(left) != 1 {
		t.Errorf("la livraison de l'événement gardé a disparu : %+v", left)
	}
}

func TestDeleteChannel_TakesItsAuditDeliveriesAlong(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	channel := insertAuditChannel(t, db, "recoit", true, true)
	id, err := db.InsertAuditEvent(ctx, auditEvent(audit.ObjectProbe, audit.ProbePause, "p1", time.Unix(1_800_000_000, 0)))
	if err != nil {
		t.Fatal(err)
	}
	if err := db.DeleteChannel(ctx, channel); err != nil {
		t.Fatal(err)
	}
	if left, _ := db.ListAuditDeliveries(ctx, id); len(left) != 0 {
		t.Errorf("la livraison du canal supprimé reste : %+v", left)
	}
	// L'événement, lui, reste : le journal ne dépend pas de ses canaux.
	if _, err := db.GetAuditEvent(ctx, id); err != nil {
		t.Errorf("l'événement a disparu avec le canal : %v", err)
	}
}

func TestListDueAuditDeliveries_OnlyFailedAndDue(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	for _, name := range []string{"a", "b", "c", "d"} {
		insertAuditChannel(t, db, name, true, true)
	}
	now := time.Unix(1_800_000_000, 0)
	id, err := db.InsertAuditEvent(ctx, auditEvent(audit.ObjectProbe, audit.ProbePause, "p1", now))
	if err != nil {
		t.Fatal(err)
	}
	deliveries, err := db.ListAuditDeliveries(ctx, id)
	if err != nil || len(deliveries) != 4 {
		t.Fatalf("livraisons : %d, %v", len(deliveries), err)
	}
	// échue ; pas encore échue ; abandonnée (échec sans prochain essai) ; livrée.
	due, later, abandoned, delivered := deliveries[0], deliveries[1], deliveries[2], deliveries[3]
	due.Status, due.NextAttemptAt = alert.DeliveryFailed, now.Add(-time.Second)
	later.Status, later.NextAttemptAt = alert.DeliveryFailed, now.Add(time.Minute)
	abandoned.Status = alert.DeliveryFailed
	delivered.Status = alert.DeliveryDelivered
	for _, delivery := range []alert.Delivery{due, later, abandoned, delivered} {
		delivery.Attempts, delivery.Reason, delivery.Code, delivery.UpdatedAt = 1, alert.ReasonStatus, 500, now
		if err := db.UpdateAuditDelivery(ctx, delivery); err != nil {
			t.Fatal(err)
		}
	}
	got, err := db.ListDueAuditDeliveries(ctx, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != due.ID {
		t.Fatalf("échues : %+v", got)
	}
	if got[0].Reason != alert.ReasonStatus || got[0].Code != 500 || got[0].Attempts != 1 {
		t.Errorf("l'issue du dernier essai n'est pas relue : %+v", got[0])
	}
}

func TestUpdateAuditDelivery_UnknownIDIsNotFound(t *testing.T) {
	db := openTestDB(t)
	err := db.UpdateAuditDelivery(context.Background(), alert.Delivery{ID: 99, Status: alert.DeliveryDelivered})
	if !errors.Is(err, alert.ErrNotFound) {
		t.Errorf("erreur : %v", err)
	}
}

func TestGetAuditEvent_ReadsWhatWasWrittenAndReportsAMissingOne(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	at := time.Date(2026, 10, 21, 9, 0, 0, 0, time.UTC)
	id, err := db.InsertAuditEvent(ctx, auditEvent(audit.ObjectProbe, audit.ProbePause, "p1", at))
	if err != nil {
		t.Fatal(err)
	}
	event, err := db.GetAuditEvent(ctx, id)
	if err != nil || event.ID != id || event.Action != audit.ProbePause || event.Details["k"] != "v" {
		t.Fatalf("relu : %+v, %v", event, err)
	}
	if _, err := db.GetAuditEvent(ctx, id+1); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("événement absent : %v", err)
	}
}

func TestCountAuditForwarding_CountsReceivingChannelsAndFailedDeliveries(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	insertAuditChannel(t, db, "recoit", true, true)
	insertAuditChannel(t, db, "desactive", false, true)
	insertAuditChannel(t, db, "alertes-seules", true, false)
	channels, failing, err := db.CountAuditForwarding(ctx)
	if err != nil || channels != 1 || failing != 0 {
		t.Fatalf("avant : %d %d %v", channels, failing, err)
	}
	id, err := db.InsertAuditEvent(ctx, auditEvent(audit.ObjectProbe, audit.ProbePause, "p1", time.Unix(1_800_000_000, 0)))
	if err != nil {
		t.Fatal(err)
	}
	deliveries, _ := db.ListAuditDeliveries(ctx, id)
	deliveries[0].Status = alert.DeliveryFailed
	deliveries[0].UpdatedAt = time.Unix(1_800_000_000, 0)
	deliveries[0].NextAttemptAt = time.Unix(1_800_000_005, 0)
	if err := db.UpdateAuditDelivery(ctx, deliveries[0]); err != nil {
		t.Fatal(err)
	}
	if _, failing, err = db.CountAuditForwarding(ctx); err != nil || failing != 1 {
		t.Fatalf("après : %d %v", failing, err)
	}
	// Abandonnée (plus d'essai suivant), elle ne compte plus : l'avertissement
	// ne reste pas jusqu'à la purge de l'événement.
	deliveries[0].NextAttemptAt = time.Time{}
	if err := db.UpdateAuditDelivery(ctx, deliveries[0]); err != nil {
		t.Fatal(err)
	}
	if _, failing, err = db.CountAuditForwarding(ctx); err != nil || failing != 0 {
		t.Fatalf("abandonnée : %d %v", failing, err)
	}
}

func TestChannel_ReceivesAuditRoundTrips(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	id := insertAuditChannel(t, db, "recoit", true, true)
	channel, err := db.GetChannel(ctx, id)
	if err != nil || !channel.ReceivesAudit {
		t.Fatalf("relu : %+v, %v", channel, err)
	}
	channel.ReceivesAudit = false
	if err := db.UpdateChannel(ctx, channel); err != nil {
		t.Fatal(err)
	}
	if channel, _ = db.GetChannel(ctx, id); channel.ReceivesAudit {
		t.Error("receives_audit n'a pas été réécrit")
	}
}
