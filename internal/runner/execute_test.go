package runner

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/ldesfontaine/opencloud/internal/actiondir"
	"github.com/ldesfontaine/opencloud/internal/catalog"
	"github.com/ldesfontaine/opencloud/internal/refusal"
	"github.com/ldesfontaine/opencloud/internal/store"
	"github.com/ldesfontaine/opencloud/internal/transport"
)

func TestEnqueue_JournalsThenApplies_AndKeepsTheResultLine(t *testing.T) {
	database := newTestStore(t)
	machineTransport := newFakeTransport()
	runner := newTestRunner(t, database, newFakeCatalog(), &fakeTransports{transport: machineTransport})

	action, err := runner.Enqueue(context.Background(), store.LocalMachineID, catalog.KindDiagnostiquer, nil)
	if err != nil {
		t.Fatal(err)
	}

	if !actiondir.ValidID(action.ID) {
		t.Fatalf("identifiant %q hors de la forme du lanceur", action.ID)
	}
	if action.UnitName != actiondir.UnitName(action.ID) || action.State != store.StatePrepared {
		t.Fatalf("action journalisée = %+v", action)
	}

	concluded := waitForConclusion(t, database, action.ID)
	if concluded.State != store.StateApplied {
		t.Fatalf("état = %s, note = %q", concluded.State, concluded.Note)
	}
	if concluded.Result != "rien à signaler" {
		t.Fatalf("résultat = %q", concluded.Result)
	}
	if concluded.ExitCode == nil || *concluded.ExitCode != 0 || concluded.LaunchedAt.IsZero() {
		t.Fatalf("code = %v, lancée le %s", concluded.ExitCode, concluded.LaunchedAt)
	}

	lines, err := database.Lines(context.Background(), action.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != 2 || lines[0].Text != "étape: disque" {
		t.Fatalf("lignes = %+v", lines)
	}
	if concluded.LastCursor != "c2" {
		t.Fatalf("curseur = %q", concluded.LastCursor)
	}
}

func TestEnqueue_Refusal_IsReturnedAsIs_AndNothingIsJournalled(t *testing.T) {
	database := newTestStore(t)
	actionCatalog := newFakeCatalog()
	actionCatalog.err = refusal.Refusal{Cause: "le domaine est vide", Remedy: "donner un domaine"}
	runner := newTestRunner(t, database, actionCatalog, &fakeTransports{transport: newFakeTransport()})

	_, err := runner.Enqueue(context.Background(), store.LocalMachineID, catalog.KindDiagnostiquer, nil)

	var refused refusal.Refusal
	if !errors.As(err, &refused) || refused.Cause != "le domaine est vide" {
		t.Fatalf("err = %v, attendu le refus tel quel", err)
	}
	actions, err := database.ActionsForMachine(context.Background(), store.LocalMachineID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(actions) != 0 {
		t.Fatalf("actions journalisées = %d, attendu 0", len(actions))
	}
}

func TestEnqueue_UnknownMachine_IsNotFound(t *testing.T) {
	database := newTestStore(t)
	runner := newTestRunner(t, database, newFakeCatalog(), &fakeTransports{transport: newFakeTransport()})

	_, err := runner.Enqueue(context.Background(), "absente", catalog.KindDiagnostiquer, nil)

	if !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("err = %v, attendu ErrNotFound", err)
	}
}

func TestExecute_DepositsScriptParamsTimeoutAndFiles_WithTheirModes(t *testing.T) {
	database := newTestStore(t)
	actionCatalog := newFakeCatalog()
	actionCatalog.files = []catalog.File{{Path: "traefik/site.yml", Content: []byte("http: {}"), Mode: 0o644}}
	machineTransport := newFakeTransport()
	runner := newTestRunner(t, database, actionCatalog, &fakeTransports{transport: machineTransport})

	action, err := runner.Enqueue(context.Background(), store.LocalMachineID, catalog.KindDiagnostiquer, nil)
	if err != nil {
		t.Fatal(err)
	}
	waitForConclusion(t, database, action.ID)

	want := []string{"run.sh", "params.env", "timeout", "files/traefik/site.yml"}
	got := machineTransport.putNames()
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("fichiers déposés = %v, attendu %v", got, want)
	}
	deposited := machineTransport.putRecords()
	modes := []uint32{0o755, 0o600, 0o644, 0o644}
	for index, put := range deposited {
		if uint32(put.mode) != modes[index] {
			t.Fatalf("mode de %s = %o, attendu %o", put.name, put.mode, modes[index])
		}
	}
	if string(deposited[2].content) != "600\n" {
		t.Fatalf("timeout déposé = %q", deposited[2].content)
	}
}

