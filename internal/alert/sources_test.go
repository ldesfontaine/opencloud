package alert_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/ldesfontaine/opencloud/internal/alert"
	"github.com/ldesfontaine/opencloud/internal/container"
	"github.com/ldesfontaine/opencloud/internal/heartbeat"
	"github.com/ldesfontaine/opencloud/internal/lang"
	"github.com/ldesfontaine/opencloud/internal/machine"
	"github.com/ldesfontaine/opencloud/internal/probe"
	"github.com/ldesfontaine/opencloud/internal/resource"
	"github.com/ldesfontaine/opencloud/internal/sampler"
	"github.com/ldesfontaine/opencloud/internal/store/storetest"
)

const remoteID = "11111111-2222-4333-8444-555555555555"

// kinds rend « type:gravité » des alertes ouvertes, dans l'ordre de la page.
func (f *fixture) kinds(t *testing.T) string {
	t.Helper()
	var kinds []string
	for _, found := range f.open(t, alert.StatusOpen) {
		kinds = append(kinds, string(found.Kind)+":"+string(found.Severity))
	}
	return strings.Join(kinds, ",")
}

// --- Machines ---

func (f *fixture) enrollRemote(t *testing.T) machine.Machine {
	t.Helper()
	return storetest.EnrollMachine(t, f.machines, remoteID, "vps-paris-1")
}

