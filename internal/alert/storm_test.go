package alert_test

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/ldesfontaine/opencloud/internal/alert"
	"github.com/ldesfontaine/opencloud/internal/lang"
	"github.com/ldesfontaine/opencloud/internal/machine"
	"github.com/ldesfontaine/opencloud/internal/service"
)

// stormCounts est ce qu'une tempête a produit : alertes ouvertes en tout,
// résolues, et livraisons confiées au notifieur.
type stormCounts struct {
	opened, resolved, deliveries int
}

// replayOOMLoop rejoue un conteneur en boucle OOM : un die OOM puis un
// start, en alternance toutes les 10 s pendant 3 min, chacun dans son
// signal, le balayage toutes les 30 s comme en production.
func (f *fixture) replayOOMLoop(t *testing.T, tracker *service.Tracker) {
	t.Helper()
	ctx := context.Background()
	for step := range 18 {
		f.advance(10 * time.Second)
		event := f.event("start", service.StateRunning, 0, "", false)
		if step%2 == 0 {
			event = f.event("die", service.StateExited, 137, "", false)
			event.Container.OOMKilled = true
		}
		if err := tracker.Record(ctx, machine.LocalID, service.Report{Events: []service.Event{event}}); err != nil {
			t.Fatal(err)
		}
		if step%3 == 2 {
			f.Sweep(ctx)
		}
	}
}

func (f *fixture) stormCounts(t *testing.T) stormCounts {
	t.Helper()
	opened := f.open(t, alert.StatusOpen)
	resolved := f.open(t, alert.StatusResolved)
	return stormCounts{opened: len(opened) + len(resolved), resolved: len(resolved), deliveries: len(f.sender.jobs)}
}

func TestService_OOMRestartLoop_DoesNotFloodTheChannel(t *testing.T) {
	f := newFixture(t)
	f.channel(t, "équipe", alert.SeverityAttention, true)
	tracker := f.tracker(t)
	f.replayOOMLoop(t, tracker)
	// Puis le calme : le service tient, le balayage passe toutes les 30 s.
	for range 24 {
		f.advance(30 * time.Second)
		f.Sweep(context.Background())
	}
	counts := f.stormCounts(t)
	t.Logf("storm: %d alerts opened, %d resolved, %d deliveries: %s", counts.opened, counts.resolved, counts.deliveries, f.sender.events())
	// Avant l'anti-rebond : 10 ouvertes, 10 résolues, 21 envois.
	if counts != (stormCounts{opened: 1, resolved: 1, deliveries: 3}) || f.sender.events() != "opened:équipe,aggravated:équipe,resolved:équipe" {
		t.Fatalf("a restart loop must be one alert, told three times: %+v %s", counts, f.sender.events())
	}
	resolved := f.only(t, alert.StatusResolved)
	if resolved.Details.Count < alert.RestartLoopThreshold || resolved.Details.Reason != alert.ReasonOOM || !resolved.IsLoop() {
		t.Fatalf("the loop keeps its counter and its reason: %+v", resolved.Details)
	}
}

// Un canal qui ne reçoit que le danger apprend la fin de ce qu'on lui a
// annoncé, même redescendu en attention.
func TestService_DangerOnlyChannel_HearsTheEndOfACrashItWasTold(t *testing.T) {
	f := newFixture(t)
	f.channel(t, "astreinte", alert.SeverityDanger, true)
	ctx := context.Background()
	tracker := f.tracker(t)
	die := f.event("die", service.StateExited, 1, "", false)
	if err := tracker.Record(ctx, machine.LocalID, service.Report{Events: []service.Event{die}}); err != nil {
		t.Fatal(err)
	}
	f.advance(10 * time.Second)
	if err := tracker.Record(ctx, machine.LocalID, service.Report{Events: []service.Event{f.event("start", service.StateRunning, 0, "", false)}}); err != nil {
		t.Fatal(err)
	}
	if f.kinds(t) != "service_restart:attention" {
		t.Fatalf("kinds %q", f.kinds(t))
	}
	f.advance(alert.RestartWindow + time.Second)
	f.Sweep(ctx)
	if f.sender.events() != "opened:astreinte,resolved:astreinte" {
		t.Fatalf("events %s", f.sender.events())
	}
}

