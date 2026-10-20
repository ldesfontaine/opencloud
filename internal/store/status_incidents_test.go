package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/ldesfontaine/opencloud/internal/status"
)

var incidentNow = time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)

// seedIncident pose deux composants et un incident ouvert sur le premier.
func seedIncident(t *testing.T, db *DB) status.Incident {
	t.Helper()
	ctx := context.Background()
	for _, id := range []string{"c1", "c2"} {
		if err := db.InsertComponent(ctx, status.Component{ID: id, Name: id, CreatedAt: incidentNow}); err != nil {
			t.Fatal(err)
		}
	}
	incident := status.Incident{
		ID: "i1", Title: "Panne", Impact: status.ImpactDown, Status: status.StatusInvestigating,
		CreatedAt: incidentNow, UpdatedAt: incidentNow,
		Components: []status.ComponentRef{{ID: "c1"}},
		Updates:    []status.Update{{Status: status.StatusInvestigating, Message: "On regarde.", CreatedAt: incidentNow}},
	}
	if err := db.InsertIncident(ctx, incident); err != nil {
		t.Fatal(err)
	}
	return incident
}

func TestUpdateIncident_RewritesTitleAndComponentsAndKeepsTheRest(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	incident := seedIncident(t, db)
	incident.Title = "Panne corrigée"
	incident.UpdatedAt = incidentNow.Add(time.Minute)
	incident.Components = []status.ComponentRef{{ID: "c2"}}
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
	if len(got.Components) != 1 || got.Components[0].ID != "c2" {
		t.Fatalf("components %+v, want c2 only", got.Components)
	}
	if got.Impact != status.ImpactDown || got.Status != status.StatusInvestigating || len(got.Updates) != 1 {
		t.Fatalf("impact %q status %q, %d updates: the correction must not touch them", got.Impact, got.Status, len(got.Updates))
	}
	requireRowIDs(t, rowIDs(t, db, `SELECT component_id FROM incident_components WHERE incident_id = 'i1'`), "c2")
}

func TestUpdateIncident_UnknownIncidentIsNotFound_UnknownComponentIsRefusedAndRolledBack(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	incident := seedIncident(t, db)
	missing := incident
	missing.ID = "nope"
	if err := db.UpdateIncident(ctx, missing); !errors.Is(err, status.ErrNotFound) {
		t.Fatalf("unknown incident: %v", err)
	}
	incident.Title = "Autre titre"
	incident.Components = []status.ComponentRef{{ID: "ghost"}}
	if err := db.UpdateIncident(ctx, incident); !errors.Is(err, status.ErrComponentInvalid) {
		t.Fatalf("unknown component: %v", err)
	}
	got, err := db.GetIncident(ctx, "i1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != "Panne" || len(got.Components) != 1 || got.Components[0].ID != "c1" {
		t.Fatalf("a refused change must roll back whole: %+v", got)
	}
}

func TestDeleteIncident_TakesItsComponentsAndTimelineAndSparesTheComponents(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	seedIncident(t, db)
	if err := db.DeleteIncident(ctx, "i1"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.GetIncident(ctx, "i1"); !errors.Is(err, status.ErrNotFound) {
		t.Fatalf("still there: %v", err)
	}
	requireRowIDs(t, rowIDs(t, db, `SELECT incident_id FROM incident_components`))
	requireRowIDs(t, rowIDs(t, db, `SELECT incident_id FROM incident_updates`))
	requireRowIDs(t, rowIDs(t, db, `SELECT id FROM status_components ORDER BY id`), "c1", "c2")
	if err := db.DeleteIncident(ctx, "i1"); !errors.Is(err, status.ErrNotFound) {
		t.Fatalf("twice: %v", err)
	}
}
