package action_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/ldesfontaine/opencloud/internal/action"
	"github.com/ldesfontaine/opencloud/internal/agentwire"
	"github.com/ldesfontaine/opencloud/internal/audit"
	"github.com/ldesfontaine/opencloud/internal/machine"
)

// fakeUpdates tient lieu du composant deploy : il rend la fin qu'on lui donne.
type fakeUpdates struct {
	end   action.UpdateEnd
	found bool
	runs  []action.Run
	late  []string
}

func (f *fakeUpdates) SettleUpdate(_ context.Context, run action.Run) (action.UpdateEnd, bool, error) {
	f.runs = append(f.runs, run)
	return f.end, f.found, nil
}

func (f *fakeUpdates) SettleLateUpdate(_ context.Context, runID, reason string) (action.UpdateEnd, bool, error) {
	f.late = append(f.late, runID+":"+reason)
	end := f.end
	end.Late = true
	return end, f.found, nil
}

func updateOrderBench(t *testing.T, updates *fakeUpdates) (*updateBench, *fakeAlerter, action.Run) {
	t.Helper()
	b := newUpdateBench(t, updateOptions{rights: "observe,docker.update", agentVersion: "v0.5.0", arch: "amd64"})
	alerts := &fakeAlerter{}
	b.service.SetAlerter(alerts)
	b.service.SetServiceUpdates(updates)
	safety := agentwire.DefaultServiceRollback()
	params, _ := json.Marshal(agentwire.DockerParams{ServiceID: "svc_1", Project: "nextcloud", Compose: "x", Rollback: &safety})
	run, err := b.service.SendForService(operator(), action.ServiceOrder{MachineID: machineID, Kind: agentwire.KindDockerUpdate, Params: params})
	if err != nil {
		t.Fatal(err)
	}
	return b, alerts, run
}

func rolledBackEnd() action.UpdateEnd {
	return action.UpdateEnd{ServiceID: "svc_1", ServiceName: "Nextcloud", UnitName: "app", FromVersion: "29.0.4", ToVersion: "29.0.5", Reverted: true}
}

// Une montée revenue en arrière : l'audit nomme l'agent, l'alerte porte les
// versions et le journal, et le run se ferme en échec.
func TestRecord_AServiceUpdateThatWentBackRaisesTheAlertWithItsJournal(t *testing.T) {
	updates := &fakeUpdates{end: rolledBackEnd(), found: true}
	b, alerts, run := updateOrderBench(t, updates)
	agentCtx := audit.WithActor(context.Background(), audit.Agent(machineID))
	journal := []string{"app | connection refused", "app | exiting"}
	report := agentwire.ActionReport{Results: []agentwire.ActionResult{{
		RunID: run.ID, State: agentwire.ActionFailed, Reason: agentwire.ReasonRolledBackVersion, Journal: journal,
	}}}
	if err := b.service.Record(agentCtx, machineID, report); err != nil {
		t.Fatal(err)
	}
	got, err := b.db.GetActionRun(context.Background(), run.ID)
	if err != nil || got.State != action.StateFailed || got.Reason != agentwire.ReasonRolledBackVersion {
		t.Fatalf("run %+v %v", got, err)
	}
	if len(alerts.serviceRolledBack) != 1 {
		t.Fatalf("alerts %+v", alerts.serviceRolledBack)
	}
	call := alerts.serviceRolledBack[0]
	if call.serviceID != "svc_1" || call.from != "29.0.4" || call.to != "29.0.5" || call.failed || strings.Join(call.journal, "|") != strings.Join(journal, "|") {
		t.Fatalf("alert %+v", call)
	}
	events, _ := b.db.ListAuditEvents(context.Background(), audit.Query{Limit: 10, ObjectKind: audit.ObjectAction})
	var found *audit.Event
	for index := range events {
		if events[index].Action == audit.ActionRolledBack {
			found = &events[index]
		}
	}
	if found == nil || found.Actor != audit.Agent(machineID) || found.Details["service_id"] != "svc_1" || found.Details["target"] != "29.0.5" || found.Details["reason"] != agentwire.ReasonRolledBackVersion {
		t.Fatalf("audit %+v", events)
	}

	// Le même rapport rejoué (le run est fermé) ne refait ni audit ni alerte.
	updates.found = false // la montée est fermée : le composant ne la retrouve plus
	if err := b.service.Record(agentCtx, machineID, report); err != nil || len(alerts.serviceRolledBack) != 1 || len(updates.runs) != 1 {
		t.Fatalf("replay: alerts %d, settles %d, err %v", len(alerts.serviceRolledBack), len(updates.runs), err)
	}
}