// Une santé qui échoue, revient, échoue : la même alerte, résolue après
// ServiceClearDelay de calme seulement.
func TestService_FlappingHealth_KeepsOneAlertUntilCalm(t *testing.T) {
	f := newFixture(t)
	f.channel(t, "équipe", alert.SeverityAttention, true)
	ctx := context.Background()
	tracker := f.tracker(t)
	for range 4 {
		f.advance(20 * time.Second)
		if err := tracker.Record(ctx, machine.LocalID, service.Report{Events: []service.Event{f.event("health_status", service.StateRunning, 0, service.HealthUnhealthy, false)}}); err != nil {
			t.Fatal(err)
		}
		f.advance(20 * time.Second)
		if err := tracker.Record(ctx, machine.LocalID, service.Report{Events: []service.Event{f.event("health_status", service.StateRunning, 0, service.HealthHealthy, false)}}); err != nil {
			t.Fatal(err)
		}
		f.Sweep(ctx)
	}
	if f.kinds(t) != "service_unhealthy:attention" {
		t.Fatalf("kinds %q", f.kinds(t))
	}
	f.advance(alert.ServiceClearDelay - time.Second)
	f.Sweep(ctx)
	if f.kinds(t) != "service_unhealthy:attention" {
		t.Fatalf("resolved before the delay: %q", f.kinds(t))
	}
	f.advance(2 * time.Second)
	f.Sweep(ctx)
	if f.kinds(t) != "" || f.sender.events() != "opened:équipe,resolved:équipe" {
		t.Fatalf("kinds %q, events %s", f.kinds(t), f.sender.events())
	}
}

// Au-delà de ChannelRateLimit envois sur la fenêtre, le canal ne reçoit
// plus qu'un résumé, et les livraisons retenues restent lisibles.
func TestDeliver_ChannelRateLimit_HoldsTheSurplusAndSummarizesIt(t *testing.T) {
	f := newFixture(t)
	f.channel(t, "équipe", alert.SeverityAttention, false)
	ctx := context.Background()
	for i := range alert.ChannelRateLimit + 5 {
		mount := fmt.Sprintf("/data%02d", i)
		f.Open(ctx, alert.Fact{
			Kind: alert.KindDiskFull, Severity: alert.SeverityDanger,
			Object: alert.Object{Kind: alert.ObjectVolume, ID: alert.VolumeID(machine.LocalID, mount), Name: mount}, MachineID: machine.LocalID,
			Details: alert.Details{MountPoint: mount, Percent: 97},
		})
	}
	if got := len(f.sender.jobs); got != alert.ChannelRateLimit {
		t.Fatalf("sent %d, want %d", got, alert.ChannelRateLimit)
	}
	last := f.open(t, alert.StatusOpen)[0]
	deliveries, err := f.Deliveries(ctx, last.ID)
	if err != nil || len(deliveries) != 1 || deliveries[0].Status != alert.DeliveryHeld {
		t.Fatalf("deliveries %+v %v", deliveries, err)
	}
	f.Sweep(ctx)
	if got := len(f.sender.jobs); got != alert.ChannelRateLimit {
		t.Fatalf("the summary waits for room in the window: %d", got)
	}
	f.advance(alert.ChannelRateWindow)
	f.Sweep(ctx)
	f.Sweep(ctx)
	summary := f.sender.jobs[len(f.sender.jobs)-1]
	if len(f.sender.jobs) != alert.ChannelRateLimit+1 || summary.Delivery.Event != alert.EventSummary || summary.Held != 5 {
		t.Fatalf("summary %+v (%d jobs)", summary, len(f.sender.jobs))
	}
	catalogs, err := lang.Load()
	if err != nil {
		t.Fatal(err)
	}
	body, err := alert.RenderSummary(catalogs.For(lang.French), alert.FormatText, summary.Held, f.clock, "https://oc.exemple.fr/alertes")
	if err != nil || !strings.Contains(string(body.Content), "5 alertes de plus, voir openCloud") {
		t.Fatalf("body %q %v", body.Content, err)
	}
}
