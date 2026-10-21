package status_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/ldesfontaine/opencloud/internal/probe"
	"github.com/ldesfontaine/opencloud/internal/status"
)

// twoSites pose deux composants en bonne santé, chacun sur sa sonde : un
// incident n'en touche qu'un, l'autre sert de témoin.
func twoSites(t *testing.T, f *fixture) (site, mail status.Component) {
	t.Helper()
	siteProbe := f.probe(t, "site", probe.OutcomeUp, nil)
	mailProbe := f.probe(t, "mail", probe.OutcomeUp, nil)
	site = f.component(t, "Site web", status.MemberRef{Kind: status.KindProbe, ID: siteProbe.ID})
	mail = f.component(t, "Messagerie", status.MemberRef{Kind: status.KindProbe, ID: mailProbe.ID})
	return site, mail
}

func (f *fixture) effective(t *testing.T, componentID string) status.State {
	t.Helper()
	component, err := f.status.GetComponent(context.Background(), componentID)
	if err != nil {
		t.Fatal(err)
	}
	return component.Effective
}

func TestChangeIncident_MovesTheTitleAndTheComponentsAndKeepsTheTimeline(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	site, mail := twoSites(t, f)
	opened, err := f.status.OpenIncident(ctx, status.IncidentDefinition{
		Title: "Panne du site", Impact: status.ImpactDown, Status: status.StatusInvestigating,
		Message: "On regarde.", ComponentIDs: []string{site.ID},
	})
	if err != nil {
		t.Fatal(err)
	}
	if f.effective(t, site.ID) != status.StateDown || f.effective(t, mail.ID) != status.StateOperational {
		t.Fatal("the incident must start on the site only")
	}
	f.now = f.now.Add(10 * time.Minute)
	published := f.published.count

	changed, err := f.status.ChangeIncident(ctx, opened.ID, status.IncidentChange{Title: "  Panne de la messagerie  ", ComponentIDs: []string{mail.ID}})
	if err != nil {
		t.Fatal(err)
	}
	if changed.Title != "Panne de la messagerie" {
		t.Fatalf("title %q, want it trimmed", changed.Title)
	}
	if len(changed.Components) != 1 || changed.Components[0].ID != mail.ID || changed.Components[0].Name != "Messagerie" {
		t.Fatalf("components %+v, want the mail only", changed.Components)
	}
	// Ce que l'opérateur ne corrige pas reste : impact, statut, fil.
	if changed.Impact != status.ImpactDown || changed.Status != status.StatusInvestigating {
		t.Fatalf("impact %q status %q", changed.Impact, changed.Status)
	}
	if len(changed.Updates) != 1 || changed.Updates[0].Message != "On regarde." {
		t.Fatalf("timeline %+v", changed.Updates)
	}
	if !changed.CreatedAt.Equal(opened.CreatedAt) || !changed.UpdatedAt.Equal(f.now) {
		t.Fatalf("created %v updated %v, want the creation kept and the update at %v", changed.CreatedAt, changed.UpdatedAt, f.now)
	}
	// L'impact suit les composants touchés.
	if f.effective(t, site.ID) != status.StateOperational || f.effective(t, mail.ID) != status.StateDown {
		t.Fatalf("site %q mail %q: the outage must have moved to the mail", f.effective(t, site.ID), f.effective(t, mail.ID))
	}
	if f.published.count != published+1 {
		t.Fatalf("the public page must be told once, got %d", f.published.count-published)
	}
}

