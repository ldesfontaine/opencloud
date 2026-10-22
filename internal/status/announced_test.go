package status_test

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/ldesfontaine/opencloud/internal/alert"
	"github.com/ldesfontaine/opencloud/internal/probe"
	"github.com/ldesfontaine/opencloud/internal/status"
)

// announce écrit un silence tel que le moteur le ferait, sans passer par lui :
// ce que le composant lit, c'est la ligne.
func (f *fixture) announce(t *testing.T, mute alert.Mute) alert.Mute {
	t.Helper()
	mute.CreatedAt = f.now
	id, err := f.db.InsertMute(context.Background(), mute)
	if err != nil {
		t.Fatal(err)
	}
	mute.ID = id
	return mute
}

func (f *fixture) snapshot(t *testing.T) status.Snapshot {
	t.Helper()
	snapshot, err := f.status.Snapshot(context.Background(), f.pageID)
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}

// Un silence annoncé est une maintenance synthétique : planifiée avant sa
// fenêtre, en cours pendant, résolue après, montrée quatorze jours ; elle
// n'impose l'état « maintenance » que pendant la fenêtre.
func TestSnapshot_AnnouncedMuteIsAMaintenanceScheduledThenInProgressThenResolved(t *testing.T) {
	f := newFixture(t)
	up := f.probe(t, "site", probe.OutcomeUp, nil)
	site := f.component(t, "Site web", status.MemberRef{Kind: status.KindProbe, ID: up.ID})
	mute := f.announce(t, alert.Mute{
		Target: alert.Target{Kind: alert.TargetProbe, ID: up.ID}, StartsAt: f.now.Add(time.Hour), EndsAt: f.now.Add(2 * time.Hour),
		Announce: true, PublicTitle: "Migration du disque",
	})

	snapshot := f.snapshot(t)
	if len(snapshot.Open) != 1 || len(snapshot.History) != 0 {
		t.Fatalf("scheduled: open %+v history %+v", snapshot.Open, snapshot.History)
	}
	planned := snapshot.Open[0]
	if planned.ID != "mute-"+itoa(mute.ID) || planned.Title != "Migration du disque" || planned.Impact != status.ImpactMaintenance ||
		planned.Status != status.StatusScheduled || planned.Applies() || len(planned.Updates) != 0 || !planned.StartsAt.Equal(mute.StartsAt) || !planned.EndsAt.Equal(mute.EndsAt) {
		t.Fatalf("scheduled maintenance %+v", planned)
	}
	if len(planned.Components) != 1 || planned.Components[0].ID != site.ID || planned.Targets != nil {
		t.Fatalf("components %+v targets %+v: only the components reach the public", planned.Components, planned.Targets)
	}
	if f.effective(t, site.ID) != status.StateOperational {
		t.Fatalf("a scheduled window changes nothing yet, got %q", f.effective(t, site.ID))
	}

	f.now = f.now.Add(90 * time.Minute)
	snapshot = f.snapshot(t)
	if len(snapshot.Open) != 1 || snapshot.Open[0].Status != status.StatusInProgress || !snapshot.Open[0].Applies() || !snapshot.Open[0].UpdatedAt.Equal(mute.StartsAt) {
		t.Fatalf("in progress: %+v", snapshot.Open)
	}
	if f.effective(t, site.ID) != status.StateMaintenance || snapshot.Components[0].State != status.StateMaintenance || snapshot.Global != status.StateMaintenance {
		t.Fatalf("during the window: effective %q, snapshot %+v", f.effective(t, site.ID), snapshot.Components)
	}

	f.now = f.now.Add(time.Hour)
	snapshot = f.snapshot(t)
	if len(snapshot.Open) != 0 || len(snapshot.History) != 1 || !snapshot.History[0].IsResolved() || !snapshot.History[0].ResolvedAt.Equal(mute.EndsAt) {
		t.Fatalf("resolved: open %+v history %+v", snapshot.Open, snapshot.History)
	}
	if f.effective(t, site.ID) != status.StateOperational {
		t.Fatalf("after the window, got %q", f.effective(t, site.ID))
	}

	f.now = mute.EndsAt.Add(status.HistorySpan + time.Minute)
	if snapshot = f.snapshot(t); len(snapshot.Open) != 0 || len(snapshot.History) != 0 {
		t.Fatalf("after fourteen days nothing is shown: %+v", snapshot)
	}
}

func TestSnapshot_UnannouncedMuteShowsNothing(t *testing.T) {
	f := newFixture(t)
	up := f.probe(t, "site", probe.OutcomeUp, nil)
	site := f.component(t, "Site web", status.MemberRef{Kind: status.KindProbe, ID: up.ID})
	f.announce(t, alert.Mute{Target: alert.Target{Kind: alert.TargetProbe, ID: up.ID}, StartsAt: f.now.Add(-time.Minute), EndsAt: f.now.Add(time.Hour)})

	snapshot := f.snapshot(t)
	if len(snapshot.Open) != 0 || len(snapshot.History) != 0 || f.effective(t, site.ID) != status.StateOperational {
		t.Fatalf("an internal silence must stay internal: %+v", snapshot)
	}
}

// Une maintenance annoncée ne passe pas devant une panne, et elle ne
// s'écrit pas : l'administration ne liste que les incidents écrits.
func TestSnapshot_AnnouncedMuteNeverHidesAnOutageAndIsNotAnIncident(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	up := f.probe(t, "site", probe.OutcomeUp, nil)
	site := f.component(t, "Site web", status.MemberRef{Kind: status.KindProbe, ID: up.ID})
	f.announce(t, alert.Mute{
		Target: alert.Target{Kind: alert.TargetProbe, ID: up.ID}, StartsAt: f.now.Add(-time.Minute), EndsAt: f.now.Add(time.Hour),
		Announce: true, PublicTitle: "Migration",
	})
	if _, err := f.status.OpenIncident(ctx, status.IncidentDefinition{Title: "Panne", Impact: status.ImpactDown, Status: status.StatusInvestigating, Targets: targetsOf(site)}); err != nil {
		t.Fatal(err)
	}
	if f.effective(t, site.ID) != status.StateDown {
		t.Fatalf("an outage outranks a maintenance, got %q", f.effective(t, site.ID))
	}
	if written, err := f.status.Incidents(ctx, ""); err != nil || len(written) != 1 {
		t.Fatalf("incidents %+v %v", written, err)
	}
}

func itoa(id int64) string { return strconv.FormatInt(id, 10) }
