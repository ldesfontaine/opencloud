package alert_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/ldesfontaine/opencloud/internal/alert"
	"github.com/ldesfontaine/opencloud/internal/machine"
	"github.com/ldesfontaine/opencloud/internal/probe"
)

func TestOpen_SameKeyDedups_HigherSeverityAggravates(t *testing.T) {
	f := newFixture(t)
	f.channel(t, "ops", alert.SeverityAttention, true)
	ctx := context.Background()
	f.Open(ctx, volumeFact(alert.SeverityAttention, 86))
	f.Open(ctx, volumeFact(alert.SeverityAttention, 86))
	opened := f.only(t, alert.StatusOpen)
	if opened.Severity != alert.SeverityAttention || opened.Details.Percent != 86 || opened.MachineName != "opencloud" {
		t.Fatalf("alert %+v", opened)
	}
	if _, err := f.Acknowledge(ctx, opened.ID); err != nil {
		t.Fatal(err)
	}
	// Un détail qui bouge se lit dans la page, sans prévenir personne.
	f.Open(ctx, volumeFact(alert.SeverityAttention, 88))
	if f.only(t, alert.StatusOpen).Details.Percent != 88 || f.sender.events() != "opened:ops" {
		t.Fatalf("details %+v, events %q", f.only(t, alert.StatusOpen).Details, f.sender.events())
	}
	f.advance(time.Minute)
	f.Open(ctx, volumeFact(alert.SeverityDanger, 96))
	aggravated := f.only(t, alert.StatusOpen)
	if aggravated.ID != opened.ID || aggravated.Severity != alert.SeverityDanger || aggravated.IsAcknowledged() || !aggravated.UpdatedAt.After(opened.UpdatedAt) {
		t.Fatalf("aggravated %+v", aggravated)
	}
	if f.sender.events() != "opened:ops,aggravated:ops" {
		t.Fatalf("events %q", f.sender.events())
	}
	// Redescendre ne dégrade jamais : l'alerte garde sa gravité jusqu'à sa fin.
	f.Open(ctx, volumeFact(alert.SeverityAttention, 90))
	if f.only(t, alert.StatusOpen).Severity != alert.SeverityDanger {
		t.Fatal("severity went down")
	}
}

func TestResolve_ClosesAndTellsOnlyTheChannelsThatWantIt(t *testing.T) {
	f := newFixture(t)
	f.channel(t, "ops", alert.SeverityAttention, true)
	f.channel(t, "pager", alert.SeverityDanger, false)
	ctx := context.Background()
	f.Open(ctx, volumeFact(alert.SeverityDanger, 97))
	f.advance(time.Hour)
	f.Resolve(ctx, alert.KindDiskFull, volumeFact(alert.SeverityDanger, 0).Object)
	f.Resolve(ctx, alert.KindDiskFull, volumeFact(alert.SeverityDanger, 0).Object)
	resolved := f.only(t, alert.StatusResolved)
	if !resolved.ResolvedAt.Equal(f.clock) || len(f.open(t, alert.StatusOpen)) != 0 {
		t.Fatalf("resolved %+v", resolved)
	}
	if f.sender.events() != "opened:ops,opened:pager,resolved:ops" {
		t.Fatalf("events %q", f.sender.events())
	}
	deliveries, err := f.Deliveries(ctx, resolved.ID)
	if err != nil || len(deliveries) != 3 || deliveries[0].Status != alert.DeliveryPending {
		t.Fatalf("deliveries %+v, %v", deliveries, err)
	}
	// Une alerte fermée ne s'acquitte plus.
	if _, err := f.Acknowledge(ctx, resolved.ID); !errors.Is(err, alert.ErrAlreadyClosed) {
		t.Fatalf("acknowledge a resolved alert: %v", err)
	}
}

func TestChannel_MinSeverityAndDisabledFilter(t *testing.T) {
	f := newFixture(t)
	f.channel(t, "pager", alert.SeverityDanger, true)
	off := f.channel(t, "off", alert.SeverityAttention, true)
	ctx := context.Background()
	if _, err := f.UpdateChannel(ctx, off.ID, alert.ChannelDefinition{Name: "off", URL: off.URL, Format: off.Format, MinSeverity: off.MinSeverity, Enabled: false}); err != nil {
		t.Fatal(err)
	}
	f.Open(ctx, volumeFact(alert.SeverityAttention, 86))
	if f.sender.events() != "" {
		t.Fatalf("events %q", f.sender.events())
	}
	f.Open(ctx, volumeFact(alert.SeverityDanger, 96))
	if f.sender.events() != "aggravated:pager" {
		t.Fatalf("events %q", f.sender.events())
	}
}