func TestChangeIncident_TheSnapshotShowsTheChange(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	site, mail := twoSites(t, f)
	opened, err := f.status.OpenIncident(ctx, status.IncidentDefinition{Title: "Panne", Impact: status.ImpactDown, Status: status.StatusInvestigating, ComponentIDs: []string{site.ID}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.status.ChangeIncident(ctx, opened.ID, status.IncidentChange{Title: "Panne corrigée", ComponentIDs: []string{site.ID, mail.ID}}); err != nil {
		t.Fatal(err)
	}
	snapshot, err := f.status.Snapshot(ctx, f.pageID)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Open) != 1 || snapshot.Open[0].Title != "Panne corrigée" || len(snapshot.Open[0].Components) != 2 {
		t.Fatalf("open incidents %+v", snapshot.Open)
	}
	for _, component := range snapshot.Components {
		if component.State != status.StateDown {
			t.Errorf("%s is %q, want down: both are touched now", component.Name, component.State)
		}
	}
	if snapshot.Global != status.StateDown {
		t.Fatalf("global %q", snapshot.Global)
	}
}

func TestChangeIncident_ReschedulesAMaintenanceWindow(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	site, _ := twoSites(t, f)
	planned, err := f.status.OpenIncident(ctx, status.IncidentDefinition{
		Title: "Redémarrage", Impact: status.ImpactMaintenance, Status: status.StatusScheduled,
		ComponentIDs: []string{site.ID}, StartsAt: f.now.Add(time.Hour), EndsAt: f.now.Add(2 * time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	startsAt, endsAt := f.now.Add(3*time.Hour), f.now.Add(4*time.Hour)
	changed, err := f.status.ChangeIncident(ctx, planned.ID, status.IncidentChange{Title: planned.Title, ComponentIDs: []string{site.ID}, StartsAt: startsAt, EndsAt: endsAt})
	if err != nil {
		t.Fatal(err)
	}
	if !changed.StartsAt.Equal(startsAt) || !changed.EndsAt.Equal(endsAt) || changed.Status != status.StatusScheduled {
		t.Fatalf("window %v → %v, status %q", changed.StartsAt, changed.EndsAt, changed.Status)
	}
	// La boucle suit la nouvelle fenêtre : rien à 61 minutes, ouverture à 3 h.
	f.now = f.now.Add(61 * time.Minute)
	f.status.ApplyWindows(ctx)
	if got, _ := f.status.GetIncident(ctx, planned.ID); got.Status != status.StatusScheduled {
		t.Fatalf("the old window opened it: %q", got.Status)
	}
	f.now = f.now.Add(2 * time.Hour)
	f.status.ApplyWindows(ctx)
	if got, _ := f.status.GetIncident(ctx, planned.ID); got.Status != status.StatusInProgress {
		t.Fatalf("the new window did not open it: %q", got.Status)
	}
}

func TestChangeIncident_RefusesWhatMakesNoSenseAndChangesNothing(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	site, mail := twoSites(t, f)
	opened, err := f.status.OpenIncident(ctx, status.IncidentDefinition{Title: "Panne", Impact: status.ImpactDown, Status: status.StatusInvestigating, ComponentIDs: []string{site.ID}})
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string]struct {
		id     string
		change status.IncidentChange
		want   error
	}{
		"unknown incident":      {"nope", status.IncidentChange{Title: "x", ComponentIDs: []string{site.ID}}, status.ErrNotFound},
		"empty title":           {opened.ID, status.IncidentChange{Title: "  ", ComponentIDs: []string{site.ID}}, status.ErrTitleInvalid},
		"no component":          {opened.ID, status.IncidentChange{Title: "x"}, status.ErrComponentInvalid},
		"unknown component":     {opened.ID, status.IncidentChange{Title: "x", ComponentIDs: []string{"nope"}}, status.ErrComponentInvalid},
		"component twice":       {opened.ID, status.IncidentChange{Title: "x", ComponentIDs: []string{mail.ID, mail.ID}}, status.ErrComponentInvalid},
		"window on an incident": {opened.ID, status.IncidentChange{Title: "x", ComponentIDs: []string{site.ID}, StartsAt: f.now}, status.ErrWindowInvalid},
	}
	for name, tc := range cases {
		if _, err := f.status.ChangeIncident(ctx, tc.id, tc.change); !errors.Is(err, tc.want) {
			t.Errorf("%s: got %v, want %v", name, err, tc.want)
		}
	}
	unchanged, err := f.status.GetIncident(ctx, opened.ID)
	if err != nil {
		t.Fatal(err)
	}
	if unchanged.Title != "Panne" || len(unchanged.Components) != 1 || unchanged.Components[0].ID != site.ID {
		t.Fatalf("a refused change reached the base: %+v", unchanged)
	}
}

func TestDeleteIncident_TakesTheIncidentOffTheComponentsAndTheSnapshot(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	site, mail := twoSites(t, f)
	opened, err := f.status.OpenIncident(ctx, status.IncidentDefinition{Title: "Panne", Impact: status.ImpactDown, Status: status.StatusInvestigating, ComponentIDs: []string{site.ID, mail.ID}})
	if err != nil {
		t.Fatal(err)
	}
	kept, err := f.status.OpenIncident(ctx, status.IncidentDefinition{Title: "Lenteur", Impact: status.ImpactDegraded, Status: status.StatusInvestigating, ComponentIDs: []string{mail.ID}})
	if err != nil {
		t.Fatal(err)
	}
	published := f.published.count

	if err := f.status.DeleteIncident(ctx, opened.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.status.GetIncident(ctx, opened.ID); !errors.Is(err, status.ErrNotFound) {
		t.Fatalf("still there: %v", err)
	}
	// Le site retrouve son état dérivé ; la messagerie garde l'autre incident.
	if f.effective(t, site.ID) != status.StateOperational || f.effective(t, mail.ID) != status.StateDegraded {
		t.Fatalf("site %q mail %q", f.effective(t, site.ID), f.effective(t, mail.ID))
	}
	snapshot, err := f.status.Snapshot(ctx, f.pageID)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Open) != 1 || snapshot.Open[0].ID != kept.ID {
		t.Fatalf("open incidents %+v, want the other one only", snapshot.Open)
	}
	if f.published.count != published+1 {
		t.Fatalf("the public page must be told once, got %d", f.published.count-published)
	}
	if count, err := f.status.CountOpenIncidents(ctx); err != nil || count != 1 {
		t.Fatalf("open incidents %d, %v", count, err)
	}
}

func TestDeleteIncident_UnknownIsNotFoundAndSilent(t *testing.T) {
	f := newFixture(t)
	twoSites(t, f)
	published := f.published.count
	if err := f.status.DeleteIncident(context.Background(), "nope"); !errors.Is(err, status.ErrNotFound) {
		t.Fatalf("got %v", err)
	}
	if f.published.count != published {
		t.Fatal("a refusal must not wake the public page")
	}
}
