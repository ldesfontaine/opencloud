package alert_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/ldesfontaine/opencloud/internal/alert"
	"github.com/ldesfontaine/opencloud/internal/container"
	"github.com/ldesfontaine/opencloud/internal/directory"
	"github.com/ldesfontaine/opencloud/internal/machine"
	"github.com/ldesfontaine/opencloud/internal/probe"
	"github.com/ldesfontaine/opencloud/internal/status"
	"github.com/ldesfontaine/opencloud/internal/store/storetest"
)

func (f *fixture) mute(t *testing.T, definition alert.MuteDefinition) alert.Mute {
	t.Helper()
	created, err := f.CreateMute(context.Background(), definition)
	if err != nil {
		t.Fatal(err)
	}
	return created
}

// statuses rend l'état de chaque livraison de l'alerte, dans l'ordre.
func (f *fixture) statuses(t *testing.T, alertID int64) string {
	t.Helper()
	deliveries, err := f.db.ListDeliveries(context.Background(), alertID)
	if err != nil {
		t.Fatal(err)
	}
	var statuses []string
	for _, delivery := range deliveries {
		statuses = append(statuses, string(delivery.Event)+":"+string(delivery.Status))
	}
	return strings.Join(statuses, ",")
}

// Taire se juge à chaque envoi : l'ouverture sous la fenêtre est notée tue,
// et la fenêtre finie, l'aggravation et la résolution partent.
func TestMute_IsJudgedAtEachSend(t *testing.T) {
	f := newFixture(t)
	f.channel(t, "ops", alert.SeverityAttention, true)
	ctx := context.Background()
	f.mute(t, alert.MuteDefinition{Kind: alert.KindDiskFull, Duration: time.Hour, Reason: "migration"})
	f.Open(ctx, volumeFact(alert.SeverityAttention, 86))
	muted := f.only(t, alert.StatusOpen)
	if !muted.Muted || f.sender.events() != "" || f.statuses(t, muted.ID) != "opened:muted" {
		t.Fatalf("muted %v, events %q, deliveries %q", muted.Muted, f.sender.events(), f.statuses(t, muted.ID))
	}
	f.advance(2 * time.Hour)
	if f.only(t, alert.StatusOpen).Muted {
		t.Fatal("the window is over: the alert is no longer muted")
	}
	f.Open(ctx, volumeFact(alert.SeverityDanger, 96))
	f.Resolve(ctx, alert.KindDiskFull, muted.Object)
	if f.sender.events() != "aggravated:ops,resolved:ops" {
		t.Fatalf("events %q", f.sender.events())
	}
}

// Une alerte ouverte avant la fenêtre se tait aussi : sa résolution, pendant
// la fenêtre, ne part pas.
func TestMute_AlsoCoversAnAlertOpenedBefore(t *testing.T) {
	f := newFixture(t)
	f.channel(t, "ops", alert.SeverityAttention, true)
	ctx := context.Background()
	f.Open(ctx, volumeFact(alert.SeverityAttention, 86))
	f.mute(t, alert.MuteDefinition{Target: alert.Target{Kind: alert.TargetMachine, ID: machine.LocalID}, Duration: time.Hour})
	f.Resolve(ctx, alert.KindDiskFull, volumeFact(alert.SeverityAttention, 0).Object)
	if f.sender.events() != "opened:ops" {
		t.Fatalf("events %q", f.sender.events())
	}
}

