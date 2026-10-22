package status_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/ldesfontaine/opencloud/internal/container"
	"github.com/ldesfontaine/opencloud/internal/heartbeat"
	"github.com/ldesfontaine/opencloud/internal/machine"
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

func TestChangeIncident_MovesTheTitleAndTheTargetsAndKeepsTheTimeline(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	site, mail := twoSites(t, f)
	opened, err := f.status.OpenIncident(ctx, status.IncidentDefinition{
		Title: "Panne du site", Impact: status.ImpactDown, Status: status.StatusInvestigating,
		Message: "On regarde.", Targets: targetsOf(site),
	})
	if err != nil {
		t.Fatal(err)
	}
	if f.effective(t, site.ID) != status.StateDown || f.effective(t, mail.ID) != status.StateOperational {
		t.Fatal("the incident must start on the site only")
	}
	f.now = f.now.Add(10 * time.Minute)
	published := f.published.count

	changed, err := f.status.ChangeIncident(ctx, opened.ID, status.IncidentChange{Title: "  Panne de la messagerie  ", Targets: targetsOf(mail)})
	if err != nil {
		t.Fatal(err)
	}
	if changed.Title != "Panne de la messagerie" {
		t.Fatalf("title %q, want it trimmed", changed.Title)
	}
	if len(changed.Targets) != 1 || changed.Targets[0].ID != targetsOf(mail)[0].ID || changed.Targets[0].Name != "mail" {
		t.Fatalf("targets %+v, want the mail probe only", changed.Targets)
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
	// L'impact suit les composants que les cibles touchent.
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
	opened, err := f.status.OpenIncident(ctx, status.IncidentDefinition{Title: "Panne", Impact: status.ImpactDown, Status: status.StatusInvestigating, Targets: targetsOf(site)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.status.ChangeIncident(ctx, opened.ID, status.IncidentChange{Title: "Panne corrigée", Targets: targetsOf(site, mail)}); err != nil {
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
		Targets: targetsOf(site), StartsAt: f.now.Add(time.Hour), EndsAt: f.now.Add(2 * time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	startsAt, endsAt := f.now.Add(3*time.Hour), f.now.Add(4*time.Hour)
	changed, err := f.status.ChangeIncident(ctx, planned.ID, status.IncidentChange{Title: planned.Title, Targets: targetsOf(site), StartsAt: startsAt, EndsAt: endsAt})
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
	opened, err := f.status.OpenIncident(ctx, status.IncidentDefinition{Title: "Panne", Impact: status.ImpactDown, Status: status.StatusInvestigating, Targets: targetsOf(site)})
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string]struct {
		id     string
		change status.IncidentChange
		want   error
	}{
		"unknown incident":      {"nope", status.IncidentChange{Title: "x", Targets: targetsOf(site)}, status.ErrNotFound},
		"empty title":           {opened.ID, status.IncidentChange{Title: "  ", Targets: targetsOf(site)}, status.ErrTitleInvalid},
		"no target":             {opened.ID, status.IncidentChange{Title: "x"}, status.ErrTargetInvalid},
		"unknown target":        {opened.ID, status.IncidentChange{Title: "x", Targets: []status.MemberRef{{Kind: status.KindProbe, ID: "nope"}}}, status.ErrTargetInvalid},
		"unknown kind":          {opened.ID, status.IncidentChange{Title: "x", Targets: []status.MemberRef{{Kind: "container", ID: "nope"}}}, status.ErrTargetInvalid},
		"target without id":     {opened.ID, status.IncidentChange{Title: "x", Targets: []status.MemberRef{{Kind: status.KindProbe}}}, status.ErrTargetInvalid},
		"target twice":          {opened.ID, status.IncidentChange{Title: "x", Targets: targetsOf(mail, mail)}, status.ErrTargetInvalid},
		"window on an incident": {opened.ID, status.IncidentChange{Title: "x", Targets: targetsOf(site), StartsAt: f.now}, status.ErrWindowInvalid},
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
	if unchanged.Title != "Panne" || len(unchanged.Targets) != 1 || unchanged.Targets[0].ID != targetsOf(site)[0].ID || len(unchanged.Components) != 1 || unchanged.Components[0].ID != site.ID {
		t.Fatalf("a refused change reached the base: %+v", unchanged)
	}
}

func TestDeleteIncident_TakesTheIncidentOffTheComponentsAndTheSnapshot(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	site, mail := twoSites(t, f)
	opened, err := f.status.OpenIncident(ctx, status.IncidentDefinition{Title: "Panne", Impact: status.ImpactDown, Status: status.StatusInvestigating, Targets: targetsOf(site, mail)})
	if err != nil {
		t.Fatal(err)
	}
	kept, err := f.status.OpenIncident(ctx, status.IncidentDefinition{Title: "Lenteur", Impact: status.ImpactDegraded, Status: status.StatusInvestigating, Targets: targetsOf(mail)})
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

// serviceWithProbe pose un service déclaré sur la machine openCloud et une
// sonde qui le regarde depuis la machine donnée, en bonne santé.
func (f *fixture) serviceWithProbe(t *testing.T, name, probeMachineID string) (serviceID string, watching probe.Probe) {
	t.Helper()
	ctx := context.Background()
	serviceID = f.service(t, name, container.StateRunning, 0)
	watching, err := f.probes.Create(ctx, probe.Definition{
		Name: "sonde-" + name, Kind: probe.KindHTTP, Target: "https://" + name + ".exemple.fr/", MachineID: probeMachineID, ServiceID: serviceID,
	})
	if err != nil {
		t.Fatal(err)
	}
	result := probe.Result{ProbeID: watching.ID, CheckedAt: f.now.Add(-time.Minute), Outcome: probe.OutcomeUp, DurationMs: 100}
	if err := f.probes.Record(ctx, probeMachineID, probe.Report{Results: []probe.Result{result}}); err != nil {
		t.Fatal(err)
	}
	return serviceID, watching
}

// Un incident sur un service touche le composant de sa sonde : l'état du
// composant devient celui de l'incident, et l'incident paraît sur la page,
// réduit à ce composant. L'opérateur lit les noms des cibles.
func TestSnapshot_IncidentOnAServiceTouchesTheComponentOfItsProbe(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	serviceID, watching := f.serviceWithProbe(t, "Nextcloud", machine.LocalID)
	web := f.component(t, "Site web", status.MemberRef{Kind: status.KindProbe, ID: watching.ID})
	elsewhere := f.probe(t, "autre", probe.OutcomeUp, nil)
	other := f.component(t, "Autre", status.MemberRef{Kind: status.KindProbe, ID: elsewhere.ID})

	opened, err := f.status.OpenIncident(ctx, status.IncidentDefinition{
		Title: "Nextcloud est tombé", Impact: status.ImpactDown, Status: status.StatusInvestigating,
		Targets: []status.MemberRef{{Kind: status.KindService, ID: serviceID}},
	})
	if err != nil {
		t.Fatal(err)
	}

	if len(opened.Targets) != 1 || opened.Targets[0].Name != "Nextcloud" {
		t.Fatalf("targets %+v, want the service named by its title", opened.Targets)
	}
	if len(opened.Components) != 1 || opened.Components[0].ID != web.ID {
		t.Fatalf("components %+v, want the web component only", opened.Components)
	}
	if f.effective(t, web.ID) != status.StateDown || f.effective(t, other.ID) != status.StateOperational {
		t.Fatalf("web %q other %q", f.effective(t, web.ID), f.effective(t, other.ID))
	}
	snapshot, err := f.status.Snapshot(ctx, f.pageID)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Open) != 1 || len(snapshot.Open[0].Components) != 1 || snapshot.Open[0].Components[0].Name != "Site web" {
		t.Fatalf("open %+v, want the incident reduced to the web component", snapshot.Open)
	}
}

// La machine qui lance une sonde ne la touche pas : une sonde qui regarde de
// dehors reste juste. La machine qui héberge le service, si.
func TestSnapshot_IncidentOnAMachineDoesNotTouchAProbeRunFromIt(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	remote := f.enroll(t, "vps-paris-1")
	_, watching := f.serviceWithProbe(t, "Nextcloud", remote)
	web := f.component(t, "Site web", status.MemberRef{Kind: status.KindProbe, ID: watching.ID})

	fromRemote, err := f.status.OpenIncident(ctx, status.IncidentDefinition{
		Title: "Le VPS de contrôle tombe", Impact: status.ImpactDown, Status: status.StatusInvestigating,
		Targets: []status.MemberRef{{Kind: status.KindMachine, ID: remote}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(fromRemote.Components) != 0 || f.effective(t, web.ID) != status.StateOperational {
		t.Fatalf("components %+v, effective %q: the machine running the probe must not touch it", fromRemote.Components, f.effective(t, web.ID))
	}
	if snapshot, err := f.status.Snapshot(ctx, f.pageID); err != nil || len(snapshot.Open) != 0 {
		t.Fatalf("the incident must show on no page: %+v %v", snapshot.Open, err)
	}

	onHost, err := f.status.OpenIncident(ctx, status.IncidentDefinition{
		Title: "Le serveur tombe", Impact: status.ImpactDegraded, Status: status.StatusInvestigating,
		Targets: []status.MemberRef{{Kind: status.KindMachine, ID: machine.LocalID}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(onHost.Components) != 1 || f.effective(t, web.ID) != status.StateDegraded {
		t.Fatalf("components %+v, effective %q: the host of the service touches its probe", onHost.Components, f.effective(t, web.ID))
	}
}

// Un incident dont la seule cible a disparu garde son texte et son fil, sans
// cible : l'administration le liste, aucune page ne le montre.
func TestIncident_WhoseTargetVanishedKeepsItsTextAndShowsOnNoPage(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	up := f.probe(t, "site", probe.OutcomeUp, nil)
	f.component(t, "Site web", status.MemberRef{Kind: status.KindProbe, ID: up.ID})
	job, err := f.heartbeats.Create(ctx, heartbeat.Definition{Name: "sauvegarde", Interval: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	opened, err := f.status.OpenIncident(ctx, status.IncidentDefinition{
		Title: "Sauvegarde en retard", Impact: status.ImpactDegraded, Status: status.StatusInvestigating, Message: "On regarde.",
		Targets: []status.MemberRef{{Kind: status.KindHeartbeat, ID: job.ID}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.heartbeats.Delete(ctx, job.ID); err != nil {
		t.Fatal(err)
	}

	kept, err := f.status.GetIncident(ctx, opened.ID)
	if err != nil || kept.Title != "Sauvegarde en retard" || len(kept.Updates) != 1 || len(kept.Targets) != 0 || len(kept.Components) != 0 {
		t.Fatalf("kept %+v %v", kept, err)
	}
	all, err := f.status.Incidents(ctx, "")
	if err != nil || len(all) != 1 {
		t.Fatalf("the administration lists it: %+v %v", all, err)
	}
	if onPage, err := f.status.Incidents(ctx, f.pageID); err != nil || len(onPage) != 0 {
		t.Fatalf("no page shows it: %+v %v", onPage, err)
	}
}
