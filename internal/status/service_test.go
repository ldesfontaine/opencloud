package status_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/ldesfontaine/opencloud/internal/heartbeat"
	"github.com/ldesfontaine/opencloud/internal/lang"
	"github.com/ldesfontaine/opencloud/internal/live"
	"github.com/ldesfontaine/opencloud/internal/machine"
	"github.com/ldesfontaine/opencloud/internal/probe"
	"github.com/ldesfontaine/opencloud/internal/service"
	"github.com/ldesfontaine/opencloud/internal/settings"
	"github.com/ldesfontaine/opencloud/internal/status"
	"github.com/ldesfontaine/opencloud/internal/store"
)

var testNow = time.Date(2026, 9, 21, 12, 0, 0, 0, time.UTC)

// Le composant se teste sur la vraie base et les vrais composants qui
// écrivent les objets : ce qu'il dérive, il le dérive de leurs faits.
type fixture struct {
	status     *status.Service
	machines   *machine.Service
	heartbeats *heartbeat.Service
	services   *service.Tracker
	probes     *probe.Service
	bus        *live.Bus
	published  *counter
	now        time.Time
}

type counter struct{ count int }

func (c *counter) StatusChanged() { c.count++ }

func newFixture(t *testing.T) *fixture {
	t.Helper()
	root, err := os.OpenRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { root.Close() })
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	db, err := store.Open(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	f := &fixture{bus: live.New(), published: &counter{}, now: testNow}
	clock := func() time.Time { return f.now }
	f.machines = machine.New(db, machine.NewSessions(), logger)
	f.machines.SetClock(clock)
	if err := f.machines.EnsureLocal(context.Background(), machine.LocalInfo{Hostname: "opencloud-host", Address: "10.8.0.1", OS: "Debian 12", Arch: "amd64", Version: "v0.0.1"}); err != nil {
		t.Fatal(err)
	}
	f.heartbeats = heartbeat.New(db, logger)
	f.heartbeats.SetClock(clock)
	f.services = service.New(db, logger)
	f.services.SetClock(clock)
	f.probes = probe.New(db, logger)
	f.probes.SetClock(clock)
	f.status = status.New(db, f.machines, settings.New(root), logger)
	f.status.SetClock(clock)
	f.status.AddWatcher(f.published)
	f.status.SetFeed(f.bus)
	return f
}

func (f *fixture) component(t *testing.T, name string, members ...status.MemberRef) status.Component {
	t.Helper()
	created, err := f.status.CreateComponent(context.Background(), status.ComponentDefinition{Name: name, Members: members})
	if err != nil {
		t.Fatal(err)
	}
	return created
}

func (f *fixture) probe(t *testing.T, name string, outcome probe.Outcome, certificate *probe.Certificate) probe.Probe {
	t.Helper()
	ctx := context.Background()
	created, err := f.probes.Create(ctx, probe.Definition{Name: name, Kind: probe.KindHTTP, Target: "https://" + name + ".exemple.fr/", MachineID: machine.LocalID})
	if err != nil {
		t.Fatal(err)
	}
	result := probe.Result{ProbeID: created.ID, CheckedAt: f.now.Add(-time.Minute), Outcome: outcome, DurationMs: 100, Certificate: certificate}
	if outcome == probe.OutcomeDown {
		result.Reason = probe.ReasonTimeout
	}
	if err := f.probes.Record(ctx, machine.LocalID, probe.Report{Results: []probe.Result{result}}); err != nil {
		t.Fatal(err)
	}
	return created
}

// enroll une machine distante, sans flux : hors ligne.
func (f *fixture) enroll(t *testing.T, name string) string {
	t.Helper()
	ctx := context.Background()
	cleartext, _, err := f.machines.CreateToken(ctx, name)
	if err != nil {
		t.Fatal(err)
	}
	public, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	enrolled, err := f.machines.Enroll(ctx, machine.Enrollment{
		MachineID: "22222222-2222-4333-8444-555555555555", PublicKey: public, Token: cleartext,
		Hostname: name, Address: "51.15.20.114", OS: "Debian 12", Arch: "amd64", AgentVersion: "v0.0.1",
	})
	if err != nil {
		t.Fatal(err)
	}
	return enrolled.ID
}