func TestExecute_ExitCode_DecidesTheState(t *testing.T) {
	cases := []struct {
		code  int
		state store.ActionState
	}{
		{catalog.ExitDone, store.StateApplied},
		{catalog.ExitFailed, store.StateFailed},
		{catalog.ExitRefused, store.StateRefused},
	}
	for _, current := range cases {
		database := newTestStore(t)
		machineTransport := newFakeTransport()
		machineTransport.outcome = transport.Outcome{ExitCode: current.code}
		runner := newTestRunner(t, database, newFakeCatalog(), &fakeTransports{transport: machineTransport})

		action, err := runner.Enqueue(context.Background(), store.LocalMachineID, catalog.KindDiagnostiquer, nil)
		if err != nil {
			t.Fatal(err)
		}

		concluded := waitForConclusion(t, database, action.ID)
		if concluded.State != current.state {
			t.Fatalf("code %d → %s, attendu %s", current.code, concluded.State, current.state)
		}
	}
}

func TestExecute_TimedOut_FailsWithANoteThatSaysIt(t *testing.T) {
	database := newTestStore(t)
	machineTransport := newFakeTransport()
	machineTransport.outcome = transport.Outcome{ExitCode: 1, Killed: true, TimedOut: true}
	runner := newTestRunner(t, database, newFakeCatalog(), &fakeTransports{transport: machineTransport})

	action, err := runner.Enqueue(context.Background(), store.LocalMachineID, catalog.KindDiagnostiquer, nil)
	if err != nil {
		t.Fatal(err)
	}

	concluded := waitForConclusion(t, database, action.ID)
	if concluded.State != store.StateFailed || !strings.Contains(concluded.Note, "délai maximum dépassé") {
		t.Fatalf("état = %s, note = %q", concluded.State, concluded.Note)
	}
}

func TestExecute_Killed_FailsAndTellsWhereToGoSee(t *testing.T) {
	database := newTestStore(t)
	machineTransport := newFakeTransport()
	machineTransport.outcome = transport.Outcome{ExitCode: 1, Killed: true}
	runner := newTestRunner(t, database, newFakeCatalog(), &fakeTransports{transport: machineTransport})

	action, err := runner.Enqueue(context.Background(), store.LocalMachineID, catalog.KindDiagnostiquer, nil)
	if err != nil {
		t.Fatal(err)
	}

	concluded := waitForConclusion(t, database, action.ID)
	if concluded.State != store.StateFailed || !strings.Contains(concluded.Note, "journalctl -u oc-action-") {
		t.Fatalf("état = %s, note = %q", concluded.State, concluded.Note)
	}
}

// L'unité existe déjà : l'action est partie une première fois, on la suit.
func TestExecute_AlreadyLaunched_IsFollowedNotRelaunched(t *testing.T) {
	database := newTestStore(t)
	machineTransport := newFakeTransport()
	machineTransport.launchErr = transport.ErrAlreadyLaunched
	runner := newTestRunner(t, database, newFakeCatalog(), &fakeTransports{transport: machineTransport})

	action, err := runner.Enqueue(context.Background(), store.LocalMachineID, catalog.KindDiagnostiquer, nil)
	if err != nil {
		t.Fatal(err)
	}

	concluded := waitForConclusion(t, database, action.ID)
	if concluded.State != store.StateApplied {
		t.Fatalf("état = %s, note = %q", concluded.State, concluded.Note)
	}
	_, follows := machineTransport.counts()
	if follows != 1 {
		t.Fatalf("suivis = %d, attendu 1", follows)
	}
}

func TestExecute_NotEnrolledMachine_IsRefusedWithTheCommandToPlay(t *testing.T) {
	database := newTestStore(t)
	runner := newTestRunner(t, database, newFakeCatalog(), &fakeTransports{err: ErrNotEnrolled})

	action, err := runner.Enqueue(context.Background(), store.LocalMachineID, catalog.KindDiagnostiquer, nil)
	if err != nil {
		t.Fatal(err)
	}

	concluded := waitForConclusion(t, database, action.ID)
	if concluded.State != store.StateRefused {
		t.Fatalf("état = %s", concluded.State)
	}
	if !strings.Contains(concluded.Note, "sudo opencloud enroll-local") {
		t.Fatalf("note = %q", concluded.Note)
	}
}