func TestMute_RefusesWhatMakesNoSense_AndTakesEverythingWithoutATarget(t *testing.T) {
	f := newFixture(t)
	f.channel(t, "ops", alert.SeverityAttention, true)
	ctx := context.Background()
	refused := []struct {
		definition alert.MuteDefinition
		want       error
	}{
		{alert.MuteDefinition{Kind: "nope", Duration: time.Hour}, alert.ErrMuteInvalid},
		{alert.MuteDefinition{Target: alert.Target{Kind: "volume", ID: "x"}, Duration: time.Hour}, alert.ErrMuteInvalid},
		{alert.MuteDefinition{Target: alert.Target{Kind: alert.TargetMachine}, Duration: time.Hour}, alert.ErrMuteInvalid},
		{alert.MuteDefinition{Duration: 8 * 24 * time.Hour}, alert.ErrMuteDurationInvalid},
		{alert.MuteDefinition{Duration: time.Hour, StartsAt: f.clock.Add(400 * 24 * time.Hour)}, alert.ErrMuteStartInvalid},
		{alert.MuteDefinition{Target: alert.Target{Kind: alert.TargetProbe, ID: "inconnue"}, Duration: time.Hour}, alert.ErrMuteTargetUnknown},
	}
	for _, refusal := range refused {
		if _, err := f.CreateMute(ctx, refusal.definition); !errors.Is(err, refusal.want) {
			t.Errorf("%+v: %v, want %v", refusal.definition, err, refusal.want)
		}
	}
	everything := f.mute(t, alert.MuteDefinition{Duration: time.Hour, Reason: "  bascule  "})
	if everything.Reason != "bascule" || !everything.EndsAt.Equal(f.clock.Add(time.Hour)) {
		t.Fatalf("mute %+v", everything)
	}
	f.Open(ctx, volumeFact(alert.SeverityAttention, 86))
	if !f.only(t, alert.StatusOpen).Muted || f.sender.events() != "" {
		t.Fatal("a window without a target mutes everything")
	}
	if err := f.DeleteMute(ctx, everything.ID); err != nil {
		t.Fatal(err)
	}
	if mutes, err := f.db.ListMutes(ctx); err != nil || len(mutes) != 0 {
		t.Fatalf("mutes %+v, %v", mutes, err)
	}
	if err := f.DeleteMute(ctx, everything.ID); !errors.Is(err, alert.ErrNotFound) {
		t.Fatalf("delete twice: %v", err)
	}
}

// Annoncer exige un titre public et une cible de l'annuaire ; un titre sans
// la case est refusé, et un silence annoncé se relit tel qu'écrit.
func TestMute_AnnounceNeedsAPublicTitleAndADirectoryTarget(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	machineTarget := alert.Target{Kind: alert.TargetMachine, ID: machine.LocalID}
	refused := []alert.MuteDefinition{
		{Target: machineTarget, Duration: time.Hour, Announce: true},
		{Target: machineTarget, Duration: time.Hour, Announce: true, PublicTitle: "   "},
		{Target: machineTarget, Duration: time.Hour, Announce: true, PublicTitle: strings.Repeat("é", alert.MaxPublicTitleLength+1)},
		{Target: machineTarget, Duration: time.Hour, Announce: true, PublicTitle: "deux\nlignes"},
		{Duration: time.Hour, Announce: true, PublicTitle: "Tout"},
		{Target: alert.Target{Kind: alert.TargetContainer, ID: "x"}, Duration: time.Hour, Announce: true, PublicTitle: "Conteneur"},
		{Target: machineTarget, Duration: time.Hour, PublicTitle: "Sans la case"},
	}
	for _, definition := range refused {
		if _, err := f.CreateMute(ctx, definition); !errors.Is(err, alert.ErrMuteAnnounceInvalid) {
			t.Errorf("%+v: %v, want %v", definition, err, alert.ErrMuteAnnounceInvalid)
		}
	}
	announced := f.mute(t, alert.MuteDefinition{Target: machineTarget, Duration: time.Hour, Announce: true, PublicTitle: "  Migration du disque  "})
	if !announced.Announce || announced.PublicTitle != "Migration du disque" {
		t.Fatalf("announced %+v", announced)
	}
	plain := f.mute(t, alert.MuteDefinition{Target: machineTarget, Duration: time.Hour})
	mutes, err := f.db.ListMutes(ctx)
	if err != nil || len(mutes) != 2 {
		t.Fatalf("mutes %+v, %v", mutes, err)
	}
	for _, found := range mutes {
		if found.ID == announced.ID && (!found.Announce || found.PublicTitle != "Migration du disque") {
			t.Errorf("the announced mute lost its announcement: %+v", found)
		}
		if found.ID == plain.ID && (found.Announce || found.PublicTitle != "") {
			t.Errorf("a plain mute must stay internal: %+v", found)
		}
	}
}

