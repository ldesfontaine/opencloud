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

// Le serveur juge « Déployer » ou « Redéployer » sur le dernier constat de
// l'agent sur le dossier : un run réussi, un échec de Compose, un run coupé,
// ou un refus faute de dossier. Un refus d'avant l'écriture n'en dit rien,
// ni un dossier déjà là que le serveur ne sait pas avoir écrit.
func TestLastFolderRun_KeepsWhatTheAgentSaidOfTheFolder(t *testing.T) {
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
	lastFolder := func(kinds []agentwire.Kind) (action.Run, bool) {
		t.Helper()
		run, found, err := b.db.LastFolderRun(ctx, service.ID, kinds)
		if err != nil {
			t.Fatal(err)
		}
		return run, found
	}

	if _, found := lastFolder(kinds); found {
		t.Fatal("nothing was sent yet")
	}
	refused := send(agentwire.KindDockerDeploy)
	answer(refused, agentwire.ActionRefused, agentwire.ReasonDockerMissing)
	failedEarly := send(agentwire.KindDockerDeploy)
	answer(failedEarly, agentwire.ActionFailed, agentwire.ReasonDiskFull)
	if _, found := lastFolder(kinds); found {
		t.Fatal("a refusal or an early failure says nothing of the folder")
	}
	if last, found, err := b.db.LastServiceRun(ctx, service.ID, kinds); err != nil || !found || last.ID != failedEarly.ID {
		t.Fatalf("the last run, whatever its state: %+v %v %v", last, found, err)
	}

	composeFailed := send(agentwire.KindDockerDeploy)
	answer(composeFailed, agentwire.ActionFailed, agentwire.ReasonComposeFailed)
	if last, found := lastFolder(kinds); !found || last.ID != composeFailed.ID {
		t.Fatalf("compose failed after the folder was written: %+v %v", last, found)
	}

	// Un dossier déjà là que le serveur ne sait pas avoir écrit : rien de sûr,
	// le constat d'avant reste le dernier.
	exists := send(agentwire.KindDockerDeploy)
	answer(exists, agentwire.ActionRefused, agentwire.ReasonServiceDirExists)
	if last, found := lastFolder(kinds); !found || last.ID != composeFailed.ID {
		t.Fatalf("service_dir_exists must not count: %+v %v", last, found)
	}

	// Accepté puis plus de nouvelles : la boucle de fond le passe en timeout,
	// et l'agent a pu écrire le dossier.
	cut := send(agentwire.KindDockerUpdate)
	answer(cut, agentwire.ActionAccepted, "")
	if last, _ := lastFolder(kinds); last.ID != composeFailed.ID {
		t.Fatalf("an accepted run says nothing yet: %+v", last)
	}
	b.now = b.now.Add(action.MaxRunDuration + time.Minute)
	if err := b.service.Timeout(context.Background()); err != nil {
		t.Fatal(err)
	}
	if last, found := lastFolder(kinds); !found || last.ID != cut.ID || last.State != action.StateFailed || last.Reason != action.ReasonTimeout {
		t.Fatalf("a timed out run says the folder may exist: %+v %v", last, found)
	}

	// L'agent a regardé et n'a pas de dossier : c'est le dernier constat.
	missing := send(agentwire.KindDockerUpdate)
	answer(missing, agentwire.ActionRefused, agentwire.ReasonServiceDirMissing)
	if last, found := lastFolder(kinds); !found || last.ID != missing.ID {
		t.Fatalf("a missing folder is the latest word: %+v %v", last, found)
	}

	succeeded := send(agentwire.KindDockerDeploy)
	answer(succeeded, agentwire.ActionSucceeded, "")
	if last, found := lastFolder(kinds); !found || last.ID != succeeded.ID {
		t.Fatalf("the newest word: %+v %v", last, found)
	}
	if _, found := lastFolder(kinds[:1]); !found {
		t.Fatal("by kind")
	}
	if _, found, err := b.db.LastFolderRun(ctx, "svc_other", kinds); err != nil || found {
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
