package store

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/ldesfontaine/opencloud/internal/manifest"
)

var appliesNow = time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)

func sampleApply(id string, at time.Time) manifest.Apply {
	return manifest.Apply{
		ID: id, At: at, Actor: manifest.Actor{Kind: manifest.ActorCLI, Name: "opencloud"},
		FileHash: "sha256:aa", FileName: "opencloud.yaml", Prune: true, PlanHash: "sha256:bb",
		State: manifest.ApplyConfirmed, Created: 1, Updated: 1, Deleted: 1, Unchanged: 4,
		Changes: []manifest.Change{
			{Action: manifest.ActionUpdate, Kind: manifest.KindProbe, ID: "p1", Name: "site",
				Fields: []manifest.FieldChange{{Field: "interval", Before: "1m", After: "30s"}},
				Notes:  []manifest.Note{manifest.NoteProbeResets}},
			{Action: manifest.ActionDelete, Kind: manifest.KindHeartbeat, ID: "h1", Name: "dump",
				Carries: []manifest.Carry{{What: manifest.CarryHeartbeatPings, Count: 12}}},
			{Action: manifest.ActionCreate, Kind: manifest.KindProject, Name: "ops"},
		},
	}
}

func TestApplies_RoundTrip(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	first := sampleApply("apl_0000000000000001", appliesNow)
	second := sampleApply("apl_0000000000000002", appliesNow.Add(time.Minute))
	second.Actor = manifest.Actor{Kind: manifest.ActorOperator, Name: "lucas"}
	for _, item := range []manifest.Apply{first, second} {
		if err := db.InsertApply(ctx, item); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.SetApplyProgress(ctx, first.ID, 2); err != nil {
		t.Fatal(err)
	}
	got, err := db.GetApply(ctx, first.ID)
	if err != nil || got.Done != 2 || got.State != manifest.ApplyConfirmed || !got.FinishedAt.IsZero() {
		t.Fatalf("in progress: %+v %v", got, err)
	}
	end := manifest.ApplyEnd{State: manifest.ApplyFailed, FinishedAt: appliesNow.Add(2 * time.Second), Done: 2, Error: "directory.name_taken", ErrorObject: "service/app-1/wiki"}
	if err := db.FinishApply(ctx, first.ID, end); err != nil {
		t.Fatal(err)
	}

	got, err = db.GetApply(ctx, first.ID)
	if err != nil {
		t.Fatal(err)
	}
	got.At, got.FinishedAt = got.At.UTC(), got.FinishedAt.UTC()
	want := first
	want.State, want.FinishedAt, want.Done, want.Error, want.ErrorObject = end.State, end.FinishedAt, 2, end.Error, end.ErrorObject
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("round trip:\n%+v\n%+v", got, want)
	}

	list, err := db.ListApplies(ctx, 10)
	if err != nil || len(list) != 2 || list[0].ID != second.ID || list[1].ID != first.ID {
		t.Fatalf("list (newest first): %+v %v", list, err)
	}
	for _, item := range list {
		if len(item.Changes) != 0 {
			t.Errorf("the list does not carry the plan: %+v", item)
		}
	}
	if list[0].Actor.Kind != manifest.ActorOperator || list[0].Actor.Name != "lucas" || list[1].Created != 1 || list[1].Unchanged != 4 {
		t.Fatalf("list fields: %+v", list)
	}
	if limited, _ := db.ListApplies(ctx, 1); len(limited) != 1 || limited[0].ID != second.ID {
		t.Fatalf("limit: %+v", limited)
	}
	if _, err := db.GetApply(ctx, "apl_missing"); !errors.Is(err, manifest.ErrApplyNotFound) {
		t.Fatalf("unknown id: %v", err)
	}
	if err := db.FinishApply(ctx, "apl_missing", end); !errors.Is(err, manifest.ErrApplyNotFound) {
		t.Fatalf("finishing an unknown line: %v", err)
	}
}

func TestFailInterruptedApplies_MarksConfirmedRows(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	left := sampleApply("apl_00000000000000a1", appliesNow)
	done := sampleApply("apl_00000000000000a2", appliesNow.Add(time.Minute))
	if err := db.InsertApply(ctx, left); err != nil {
		t.Fatal(err)
	}
	if err := db.InsertApply(ctx, done); err != nil {
		t.Fatal(err)
	}
	if err := db.FinishApply(ctx, done.ID, manifest.ApplyEnd{State: manifest.ApplyApplied, FinishedAt: appliesNow.Add(time.Minute), Done: 3}); err != nil {
		t.Fatal(err)
	}
	count, err := db.FailInterruptedApplies(ctx, appliesNow.Add(time.Hour), manifest.KeyInterrupted)
	if err != nil || count != 1 {
		t.Fatalf("count %d, %v", count, err)
	}
	got, _ := db.GetApply(ctx, left.ID)
	if got.State != manifest.ApplyFailed || got.Error != manifest.KeyInterrupted || !got.FinishedAt.Equal(appliesNow.Add(time.Hour)) {
		t.Fatalf("left: %+v", got)
	}
	if got, _ := db.GetApply(ctx, done.ID); got.State != manifest.ApplyApplied || got.Error != "" {
		t.Fatalf("a finished line must not move: %+v", got)
	}
	if count, _ := db.FailInterruptedApplies(ctx, appliesNow, manifest.KeyInterrupted); count != 0 {
		t.Fatalf("second pass: %d", count)
	}
}