func TestOpen_UnderASilence_IsShownNeverDelivered(t *testing.T) {
	f := newFixture(t)
	f.channel(t, "ops", alert.SeverityAttention, true)
	ctx := context.Background()
	if _, err := f.CreateSilence(ctx, alert.SilenceDefinition{Kind: alert.KindDiskFull, Duration: time.Hour, Reason: "migration"}); err != nil {
		t.Fatal(err)
	}
	f.Open(ctx, volumeFact(alert.SeverityAttention, 86))
	silenced := f.only(t, alert.StatusOpen)
	if !silenced.Silenced || f.sender.events() != "" {
		t.Fatalf("silenced %v, events %q", silenced.Silenced, f.sender.events())
	}
	// Le silence tombé, l'alerte reste silencieuse : même aggravée, même résolue.
	f.advance(2 * time.Hour)
	f.Open(ctx, volumeFact(alert.SeverityDanger, 96))
	f.Resolve(ctx, alert.KindDiskFull, silenced.Object)
	if f.sender.events() != "" {
		t.Fatalf("events %q", f.sender.events())
	}
	// Mais une alerte nouvelle part.
	f.Open(ctx, volumeFact(alert.SeverityAttention, 86))
	if f.sender.events() != "opened:ops" || f.only(t, alert.StatusOpen).Silenced {
		t.Fatalf("events %q", f.sender.events())
	}
}