func (f *fixture) service(t *testing.T, name string, state service.State, exitCode int) string {
	t.Helper()
	container := service.Container{ContainerID: strings.Repeat("a", 64), Name: name, Image: name + ":1", State: state, ExitCode: exitCode, CreatedAt: f.now.Add(-time.Hour)}
	report := service.Report{Engine: &service.EngineReport{Present: true, Version: "29.8.0", APIVersion: "1.56"}, Complete: true, Inventory: []service.Container{container}}
	if err := f.services.Record(context.Background(), machine.LocalID, report); err != nil {
		t.Fatal(err)
	}
	return service.ID(machine.LocalID, container.ContainerID)
}

func TestDerive_TheWorstObjectWins(t *testing.T) {
	f := newFixture(t)
	up := f.probe(t, "site", probe.OutcomeUp, nil)
	down := f.probe(t, "api", probe.OutcomeDown, nil)
	// Une sonde hors ligne ne bascule qu'au seuil : trois échecs.
	for range 2 {
		f.now = f.now.Add(time.Minute)
		if err := f.probes.Record(context.Background(), machine.LocalID, probe.Report{Results: []probe.Result{{ProbeID: down.ID, CheckedAt: f.now, Outcome: probe.OutcomeDown, Reason: probe.ReasonTimeout}}}); err != nil {
			t.Fatal(err)
		}
	}
	both := f.component(t, "Site web", status.MemberRef{Kind: status.KindProbe, ID: up.ID}, status.MemberRef{Kind: status.KindProbe, ID: down.ID})
	if both.Derived != status.StateDown || both.Effective != status.StateDown {
		t.Fatalf("derived %q effective %q, want down", both.Derived, both.Effective)
	}
	for _, member := range both.Members {
		if !member.Present || !member.Counts || member.Name == "" {
			t.Fatalf("member %+v not filled", member)
		}
	}
	fine := f.component(t, "Courrier", status.MemberRef{Kind: status.KindProbe, ID: up.ID})
	if fine.Effective != status.StateOperational {
		t.Fatalf("effective %q, want operational", fine.Effective)
	}
}

