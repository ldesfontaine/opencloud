package store

import (
	"context"
	"testing"
	"time"

	"github.com/ldesfontaine/opencloud/internal/alert"
)

func insertSilence(t *testing.T, db *DB, kind alert.Kind, object alert.Object, endsAt time.Time) {
	t.Helper()
	silence := alert.Silence{Kind: kind, Object: object, StartsAt: endsAt.Add(-time.Hour), EndsAt: endsAt, CreatedAt: endsAt.Add(-time.Hour)}
	if _, err := db.InsertSilence(context.Background(), silence); err != nil {
		t.Fatal(err)
	}
}

func TestPurgeSilences_ForgetsOnlyThoseEndedBeforeTheCutoff(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	now := time.Unix(1_800_000_000, 0)
	cutoff := now.Add(-alert.SilenceRetention)
	insertSilence(t, db, alert.KindDiskFull, alert.Object{}, cutoff.Add(-time.Minute))
	insertSilence(t, db, alert.KindProbeDown, alert.Object{}, cutoff.Add(time.Minute))
	insertSilence(t, db, alert.KindJobLate, alert.Object{}, now.Add(time.Hour))

	if err := db.PurgeSilences(ctx, cutoff); err != nil {
		t.Fatal(err)
	}
	kept, err := db.ListSilences(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(kept) != 2 || kept[0].Kind != alert.KindJobLate || kept[1].Kind != alert.KindProbeDown {
		t.Fatalf("kept %+v", kept)
	}
}

// Seuls les silences de cet objet partent : ni ceux d'un autre objet du même
// genre, ni ceux d'un type seul.
func TestDeleteObjectSilences_TakesOnlyThatObject(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	now := time.Unix(1_800_000_000, 0)
	gone := alert.Object{Kind: alert.ObjectProbe, ID: "aaaaaaaaaaaaaaaa"}
	other := alert.Object{Kind: alert.ObjectProbe, ID: "bbbbbbbbbbbbbbbb"}
	insertSilence(t, db, "", gone, now.Add(time.Hour))
	insertSilence(t, db, alert.KindProbeDown, gone, now.Add(2*time.Hour))
	insertSilence(t, db, "", other, now.Add(3*time.Hour))
	insertSilence(t, db, alert.KindProbeDown, alert.Object{}, now.Add(4*time.Hour))

	deleted, err := db.DeleteObjectSilences(ctx, gone)
	if err != nil {
		t.Fatal(err)
	}
	if deleted != 2 {
		t.Fatalf("deleted %d, want 2", deleted)
	}
	kept, err := db.ListSilences(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, silence := range kept {
		if silence.Object == gone {
			t.Fatalf("a silence of the removed object stayed: %+v", silence)
		}
	}
	if len(kept) != 2 {
		t.Fatalf("kept %d, want 2", len(kept))
	}
}