func TestSilence_ByMachineCoversWhatLivesOnIt_AndNeedsAFilter(t *testing.T) {
	f := newFixture(t)
	f.channel(t, "ops", alert.SeverityAttention, true)
	ctx := context.Background()
	if _, err := f.CreateSilence(ctx, alert.SilenceDefinition{Duration: time.Hour}); !errors.Is(err, alert.ErrSilenceInvalid) {
		t.Fatalf("silence without filter: %v", err)
	}
	if _, err := f.CreateSilence(ctx, alert.SilenceDefinition{Kind: alert.KindDiskFull, Duration: 8 * 24 * time.Hour}); !errors.Is(err, alert.ErrSilenceDurationInvalid) {
		t.Fatalf("silence too long: %v", err)
	}
	silence, err := f.CreateSilence(ctx, alert.SilenceDefinition{Object: alert.Object{Kind: alert.ObjectMachine, ID: machine.LocalID, Name: "opencloud"}, Duration: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	f.Open(ctx, volumeFact(alert.SeverityAttention, 86))
	if !f.only(t, alert.StatusOpen).Silenced {
		t.Fatal("a volume of a silenced machine should be silenced")
	}
	if err := f.DeleteSilence(ctx, silence.ID); err != nil {
		t.Fatal(err)
	}
	if silences, err := f.Silences(ctx); err != nil || len(silences) != 0 {
		t.Fatalf("silences %v, %v", silences, err)
	}
}

func TestOpen_UnderMaintenance_IsSilenced_VolumeFollowsItsMachine(t *testing.T) {
	f := newFixture(t)
	f.channel(t, "ops", alert.SeverityAttention, true)
	f.maintenance.under["machine:"+machine.LocalID] = true
	f.Open(context.Background(), volumeFact(alert.SeverityAttention, 86))
	if !f.only(t, alert.StatusOpen).Silenced || f.sender.events() != "" {
		t.Fatal("an alert under maintenance should be silenced")
	}
}

func TestGone_ForAMachine_ResolvesEverythingOnIt(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	f.Open(ctx, volumeFact(alert.SeverityAttention, 86))
	other := volumeFact(alert.SeverityAttention, 90)
	other.Object.ID, other.Object.Name, other.Details.MountPoint = alert.VolumeID(machine.LocalID, "/"), "/", "/"
	f.Open(ctx, other)
	if len(f.open(t, alert.StatusOpen)) != 2 {
		t.Fatal("two volumes, two alerts")
	}
	f.Gone(ctx, alert.Object{Kind: alert.ObjectMachine, ID: machine.LocalID})
	if len(f.open(t, alert.StatusOpen)) != 0 || len(f.open(t, alert.StatusResolved)) != 2 {
		t.Fatal("the machine should take its volumes with it")
	}
}

// Une sonde supprimée directement en base, comme une machine retirée
// emporte les siennes : la clé étrangère passe à NULL et le balayage
// résout ce qui restait ouvert.
func TestSweep_ResolvesAlertsWhoseObjectVanished(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	probes := probe.New(f.db, discardLogger())
	created, err := probes.Create(ctx, probe.Definition{Name: "base", Kind: probe.KindTCP, Target: "127.0.0.1:5432", MachineID: machine.LocalID})
	if err != nil {
		t.Fatal(err)
	}
	f.Open(ctx, alert.Fact{
		Kind: alert.KindProbeDown, Severity: alert.SeverityDanger,
		Object: alert.Object{Kind: alert.ObjectProbe, ID: created.ID, Name: created.Name}, MachineID: machine.LocalID,
		Details: alert.Details{Target: created.Target, Reason: "refused"},
	})
	f.Sweep(ctx)
	if len(f.open(t, alert.StatusOpen)) != 1 {
		t.Fatal("the probe exists: nothing to sweep")
	}
	if err := f.db.DeleteProbe(ctx, created.ID); err != nil {
		t.Fatal(err)
	}
	f.Sweep(ctx)
	resolved := f.only(t, alert.StatusResolved)
	if resolved.Object.Name != "base" {
		t.Fatalf("the name should survive the object: %+v", resolved)
	}
}

func TestOpen_FactWithoutObject_IsRefused(t *testing.T) {
	f := newFixture(t)
	fact := volumeFact(alert.SeverityAttention, 86)
	fact.Object.ID = ""
	f.Open(context.Background(), fact)
	if len(f.open(t, alert.StatusOpen)) != 0 {
		t.Fatal("a fact without object must never open an alert")
	}
}

func TestCount_AcknowledgedLeavesTheRedCounter(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	f.Open(ctx, volumeFact(alert.SeverityAttention, 86))
	opened := f.only(t, alert.StatusOpen)
	if _, err := f.Acknowledge(ctx, opened.ID); err != nil {
		t.Fatal(err)
	}
	counts, err := f.Count(ctx)
	if err != nil || counts.Open != 1 || counts.Unacknowledged != 0 {
		t.Fatalf("counts %+v, %v", counts, err)
	}
	if f.watcher.changes < 2 {
		t.Fatalf("the live should have been told twice, got %d", f.watcher.changes)
	}
}

func TestRequeue_ReplaysPendingDeliveries(t *testing.T) {
	f := newFixture(t)
	f.channel(t, "ops", alert.SeverityAttention, true)
	ctx := context.Background()
	f.Open(ctx, volumeFact(alert.SeverityAttention, 86))
	f.sender.jobs = nil
	if err := f.Requeue(ctx); err != nil {
		t.Fatal(err)
	}
	if f.sender.events() != "opened:ops" {
		t.Fatalf("events %q", f.sender.events())
	}
}

func TestPurge_ForgetsOldResolvedAlertsOnly(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	f.Open(ctx, volumeFact(alert.SeverityAttention, 86))
	f.Resolve(ctx, alert.KindDiskFull, volumeFact(alert.SeverityAttention, 0).Object)
	f.Open(ctx, volumeFact(alert.SeverityAttention, 86))
	f.advance(alert.ResolvedRetention + time.Hour)
	if err := f.Purge(ctx); err != nil {
		t.Fatal(err)
	}
	if len(f.open(t, alert.StatusResolved)) != 0 || len(f.open(t, alert.StatusOpen)) != 1 {
		t.Fatal("purge should forget the resolved one and keep the open one")
	}
}

func TestChannel_SecretStaysAndCanBeCleared(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	created, err := f.CreateChannel(ctx, alert.ChannelDefinition{Name: "ops", URL: "https://example.org/hook", Format: alert.FormatSlack, Secret: "s3cret", MinSeverity: alert.SeverityAttention, Enabled: true})
	if err != nil || !created.HasSecret {
		t.Fatalf("channel %+v, %v", created, err)
	}
	updated, err := f.UpdateChannel(ctx, created.ID, alert.ChannelDefinition{Name: "ops", URL: "https://example.org/hook", Format: alert.FormatSlack, MinSeverity: alert.SeverityDanger, Enabled: true})
	if err != nil || !updated.HasSecret || updated.Secret != "s3cret" || updated.MinSeverity != alert.SeverityDanger {
		t.Fatalf("an empty secret should keep the one in place: %+v, %v", updated, err)
	}
	cleared, err := f.UpdateChannel(ctx, created.ID, alert.ChannelDefinition{Name: "ops", URL: "https://example.org/hook", Format: alert.FormatSlack, ClearSecret: true, MinSeverity: alert.SeverityDanger, Enabled: true})
	if err != nil || cleared.HasSecret {
		t.Fatalf("clear secret: %+v, %v", cleared, err)
	}
	_, err = f.CreateChannel(ctx, alert.ChannelDefinition{Name: "meta", URL: "http://169.254.169.254/", Format: alert.FormatJSON, MinSeverity: alert.SeverityAttention})
	if !errors.Is(err, alert.ErrChannelURLForbidden) {
		t.Fatalf("link-local url: %v", err)
	}
	_, err = f.CreateChannel(ctx, alert.ChannelDefinition{Name: "ftp", URL: "ftp://example.org/", Format: alert.FormatJSON, MinSeverity: alert.SeverityAttention})
	if !errors.Is(err, alert.ErrChannelURLInvalid) {
		t.Fatalf("ftp url: %v", err)
	}
}