func TestDerive_ObjectsThatSayNothingDoNotCount(t *testing.T) {
	f := newFixture(t)
	fresh, err := f.probes.Create(context.Background(), probe.Definition{Name: "neuve", Kind: probe.KindHTTP, Target: "https://neuve.exemple.fr/", MachineID: machine.LocalID})
	if err != nil {
		t.Fatal(err)
	}
	job, err := f.heartbeats.Create(context.Background(), heartbeat.Definition{Name: "sauvegarde", Interval: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	empty := f.component(t, "Silence", status.MemberRef{Kind: status.KindProbe, ID: fresh.ID}, status.MemberRef{Kind: status.KindHeartbeat, ID: job.ID})
	if empty.Derived != status.StateUnknown || empty.Effective != status.StateUnknown {
		t.Fatalf("a component whose objects say nothing must be unknown, got %q", empty.Effective)
	}
	snapshot, err := f.status.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Components) != 0 || snapshot.Global != status.StateUnknown {
		t.Fatalf("an unknown component must stay hidden: %+v", snapshot.Components)
	}
}

func TestDerive_CertificateAboutToExpireDegrades(t *testing.T) {
	f := newFixture(t)
	soon := &probe.Certificate{Subject: "site", NotBefore: f.now.Add(-60 * 24 * time.Hour), NotAfter: f.now.Add(10 * 24 * time.Hour), Fingerprint: strings.Repeat("e", 64), ChainValid: true, HostnameMatch: true}
	expiring := f.probe(t, "site", probe.OutcomeUp, soon)
	if got := f.component(t, "Site web", status.MemberRef{Kind: status.KindProbe, ID: expiring.ID}).Effective; got != status.StateDegraded {
		t.Fatalf("effective %q, want degraded", got)
	}
}

func TestDerive_ServicesAndMachines(t *testing.T) {
	f := newFixture(t)
	stopped := f.service(t, "nextcloud", service.StateExited, 0)
	if got := f.component(t, "Nuage", status.MemberRef{Kind: status.KindService, ID: stopped}).Effective; got != status.StateDown {
		t.Fatalf("a stopped service is an outage, got %q", got)
	}
	// La machine openCloud est son propre agent : toujours en ligne. Une
	// machine enrôlée sans flux, elle, est hors ligne.
	if got := f.component(t, "Serveur", status.MemberRef{Kind: status.KindMachine, ID: machine.LocalID}).Effective; got != status.StateOperational {
		t.Fatalf("the openCloud machine is always online, got %q", got)
	}
	remote := f.enroll(t, "vps-paris-1")
	if got := f.component(t, "VPS", status.MemberRef{Kind: status.KindMachine, ID: remote}).Effective; got != status.StateDown {
		t.Fatalf("an offline machine is an outage, got %q", got)
	}
}

func TestIncident_OverridesTheDerivedStateWhileOpen(t *testing.T) {
	f := newFixture(t)
	up := f.probe(t, "site", probe.OutcomeUp, nil)
	site := f.component(t, "Site web", status.MemberRef{Kind: status.KindProbe, ID: up.ID})
	opened, err := f.status.OpenIncident(context.Background(), status.IncidentDefinition{
		Title: "Migration en cours", Impact: status.ImpactMaintenance, Status: status.StatusScheduled, Message: "Lecture seule.",
		ComponentIDs: []string{site.ID}, StartsAt: f.now.Add(-time.Minute), EndsAt: f.now.Add(time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	if opened.Status != status.StatusInProgress {
		t.Fatalf("a window already open starts now, got %q", opened.Status)
	}
	current, err := f.status.GetComponent(context.Background(), site.ID)
	if err != nil {
		t.Fatal(err)
	}
	if current.Derived != status.StateOperational || current.Effective != status.StateMaintenance {
		t.Fatalf("derived %q effective %q", current.Derived, current.Effective)
	}
	if _, err := f.status.AddUpdate(context.Background(), opened.ID, status.StatusResolved, "Fini."); err != nil {
		t.Fatal(err)
	}
	current, _ = f.status.GetComponent(context.Background(), site.ID)
	if current.Effective != status.StateOperational {
		t.Fatalf("resolved: effective %q, want the derived state back", current.Effective)
	}
	if _, err := f.status.AddUpdate(context.Background(), opened.ID, status.StatusMonitoring, "encore"); !errors.Is(err, status.ErrResolved) {
		t.Fatalf("an update on a resolved incident must be refused, got %v", err)
	}
}

func TestIncident_ScheduledMaintenanceOpensAndClosesOnTime(t *testing.T) {
	f := newFixture(t)
	up := f.probe(t, "site", probe.OutcomeUp, nil)
	site := f.component(t, "Site web", status.MemberRef{Kind: status.KindProbe, ID: up.ID})
	planned, err := f.status.OpenIncident(context.Background(), status.IncidentDefinition{
		Title: "Redémarrage", Impact: status.ImpactMaintenance, Status: status.StatusScheduled,
		ComponentIDs: []string{site.ID}, StartsAt: f.now.Add(time.Hour), EndsAt: f.now.Add(2 * time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := f.status.GetComponent(context.Background(), site.ID); got.Effective != status.StateOperational {
		t.Fatalf("a scheduled maintenance changes nothing yet, got %q", got.Effective)
	}
	f.now = f.now.Add(61 * time.Minute)
	f.status.ApplyWindows(context.Background())
	started, _ := f.status.GetIncident(context.Background(), planned.ID)
	if started.Status != status.StatusInProgress || len(started.Updates) != 2 {
		t.Fatalf("status %q, %d updates", started.Status, len(started.Updates))
	}
	if got, _ := f.status.GetComponent(context.Background(), site.ID); got.Effective != status.StateMaintenance {
		t.Fatalf("in progress: effective %q", got.Effective)
	}
	f.now = f.now.Add(time.Hour)
	f.status.ApplyWindows(context.Background())
	done, _ := f.status.GetIncident(context.Background(), planned.ID)
	if !done.IsResolved() || done.ResolvedAt.IsZero() {
		t.Fatalf("the window passed, status %q", done.Status)
	}
}

func TestIncident_RefusesWhatMakesNoSense(t *testing.T) {
	f := newFixture(t)
	up := f.probe(t, "site", probe.OutcomeUp, nil)
	site := f.component(t, "Site web", status.MemberRef{Kind: status.KindProbe, ID: up.ID})
	cases := map[string]struct {
		definition status.IncidentDefinition
		want       error
	}{
		"no component":            {status.IncidentDefinition{Title: "x", Impact: status.ImpactDown, Status: status.StatusInvestigating}, status.ErrComponentInvalid},
		"unknown component":       {status.IncidentDefinition{Title: "x", Impact: status.ImpactDown, Status: status.StatusInvestigating, ComponentIDs: []string{"nope"}}, status.ErrComponentInvalid},
		"maintenance status":      {status.IncidentDefinition{Title: "x", Impact: status.ImpactDown, Status: status.StatusInProgress, ComponentIDs: []string{site.ID}}, status.ErrStatusInvalid},
		"window on an incident":   {status.IncidentDefinition{Title: "x", Impact: status.ImpactDown, Status: status.StatusInvestigating, ComponentIDs: []string{site.ID}, StartsAt: f.now}, status.ErrWindowInvalid},
		"maintenance without end": {status.IncidentDefinition{Title: "x", Impact: status.ImpactMaintenance, Status: status.StatusScheduled, ComponentIDs: []string{site.ID}, StartsAt: f.now}, status.ErrWindowInvalid},
		"empty title":             {status.IncidentDefinition{Title: " ", Impact: status.ImpactDown, Status: status.StatusInvestigating, ComponentIDs: []string{site.ID}}, status.ErrTitleInvalid},
	}
	for name, tc := range cases {
		if _, err := f.status.OpenIncident(context.Background(), tc.definition); !errors.Is(err, tc.want) {
			t.Errorf("%s: got %v, want %v", name, err, tc.want)
		}
	}
	if _, err := f.status.CreateComponent(context.Background(), status.ComponentDefinition{Name: "x", Members: []status.MemberRef{{status.KindProbe, "nope"}}}); !errors.Is(err, status.ErrMemberInvalid) {
		t.Errorf("unknown member: %v", err)
	}
}

func TestRefresh_PublishesOnlyWhenSomethingVisibleChanges(t *testing.T) {
	f := newFixture(t)
	up := f.probe(t, "site", probe.OutcomeUp, nil)
	f.status.Refresh(context.Background())
	before := f.published.count
	f.status.Refresh(context.Background())
	if f.published.count != before {
		t.Fatalf("nothing changed, yet published %d times", f.published.count-before)
	}
	f.component(t, "Site web", status.MemberRef{Kind: status.KindProbe, ID: up.ID})
	if f.published.count != before+1 {
		t.Fatalf("a new component must publish once, got %d", f.published.count-before)
	}
	f.status.Refresh(context.Background())
	if f.published.count != before+1 {
		t.Fatal("a refresh without change must stay silent")
	}
}

func TestSnapshot_SaysNothingOfTheObjects(t *testing.T) {
	f := newFixture(t)
	certificate := &probe.Certificate{Subject: "SECRET-subject", Issuer: "SECRET-issuer", NotBefore: f.now.Add(-24 * time.Hour), NotAfter: f.now.Add(90 * 24 * time.Hour), Fingerprint: strings.Repeat("f", 64), ChainValid: true, HostnameMatch: true}
	site := f.probe(t, "SECRET-probe", probe.OutcomeUp, certificate)
	stopped := f.service(t, "SECRET-container", service.StateExited, 1)
	job, err := f.heartbeats.Create(context.Background(), heartbeat.Definition{Name: "SECRET-job", Interval: time.Hour, MachineID: machine.LocalID})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.heartbeats.Receive(context.Background(), job.Token, heartbeat.Ping{Kind: heartbeat.KindFinish, Source: "10.0.0.9", Method: "GET"}); err != nil {
		t.Fatal(err)
	}
	all := f.component(t, "Nuage", status.MemberRef{Kind: status.KindProbe, ID: site.ID}, status.MemberRef{Kind: status.KindService, ID: stopped}, status.MemberRef{Kind: status.KindHeartbeat, ID: job.ID}, status.MemberRef{Kind: status.KindMachine, ID: machine.LocalID})
	if _, err := f.status.OpenIncident(context.Background(), status.IncidentDefinition{Title: "Panne du nuage", Impact: status.ImpactDown, Status: status.StatusInvestigating, Message: "On regarde.", ComponentIDs: []string{all.ID}}); err != nil {
		t.Fatal(err)
	}
	snapshot, err := f.status.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Components) != 1 || snapshot.Components[0].Name != "Nuage" || len(snapshot.Open) != 1 {
		t.Fatalf("snapshot %+v", snapshot)
	}
	if snapshot.Open[0].Components[0].Name != "Nuage" || snapshot.Open[0].Components[0].ID != all.ID {
		t.Fatalf("incident components %+v", snapshot.Open[0].Components)
	}
	if len(snapshot.Components[0].Days) != 0 {
		t.Fatalf("no rollup yet, days %+v", snapshot.Components[0].Days)
	}
}

func TestPage_KeepsTheInterfaceLanguageAndDefaultsToIt(t *testing.T) {
	f := newFixture(t)
	page, err := f.status.Page()
	if err != nil {
		t.Fatal(err)
	}
	if page.Language != lang.Default || page.Title != "" {
		t.Fatalf("page %+v", page)
	}
	saved, err := f.status.SetPage(context.Background(), status.Page{Title: " Statut de la maison ", Announcement: "Coupure samedi.", Language: lang.English})
	if err != nil {
		t.Fatal(err)
	}
	if saved.Title != "Statut de la maison" || saved.Language != lang.English {
		t.Fatalf("saved %+v", saved)
	}
	if _, err := f.status.SetPage(context.Background(), status.Page{Language: "de"}); !errors.Is(err, status.ErrLanguageInvalid) {
		t.Fatalf("unknown language: %v", err)
	}
	if _, err := f.status.SetPage(context.Background(), status.Page{Announcement: strings.Repeat("a", status.MaxAnnouncementLength+1)}); !errors.Is(err, status.ErrAnnouncementInvalid) {
		t.Fatalf("long announcement: %v", err)
	}
}

func TestPurge_ForgetsIncidentsResolvedOverAYearAgo(t *testing.T) {
	f := newFixture(t)
	up := f.probe(t, "site", probe.OutcomeUp, nil)
	site := f.component(t, "Site web", status.MemberRef{Kind: status.KindProbe, ID: up.ID})
	old, err := f.status.OpenIncident(context.Background(), status.IncidentDefinition{Title: "Vieux", Impact: status.ImpactDown, Status: status.StatusInvestigating, ComponentIDs: []string{site.ID}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.status.AddUpdate(context.Background(), old.ID, status.StatusResolved, ""); err != nil {
		t.Fatal(err)
	}
	f.now = f.now.Add(status.IncidentRetention + time.Hour)
	if err := f.status.Purge(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := f.status.GetIncident(context.Background(), old.ID); !errors.Is(err, status.ErrNotFound) {
		t.Fatalf("still there: %v", err)
	}
}

func TestWatch_RefreshesOnAnyTopicOfTheBus(t *testing.T) {
	f := newFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go f.status.Watch(ctx)
	deadline := time.Now().Add(2 * time.Second)
	for f.bus.Count() == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if f.bus.Count() != 1 {
		t.Fatal("the service must listen to the bus")
	}
	// Un objet qui change sans passer par le service : seul le bus le dit.
	if _, err := f.probes.Create(ctx, probe.Definition{Name: "site", Kind: probe.KindHTTP, Target: "https://site.exemple.fr/", MachineID: machine.LocalID}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.status.CreateComponent(ctx, status.ComponentDefinition{Name: "Site web"}); err != nil {
		t.Fatal(err)
	}
	f.bus.Publish(live.TopicProbes)
	time.Sleep(50 * time.Millisecond)
	cancel()
	deadline = time.Now().Add(2 * time.Second)
	for f.bus.Count() != 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if f.bus.Count() != 0 {
		t.Fatal("the subscription must close with the context")
	}
}
