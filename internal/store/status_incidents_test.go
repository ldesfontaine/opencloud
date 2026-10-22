package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/ldesfontaine/opencloud/internal/status"
)

var incidentNow = time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)

// seedIncident pose deux composants, deux machines et un incident ouvert qui
// vise la première machine.
func seedIncident(t *testing.T, db *DB) status.Incident {
	t.Helper()
	ctx := context.Background()
	exec(t, db, `INSERT INTO machines (id, name, created_at) VALUES ('m1', 'vps-1', 1), ('m2', 'vps-2', 1)`)
	for _, id := range []string{"c1", "c2"} {
		if err := db.InsertComponent(ctx, status.Component{ID: id, PageID: insertTestPage(t, db, "statut"), Name: id, CreatedAt: incidentNow}); err != nil {
			t.Fatal(err)
		}
	}
	incident := status.Incident{
		ID: "i1", Title: "Panne", Impact: status.ImpactDown, Status: status.StatusInvestigating,
		CreatedAt: incidentNow, UpdatedAt: incidentNow,
		Targets: []status.Target{{Kind: status.KindMachine, ID: "m1"}},
		Updates: []status.Update{{Status: status.StatusInvestigating, Message: "On regarde.", CreatedAt: incidentNow}},
	}
	if err := db.InsertIncident(ctx, incident); err != nil {
		t.Fatal(err)
	}
	return incident
}

func TestUpdateIncident_RewritesTitleAndTargetsAndKeepsTheRest(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	incident := seedIncident(t, db)
	incident.Title = "Panne corrigée"
	incident.UpdatedAt = incidentNow.Add(time.Minute)
	incident.Targets = []status.Target{{Kind: status.KindMachine, ID: "m2"}}
	if err := db.UpdateIncident(ctx, incident); err != nil {
		t.Fatal(err)
	}
	got, err := db.GetIncident(ctx, "i1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != "Panne corrigée" || !got.UpdatedAt.Equal(incident.UpdatedAt) {
		t.Fatalf("title %q updated %v", got.Title, got.UpdatedAt)
	}
	if len(got.Targets) != 1 || got.Targets[0] != (status.Target{Kind: status.KindMachine, ID: "m2"}) {
		t.Fatalf("targets %+v, want m2 only", got.Targets)
	}
	if got.Impact != status.ImpactDown || got.Status != status.StatusInvestigating || len(got.Updates) != 1 {
		t.Fatalf("impact %q status %q, %d updates: the correction must not touch them", got.Impact, got.Status, len(got.Updates))
	}
	requireRowIDs(t, rowIDs(t, db, `SELECT machine_id FROM incident_targets WHERE incident_id = 'i1'`), "m2")
}

func TestUpdateIncident_UnknownIncidentIsNotFound_UnknownTargetIsRefusedAndRolledBack(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	incident := seedIncident(t, db)
	missing := incident
	missing.ID = "nope"
	if err := db.UpdateIncident(ctx, missing); !errors.Is(err, status.ErrNotFound) {
		t.Fatalf("unknown incident: %v", err)
	}
	incident.Title = "Autre titre"
	incident.Targets = []status.Target{{Kind: status.KindMachine, ID: "ghost"}}
	if err := db.UpdateIncident(ctx, incident); !errors.Is(err, status.ErrTargetInvalid) {
		t.Fatalf("unknown target: %v", err)
	}
	got, err := db.GetIncident(ctx, "i1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != "Panne" || len(got.Targets) != 1 || got.Targets[0].ID != "m1" {
		t.Fatalf("a refused change must roll back whole: %+v", got)
	}
}

func TestDeleteIncident_TakesItsTargetsAndTimelineAndSparesTheObjects(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	seedIncident(t, db)
	if err := db.DeleteIncident(ctx, "i1"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.GetIncident(ctx, "i1"); !errors.Is(err, status.ErrNotFound) {
		t.Fatalf("still there: %v", err)
	}
	requireRowIDs(t, rowIDs(t, db, `SELECT incident_id FROM incident_targets`))
	requireRowIDs(t, rowIDs(t, db, `SELECT incident_id FROM incident_updates`))
	requireRowIDs(t, rowIDs(t, db, `SELECT id FROM machines ORDER BY id`), "m1", "m2")
	if err := db.DeleteIncident(ctx, "i1"); !errors.Is(err, status.ErrNotFound) {
		t.Fatalf("twice: %v", err)
	}
}
