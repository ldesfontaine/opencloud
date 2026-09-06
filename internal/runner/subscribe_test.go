package runner

import (
	"context"
	"testing"
	"time"

	"github.com/ldesfontaine/opencloud/internal/catalog"
	"github.com/ldesfontaine/opencloud/internal/store"
)

func nextEvent(t *testing.T, events <-chan Event) (Event, bool) {
	t.Helper()
	select {
	case event, open := <-events:
		return event, open
	case <-time.After(5 * time.Second):
		t.Fatal("aucun événement reçu à temps")
		return Event{}, false
	}
}

func TestSubscribe_ReceivesEachLineThenTheEnd_AndClosesTheChannel(t *testing.T) {
	database := newTestStore(t)
	machineTransport := newFakeTransport()
	// La porte tient le suivi le temps que l'abonnement soit posé.
	machineTransport.gate = make(chan struct{})
	runner := newTestRunner(t, database, newFakeCatalog(), &fakeTransports{transport: machineTransport})

	action, err := runner.Enqueue(context.Background(), store.LocalMachineID, catalog.KindDiagnostiquer, nil)
	if err != nil {
		t.Fatal(err)
	}
	events, unsubscribe := runner.Subscribe(action.ID)
	defer unsubscribe()
	close(machineTransport.gate)

	first, _ := nextEvent(t, events)
	if first.Done || first.Seq != 1 || first.Text != "étape: disque" {
		t.Fatalf("premier événement = %+v", first)
	}
	second, _ := nextEvent(t, events)
	if second.Seq != 2 {
		t.Fatalf("deuxième événement = %+v", second)
	}
	end, _ := nextEvent(t, events)
	if !end.Done || end.State != store.StateApplied || end.Result != "rien à signaler" {
		t.Fatalf("fin = %+v", end)
	}
	if _, open := nextEvent(t, events); open {
		t.Fatal("le canal doit se fermer après la fin de l'action")
	}
}

func TestUnsubscribe_ClosesTheChannel(t *testing.T) {
	database := newTestStore(t)
	runner := newTestRunner(t, database, newFakeCatalog(), &fakeTransports{transport: newFakeTransport()})

	events, unsubscribe := runner.Subscribe("diagnostiquer-absente")
	unsubscribe()

	if _, open := nextEvent(t, events); open {
		t.Fatal("le canal doit être fermé après le désabonnement")
	}
}

func TestClose_StopsTheRunner_AndLeavesRunningActionsToTheNextResume(t *testing.T) {
	database := newTestStore(t)
	machineTransport := newFakeTransport()
	machineTransport.gate = make(chan struct{})
	runner := New(database, newFakeCatalog(), &fakeTransports{transport: machineTransport}, discardLogger())

	action, err := runner.Enqueue(context.Background(), store.LocalMachineID, catalog.KindDiagnostiquer, nil)
	if err != nil {
		t.Fatal(err)
	}
	waitForState(t, database, action.ID, store.StateRunning)
	runner.Close()

	stopped, err := database.Action(context.Background(), action.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stopped.State != store.StateRunning {
		t.Fatalf("état = %s : l'arrêt ne conclut rien, la reprise s'en occupe", stopped.State)
	}
}