func TestRecord_AServiceUpdateThatFailedToGoBackIsAnAlertInDanger(t *testing.T) {
	end := rolledBackEnd()
	end.Failed = true
	b, alerts, run := updateOrderBench(t, &fakeUpdates{end: end, found: true})
	report := agentwire.ActionReport{Results: []agentwire.ActionResult{{RunID: run.ID, State: agentwire.ActionFailed, Reason: agentwire.ReasonRollbackFailed}}}
	if err := b.service.Record(context.Background(), machineID, report); err != nil {
		t.Fatal(err)
	}
	if len(alerts.serviceRolledBack) != 1 || !alerts.serviceRolledBack[0].failed {
		t.Fatalf("alerts %+v", alerts.serviceRolledBack)
	}
}

// Une montée confirmée referme l'alerte ; une fin que le composant ne connaît
// pas (un redéploiement) ne dit rien à personne.
func TestRecord_AConfirmedServiceUpdateClosesTheAlertAndAPlainRedeploymentSaysNothing(t *testing.T) {
	confirmed := &fakeUpdates{end: action.UpdateEnd{ServiceID: "svc_1", Confirmed: true}, found: true}
	b, alerts, run := updateOrderBench(t, confirmed)
	succeeded := agentwire.ActionReport{Results: []agentwire.ActionResult{{RunID: run.ID, State: agentwire.ActionSucceeded}}}
	if err := b.service.Record(context.Background(), machineID, succeeded); err != nil {
		t.Fatal(err)
	}
	if len(alerts.serviceUpdated) != 1 || alerts.serviceUpdated[0] != "svc_1" || len(alerts.serviceRolledBack) != 0 {
		t.Fatalf("updated %v, rolled back %v", alerts.serviceUpdated, alerts.serviceRolledBack)
	}

	unknown := &fakeUpdates{found: false}
	b, alerts, run = updateOrderBench(t, unknown)
	failed := agentwire.ActionReport{Results: []agentwire.ActionResult{{RunID: run.ID, State: agentwire.ActionFailed, Reason: agentwire.ReasonRolledBackVersion}}}
	if err := b.service.Record(context.Background(), machineID, failed); err != nil {
		t.Fatal(err)
	}
	if len(alerts.serviceRolledBack) != 0 || len(alerts.serviceUpdated) != 0 {
		t.Fatalf("an unknown update spoke: %+v %v", alerts.serviceRolledBack, alerts.serviceUpdated)
	}
}