func TestMachine_LostAfterTheDelay_BackOnConnect_GoneOnRemove(t *testing.T) {
	f := newFixture(t)
	f.channel(t, "ops", alert.SeverityAttention, true)
	ctx := context.Background()
	// La machine openCloud du banc n'a jamais donné signe de vie : sans
	// agent actif, elle ne se perd pas, et ne compte pas ici.
	if err := f.machines.Revoke(ctx, machine.LocalID); err != nil {
		t.Fatal(err)
	}
	enrolled := f.enrollRemote(t)
	session, err := f.machines.Connect(ctx, enrolled, "51.15.20.114", "v0.0.1", "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	f.machines.Disconnect(session)
	f.advance(time.Minute)
	if err := f.machines.CheckLost(ctx); err != nil || f.kinds(t) != "" {
		t.Fatalf("one minute is a hiccup: %q, %v", f.kinds(t), err)
	}
	f.advance(2 * time.Minute)
	if err := f.machines.CheckLost(ctx); err != nil || f.kinds(t) != "machine_lost:danger" {
		t.Fatalf("kinds %q, %v", f.kinds(t), err)
	}
	lost := f.only(t, alert.StatusOpen)
	if lost.Object.Name != "vps-paris-1" || lost.MachineName != "vps-paris-1" || lost.Details.Since.IsZero() {
		t.Fatalf("lost %+v", lost)
	}
	// Une seconde passe ne fait rien de plus.
	if err := f.machines.CheckLost(ctx); err != nil || len(f.open(t, alert.StatusOpen)) != 1 {
		t.Fatal("dedup")
	}
	if _, err := f.machines.Connect(ctx, enrolled, "51.15.20.114", "v0.0.1", "", "", ""); err != nil {
		t.Fatal(err)
	}
	if f.kinds(t) != "" || f.sender.events() != "opened:ops,resolved:ops" {
		t.Fatalf("kinds %q, events %q", f.kinds(t), f.sender.events())
	}
	f.Open(ctx, alert.Fact{Kind: alert.KindMachineLost, Severity: alert.SeverityDanger, Object: alert.Object{Kind: alert.ObjectMachine, ID: enrolled.ID, Name: "vps-paris-1"}, MachineID: enrolled.ID})
	if err := f.machines.Remove(ctx, enrolled.ID); err != nil {
		t.Fatal(err)
	}
	if f.kinds(t) != "" {
		t.Fatalf("a removed machine leaves nothing open: %q", f.kinds(t))
	}
	if resolved := f.open(t, alert.StatusResolved); len(resolved) != 2 || resolved[0].Object.Name != "vps-paris-1" || resolved[0].MachineName != "" {
		t.Fatalf("history keeps the object name, not the machine: %+v", resolved)
	}
}

// Une machine coupée à chaud garde son flux ouvert : l'alerte s'ouvre sur
// l'âge du dernier signal, se résout quand les signaux reprennent sur ce même
// flux, et dix passages de plus, flux ouvert et signaux réguliers, n'ajoutent
// rien : ni alerte, ni livraison, ni changement publié.
func TestMachine_LostOnAnOpenStream_BackWhenSignalsResume_ThenQuiet(t *testing.T) {
	f := newFixture(t)
	f.channel(t, "ops", alert.SeverityAttention, true)
	ctx := context.Background()
	if err := f.machines.Revoke(ctx, machine.LocalID); err != nil {
		t.Fatal(err)
	}
	enrolled := f.enrollRemote(t)
	session, err := f.machines.Connect(ctx, enrolled, "51.15.20.114", "v0.0.1", "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	f.advance(machine.LostAfter + 10*time.Second)
	if err := f.machines.CheckLost(ctx); err != nil || f.kinds(t) != "machine_lost:danger" {
		t.Fatalf("flux ouvert, signal de plus de 2 min : %q, %v", f.kinds(t), err)
	}
	if _, err := f.machines.Signal(ctx, session.Token); err != nil {
		t.Fatal(err)
	}
	if err := f.machines.CheckLost(ctx); err != nil || f.kinds(t) != "" {
		t.Fatalf("signaux repris : %q, %v", f.kinds(t), err)
	}
	changes := f.watcher.changes.Load()
	for range 10 {
		f.advance(30 * time.Second)
		if _, err := f.machines.Signal(ctx, session.Token); err != nil {
			t.Fatal(err)
		}
		if err := f.machines.CheckLost(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if f.kinds(t) != "" || len(f.open(t, alert.StatusResolved)) != 1 || f.sender.events() != "opened:ops,resolved:ops" {
		t.Fatalf("après dix passages : ouvertes %q, résolues %d, livraisons %q", f.kinds(t), len(f.open(t, alert.StatusResolved)), f.sender.events())
	}
	if got := f.watcher.changes.Load(); got != changes {
		t.Fatalf("dix passages sans rien de neuf publient %d changements", got-changes)
	}
}

// --- Services ---

func inspection(state container.State, exitCode int, health container.Health) *container.Observed {
	return &container.Observed{
		ContainerID: strings.Repeat("a", 64), Name: "nextcloud", Image: "nextcloud:29", State: state, ExitCode: exitCode, Health: health,
		CreatedAt: testNow.Add(-time.Hour),
	}
}

func (f *fixture) tracker(t *testing.T) *container.Tracker {
	t.Helper()
	tracker := container.New(f.db, discardLogger())
	tracker.SetClock(func() time.Time { return f.clock })
	tracker.SetAlerter(f.Engine)
	if err := tracker.Record(context.Background(), machine.LocalID, container.Report{Complete: true, Inventory: []container.Observed{*inspection(container.StateRunning, 0, "")}}); err != nil {
		t.Fatal(err)
	}
	return tracker
}

func (f *fixture) event(action string, state container.State, exitCode int, health container.Health, replayed bool) container.Event {
	code := exitCode
	return container.Event{At: f.clock, Action: action, ContainerID: strings.Repeat("a", 64), State: state, Health: health, ExitCode: &code, Replayed: replayed, Container: inspection(state, exitCode, health)}
}

func TestService_UnexpectedStop_ThenRestartsUpToALoop(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	tracker := f.tracker(t)
	if f.kinds(t) != "" {
		t.Fatalf("a running service opens nothing: %q", f.kinds(t))
	}
	// Un die en 1 dans un signal, le start dans le suivant.
	if err := tracker.Record(ctx, machine.LocalID, container.Report{Events: []container.Event{f.event("die", container.StateExited, 1, "", false)}}); err != nil {
		t.Fatal(err)
	}
	if f.kinds(t) != "service_down:danger" || f.only(t, alert.StatusOpen).Details.ExitCode != 1 {
		t.Fatalf("kinds %q", f.kinds(t))
	}
	f.advance(20 * time.Second)
	if err := tracker.Record(ctx, machine.LocalID, container.Report{Events: []container.Event{f.event("start", container.StateRunning, 0, "", false)}}); err != nil {
		t.Fatal(err)
	}
	if f.kinds(t) != "service_restart:attention" || f.only(t, alert.StatusOpen).Details.Count != 1 {
		t.Fatalf("kinds %q, %+v", f.kinds(t), f.open(t, alert.StatusOpen))
	}
	// Deux fois de plus dans le même signal : la boucle.
	f.advance(time.Minute)
	crashes := []container.Event{
		f.event("die", container.StateExited, 137, "", false), f.event("start", container.StateRunning, 0, "", false),
		f.event("die", container.StateExited, 2, "", false), f.event("start", container.StateRunning, 0, "", false),
		f.event("die", container.StateExited, 2, "", false), f.event("start", container.StateRunning, 0, "", false),
	}
	if err := tracker.Record(ctx, machine.LocalID, container.Report{Events: crashes}); err != nil {
		t.Fatal(err)
	}
	loop := f.only(t, alert.StatusOpen)
	if f.kinds(t) != "service_restart:danger" || loop.Details.Count != 3 || loop.Details.ExitCode != 2 {
		t.Fatalf("a stop asked for (137) does not count: %q %+v", f.kinds(t), loop.Details)
	}
	// Dix minutes de calme, et le balayage résout.
	f.advance(alert.RestartWindow + time.Second)
	f.Sweep(ctx)
	if f.kinds(t) != "" {
		t.Fatalf("kinds %q", f.kinds(t))
	}
}

func TestService_ReplayedAndCleanStopsAreNotAlerts_UnhealthyIs_ArchivedResolves(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	tracker := f.tracker(t)
	replayed := []container.Event{f.event("die", container.StateExited, 1, "", true), f.event("start", container.StateRunning, 0, "", true)}
	if err := tracker.Record(ctx, machine.LocalID, container.Report{Events: replayed}); err != nil {
		t.Fatal(err)
	}
	if err := tracker.Record(ctx, machine.LocalID, container.Report{Events: []container.Event{f.event("die", container.StateExited, 143, "", false)}}); err != nil {
		t.Fatal(err)
	}
	if f.kinds(t) != "" {
		t.Fatalf("replayed or asked-for stops open nothing: %q", f.kinds(t))
	}
	if err := tracker.Record(ctx, machine.LocalID, container.Report{Events: []container.Event{f.event("start", container.StateRunning, 0, container.HealthUnhealthy, false)}}); err != nil {
		t.Fatal(err)
	}
	if f.kinds(t) != "service_unhealthy:attention" {
		t.Fatalf("kinds %q", f.kinds(t))
	}
	if err := tracker.Record(ctx, machine.LocalID, container.Report{Events: []container.Event{{At: f.clock, Action: container.ActionDestroy, ContainerID: strings.Repeat("a", 64)}}}); err != nil {
		t.Fatal(err)
	}
	if f.kinds(t) != "" {
		t.Fatalf("an archived service takes its alerts with it: %q", f.kinds(t))
	}
}

// Un conteneur tué faute de mémoire sort en 137, comme un docker stop :
// c'est OOMKilled qui ouvre l'alerte, avec son motif ; relancé, elle se
// résout et le redémarrage compte.
func TestService_OutOfMemoryKill_OpensADangerAlert(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	tracker := f.tracker(t)
	killed := f.event("die", container.StateExited, 137, "", false)
	killed.Container.OOMKilled = true
	if err := tracker.Record(ctx, machine.LocalID, container.Report{Events: []container.Event{killed}}); err != nil {
		t.Fatal(err)
	}
	listed, err := tracker.List(ctx, machine.LocalID)
	if err != nil || len(listed) != 1 || !listed[0].OOMKilled || !container.NeedsAttention(listed[0]) {
		t.Fatalf("la fiche doit garder le manque de mémoire: %+v %v", listed, err)
	}
	opened := f.only(t, alert.StatusOpen)
	if f.kinds(t) != "service_down:danger" || opened.Details.Reason != alert.ReasonOOM || opened.Details.ExitCode != 137 {
		t.Fatalf("kinds %q, %+v", f.kinds(t), opened.Details)
	}
	catalogs, err := lang.Load()
	if err != nil {
		t.Fatal(err)
	}
	if text := alert.Describe(catalogs.For(lang.French), opened, f.clock); text.Fact != "Conteneur arrêté par manque de mémoire" {
		t.Fatalf("fact %q", text.Fact)
	}
	f.advance(20 * time.Second)
	if err := tracker.Record(ctx, machine.LocalID, container.Report{Events: []container.Event{f.event("start", container.StateRunning, 0, "", false)}}); err != nil {
		t.Fatal(err)
	}
	if f.kinds(t) != "service_restart:attention" {
		t.Fatalf("restart after an out-of-memory kill: %q", f.kinds(t))
	}
}

// Un docker kill sort en 137 comme un docker stop : c'est l'agent, qui
// n'a pas vu de demande d'arrêt, qui le dit tué ; l'alerte porte son motif.
// Sans ce fait (agent ancien, ou arrêt demandé), 137 reste propre.
func TestService_KilledWithoutAStopRequest_OpensADangerAlert(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	tracker := f.tracker(t)
	if err := tracker.Record(ctx, machine.LocalID, container.Report{Events: []container.Event{f.event("die", container.StateExited, 137, "", false)}}); err != nil {
		t.Fatal(err)
	}
	if f.kinds(t) != "" {
		t.Fatalf("a stop asked for opens nothing: %q", f.kinds(t))
	}
	if err := tracker.Record(ctx, machine.LocalID, container.Report{Events: []container.Event{f.event("start", container.StateRunning, 0, "", false)}}); err != nil {
		t.Fatal(err)
	}
	killed := f.event("die", container.StateExited, 137, "", false)
	killed.Killed = true
	if err := tracker.Record(ctx, machine.LocalID, container.Report{Events: []container.Event{killed}}); err != nil {
		t.Fatal(err)
	}
	listed, err := tracker.List(ctx, machine.LocalID)
	if err != nil || len(listed) != 1 || !listed[0].Killed || !container.NeedsAttention(listed[0]) {
		t.Fatalf("la fiche doit garder l'arrêt brutal: %+v %v", listed, err)
	}
	opened := f.only(t, alert.StatusOpen)
	if f.kinds(t) != "service_down:danger" || opened.Details.Reason != alert.ReasonKilled || opened.Details.ExitCode != 137 {
		t.Fatalf("kinds %q, %+v", f.kinds(t), opened.Details)
	}
	catalogs, err := lang.Load()
	if err != nil {
		t.Fatal(err)
	}
	text := alert.Describe(catalogs.For(lang.French), opened, f.clock)
	if text.Fact != "Conteneur arrêté brutalement" || !strings.Contains(text.Cause, "arrêté brutalement (signal KILL), sans arrêt demandé") {
		t.Fatalf("text %+v", text)
	}
	// L'inventaire suivant ne sait rien du kill : l'arrêt reste brutal.
	if err := tracker.Record(ctx, machine.LocalID, container.Report{Complete: true, Inventory: []container.Observed{*killed.Container}}); err != nil {
		t.Fatal(err)
	}
	if f.kinds(t) != "service_down:danger" {
		t.Fatalf("an inventory keeps the brutal stop: %q", f.kinds(t))
	}
	f.advance(20 * time.Second)
	if err := tracker.Record(ctx, machine.LocalID, container.Report{Events: []container.Event{f.event("start", container.StateRunning, 0, "", false)}}); err != nil {
		t.Fatal(err)
	}
	if f.kinds(t) != "service_restart:attention" {
		t.Fatalf("restart after a kill: %q", f.kinds(t))
	}
}

// --- Sondes ---

func (f *fixture) prober(t *testing.T) (*probe.Service, probe.Probe) {
	t.Helper()
	probes := probe.New(f.db, discardLogger())
	probes.SetClock(func() time.Time { return f.clock })
	probes.SetAlerter(f.Engine)
	created, err := probes.Create(context.Background(), probe.Definition{Name: "site", Kind: probe.KindHTTP, Target: "https://cloud.exemple.fr/status", MachineID: machine.LocalID})
	if err != nil {
		t.Fatal(err)
	}
	return probes, created
}

func (f *fixture) result(id string, outcome probe.Outcome, certificate *probe.Certificate) probe.Result {
	reason := probe.ReasonNone
	if outcome == probe.OutcomeDown {
		reason = probe.ReasonRefused
	}
	return probe.Result{ProbeID: id, CheckedAt: f.clock, Outcome: outcome, DurationMs: 12, Reason: reason, Certificate: certificate}
}

func TestProbe_DownAtTheThreshold_UpAtRecovery_PauseResolves(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	probes, created := f.prober(t)
	record := func(outcomes ...probe.Outcome) {
		for _, outcome := range outcomes {
			f.advance(time.Minute)
			if err := probes.Record(ctx, machine.LocalID, probe.Report{Results: []probe.Result{f.result(created.ID, outcome, nil)}}); err != nil {
				t.Fatal(err)
			}
		}
	}
	record(probe.OutcomeUp, probe.OutcomeDown, probe.OutcomeDown)
	if f.kinds(t) != "" {
		t.Fatalf("two failures are under the threshold: %q", f.kinds(t))
	}
	record(probe.OutcomeDown)
	down := f.only(t, alert.StatusOpen)
	if f.kinds(t) != "probe_down:danger" || down.Details.Reason != "refused" || down.Details.Target != "https://cloud.exemple.fr/status" {
		t.Fatalf("kinds %q, %+v", f.kinds(t), down.Details)
	}
	record(probe.OutcomeUp)
	if f.kinds(t) != "probe_down:danger" {
		t.Fatal("one success is not a recovery")
	}
	record(probe.OutcomeUp)
	if f.kinds(t) != "" {
		t.Fatalf("kinds %q", f.kinds(t))
	}
	record(probe.OutcomeDown, probe.OutcomeDown, probe.OutcomeDown)
	if err := probes.Pause(ctx, created.ID); err != nil {
		t.Fatal(err)
	}
	if f.kinds(t) != "" {
		t.Fatalf("a paused probe says nothing of the target: %q", f.kinds(t))
	}
}

func TestProbe_CertificateExpiryAndTrustAreTwoAlerts(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	probes, created := f.prober(t)
	record := func(certificate *probe.Certificate) {
		f.advance(time.Minute)
		if err := probes.Record(ctx, machine.LocalID, probe.Report{Results: []probe.Result{f.result(created.ID, probe.OutcomeUp, certificate)}}); err != nil {
			t.Fatal(err)
		}
	}
	untrusted := &probe.Certificate{Subject: "cloud.exemple.fr", Issuer: "openCloud dev", NotBefore: f.clock.Add(-30 * 24 * time.Hour), NotAfter: f.clock.Add(12 * 24 * time.Hour), Fingerprint: strings.Repeat("e", 64), HostnameMatch: true}
	record(untrusted)
	if f.kinds(t) != "cert_untrusted:attention,cert_expiring:attention" {
		t.Fatalf("kinds %q", f.kinds(t))
	}
	for _, found := range f.open(t, alert.StatusOpen) {
		if found.Details.Target != "cloud.exemple.fr" || (found.Kind == alert.KindCertUntrusted && found.Details.Reason != alert.ReasonChain) {
			t.Fatalf("details %+v", found.Details)
		}
	}
	// Le seuil de danger se franchit sans nouvel essai : la revue le voit.
	f.advance(6 * 24 * time.Hour)
	if err := probes.ReviewCertificates(ctx); err != nil {
		t.Fatal(err)
	}
	if f.kinds(t) != "cert_expiring:danger,cert_untrusted:attention" {
		t.Fatalf("kinds %q", f.kinds(t))
	}
	f.advance(7 * 24 * time.Hour)
	record(untrusted)
	// Expiré remplace « à renouveler », et la chaîne refusée à cause de
	// l'expiration ne se compte pas deux fois.
	if f.kinds(t) != "cert_expired:danger" {
		t.Fatalf("expired replaces expiring: %q", f.kinds(t))
	}
	renewed := &probe.Certificate{Subject: "cloud.exemple.fr", Issuer: "Let's Encrypt", NotBefore: f.clock, NotAfter: f.clock.Add(90 * 24 * time.Hour), Fingerprint: strings.Repeat("f", 64), ChainValid: true, HostnameMatch: true}
	record(renewed)
	if f.kinds(t) != "" {
		t.Fatalf("a renewed, trusted certificate resolves everything: %q", f.kinds(t))
	}
	revoked := *renewed
	revoked.OCSP = probe.OCSPRevoked
	record(&revoked)
	if f.kinds(t) != "cert_untrusted:attention" || f.only(t, alert.StatusOpen).Details.Reason != alert.ReasonRevoked {
		t.Fatalf("kinds %q", f.kinds(t))
	}
}

// --- Tâches ---

func TestHeartbeat_LateThenFailedThenRecovered(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	heartbeats := heartbeat.New(f.db, discardLogger())
	heartbeats.SetClock(func() time.Time { return f.clock })
	heartbeats.SetAlerter(f.Engine)
	job, err := heartbeats.Create(ctx, heartbeat.Definition{Name: "sauvegarde", Interval: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := heartbeats.Receive(ctx, job.Token, heartbeat.Ping{Kind: heartbeat.KindFinish}); err != nil {
		t.Fatal(err)
	}
	f.advance(3 * time.Minute)
	if err := heartbeats.CheckDeadlines(ctx); err != nil || f.kinds(t) != "job_late:attention" {
		t.Fatalf("kinds %q, %v", f.kinds(t), err)
	}
	one := 1
	if _, err := heartbeats.Receive(ctx, job.Token, heartbeat.Ping{Kind: heartbeat.KindExitCode, ExitCode: &one}); err != nil {
		t.Fatal(err)
	}
	if f.kinds(t) != "job_failed:danger" || f.only(t, alert.StatusOpen).Details.ExitCode != 1 {
		t.Fatalf("a failed ping lifts the delay: %q", f.kinds(t))
	}
	zero := 0
	if _, err := heartbeats.Receive(ctx, job.Token, heartbeat.Ping{Kind: heartbeat.KindExitCode, ExitCode: &zero}); err != nil {
		t.Fatal(err)
	}
	if f.kinds(t) != "" {
		t.Fatalf("kinds %q", f.kinds(t))
	}
	if _, err := heartbeats.Receive(ctx, job.Token, heartbeat.Ping{Kind: heartbeat.KindExitCode, ExitCode: &one}); err != nil {
		t.Fatal(err)
	}
	if err := heartbeats.Pause(ctx, job.ID); err != nil || f.kinds(t) != "" {
		t.Fatalf("a paused job says nothing: %q, %v", f.kinds(t), err)
	}
}

// Une tâche qui repart par un start puis une fin réussie lève son alerte,
// qu'elle ait été en retard ou en échec : c'est le chemin de l'extrait
// « script ». Le start seul ne lève rien.
func TestHeartbeat_StartThenSuccess_ResolvesLateAndFailed(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	heartbeats := heartbeat.New(f.db, discardLogger())
	heartbeats.SetClock(func() time.Time { return f.clock })
	heartbeats.SetAlerter(f.Engine)
	job, err := heartbeats.Create(ctx, heartbeat.Definition{Name: "sauvegarde", Interval: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	receive := func(kind heartbeat.Kind, exitCode *int) {
		t.Helper()
		if _, err := heartbeats.Receive(ctx, job.Token, heartbeat.Ping{Kind: kind, ExitCode: exitCode}); err != nil {
			t.Fatal(err)
		}
	}
	zero, one := 0, 1

	receive(heartbeat.KindFinish, nil)
	f.advance(3 * time.Minute)
	if err := heartbeats.CheckDeadlines(ctx); err != nil || f.kinds(t) != "job_late:attention" {
		t.Fatalf("kinds %q, %v", f.kinds(t), err)
	}
	receive(heartbeat.KindStart, nil)
	if f.kinds(t) != "job_late:attention" {
		t.Fatalf("a start alone lifts nothing: %q", f.kinds(t))
	}
	receive(heartbeat.KindExitCode, &zero)
	if f.kinds(t) != "" {
		t.Fatalf("late, start, success: %q", f.kinds(t))
	}

	receive(heartbeat.KindExitCode, &one)
	if f.kinds(t) != "job_failed:danger" {
		t.Fatalf("kinds %q", f.kinds(t))
	}
	receive(heartbeat.KindStart, nil)
	receive(heartbeat.KindExitCode, &zero)
	if f.kinds(t) != "" {
		t.Fatalf("failed, start, success: %q", f.kinds(t))
	}
}

func TestHeartbeat_NeverPinged_OpensTheLateAlertWithItsAge(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	heartbeats := heartbeat.New(f.db, discardLogger())
	heartbeats.SetClock(func() time.Time { return f.clock })
	heartbeats.SetAlerter(f.Engine)
	job, err := heartbeats.Create(ctx, heartbeat.Definition{Name: "sauvegarde", Interval: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	created := f.clock
	f.advance(2 * time.Minute)
	if err := heartbeats.CheckDeadlines(ctx); err != nil || f.kinds(t) != "job_late:attention" {
		t.Fatalf("kinds %q, %v", f.kinds(t), err)
	}
	details := f.only(t, alert.StatusOpen).Details
	if details.Reason != alert.ReasonNeverPinged || !details.Since.Equal(created) {
		t.Fatalf("details %+v", details)
	}
	if _, err := heartbeats.Receive(ctx, job.Token, heartbeat.Ping{Kind: heartbeat.KindFinish}); err != nil || f.kinds(t) != "" {
		t.Fatalf("the first ping resolves it: %q, %v", f.kinds(t), err)
	}
}

// Une tâche déplacée sur une autre machine : son alerte ouverte suit, avec
// son nom, au lieu de rester attachée à l'ancienne.
func TestHeartbeat_EditedWhileLate_TheOpenAlertFollowsItsNameAndMachine(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	f.enrollRemote(t)
	heartbeats := heartbeat.New(f.db, discardLogger())
	heartbeats.SetClock(func() time.Time { return f.clock })
	heartbeats.SetAlerter(f.Engine)
	job, err := heartbeats.Create(ctx, heartbeat.Definition{Name: "sauvegarde", Interval: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	f.advance(2 * time.Minute)
	if err := heartbeats.CheckDeadlines(ctx); err != nil || f.kinds(t) != "job_late:attention" {
		t.Fatalf("kinds %q, %v", f.kinds(t), err)
	}
	if got := f.only(t, alert.StatusOpen); got.MachineID != "" {
		t.Fatalf("la tâche n'a pas de machine: %+v", got)
	}

	if _, err := heartbeats.Update(ctx, job.ID, heartbeat.Definition{Name: "sauvegarde nextcloud", MachineID: remoteID, Interval: time.Minute}); err != nil {
		t.Fatal(err)
	}
	got := f.only(t, alert.StatusOpen)
	if got.Object.Name != "sauvegarde nextcloud" || got.MachineID != remoteID {
		t.Fatalf("l'alerte doit suivre la tâche: %+v", got)
	}
	if len(f.open(t, alert.StatusOpen)) != 1 {
		t.Fatalf("une seule alerte attendue, obtenu %q", f.kinds(t))
	}
}

// --- Ressources ---

func TestResource_DiskAlertsWithHysteresis_AndForgetsAVanishedVolume(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	resources := resource.New(f.db, discardLogger())
	resources.SetClock(func() time.Time { return f.clock })
	resources.SetAlerter(f.Engine)
	record := func(percent int, extra bool) {
		f.advance(10 * time.Second)
		disks := []sampler.Disk{{MountPoint: "/data", Device: "/dev/vdb", Used: int64(percent) * 10, Total: 1000}}
		if extra {
			disks = append(disks, sampler.Disk{MountPoint: "/", Device: "/dev/vda1", Used: 990, Total: 1000})
		}
		reading := sampler.Reading{SampledAt: f.clock, CPUCores: 2, MemTotal: 1, DiskUsed: 1, DiskTotal: 2, Disks: disks}
		if err := resources.Record(ctx, machine.LocalID, []sampler.Reading{reading}); err != nil {
			t.Fatal(err)
		}
	}
	record(84, false)
	if f.kinds(t) != "" {
		t.Fatalf("kinds %q", f.kinds(t))
	}
	record(86, true)
	if f.kinds(t) != "disk_full:danger,disk_full:attention" {
		t.Fatalf("kinds %q", f.kinds(t))
	}
	record(96, false)
	if f.kinds(t) != "disk_full:danger" || f.only(t, alert.StatusOpen).Details.MountPoint != "/data" {
		t.Fatalf("the root volume vanished, /data aggravated: %q", f.kinds(t))
	}
	record(83, false)
	if f.kinds(t) != "disk_full:danger" {
		t.Fatal("between the two thresholds nothing moves")
	}
	record(79, false)
	if f.kinds(t) != "" {
		t.Fatalf("kinds %q", f.kinds(t))
	}
}

// Les seuils réglés valent dès la lecture suivante, sans redémarrer le
// moteur : la même lecture à 72 % n'alerte pas à 85, alerte à 70, et le
// retour suit l'attention cinq points dessous.
func TestResource_DiskThresholdsAreReadAtEachReading(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	thresholds := alert.DefaultDiskThresholds()
	f.SetDiskThresholds(func() alert.DiskThresholds { return thresholds })
	resources := resource.New(f.db, discardLogger())
	resources.SetClock(func() time.Time { return f.clock })
	resources.SetAlerter(f.Engine)
	record := func(percent int) {
		f.advance(10 * time.Second)
		disks := []sampler.Disk{{MountPoint: "/data", Device: "/dev/vdb", Used: int64(percent) * 10, Total: 1000}}
		reading := sampler.Reading{SampledAt: f.clock, CPUCores: 2, MemTotal: 1, DiskUsed: 1, DiskTotal: 2, Disks: disks}
		if err := resources.Record(ctx, machine.LocalID, []sampler.Reading{reading}); err != nil {
			t.Fatal(err)
		}
	}
	record(72)
	if f.kinds(t) != "" {
		t.Fatalf("72 %% under the default threshold: %q", f.kinds(t))
	}
	thresholds = alert.DiskThresholds{Attention: 70, Danger: 80}
	record(72)
	if f.kinds(t) != "disk_full:attention" || f.only(t, alert.StatusOpen).Details.Threshold != 70 {
		t.Fatalf("72 %% over a 70 %% threshold: %q", f.kinds(t))
	}
	record(81)
	if f.kinds(t) != "disk_full:danger" {
		t.Fatalf("81 %% over a 80 %% danger: %q", f.kinds(t))
	}
	record(66)
	if f.kinds(t) != "disk_full:danger" {
		t.Fatal("66 % is above the 65 % recovery: nothing moves")
	}
	record(64)
	if f.kinds(t) != "" {
		t.Fatalf("64 %% under the recovery: %q", f.kinds(t))
	}
}

func TestDiskThresholds_RefuseWhatMakesNoSense_AndFallBackToTheDefaults(t *testing.T) {
	for _, thresholds := range []alert.DiskThresholds{{Attention: 49, Danger: 95}, {Attention: 85, Danger: 100}, {Attention: 90, Danger: 90}, {Attention: 95, Danger: 85}, {}} {
		if thresholds.Validate() == nil {
			t.Errorf("%+v accepted", thresholds)
		}
		if alert.DiskThresholdsOr(thresholds.Attention, thresholds.Danger) != alert.DefaultDiskThresholds() {
			t.Errorf("%+v must fall back to the defaults", thresholds)
		}
	}
	if got := alert.DiskThresholdsOr(50, 99); got.Validate() != nil || got.Recovery() != 45 {
		t.Fatalf("50/99 refused: %+v", got)
	}
}

// Un agent revenu à sa version d'avant ouvre une alerte lisible, attention ;
// danger quand la remise en état a échoué ; une mise à jour confirmée la
// referme. Une seule alerte vivante par machine : le pire des deux l'emporte.
func TestAgentRolledBack_OpensReadableAlertAndAConfirmedUpdateResolvesIt(t *testing.T) {
	f := newFixture(t)
	f.channel(t, "ops", alert.SeverityAttention, true)
	ctx := context.Background()
	enrolled := f.enrollRemote(t)

	f.AgentRolledBack(ctx, enrolled.ID, "vps-paris-1", "v0.0.2", alert.RollbackCrash, false)
	if f.kinds(t) != "agent_rolled_back:attention" {
		t.Fatalf("kinds %q", f.kinds(t))
	}
	opened := f.only(t, alert.StatusOpen)
	if opened.Object.Kind != alert.ObjectMachine || opened.Object.ID != enrolled.ID || opened.Details.Target != "v0.0.2" || opened.Details.Reason != alert.RollbackCrash {
		t.Fatalf("alert %+v", opened)
	}
	catalogs, err := lang.Load()
	if err != nil {
		t.Fatal(err)
	}
	text := alert.Describe(catalogs.For(lang.French), opened, testNow)
	if text.Fact != "Agent revenu à la version précédente" || !strings.Contains(text.Cause, "vps-paris-1") || !strings.Contains(text.Cause, "v0.0.2") || !strings.Contains(text.Cause, "planté") {
		t.Fatalf("text %+v", text)
	}

	// La remise en état échoue à son tour : l'alerte s'aggrave, elle ne se double pas.
	f.AgentRolledBack(ctx, enrolled.ID, "vps-paris-1", "v0.0.2", alert.RollbackFailed, true)
	if f.kinds(t) != "agent_rolled_back:danger" {
		t.Fatalf("kinds %q", f.kinds(t))
	}
	failed := alert.Describe(catalogs.For(lang.French), f.only(t, alert.StatusOpen), testNow)
	if !strings.Contains(failed.Cause, "plus d'agent") {
		t.Fatalf("text %+v", failed)
	}

	f.AgentUpdated(ctx, enrolled.ID)
	if f.kinds(t) != "" {
		t.Fatalf("a confirmed update leaves the alert open: %q", f.kinds(t))
	}
	if f.sender.events() != "opened:ops,aggravated:ops,resolved:ops" {
		t.Fatalf("events %q", f.sender.events())
	}
	// Une cause inconnue se dit comme un délai passé, sans casser la phrase.
	f.AgentRolledBack(ctx, enrolled.ID, "vps-paris-1", "v0.0.2", "mystery", false)
	if text := alert.Describe(catalogs.For(lang.English), f.only(t, alert.StatusOpen), testNow); strings.Contains(text.Cause, "[") {
		t.Fatalf("text %+v", text)
	}
}

// Un arrêt demandé qui sort en code 1 (le service dépasse son propre délai
// d'arrêt) n'est pas un plantage : aucune alerte, ni à la sortie ni au retour.
// Sans arrêt demandé, le même code ouvre l'alerte ; le manque de mémoire passe
// avant l'arrêt demandé.
func TestService_StopRequestedWithANonZeroCode_OpensNothing(t *testing.T) {
	ctx := context.Background()
	record := func(t *testing.T, f *fixture, tracker *container.Tracker, events ...container.Event) {
		t.Helper()
		if err := tracker.Record(ctx, machine.LocalID, container.Report{Events: events}); err != nil {
			t.Fatal(err)
		}
	}
	t.Run("stop requested, exit 1, start", func(t *testing.T) {
		f := newFixture(t)
		tracker := f.tracker(t)
		died := f.event("die", container.StateExited, 1, "", false)
		died.StopRequested = true
		record(t, f, tracker, died)
		listed, err := tracker.List(ctx, machine.LocalID)
		if err != nil || len(listed) != 1 || !listed[0].StopRequested || container.NeedsAttention(listed[0]) {
			t.Fatalf("a requested stop needs no attention: %+v %v", listed, err)
		}
		f.advance(5 * time.Second)
		record(t, f, tracker, f.event("start", container.StateRunning, 0, "", false))
		if f.kinds(t) != "" {
			t.Fatalf("a requested stop opens nothing: %q", f.kinds(t))
		}
	})
	t.Run("exit 1 without a stop request", func(t *testing.T) {
		f := newFixture(t)
		tracker := f.tracker(t)
		record(t, f, tracker, f.event("die", container.StateExited, 1, "", false))
		f.advance(5 * time.Second)
		record(t, f, tracker, f.event("start", container.StateRunning, 0, "", false))
		if f.kinds(t) != "service_restart:attention" {
			t.Fatalf("kinds %q", f.kinds(t))
		}
	})
	t.Run("out of memory beats the stop request", func(t *testing.T) {
		f := newFixture(t)
		tracker := f.tracker(t)
		died := f.event("die", container.StateExited, 137, "", false)
		died.StopRequested = true
		died.Container.OOMKilled = true
		record(t, f, tracker, died)
		opened := f.only(t, alert.StatusOpen)
		if f.kinds(t) != "service_down:danger" || opened.Details.Reason != alert.ReasonOOM {
			t.Fatalf("kinds %q, %+v", f.kinds(t), opened.Details)
		}
	})
}
