package action_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/ldesfontaine/opencloud/internal/action"
	"github.com/ldesfontaine/opencloud/internal/agentwire"
	"github.com/ldesfontaine/opencloud/internal/audit"
	"github.com/ldesfontaine/opencloud/internal/machine"
	"github.com/ldesfontaine/opencloud/internal/refusal"
)

type rolledBackCall struct {
	machineID, machineName, target, reason string
	failed                                 bool
}

// fakeAlerter note ce que le composant demande au moteur des alertes.
type fakeAlerter struct {
	rolledBack []rolledBackCall
	updated    []string
}

func (f *fakeAlerter) AgentRolledBack(_ context.Context, machineID, machineName, target, reason string, failed bool) {
	f.rolledBack = append(f.rolledBack, rolledBackCall{machineID, machineName, target, reason, failed})
}

func (f *fakeAlerter) AgentUpdated(_ context.Context, machineID string) {
	f.updated = append(f.updated, machineID)
}

// readyBench : une machine en ligne et migrée, la version v0.0.2 importée.
func readyBench(t *testing.T) *updateBench {
	t.Helper()
	b := newUpdateBench(t, updateOptions{rights: "observe,agent.update", agentVersion: "v0.0.1", arch: "amd64"})
	b.publishBoth(t, "v0.0.2")
	b.service.SetReleases(b.library, "v0.0.2")
	return b
}

