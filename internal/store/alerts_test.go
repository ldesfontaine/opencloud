package store

import (
	"context"
	"testing"
	"time"

	"github.com/ldesfontaine/opencloud/internal/alert"
	"github.com/ldesfontaine/opencloud/internal/audit"
)

func insertMute(t *testing.T, db *DB, kind alert.Kind, target alert.Target, startsAt, endsAt time.Time) int64 {
	t.Helper()
	mute := alert.Mute{Kind: kind, Target: target, StartsAt: startsAt, EndsAt: endsAt, CreatedBy: audit.Operator("lucas"), CreatedAt: startsAt}
	id, err := db.InsertMute(context.Background(), mute)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func TestPurgeMutes_ForgetsOnlyThoseEndedBeforeTheCutoff(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	now := time.Unix(1_800_000_000, 0)
	cutoff := now.Add(-alert.MuteRetention)
	insertMute(t, db, alert.KindDiskFull, alert.Target{}, cutoff.Add(-time.Hour), cutoff.Add(-time.Minute))
	insertMute(t, db, alert.KindProbeDown, alert.Target{}, cutoff.Add(-time.Hour), cutoff.Add(time.Minute))
	insertMute(t, db, alert.KindJobLate, alert.Target{}, now, now.Add(time.Hour))

	if err := db.PurgeMutes(ctx, cutoff); err != nil {
		t.Fatal(err)
	}
	kept, err := db.ListMutes(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(kept) != 2 || kept[0].Kind != alert.KindJobLate || kept[1].Kind != alert.KindProbeDown {
		t.Fatalf("kept %+v", kept)
	}
}

// Une fenêtre est ouverte de son début inclus à sa fin exclue ; sa cible se
// lit avec son nom, et son auteur tel que le journal d'audit le dit.
func TestListActiveMutes_ReadsTheWindowTheTargetAndTheAuthor(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	declaredOnLocal(t, db)
	now := time.Unix(1_800_000_000, 0)
	service := alert.Target{Kind: alert.TargetService, ID: "svc_0123456789abcdef"}
	id := insertMute(t, db, "", service, now, now.Add(time.Hour))
	insertMute(t, db, alert.KindDiskFull, alert.Target{}, now.Add(time.Hour), now.Add(2*time.Hour))

	if before, err := db.ListActiveMutes(ctx, now.Add(-time.Second)); err != nil || len(before) != 0 {
		t.Fatalf("before the windows: %+v %v", before, err)
	}
	if after, err := db.ListActiveMutes(ctx, now.Add(time.Hour)); err != nil || len(after) != 1 || after[0].Kind != alert.KindDiskFull {
		t.Fatalf("the first window has ended, the second begins: %+v %v", after, err)
	}
	active, err := db.ListActiveMutes(ctx, now)
	if err != nil || len(active) != 1 {
		t.Fatalf("active %+v %v", active, err)
	}
	found := active[0]
	if found.ID != id || found.Kind != "" || found.Target.Kind != alert.TargetService || found.Target.ID != service.ID || found.Target.Name != "whoami" {
		t.Fatalf("mute %+v", found)
	}
	if found.CreatedBy != audit.Operator("lucas") {
		t.Fatalf("author %+v", found.CreatedBy)
	}
	if name, err := db.MuteTargetName(ctx, service); err != nil || name != "whoami" {
		t.Fatalf("target name %q %v", name, err)
	}
	if _, err := db.MuteTargetName(ctx, alert.Target{Kind: alert.TargetProbe, ID: "inconnue"}); err != alert.ErrNotFound {
		t.Fatalf("an unknown target: %v", err)
	}
}

// Seules les livraisons en échec dont l'essai suivant est échu sont dues :
// ni celles abandonnées, ni celles qui attendent encore.
func TestListDueDeliveries_OnlyFailedAndDue(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	now := time.Unix(1_800_000_000, 0)
	exec(t, db, `INSERT INTO alerts (kind, severity, status, object_kind, object_id, object_name, opened_at, updated_at)
		VALUES ('disk_full', 'danger', 'open', 'volume', 'local:/', '/', 1, 1)`)
	exec(t, db, `INSERT INTO alert_channels (id, name, url, format, min_severity, created_at) VALUES (1, 'a', '', 'json', 'attention', 1), (2, 'b', '', 'json', 'attention', 1), (3, 'c', '', 'json', 'attention', 1), (4, 'd', '', 'json', 'attention', 1)`)
	deliveries := []alert.Delivery{
		{ChannelID: 1, Status: alert.DeliveryFailed, NextAttemptAt: now.Add(-time.Second)},
		{ChannelID: 2, Status: alert.DeliveryFailed, NextAttemptAt: now.Add(time.Minute)},
		{ChannelID: 3, Status: alert.DeliveryFailed},
		{ChannelID: 4, Status: alert.DeliveryMuted},
	}
	for _, delivery := range deliveries {
		delivery.AlertID, delivery.Event, delivery.CreatedAt, delivery.UpdatedAt = 1, alert.EventOpened, now, now
		if _, err := db.InsertDelivery(ctx, delivery); err != nil {
			t.Fatal(err)
		}
	}
	due, err := db.ListDueDeliveries(ctx, now)
	if err != nil || len(due) != 1 || due[0].ChannelID != 1 || !due[0].NextAttemptAt.Equal(now.Add(-time.Second)) {
		t.Fatalf("due %+v %v", due, err)
	}
}