// Une fenêtre planifiée ne tait rien avant son heure.
func TestMute_PlannedWindowWaitsForItsStart(t *testing.T) {
	f := newFixture(t)
	f.channel(t, "ops", alert.SeverityAttention, true)
	ctx := context.Background()
	planned := f.mute(t, alert.MuteDefinition{
		Target:   alert.Target{Kind: alert.TargetMachine, ID: machine.LocalID},
		StartsAt: f.clock.Add(time.Hour), Duration: time.Hour,
	})
	if planned.Target.Name != "opencloud" || !planned.StartsAt.Equal(f.clock.Add(time.Hour)) {
		t.Fatalf("planned %+v", planned)
	}
	f.Open(ctx, volumeFact(alert.SeverityAttention, 86))
	f.advance(time.Hour)
	f.Open(ctx, volumeFact(alert.SeverityDanger, 96))
	if f.sender.events() != "opened:ops" || !f.only(t, alert.StatusOpen).Muted {
		t.Fatalf("events %q", f.sender.events())
	}
}

// Une fenêtre tue ne prend rien à la part de la machine sur le canal : la
// fenêtre finie, la première alerte suivante part.
func TestMute_DoesNotTakeTheMachineShare(t *testing.T) {
	f := newFixture(t)
	f.channel(t, "ops", alert.SeverityAttention, true)
	ctx := context.Background()
	f.mute(t, alert.MuteDefinition{Kind: alert.KindDiskFull, Duration: time.Minute})
	for index := range alert.MachineRateLimit + 1 {
		fact := volumeFact(alert.SeverityAttention, 86)
		mount := "/data" + strings.Repeat("x", index)
		fact.Object.ID, fact.Object.Name, fact.Details.MountPoint = alert.VolumeID(machine.LocalID, mount), mount, mount
		f.Open(ctx, fact)
	}
	f.advance(2 * time.Minute)
	f.Open(ctx, alert.Fact{
		Kind: alert.KindMachineLost, Severity: alert.SeverityDanger,
		Object: alert.Object{Kind: alert.ObjectMachine, ID: machine.LocalID, Name: "opencloud"}, MachineID: machine.LocalID,
	})
	if f.sender.events() != "opened:ops" {
		t.Fatalf("events %q", f.sender.events())
	}
}