// addMachine enrôle et connecte une autre machine ; migrated dit si son agent
// rapporte le retour arrière.
func (b *updateBench) addMachine(t *testing.T, id, name, version string, migrated bool) machine.Machine {
	t.Helper()
	ctx := context.Background()
	cleartext, _, err := b.machines.CreateToken(ctx, name, nil)
	if err != nil {
		t.Fatal(err)
	}
	public, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	enrolled, err := b.machines.Enroll(ctx, machine.Enrollment{
		MachineID: id, PublicKey: public, Token: cleartext, Hostname: name, Address: "192.0.2.50", OS: "Debian 12", Arch: "amd64", AgentVersion: version,
	})
	if err != nil {
		t.Fatal(err)
	}
	session, err := b.machines.Connect(ctx, enrolled, "192.0.2.50", version, "observe,agent.update", "", "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { b.machines.Disconnect(session) })
	if migrated {
		if err := b.machines.NoteRollbackVersion(ctx, enrolled.Agent.ID, "1"); err != nil {
			t.Fatal(err)
		}
	}
	return enrolled
}

const otherMachineID = "22222222-3333-4444-8555-666666666666"

// Aucun contournement : un agent qui ne rapporte pas le retour arrière ne
// reçoit aucune mise à jour, quels que soient ses droits ou ce qui est importé.
func TestUpdateAgent_RefusesAnAgentThatDoesNotRollBackByItself(t *testing.T) {
	b := newUpdateBench(t, updateOptions{rights: "observe,agent.update", agentVersion: "v0.0.1", arch: "amd64", noRollback: true})
	b.publishBoth(t, "v0.0.2")
	b.service.SetReleases(b.library, "v0.0.2")

	offer := b.offer(t)
	if offer.Available || offer.Reason != action.OfferRollbackUnavailable {
		t.Fatalf("offer %+v", offer)
	}
	_, err := b.service.UpdateAgent(operator(), machineID)
	if key, _ := refusal.KeyOf(err); !errors.Is(err, action.ErrRollbackUnavailable) || key != "action.rollback_unavailable" {
		t.Fatalf("got %v", err)
	}
	if len(b.pusher.sent) != 0 {
		t.Fatalf("an order left: %+v", b.pusher.sent)
	}
	runs, err := b.db.ListActionRuns(context.Background(), action.Query{Limit: 10})
	if err != nil || len(runs) != 0 {
		t.Fatalf("runs %+v %v", runs, err)
	}
	// Un agent qui dit 0, ou n'importe quoi, n'est pas migré non plus.
	for _, reported := range []string{"0", "-3", "yes", "99999"} {
		found, _ := b.db.GetMachine(context.Background(), machineID)
		if err := b.machines.NoteRollbackVersion(context.Background(), found.Agent.ID, reported); err != nil {
			t.Fatal(err)
		}
		if _, err := b.service.UpdateAgent(operator(), machineID); !errors.Is(err, action.ErrRollbackUnavailable) {
			t.Errorf("reported %q: %v", reported, err)
		}
	}
	// Une machine déjà à jour n'a rien à migrer pour l'instant : la raison
	// reste « même version ».
	upToDate := newUpdateBench(t, updateOptions{rights: "observe,agent.update", agentVersion: "v0.0.2", arch: "amd64", noRollback: true})
	upToDate.publishBoth(t, "v0.0.2")
	upToDate.service.SetReleases(upToDate.library, "v0.0.2")
	if offer := upToDate.offer(t); offer.Reason != action.OfferSameVersion {
		t.Fatalf("offer %+v", offer)
	}
}

func TestUpdateAgent_OnlyOneAtATime_AllMachinesTogether(t *testing.T) {
	b := readyBench(t)
	b.addMachine(t, otherMachineID, "vps-lyon-2", "v0.0.1", true)
	clock := benchNow
	b.service.SetClock(func() time.Time { return clock })
	ctx := operator()

	first, err := b.service.UpdateAgent(ctx, machineID)
	if err != nil {
		t.Fatal(err)
	}
	// La première n'est ni confirmée, ni en échec, ni échue : ni la même
	// machine, ni une autre, ni un retour arrière ne partent.
	for name, attempt := range map[string]func() error{
		"the same machine": func() error { _, err := b.service.UpdateAgent(ctx, machineID); return err },
		"another machine":  func() error { _, err := b.service.UpdateAgent(ctx, otherMachineID); return err },
		"a rollback":       func() error { _, err := b.service.RollbackAgent(ctx, otherMachineID); return err },
	} {
		err := attempt()
		if key, _ := refusal.KeyOf(err); !errors.Is(err, action.ErrUpdateInProgress) || key != "action.update_in_progress" {
			t.Errorf("%s: %v", name, err)
		}
	}
	if len(b.pusher.sent) != 1 {
		t.Fatalf("%d orders left, want 1", len(b.pusher.sent))
	}
	if run, found, err := b.service.UpdateInFlight(ctx); err != nil || !found || run.ID != first.ID {
		t.Fatalf("in flight %+v %v %v", run, found, err)
	}

	// Un échec libère : la mise à jour suivante part.
	failed := agentwire.ActionReport{Results: []agentwire.ActionResult{{RunID: first.ID, State: agentwire.ActionFailed, Reason: agentwire.ReasonSelfCheckFailed}}}
	if err := b.service.Record(context.Background(), machineID, failed); err != nil {
		t.Fatal(err)
	}
	second, err := b.service.UpdateAgent(ctx, otherMachineID)
	if err != nil {
		t.Fatalf("after a failure: %v", err)
	}
	// Une mise à jour qui ne finit jamais échoit : elle ne bloque pas pour
	// toujours.
	if _, err := b.service.UpdateAgent(ctx, machineID); !errors.Is(err, action.ErrUpdateInProgress) {
		t.Fatalf("got %v", err)
	}
	clock = clock.Add(action.UpdateInFlightWindow + time.Second)
	if _, err := b.service.UpdateAgent(ctx, machineID); err != nil {
		t.Fatalf("after the window (%s): %v", second.ID, err)
	}
}

func TestUpdateAgent_AConfirmedUpdateFreesTheNextOne(t *testing.T) {
	b := readyBench(t)
	other := b.addMachine(t, otherMachineID, "vps-lyon-2", "v0.0.1", true)
	ctx := operator()
	first, err := b.service.UpdateAgent(ctx, machineID)
	if err != nil {
		t.Fatal(err)
	}
	accepted := agentwire.ActionReport{Results: []agentwire.ActionResult{{RunID: first.ID, State: agentwire.ActionAccepted}}}
	if err := b.service.Record(context.Background(), machineID, accepted); err != nil {
		t.Fatal(err)
	}
	if _, err := b.service.UpdateAgent(ctx, other.ID); !errors.Is(err, action.ErrUpdateInProgress) {
		t.Fatalf("got %v", err)
	}
	found, _ := b.db.GetMachine(ctx, machineID)
	if err := b.service.ConfirmAgentUpdate(ctx, machineID, found.Agent.ID, "v0.0.2"); err != nil {
		t.Fatal(err)
	}
	if _, err := b.service.UpdateAgent(ctx, other.ID); err != nil {
		t.Fatalf("after the confirmation: %v", err)
	}
}

func TestUpdateAgent_RecordsFromAndToInTheAudit(t *testing.T) {
	b := readyBench(t)
	ctx := audit.WithActor(context.Background(), audit.API("tok_1", "mcp-lecteur", "alice"))
	if _, err := b.service.UpdateAgent(ctx, machineID); err != nil {
		t.Fatal(err)
	}
	events, err := b.db.ListAuditEvents(context.Background(), audit.Query{Limit: 10, ObjectKind: audit.ObjectAction})
	if err != nil || len(events) != 1 {
		t.Fatalf("events %+v %v", events, err)
	}
	event := events[0]
	if event.Action != audit.ActionOrder || event.Actor.Kind != audit.ActorAPI || event.Actor.Name != "tok_1" || event.Actor.Label != "mcp-lecteur" || event.Details["on_behalf_of"] != "alice" {
		t.Fatalf("actor %+v action %s", event.Actor, event.Action)
	}
	if event.Details["kind"] != "agent.update" || event.Details["from"] != "v0.0.1" || event.Details["to"] != "v0.0.2" {
		t.Fatalf("details %v", event.Details)
	}
}

func TestRollbackAgent_SendsAnOrderWithoutParamsUnderTheUpdateRight(t *testing.T) {
	b := readyBench(t)
	ctx := operator()
	run, err := b.service.RollbackAgent(ctx, machineID)
	if err != nil {
		t.Fatal(err)
	}
	if run.Kind != agentwire.KindAgentRollback || run.State != action.StateSent {
		t.Fatalf("run %+v", run)
	}
	var body agentwire.OrderBody
	if err := json.Unmarshal(b.pusher.sent[0].Order.Body, &body); err != nil {
		t.Fatal(err)
	}
	if body.Kind != agentwire.KindAgentRollback || len(body.Params) != 0 && string(body.Params) != "null" && string(body.Params) != "{}" {
		t.Fatalf("body %+v", body)
	}
	if right, known := action.RightOf(agentwire.KindAgentRollback); !known || right != machine.CapabilityAgentUpdate {
		t.Fatalf("right %q %v", right, known)
	}
	events, _ := b.db.ListAuditEvents(context.Background(), audit.Query{Limit: 10, ObjectKind: audit.ObjectAction})
	if len(events) != 1 || events[0].Details["from"] != "v0.0.1" {
		t.Fatalf("events %+v", events)
	}
}

func TestRollbackAgent_NeedsTheRightAndAnAgentThatRollsBack(t *testing.T) {
	missing := newUpdateBench(t, updateOptions{rights: "observe", agentVersion: "v0.0.1", arch: "amd64"})
	if _, err := missing.service.RollbackAgent(operator(), machineID); !errors.Is(err, action.ErrRightMissing) {
		t.Fatalf("without the right: %v", err)
	}
	notMigrated := newUpdateBench(t, updateOptions{rights: "observe,agent.update", agentVersion: "v0.0.1", arch: "amd64", noRollback: true})
	if _, err := notMigrated.service.RollbackAgent(operator(), machineID); !errors.Is(err, action.ErrRollbackUnavailable) {
		t.Fatalf("not migrated: %v", err)
	}
	if _, err := notMigrated.service.RollbackAgent(operator(), "unknown"); !errors.Is(err, machine.ErrNotFound) {
		t.Fatalf("unknown machine: %v", err)
	}
}

func rolledBackBench(t *testing.T) (*updateBench, *fakeAlerter, action.Run) {
	t.Helper()
	b := readyBench(t)
	alerts := &fakeAlerter{}
	b.service.SetAlerter(alerts)
	run, err := b.service.UpdateAgent(operator(), machineID)
	if err != nil {
		t.Fatal(err)
	}
	accepted := agentwire.ActionReport{Results: []agentwire.ActionResult{{RunID: run.ID, State: agentwire.ActionAccepted}}}
	if err := b.service.Record(context.Background(), machineID, accepted); err != nil {
		t.Fatal(err)
	}
	return b, alerts, run
}

// L'ancienne version revient et dit ce qui s'est passé : le run se ferme en
// échec (jamais en succès), l'audit a un événement dont l'acteur est l'agent,
// et l'alerte est levée.
func TestRecord_AnAgentThatRolledBackClosesTheRunInFailureAndRaisesTheAlert(t *testing.T) {
	for _, reason := range []string{agentwire.ReasonRolledBackTimeout, agentwire.ReasonRolledBackCrash} {
		b, alerts, run := rolledBackBench(t)
		agentCtx := audit.WithActor(context.Background(), audit.Agent(machineID))
		report := agentwire.ActionReport{Results: []agentwire.ActionResult{{
			RunID: run.ID, State: agentwire.ActionFailed, Reason: reason,
			Output: []agentwire.OutputLine{{Stream: "stdout", Line: "target v0.0.2"}},
		}}}
		if err := b.service.Record(agentCtx, machineID, report); err != nil {
			t.Fatal(err)
		}
		got, err := b.db.GetActionRun(context.Background(), run.ID)
		if err != nil || got.State != action.StateFailed || got.Reason != reason {
			t.Fatalf("%s: run %+v %v", reason, got, err)
		}
		events, err := b.db.ListAuditEvents(context.Background(), audit.Query{Limit: 10, ObjectKind: audit.ObjectAction})
		if err != nil {
			t.Fatal(err)
		}
		var found *audit.Event
		for index := range events {
			if events[index].Action == audit.ActionRolledBack {
				found = &events[index]
			}
		}
		if found == nil || found.Actor != audit.Agent(machineID) || found.ObjectID != run.ID {
			t.Fatalf("%s: audit %+v", reason, events)
		}
		if found.Details["target"] != "v0.0.2" || found.Details["reason"] != reason || found.Details["version"] != "v0.0.1" || found.Details["machine_id"] != machineID {
			t.Fatalf("%s: details %v", reason, found.Details)
		}
		wantCause := "timeout"
		if reason == agentwire.ReasonRolledBackCrash {
			wantCause = "crash"
		}
		if len(alerts.rolledBack) != 1 || alerts.rolledBack[0] != (rolledBackCall{machineID, "vps-paris-1", "v0.0.2", wantCause, false}) {
			t.Fatalf("%s: alert %+v", reason, alerts.rolledBack)
		}
		// Le même rapport rejoué ne refait ni audit ni alerte.
		if err := b.service.Record(agentCtx, machineID, report); err != nil {
			t.Fatal(err)
		}
		if len(alerts.rolledBack) != 1 {
			t.Fatalf("%s: the replay raised another alert", reason)
		}
		// Un run fermé en échec libère la mise à jour suivante.
		if _, err := b.service.UpdateAgent(operator(), machineID); err != nil {
			t.Fatalf("%s: after the return: %v", reason, err)
		}
	}
}

func TestRecord_AFailedRecoveryRaisesTheLoudAlert(t *testing.T) {
	b, alerts, run := rolledBackBench(t)
	agentCtx := audit.WithActor(context.Background(), audit.Agent(machineID))
	report := agentwire.ActionReport{Results: []agentwire.ActionResult{{RunID: run.ID, State: agentwire.ActionFailed, Reason: agentwire.ReasonRollbackFailed}}}
	if err := b.service.Record(agentCtx, machineID, report); err != nil {
		t.Fatal(err)
	}
	if len(alerts.rolledBack) != 1 || !alerts.rolledBack[0].failed || alerts.rolledBack[0].reason != "failed" {
		t.Fatalf("alert %+v", alerts.rolledBack)
	}
	got, _ := b.db.GetActionRun(context.Background(), run.ID)
	if got.State != action.StateFailed {
		t.Fatalf("run %+v", got)
	}
}

// Un "succès" ne se déclare pas avec la raison d'un échec, et une autre sorte
// d'ordre qui dirait la même chose ne lève rien.
func TestRecord_OnlyAFailedUpdateOrRollbackRaisesTheRolledBackAlert(t *testing.T) {
	b, alerts, run := rolledBackBench(t)
	agentCtx := audit.WithActor(context.Background(), audit.Agent(machineID))
	ping, err := b.service.Ping(operator(), machineID)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{ping.ID} {
		report := agentwire.ActionReport{Results: []agentwire.ActionResult{{RunID: id, State: agentwire.ActionFailed, Reason: agentwire.ReasonRolledBackTimeout}}}
		if err := b.service.Record(agentCtx, machineID, report); err != nil {
			t.Fatal(err)
		}
	}
	succeeded := agentwire.ActionReport{Results: []agentwire.ActionResult{{RunID: run.ID, State: agentwire.ActionSucceeded, Reason: agentwire.ReasonRolledBackTimeout}}}
	if err := b.service.Record(agentCtx, machineID, succeeded); err != nil {
		t.Fatal(err)
	}
	if len(alerts.rolledBack) != 0 {
		t.Fatalf("alerts %+v", alerts.rolledBack)
	}
}

func TestConfirmAgentUpdate_ResolvesTheRolledBackAlertOfThatMachine(t *testing.T) {
	b, alerts, run := rolledBackBench(t)
	found, _ := b.db.GetMachine(context.Background(), machineID)
	if err := b.service.ConfirmAgentUpdate(operator(), machineID, found.Agent.ID, "v0.0.2"); err != nil {
		t.Fatal(err)
	}
	if len(alerts.updated) != 1 || alerts.updated[0] != machineID {
		t.Fatalf("updated %v", alerts.updated)
	}
	// Une version qui ne ferme rien ne referme pas l'alerte.
	if err := b.service.ConfirmAgentUpdate(operator(), machineID, found.Agent.ID, "v0.0.9"); err != nil {
		t.Fatal(err)
	}
	if len(alerts.updated) != 1 {
		t.Fatalf("updated %v", alerts.updated)
	}
	_ = run
}

// Un retour arrière manuel : l'agent le dit lui-même une fois la version
// d'avant confirmée.
func TestRecord_AConfirmedManualRollbackSucceedsAndResolvesTheAlert(t *testing.T) {
	b := readyBench(t)
	alerts := &fakeAlerter{}
	b.service.SetAlerter(alerts)
	run, err := b.service.RollbackAgent(operator(), machineID)
	if err != nil {
		t.Fatal(err)
	}
	agentCtx := audit.WithActor(context.Background(), audit.Agent(machineID))
	for _, state := range []agentwire.ActionState{agentwire.ActionAccepted, agentwire.ActionSucceeded} {
		report := agentwire.ActionReport{Results: []agentwire.ActionResult{{RunID: run.ID, State: state}}}
		if err := b.service.Record(agentCtx, machineID, report); err != nil {
			t.Fatal(err)
		}
	}
	got, _ := b.db.GetActionRun(context.Background(), run.ID)
	if got.State != action.StateSucceeded {
		t.Fatalf("run %+v", got)
	}
	if len(alerts.updated) != 1 {
		t.Fatalf("updated %v", alerts.updated)
	}
}

// Le plan : rien ne s'exécute ; un ordre recommandé, les machines à mettre à
// jour en dernier à la fin.
func TestUpdatePlan_RanksReadyMachinesAndPutsUpdateLastAtTheEnd(t *testing.T) {
	b := readyBench(t) // vps-paris-1, v0.0.1, migrée
	proxy := b.addMachine(t, otherMachineID, "a-proxy", "v0.0.1", true)
	if err := b.db.SetUpdateLast(context.Background(), proxy.ID, true); err != nil {
		t.Fatal(err)
	}
	b.addMachine(t, "33333333-4444-4555-8666-777777777777", "z-current", "v0.0.2", true)
	b.addMachine(t, "44444444-5555-4666-8777-888888888888", "old-agent", "v0.0.1", false)

	plan, err := b.service.UpdatePlan(operator())
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]action.PlanEntry{}
	var order []string
	for _, entry := range plan {
		got[entry.MachineName] = entry
		if entry.Order > 0 {
			order = append(order, entry.MachineName)
		}
	}
	// vps-paris-1 d'abord (ordinaire), a-proxy ensuite (en dernier).
	if len(order) != 2 || order[0] != "vps-paris-1" || order[1] != "a-proxy" || got["vps-paris-1"].Order != 1 || got["a-proxy"].Order != 2 {
		t.Fatalf("order %v: %+v", order, plan)
	}
	if !got["a-proxy"].UpdateLast || got["vps-paris-1"].UpdateLast {
		t.Fatalf("update_last %+v", plan)
	}
	if entry := got["z-current"]; !entry.UpToDate || entry.Order != 0 || entry.Blocked != "" || entry.Version != "v0.0.2" || entry.Target != "v0.0.2" {
		t.Fatalf("up to date %+v", entry)
	}
	if entry := got["old-agent"]; entry.UpToDate || entry.RollbackCapable || entry.Blocked != action.OfferRollbackUnavailable || entry.Order != 0 {
		t.Fatalf("not migrated %+v", entry)
	}
	if entry := got["vps-paris-1"]; !entry.RollbackCapable || !entry.Online || entry.Blocked != "" {
		t.Fatalf("ready %+v", entry)
	}
	// Lire le plan n'envoie rien.
	if len(b.pusher.sent) != 0 {
		t.Fatalf("%d orders left while reading the plan", len(b.pusher.sent))
	}
	if b.service.TargetVersion() != "v0.0.2" {
		t.Fatalf("target %q", b.service.TargetVersion())
	}
}
