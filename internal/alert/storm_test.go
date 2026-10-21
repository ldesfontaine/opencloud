package alert_test

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/ldesfontaine/opencloud/internal/alert"
	"github.com/ldesfontaine/opencloud/internal/container"
	"github.com/ldesfontaine/opencloud/internal/lang"
	"github.com/ldesfontaine/opencloud/internal/machine"
)

// stormCounts est ce qu'une tempête a produit : alertes ouvertes en tout,
// résolues, et livraisons confiées au notifieur.
type stormCounts struct {
	opened, resolved, deliveries int
}

// replayOOMLoop rejoue un conteneur en boucle OOM : un die OOM puis un
// start, en alternance toutes les 10 s pendant 3 min, chacun dans son
// signal, le balayage toutes les 30 s comme en production.
func (f *fixture) replayOOMLoop(t *testing.T, tracker *container.Tracker) {
	t.Helper()
	ctx := context.Background()
	for step := range 18 {
		f.advance(10 * time.Second)
		event := f.event("start", container.StateRunning, 0, "", false)
		if step%2 == 0 {
			event = f.event("die", container.StateExited, 137, "", false)
			event.Container.OOMKilled = true
		}
		if err := tracker.Record(ctx, machine.LocalID, container.Report{Events: []container.Event{event}}); err != nil {
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
	die := f.event("die", container.StateExited, 1, "", false)
	if err := tracker.Record(ctx, machine.LocalID, container.Report{Events: []container.Event{die}}); err != nil {
		t.Fatal(err)
	}
	f.advance(10 * time.Second)
	if err := tracker.Record(ctx, machine.LocalID, container.Report{Events: []container.Event{f.event("start", container.StateRunning, 0, "", false)}}); err != nil {
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
		if err := tracker.Record(ctx, machine.LocalID, container.Report{Events: []container.Event{f.event("health_status", container.StateRunning, 0, container.HealthUnhealthy, false)}}); err != nil {
			t.Fatal(err)
		}
		f.advance(20 * time.Second)
		if err := tracker.Record(ctx, machine.LocalID, container.Report{Events: []container.Event{f.event("health_status", container.StateRunning, 0, container.HealthHealthy, false)}}); err != nil {
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

// Au-delà de sa part (MachineRateLimit) sur la fenêtre, une machine ne fait
// plus recevoir au canal qu'un résumé, et les livraisons retenues restent
// lisibles.
func TestDeliver_MachineShare_HoldsTheSurplusAndSummarizesIt(t *testing.T) {
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
	if got := len(f.sender.jobs); got != alert.MachineRateLimit {
		t.Fatalf("sent %d, want %d", got, alert.MachineRateLimit)
	}
	last := f.open(t, alert.StatusOpen)[0]
	deliveries, err := f.db.ListDeliveries(ctx, last.ID)
	if err != nil || len(deliveries) != 1 || deliveries[0].Status != alert.DeliveryHeld {
		t.Fatalf("deliveries %+v %v", deliveries, err)
	}
	f.Sweep(ctx)
	if got := len(f.sender.jobs); got != alert.MachineRateLimit {
		t.Fatalf("the summary waits for room in the window: %d", got)
	}
	f.advance(alert.ChannelRateWindow)
	f.Sweep(ctx)
	f.Sweep(ctx)
	summary := f.sender.jobs[len(f.sender.jobs)-1]
	held := alert.ChannelRateLimit + 5 - alert.MachineRateLimit
	if len(f.sender.jobs) != alert.MachineRateLimit+1 || summary.Delivery.Event != alert.EventSummary || summary.Held.Count != held {
		t.Fatalf("summary %+v (%d jobs)", summary, len(f.sender.jobs))
	}
	catalogs, err := lang.Load()
	if err != nil {
		t.Fatal(err)
	}
	body, err := alert.RenderSummary(catalogs.For(lang.French), alert.FormatText, summary.Held, f.clock, "https://oc.exemple.fr/alertes")
	if err != nil || !strings.Contains(string(body.Content), "10 alertes de plus, voir openCloud") || !strings.Contains(string(body.Content), "Machines concernées : opencloud.") {
		t.Fatalf("body %q %v", body.Content, err)
	}
}

// Dix alertes de danger inventées par une machine remplissent sa part, pas
// celle des autres : l'alerte d'une autre machine part, non retenue.
func TestDeliver_ANoisyMachine_DoesNotHoldAnotherMachinesAlert(t *testing.T) {
	f := newFixture(t)
	f.channel(t, "astreinte", alert.SeverityDanger, false)
	ctx := context.Background()
	remote := f.enrollRemote(t)
	tracker := f.tracker(t)
	f.inventDeaths(t, tracker, remote.ID, alert.ChannelRateLimit)
	if got := len(f.sender.jobs); got != alert.MachineRateLimit {
		t.Fatalf("the liar spends its own share only: %d jobs, want %d", got, alert.MachineRateLimit)
	}
	f.advance(30 * time.Second)
	if err := tracker.Record(ctx, machine.LocalID, container.Report{Events: []container.Event{f.event("die", container.StateExited, 1, "", false)}}); err != nil {
		t.Fatal(err)
	}
	if got := len(f.sender.jobs); got != alert.MachineRateLimit+1 {
		t.Fatalf("the honest machine's alert was held: %d jobs", got)
	}
	victim := f.sender.jobs[len(f.sender.jobs)-1]
	if victim.Alert.MachineID != machine.LocalID || victim.Alert.Severity != alert.SeverityDanger || victim.Delivery.Event != alert.EventOpened {
		t.Fatalf("job %+v", victim)
	}
	deliveries, err := f.db.ListDeliveries(ctx, victim.Alert.ID)
	if err != nil || len(deliveries) != 1 || deliveries[0].Status != alert.DeliveryPending {
		t.Fatalf("deliveries %+v %v", deliveries, err)
	}
}

// Même quand d'autres machines ont rempli le plafond du canal, la première
// alerte d'une machine calme passe, grâce à la réserve.
func TestDeliver_ChannelCeiling_LeavesAReserveToQuietMachines(t *testing.T) {
	f := newFixture(t)
	f.channel(t, "équipe", alert.SeverityAttention, false)
	ctx := context.Background()
	remote := f.enrollRemote(t)
	// Deux machines bruyantes à leur part : le canal est à son plafond.
	for _, machineID := range []string{machine.LocalID, remote.ID} {
		for i := range alert.MachineRateLimit {
			f.Open(ctx, f.volume(machineID, fmt.Sprintf("/m%d", i), alert.SeverityAttention))
		}
	}
	if got := len(f.sender.jobs); got != alert.ChannelRateLimit {
		t.Fatalf("channel at its ceiling: %d, want %d", got, alert.ChannelRateLimit)
	}
	f.Open(ctx, f.volume(remote.ID, "/more", alert.SeverityAttention))
	if got := len(f.sender.jobs); got != alert.ChannelRateLimit {
		t.Fatalf("a machine over its share is held: %d jobs", got)
	}
	// Un objet sans machine compte pour une part à lui : sa première alerte
	// passe, la suivante non, le canal étant au plafond.
	f.Open(ctx, f.volume("", "/first", alert.SeverityAttention))
	if got := len(f.sender.jobs); got != alert.ChannelRateLimit+1 {
		t.Fatalf("the quiet source's first alert was held: %d jobs", got)
	}
	f.Open(ctx, f.volume("", "/second", alert.SeverityAttention))
	if got := len(f.sender.jobs); got != alert.ChannelRateLimit+1 {
		t.Fatalf("the reserve is for the first alert only: %d jobs", got)
	}
}

// Le résumé porte la gravité la plus haute retenue et nomme les machines :
// un danger retenu n'est pas rendu en simple attention.
func TestDeliver_Summary_CarriesTheHighestHeldSeverityAndTheMachines(t *testing.T) {
	f := newFixture(t)
	f.channel(t, "astreinte", alert.SeverityAttention, false)
	ctx := context.Background()
	remote := f.enrollRemote(t)
	for i := range alert.MachineRateLimit {
		f.Open(ctx, f.volume(remote.ID, fmt.Sprintf("/a%d", i), alert.SeverityAttention))
	}
	f.Open(ctx, f.volume(remote.ID, "/held-attention", alert.SeverityAttention))
	f.Open(ctx, f.volume(remote.ID, "/held-danger", alert.SeverityDanger))
	f.advance(alert.ChannelRateWindow)
	f.Sweep(ctx)
	summary := f.sender.jobs[len(f.sender.jobs)-1]
	if summary.Delivery.Event != alert.EventSummary || summary.Held.Count != 2 || summary.Held.Severity != alert.SeverityDanger {
		t.Fatalf("summary %+v", summary)
	}
	if want := []string{"vps-paris-1"}; !slices.Equal(summary.Held.Machines, want) || summary.Held.Others != 0 {
		t.Fatalf("machines %v (+%d), want %v", summary.Held.Machines, summary.Held.Others, want)
	}
	catalogs, err := lang.Load()
	if err != nil {
		t.Fatal(err)
	}
	for code, want := range map[lang.Code][]string{
		lang.French:  {"gravité danger", "Machines concernées : vps-paris-1."},
		lang.English: {"danger severity", "Machines involved: vps-paris-1."},
	} {
		body, err := alert.RenderSummary(catalogs.For(code), alert.FormatText, summary.Held, f.clock, "https://oc.exemple.fr/alertes")
		if err != nil || !body.Urgent {
			t.Fatalf("%s: urgent=%v err=%v", code, body.Urgent, err)
		}
		for _, text := range want {
			if !strings.Contains(string(body.Content), text) {
				t.Errorf("%s: %q not in %q", code, text, body.Content)
			}
		}
	}
	// Retenu en attention seulement : ni urgent, ni marqué danger.
	calm := alert.Held{Count: 2, Severity: alert.SeverityAttention}
	body, err := alert.RenderSummary(catalogs.For(lang.French), alert.FormatText, calm, f.clock, "")
	if err != nil || body.Urgent || strings.Contains(string(body.Content), "danger") {
		t.Fatalf("calm summary: urgent=%v %q %v", body.Urgent, body.Content, err)
	}
}

func TestSummary_NamesAFewMachinesThenCountsTheOthers(t *testing.T) {
	catalogs, err := lang.Load()
	if err != nil {
		t.Fatal(err)
	}
	held := alert.Held{Count: 9, Severity: alert.SeverityAttention, Machines: []string{"a", "b", "c"}, Others: 2}
	body, err := alert.RenderSummary(catalogs.For(lang.French), alert.FormatText, held, testNow, "")
	if err != nil || !strings.Contains(string(body.Content), "Machines concernées : a, b, c et 2 autres.") {
		t.Fatalf("body %q %v", body.Content, err)
	}
}

// ntfy lit la priorité : un résumé qui retient du danger part en haute.
func TestNotifier_SummaryHoldingDangerGoesOutWithHighPriority(t *testing.T) {
	f := newFixture(t)
	notifier := f.notifier(t, lang.French)
	target := newReceiver(http.StatusNoContent)
	defer target.Close()
	channel := f.webhook(t, target.URL, alert.FormatText, "")
	catalogs, err := lang.Load()
	if err != nil {
		t.Fatal(err)
	}
	for severity, want := range map[alert.Severity]string{alert.SeverityDanger: "high", alert.SeverityAttention: "default"} {
		body, err := alert.RenderSummary(catalogs.For(lang.French), alert.FormatText, alert.Held{Count: 3, Severity: severity}, f.clock, "")
		if err != nil {
			t.Fatal(err)
		}
		if outcome := notifier.Send(context.Background(), channel, alert.EventSummary, body); !outcome.OK() {
			t.Fatalf("outcome %+v", outcome)
		}
		if got := target.last().headers.Get("Priority"); got != want {
			t.Errorf("%s summary: priority %q, want %q", severity, got, want)
		}
	}
}

// inventDeaths rapporte, pour une machine, des arrêts en erreur de
// conteneurs qui n'existent pas : ce que dirait un agent qui ment.
func (f *fixture) inventDeaths(t *testing.T, tracker *container.Tracker, machineID string, count int) {
	t.Helper()
	events := make([]container.Event, 0, count)
	for i := range count {
		id := fmt.Sprintf("%064x", i+1)
		code := 1
		events = append(events, container.Event{
			At: f.clock, Action: "die", ContainerID: id, State: container.StateExited, ExitCode: &code,
			Container: &container.Observed{ContainerID: id, Name: fmt.Sprintf("fake-%d", i), Image: "x:1", State: container.StateExited, ExitCode: 1, CreatedAt: testNow.Add(-time.Hour)},
		})
	}
	if err := tracker.Record(context.Background(), machineID, container.Report{Events: events}); err != nil {
		t.Fatal(err)
	}
}

// volume est un volume plein sur la machine donnée.
func (f *fixture) volume(machineID, mount string, severity alert.Severity) alert.Fact {
	return alert.Fact{
		Kind: alert.KindDiskFull, Severity: severity,
		Object: alert.Object{Kind: alert.ObjectVolume, ID: alert.VolumeID(machineID, mount), Name: mount}, MachineID: machineID,
		Details: alert.Details{MountPoint: mount, Percent: 90},
	}
}