// L'alerte d'un conteneur porte le service déclaré de son unité, celle
// d'une sonde le service qu'elle surveille : taire le service les tait,
// jamais un conteneur qu'aucune unité ne porte.
func TestMute_OnADeclaredService_CoversItsContainersAndProbes_NotAStranger(t *testing.T) {
	f := newFixture(t)
	f.channel(t, "ops", alert.SeverityAttention, true)
	ctx := context.Background()
	declared := directory.New(f.db, discardLogger())
	tracker := container.New(f.db, discardLogger())
	tracker.SetClock(func() time.Time { return f.clock })
	tracker.SetAlerter(f.Engine)
	tracker.SetUnits(declared)
	declared.SetRematcher(tracker)
	shop, err := declared.CreateService(ctx, directory.ServiceDefinition{
		MachineID: machine.LocalID, Name: "shop", Nature: directory.NatureApplication, Origin: directory.OriginCompose,
		ComposeProject: "shop", Units: []directory.UnitDefinition{{Name: "app", Image: "app", Version: "1"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	f.mute(t, alert.MuteDefinition{Target: alert.Target{Kind: alert.TargetService, ID: shop.ID}, Duration: time.Hour})

	stopped := func(id, name, group, service string) container.Observed {
		return container.Observed{
			ContainerID: strings.Repeat(id, 64), Name: name, Group: group, ComposeService: service, Image: "app:1",
			State: container.StateExited, ExitCode: 1, CreatedAt: f.clock.Add(-time.Hour),
		}
	}
	report := container.Report{Complete: true, Inventory: []container.Observed{stopped("a", "shop-app-1", "shop", "app"), stopped("f", "stranger", "", "")}}
	if err := tracker.Record(ctx, machine.LocalID, report); err != nil {
		t.Fatal(err)
	}
	byName := map[string]alert.Alert{}
	for _, found := range f.open(t, alert.StatusOpen) {
		byName[found.Object.Name] = found
	}
	app, stranger := byName["shop-app-1"], byName["stranger"]
	if app.Object.Kind != alert.ObjectContainer || app.ServiceID != shop.ID || !app.Muted {
		t.Fatalf("the container of the service: %+v", app)
	}
	if stranger.ServiceID != "" || stranger.Muted || f.sender.events() != "opened:ops" {
		t.Fatalf("the stranger: %+v, events %q", stranger, f.sender.events())
	}

	probes := probe.New(f.db, discardLogger())
	probes.SetClock(func() time.Time { return f.clock })
	probes.SetAlerter(f.Engine)
	watching, err := probes.Create(ctx, probe.Definition{Name: "boutique", Kind: probe.KindTCP, Target: "127.0.0.1:8080", MachineID: machine.LocalID, ServiceID: shop.ID})
	if err != nil {
		t.Fatal(err)
	}
	for range 3 {
		f.advance(time.Minute)
		if err := probes.Record(ctx, machine.LocalID, probe.Report{Results: []probe.Result{f.result(watching.ID, probe.OutcomeDown, nil)}}); err != nil {
			t.Fatal(err)
		}
	}
	for _, found := range f.open(t, alert.StatusOpen) {
		if found.Object.Kind == alert.ObjectProbe && (found.ServiceID != shop.ID || !found.Muted) {
			t.Fatalf("the probe of the service: %+v", found)
		}
	}
	if f.sender.events() != "opened:ops" {
		t.Fatalf("events %q", f.sender.events())
	}
}

// Une maintenance de la page de statut est ce que l'opérateur dit au
// public : elle ne tait plus rien. Le moteur ne lit plus la page.
func TestMaintenance_OfTheStatusPage_NoLongerMutes(t *testing.T) {
	f := newFixture(t)
	f.channel(t, "ops", alert.SeverityAttention, true)
	ctx := context.Background()
	page := status.New(f.db, f.db, f.machines, discardLogger())
	page.SetClock(func() time.Time { return f.clock })
	_, err := page.CreateComponent(ctx, status.ComponentDefinition{PageID: storetest.StatusPage(t, f.db, "statut"), Name: "Nuage", Members: []status.MemberRef{{Kind: status.KindMachine, ID: machine.LocalID}}})
	if err != nil {
		t.Fatal(err)
	}
	maintenance, err := page.OpenIncident(ctx, status.IncidentDefinition{
		Title: "Migration", Impact: status.ImpactMaintenance, Status: status.StatusScheduled,
		Targets: []status.MemberRef{{Kind: status.KindMachine, ID: machine.LocalID}}, StartsAt: f.clock.Add(-time.Minute), EndsAt: f.clock.Add(time.Hour),
	})
	if err != nil || maintenance.Status != status.StatusInProgress {
		t.Fatalf("maintenance %+v, %v", maintenance, err)
	}
	f.Open(ctx, volumeFact(alert.SeverityAttention, 86))
	if f.only(t, alert.StatusOpen).Muted || f.sender.events() != "opened:ops" {
		t.Fatalf("a maintenance mutes nothing: events %q", f.sender.events())
	}
}

// Une sonde retirée emporte ses fenêtres, par la clé étrangère ; une sonde
// mise en pause garde les siennes, elle peut revenir.
func TestProbeRemoved_TakesItsMutes_APauseKeepsThem(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	probes := probe.New(f.db, discardLogger())
	probes.SetAlerter(f.Engine)
	var created []probe.Probe
	for _, name := range []string{"site", "api"} {
		found, err := probes.Create(ctx, probe.Definition{Name: name, Kind: probe.KindTCP, Target: "127.0.0.1:443", MachineID: machine.LocalID})
		if err != nil {
			t.Fatal(err)
		}
		f.mute(t, alert.MuteDefinition{Target: alert.Target{Kind: alert.TargetProbe, ID: found.ID}, Duration: time.Hour})
		created = append(created, found)
	}
	removed, paused := created[0], created[1]
	if err := probes.Delete(ctx, removed.ID); err != nil {
		t.Fatal(err)
	}
	if err := probes.Pause(ctx, paused.ID); err != nil {
		t.Fatal(err)
	}
	mutes, err := f.db.ListMutes(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(mutes) != 1 || mutes[0].Target.ID != paused.ID || mutes[0].Target.Name != "api" {
		t.Fatalf("mutes %+v", mutes)
	}
}

func TestPurge_ForgetsLongEndedMutesOnly(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	f.mute(t, alert.MuteDefinition{Kind: alert.KindDiskFull, Duration: time.Hour})
	f.advance(alert.MuteRetention + 2*time.Hour)
	f.mute(t, alert.MuteDefinition{Kind: alert.KindProbeDown, Duration: time.Hour})
	if err := f.Purge(ctx); err != nil {
		t.Fatal(err)
	}
	mutes, err := f.db.ListMutes(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(mutes) != 1 || mutes[0].Kind != alert.KindProbeDown {
		t.Fatalf("mutes %+v", mutes)
	}
}