func TestExecute_LaunchRefused_FailsWithTheReason(t *testing.T) {
	database := newTestStore(t)
	machineTransport := newFakeTransport()
	machineTransport.launchErr = transport.ErrLaunchRefused
	runner := newTestRunner(t, database, newFakeCatalog(), &fakeTransports{transport: machineTransport})

	action, err := runner.Enqueue(context.Background(), store.LocalMachineID, catalog.KindDiagnostiquer, nil)
	if err != nil {
		t.Fatal(err)
	}

	concluded := waitForConclusion(t, database, action.ID)
	if concluded.State != store.StateFailed || !strings.Contains(concluded.Note, "lancement") {
		t.Fatalf("état = %s, note = %q", concluded.State, concluded.Note)
	}
	if _, follows := machineTransport.counts(); follows != 0 {
		t.Fatalf("suivis = %d, attendu 0", follows)
	}
}

// La file garantit la sérialisation : aucun verrou n'est écrit nulle part.
func TestQueue_TwoActionsOnTheSameMachine_NeverRunAtTheSameTime(t *testing.T) {
	database := newTestStore(t)
	machineTransport := newFakeTransport()
	machineTransport.followDelay = 20 * time.Millisecond
	runner := newTestRunner(t, database, newFakeCatalog(), &fakeTransports{transport: machineTransport})

	first, err := runner.Enqueue(context.Background(), store.LocalMachineID, catalog.KindDiagnostiquer, nil)
	if err != nil {
		t.Fatal(err)
	}
	second, err := runner.Enqueue(context.Background(), store.LocalMachineID, catalog.KindDiagnostiquer, nil)
	if err != nil {
		t.Fatal(err)
	}

	waitForConclusion(t, database, first.ID)
	waitForConclusion(t, database, second.ID)

	machineTransport.mu.Lock()
	defer machineTransport.mu.Unlock()
	if machineTransport.mostActive != 1 {
		t.Fatalf("suivis simultanés = %d, attendu 1", machineTransport.mostActive)
	}
}

func TestExecute_UnreachableDuringDeposit_RetriesThenLaunches(t *testing.T) {
	database := newTestStore(t)
	machineTransport := newFakeTransport()
	// Un ssh tué pendant le dépôt, comme à l'arrêt d'openCloud, puis la machine répond.
	machineTransport.putUnreachableLeft = 2
	runner := newTestRunner(t, database, newFakeCatalog(), &fakeTransports{transport: machineTransport})

	action, err := runner.Enqueue(context.Background(), store.LocalMachineID, catalog.KindDiagnostiquer, nil)
	if err != nil {
		t.Fatal(err)
	}

	concluded := waitForConclusion(t, database, action.ID)
	if concluded.State != store.StateApplied {
		t.Fatalf("état = %s, note = %q : un dépôt interrompu se rejoue, il ne conclut pas", concluded.State, concluded.Note)
	}
	if launches, _ := machineTransport.counts(); launches != 1 {
		t.Fatalf("lancements = %d, attendu 1", launches)
	}
}

func TestExecute_UnreachablePastTheDeadline_FailsSayingNothingLeft(t *testing.T) {
	database := newTestStore(t)
	machineTransport := newFakeTransport()
	machineTransport.putUnreachableLeft = 1000
	runner := newTestRunner(t, database, newFakeCatalog(), &fakeTransports{transport: machineTransport})
	// L'échéance est déjà passée : le premier « injoignable » suffit.
	runner.followGrace = -2 * time.Hour

	action, err := runner.Enqueue(context.Background(), store.LocalMachineID, catalog.KindDiagnostiquer, nil)
	if err != nil {
		t.Fatal(err)
	}

	concluded := waitForConclusion(t, database, action.ID)
	if concluded.State != store.StateFailed || !strings.Contains(concluded.Note, "rien n'est parti") {
		t.Fatalf("état = %s, note = %q", concluded.State, concluded.Note)
	}
	if launches, _ := machineTransport.counts(); launches != 0 {
		t.Fatalf("lancements = %d, attendu 0", launches)
	}
}

func TestEnqueue_Enroler_IsRefused_ItIsPlayedByTheCommand(t *testing.T) {
	database := newTestStore(t)
	runner := newTestRunner(t, database, newFakeCatalog(), &fakeTransports{transport: newFakeTransport()})

	_, err := runner.Enqueue(context.Background(), store.LocalMachineID, catalog.KindEnroler, map[string]string{"public_key": "ssh-ed25519 AAAA"})

	var refused refusal.Refusal
	if !errors.As(err, &refused) || !strings.Contains(refused.Remedy, "commande") {
		t.Fatalf("err = %v, attendu un refus qui renvoie à la commande d'enrôlement", err)
	}
	actions, err := database.ActionsForMachine(context.Background(), store.LocalMachineID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(actions) != 0 {
		t.Fatalf("actions journalisées = %d, attendu 0", len(actions))
	}
}
