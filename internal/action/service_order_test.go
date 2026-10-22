package action_test

import (
	"context"
	"encoding/json"
	"log/slog"
	"testing"
	"time"

	"github.com/ldesfontaine/opencloud/internal/action"
	"github.com/ldesfontaine/opencloud/internal/agentwire"
	"github.com/ldesfontaine/opencloud/internal/audit"
	"github.com/ldesfontaine/opencloud/internal/directory"
)

// declareService écrit un service Compose sur la machine du banc : le run
// d'un déploiement s'y rattache.
func (b *bench) declareService(t *testing.T) directory.Service {
	t.Helper()
	created, err := directory.New(b.db, slog.New(slog.DiscardHandler)).CreateService(operator(), directory.ServiceDefinition{
		MachineID: machineID, Name: "nextcloud", Nature: directory.NatureApplication,
		Origin: directory.OriginCompose, ComposeProject: "nextcloud",
		Units: []directory.UnitDefinition{{Name: "app", Image: "nextcloud", Version: "29.0.4"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return created
}

// Un ordre qui vise un service s'y rattache : le run le garde, l'audit le
// dit, et la liste des runs le relit. Le run survit au service.
func TestSendForService_TheRunKeepsItsServiceAndTheAuditSaysIt(t *testing.T) {
	b := newBenchReporting(t, "docker.deploy,docker.update")
	service := b.declareService(t)

	run, err := b.service.SendForService(operator(), action.ServiceOrder{
		MachineID: machineID, ServiceID: service.ID, Kind: agentwire.KindDockerDeploy,
		Params: json.RawMessage(`{"project":"nextcloud"}`), Secret: json.RawMessage(`{"env":"A=b"}`), SecretLabel: "env",
	})
	if err != nil {
		t.Fatal(err)
	}
	if run.ServiceID != service.ID {
		t.Fatalf("run %+v", run)
	}
	if stored := b.runs(t); len(stored) != 1 || stored[0].ServiceID != service.ID {
		t.Fatalf("stored runs %+v", stored)
	}
	events, err := b.db.ListAuditEvents(context.Background(), audit.Query{Limit: 10, ObjectKind: audit.ObjectAction})
	if err != nil || len(events) != 1 {
		t.Fatalf("events %v %v", events, err)
	}
	if details := events[0].Details; details["service_id"] != service.ID || details["env"] != "sent" || details["kind"] != "docker.deploy" {
		t.Fatalf("audit details %v", details)
	}

	if err := b.db.DeleteDeclaredService(context.Background(), service.ID); err != nil {
		t.Fatal(err)
	}
	if stored := b.runs(t); len(stored) != 1 || stored[0].ServiceID != "" {
		t.Fatalf("the run must survive its service, unlinked: %+v", stored)
	}
}

// Send ne rattache à aucun service.
func TestSend_LinksNoService(t *testing.T) {
	b := newBench(t)
	if _, err := b.service.Ping(operator(), machineID); err != nil {
		t.Fatal(err)
	}
	if runs := b.runs(t); len(runs) != 1 || runs[0].ServiceID != "" {
		t.Fatalf("runs %+v", runs)
	}
}

// Le serveur propose « Redéployer » quand un déploiement a écrit le dossier :
// un run réussi, ou un échec de Compose (le dossier existe). Un refus de
// l'agent, lui, n'a rien écrit.
func TestLastWrittenServiceRun_CountsWhatWroteTheFolder(t *testing.T) {
	b := newBenchReporting(t, "docker.deploy,docker.update")
	service := b.declareService(t)
	ctx := operator()
	kinds := []agentwire.Kind{agentwire.KindDockerDeploy, agentwire.KindDockerUpdate}
	send := func(kind agentwire.Kind) action.Run {
		t.Helper()
		// Le banc a une horloge fixe : un instant de plus par ordre, pour que
		// « le dernier » soit celui qui est parti en dernier.
		b.now = b.now.Add(time.Minute)
		run, err := b.service.SendForService(ctx, action.ServiceOrder{MachineID: machineID, ServiceID: service.ID, Kind: kind})
		if err != nil {
			t.Fatal(err)
		}
		return run
	}
	answer := func(run action.Run, state agentwire.ActionState, reason string) {
		t.Helper()
		report := agentwire.ActionReport{Results: []agentwire.ActionResult{{RunID: run.ID, State: state, Reason: reason}}}
		if err := b.service.Record(context.Background(), machineID, report); err != nil {
			t.Fatal(err)
		}
	}

	if _, found, err := b.db.LastWrittenServiceRun(ctx, service.ID, kinds); err != nil || found {
		t.Fatalf("nothing was sent yet: %v %v", found, err)
	}
	refused := send(agentwire.KindDockerDeploy)
	answer(refused, agentwire.ActionRefused, agentwire.ReasonDockerMissing)
	failedEarly := send(agentwire.KindDockerDeploy)
	answer(failedEarly, agentwire.ActionFailed, agentwire.ReasonDiskFull)
	if _, found, err := b.db.LastWrittenServiceRun(ctx, service.ID, kinds); err != nil || found {
		t.Fatalf("a refusal or an early failure writes nothing: %v %v", found, err)
	}
	if last, found, err := b.db.LastServiceRun(ctx, service.ID, kinds); err != nil || !found || last.ID != failedEarly.ID {
		t.Fatalf("the last run, whatever its state: %+v %v %v", last, found, err)
	}

	composeFailed := send(agentwire.KindDockerDeploy)
	answer(composeFailed, agentwire.ActionFailed, agentwire.ReasonComposeFailed)
	if last, found, err := b.db.LastWrittenServiceRun(ctx, service.ID, kinds); err != nil || !found || last.ID != composeFailed.ID {
		t.Fatalf("compose failed after the folder was written: %+v %v %v", last, found, err)
	}

	succeeded := send(agentwire.KindDockerUpdate)
	answer(succeeded, agentwire.ActionSucceeded, "")
	last, found, err := b.db.LastWrittenServiceRun(ctx, service.ID, kinds)
	if err != nil || !found || last.ID != succeeded.ID {
		t.Fatalf("the newest written run: %+v %v %v", last, found, err)
	}
	if _, found, err := b.db.LastWrittenServiceRun(ctx, service.ID, kinds[:1]); err != nil || !found {
		t.Fatalf("by kind: %v %v", found, err)
	}
	if _, found, err := b.db.LastWrittenServiceRun(ctx, "svc_other", kinds); err != nil || found {
		t.Fatalf("another service: %v %v", found, err)
	}
}

func TestRightOf_TheDockerOrdersHaveTheirOwnRight(t *testing.T) {
	for kind, want := range map[agentwire.Kind]string{
		agentwire.KindDockerDeploy: "docker.deploy",
		agentwire.KindDockerUpdate: "docker.update",
	} {
		if right, ok := action.RightOf(kind); !ok || right != want {
			t.Errorf("%s: %q %t", kind, right, ok)
		}
	}
}
