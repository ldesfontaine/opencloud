package store

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/ldesfontaine/opencloud/internal/audit"
	"github.com/ldesfontaine/opencloud/internal/manifest"
)

func auditEvent(objectKind audit.ObjectKind, action audit.Action, objectID string, at time.Time) audit.Event {
	return audit.Event{
		At: at, Actor: audit.Operator("lucas"), Action: action, ObjectKind: objectKind,
		ObjectID: objectID, ObjectName: "nom de " + objectID, Details: audit.Details{"k": "v"},
	}
}

// Une ligne ajoutée se relit telle quelle ; la liste va du plus récent au
// plus ancien, se pagine par identifiant et se filtre par objet.
func TestAuditEvents_AppendThenListByPageAndObject(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	at := time.Date(2026, 10, 21, 9, 0, 0, 0, time.UTC)
	for _, event := range []audit.Event{
		auditEvent(audit.ObjectProbe, audit.ProbeCreate, "p1", at),
		auditEvent(audit.ObjectProbe, audit.ProbePause, "p2", at),
		auditEvent(audit.ObjectChannel, audit.ChannelCreate, "1", at),
	} {
		if _, err := db.InsertAuditEvent(ctx, event); err != nil {
			t.Fatal(err)
		}
	}
	all, err := db.ListAuditEvents(ctx, audit.Query{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 3 || all[0].ObjectID != "1" || all[2].ObjectID != "p1" {
		t.Fatalf("all: %+v", all)
	}
	first := all[2]
	if first.Actor != audit.Operator("lucas") || first.Action != audit.ProbeCreate || first.ObjectKind != audit.ObjectProbe ||
		first.ObjectName != "nom de p1" || first.Details["k"] != "v" || !first.At.Equal(at) || first.ApplyID != "" {
		t.Fatalf("first: %+v", first)
	}
	page, err := db.ListAuditEvents(ctx, audit.Query{Limit: 1, Before: all[0].ID})
	if err != nil {
		t.Fatal(err)
	}
	if len(page) != 1 || page[0].ObjectID != "p2" {
		t.Fatalf("page: %+v", page)
	}
	probes, err := db.ListAuditEvents(ctx, audit.Query{Limit: 10, ObjectKind: audit.ObjectProbe})
	if err != nil {
		t.Fatal(err)
	}
	one, err := db.ListAuditEvents(ctx, audit.Query{Limit: 10, ObjectKind: audit.ObjectProbe, ObjectID: "p1"})
	if err != nil {
		t.Fatal(err)
	}
	if len(probes) != 2 || len(one) != 1 || one[0].ObjectID != "p1" {
		t.Fatalf("probes %+v, one %+v", probes, one)
	}
}

// La purge ne retire que ce qui est plus ancien que la borne.
func TestAuditEvents_PurgeRemovesOnlyTheOlder(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	now := time.Date(2026, 10, 21, 9, 0, 0, 0, time.UTC)
	for _, event := range []audit.Event{
		auditEvent(audit.ObjectProbe, audit.ProbeDelete, "old", now.Add(-audit.Retention-time.Hour)),
		auditEvent(audit.ObjectProbe, audit.ProbeDelete, "kept", now.Add(-time.Hour)),
	} {
		if _, err := db.InsertAuditEvent(ctx, event); err != nil {
			t.Fatal(err)
		}
	}
	purged, err := db.PurgeAuditEvents(ctx, now.Add(-audit.Retention))
	if err != nil {
		t.Fatal(err)
	}
	left, err := db.ListAuditEvents(ctx, audit.Query{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if purged != 1 || len(left) != 1 || left[0].ObjectID != "kept" {
		t.Fatalf("purged %d, left %+v", purged, left)
	}
}

// Une ligne du journal ne se modifie pas ; la ligne d'apply qu'elle cite
// peut partir, le lien passe à nul et la ligne reste.
func TestAuditEvents_AreAppendOnlyAndOutliveTheirApply(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	at := time.Date(2026, 10, 21, 9, 0, 0, 0, time.UTC)
	apply := manifest.Apply{
		ID: "apl_0123456789abcdef", At: at, Actor: manifest.Actor{Kind: manifest.ActorCLI, Name: "root"},
		FileHash: "sha256:x", PlanHash: "h", State: manifest.ApplyConfirmed,
	}
	if err := db.InsertApply(ctx, apply); err != nil {
		t.Fatal(err)
	}
	event := auditEvent(audit.ObjectProject, audit.ProjectCreate, "prj_1", at)
	event.ApplyID = apply.ID
	if _, err := db.InsertAuditEvent(ctx, event); err != nil {
		t.Fatal(err)
	}
	_, err := db.sql.ExecContext(ctx, `UPDATE audit_events SET actor_name = 'mallory'`)
	if err == nil || !strings.Contains(err.Error(), "append-only") {
		t.Fatalf("an update went through: %v", err)
	}
	if _, err := db.sql.ExecContext(ctx, `DELETE FROM applies WHERE id = ?`, apply.ID); err != nil {
		t.Fatal(err)
	}
	left, err := db.ListAuditEvents(ctx, audit.Query{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(left) != 1 || left[0].ApplyID != "" || left[0].Actor != audit.Operator("lucas") {
		t.Fatalf("left: %+v", left)
	}
}

// Le libellé de l'acteur se relit tel quel ; une ligne d'avant la
// migration 017 (sans colonne remplie) se lit sans libellé ; le filtre par
// genre et la recherche trouvent le nom du jeton, son identifiant et « pour
// qui ». Le libellé ne se modifie pas : le déclencheur le garde.
func TestAuditEvents_ActorLabelLegacyRowsAndFilters(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	at := time.Date(2026, 10, 28, 9, 0, 0, 0, time.UTC)
	byToken := auditEvent(audit.ObjectProbe, audit.ProbePause, "p1", at)
	byToken.Actor = audit.API("tok0000000000001", "Agent-Veille", "alice-sub")
	byToken.Details = audit.Details{"on_behalf_of": "alice-sub"}
	if _, err := db.InsertAuditEvent(ctx, byToken); err != nil {
		t.Fatal(err)
	}
	if _, err := db.InsertAuditEvent(ctx, auditEvent(audit.ObjectProbe, audit.ProbePause, "p2", at)); err != nil {
		t.Fatal(err)
	}
	// Une ligne telle que l'écrivait la version d'avant : sans actor_label.
	if _, err := db.sql.ExecContext(ctx, `
		INSERT INTO audit_events (at, actor_kind, actor_name, action, object_kind, object_id, object_name, details)
		VALUES (?, 'api', 'tok0000000000002', 'probe.pause', 'probe', 'p3', '', '{}')`, at.Unix()); err != nil {
		t.Fatal(err)
	}
	find := func(query audit.Query) []string {
		t.Helper()
		query.Limit = 10
		events, err := db.ListAuditEvents(ctx, query)
		if err != nil {
			t.Fatal(err)
		}
		var objects []string
		for _, event := range events {
			objects = append(objects, event.ObjectID)
		}
		return objects
	}
	if got := find(audit.Query{ActorKind: audit.ActorAPI}); !slices.Equal(got, []string{"p3", "p1"}) {
		t.Fatalf("api kind: %v", got)
	}
	for search, want := range map[string][]string{
		"agent-veille":     {"p1"},
		"ALICE":            {"p1"},
		"tok0000000000002": {"p3"},
		"lucas":            {"p2"},
		"absent":           nil,
	} {
		if got := find(audit.Query{Actor: search}); !slices.Equal(got, want) {
			t.Fatalf("search %q: %v, want %v", search, got, want)
		}
	}
	legacy, err := db.ListAuditEvents(ctx, audit.Query{Limit: 10, ObjectID: "p3"})
	if err != nil || len(legacy) != 1 || legacy[0].Actor.Label != "" || legacy[0].Actor.Name != "tok0000000000002" {
		t.Fatalf("legacy row: %+v %v", legacy, err)
	}
	if _, err := db.sql.ExecContext(ctx, `UPDATE audit_events SET actor_label = 'autre' WHERE object_id = 'p1'`); err == nil {
		t.Fatal("the label must stay append-only")
	}
}