func TestRecord_RejectsAJournalBeyondTheProtocolLimits(t *testing.T) {
	b, _, run := updateOrderBench(t, &fakeUpdates{})
	tooMany := make([]string, agentwire.MaxJournalLines+1)
	tooLong := []string{strings.Repeat("x", agentwire.MaxJournalBytes+1)}
	for name, journal := range map[string][]string{"too many lines": tooMany, "too many bytes": tooLong, "control character": {"a\x00b"}, "not UTF-8": {"\xff\xfe"}} {
		report := agentwire.ActionReport{Results: []agentwire.ActionResult{{RunID: run.ID, State: agentwire.ActionFailed, Reason: agentwire.ReasonRolledBackVersion, Journal: journal}}}
		if err := b.service.Record(context.Background(), machineID, report); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestRightOf_ADockerRollbackNeedsTheUpdateRight(t *testing.T) {
	if right, known := action.RightOf(agentwire.KindDockerRollback); !known || right != machine.CapabilityDockerUpdate {
		t.Fatalf("right %q %v", right, known)
	}
	if !agentwire.KindDockerRollback.IsOrder() {
		t.Fatal("docker.rollback is not a signed order")
	}
}

// Une montée refusée avant d'écrire remet la version d'avant dans l'annuaire,
// mais n'est ni un retour arrière ni une alerte : rien n'a bougé sur la machine.
func TestRecord_AServiceUpdateRefusedBeforeWritingRaisesNoAlertAndNoAudit(t *testing.T) {
	end := rolledBackEnd()
	end.Reverted = false
	b, alerts, run := updateOrderBench(t, &fakeUpdates{end: end, found: true})
	report := agentwire.ActionReport{Results: []agentwire.ActionResult{{RunID: run.ID, State: agentwire.ActionRefused, Reason: agentwire.ReasonDiskFull}}}
	if err := b.service.Record(context.Background(), machineID, report); err != nil {
		t.Fatal(err)
	}
	events, _ := b.db.ListAuditEvents(context.Background(), audit.Query{Limit: 10, ObjectKind: audit.ObjectAction})
	for _, event := range events {
		if event.Action == audit.ActionRolledBack {
			t.Fatalf("a refusal was audited as a rollback: %+v", event)
		}
	}
	if len(alerts.serviceRolledBack) != 0 {
		t.Fatalf("alerts %+v", alerts.serviceRolledBack)
	}
}

// Le rapport arrive après l'expiration du run : l'annuaire n'est PAS remis (le
// composant des montées n'est pas appelé pour cela), mais l'alerte part, dite
// « rapport tardif », avec son audit. Retirer cette voie, ou la passer par la
// fermeture normale qui remet la version, fait échouer le test.
func TestRecord_ALateServiceReportRaisesTheAlertWithoutTouchingTheDirectory(t *testing.T) {
	for _, reason := range []string{agentwire.ReasonRolledBackVersion, agentwire.ReasonRollbackFailed} {
		updates := &fakeUpdates{end: rolledBackEnd(), found: true}
		b, alerts, run := updateOrderBench(t, updates)
		end := rolledBackEnd()
		end.Failed = reason == agentwire.ReasonRollbackFailed
		updates.end = end
		// Le serveur a clos le run (une heure sans réponse).
		if err := b.db.SetRunState(context.Background(), run.ID, action.StateFailed, "timeout", time.Now()); err != nil {
			t.Fatal(err)
		}
		agentCtx := audit.WithActor(context.Background(), audit.Agent(machineID))
		report := agentwire.ActionReport{Results: []agentwire.ActionResult{{RunID: run.ID, State: agentwire.ActionFailed, Reason: reason, Journal: []string{"app | boom"}}}}
		if err := b.service.Record(agentCtx, machineID, report); err != nil {
			t.Fatal(err)
		}
		if len(updates.runs) != 0 {
			t.Fatalf("%s: the normal settle (which puts the version back) ran for a late report", reason)
		}
		if len(updates.late) != 1 || len(alerts.serviceRolledBack) != 1 {
			t.Fatalf("%s: late %v, alerts %+v", reason, updates.late, alerts.serviceRolledBack)
		}
		call := alerts.serviceRolledBack[0]
		if !call.late || call.failed != end.Failed || len(call.journal) != 1 {
			t.Fatalf("%s: alert %+v", reason, call)
		}
		events, _ := b.db.ListAuditEvents(context.Background(), audit.Query{Limit: 10, ObjectKind: audit.ObjectAction})
		late := false
		for _, event := range events {
			late = late || (event.Action == audit.ActionRolledBack && event.Details["late"] == "true" && event.Details["reason"] == reason)
		}
		if !late {
			t.Fatalf("%s: no late audit event: %+v", reason, events)
		}
		// Rejoué : la montée n'est plus en attente, rien ne repart.
		updates.found = false
		if err := b.service.Record(agentCtx, machineID, report); err != nil || len(alerts.serviceRolledBack) != 1 {
			t.Fatalf("%s: replay raised %d alerts (%v)", reason, len(alerts.serviceRolledBack), err)
		}
	}
}

// Une machine ne clôt pas la montée d'une autre, et un état qui n'est pas un
// retour arrière n'ouvre rien.
func TestRecord_ALateReportOfAnotherKindOrReasonOpensNothing(t *testing.T) {
	updates := &fakeUpdates{end: rolledBackEnd(), found: true}
	b, alerts, run := updateOrderBench(t, updates)
	if err := b.db.SetRunState(context.Background(), run.ID, action.StateFailed, "timeout", time.Now()); err != nil {
		t.Fatal(err)
	}
	for _, result := range []agentwire.ActionResult{
		{RunID: run.ID, State: agentwire.ActionSucceeded},
		{RunID: run.ID, State: agentwire.ActionFailed, Reason: agentwire.ReasonComposeFailed},
	} {
		if err := b.service.Record(context.Background(), machineID, agentwire.ActionReport{Results: []agentwire.ActionResult{result}}); err != nil {
			t.Fatal(err)
		}
	}
	if len(updates.late) != 0 || len(alerts.serviceRolledBack) != 0 {
		t.Fatalf("late %v, alerts %+v", updates.late, alerts.serviceRolledBack)
	}
}
