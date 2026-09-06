package runner

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/ldesfontaine/opencloud/internal/actiondir"
	"github.com/ldesfontaine/opencloud/internal/catalog"
	"github.com/ldesfontaine/opencloud/internal/store"
	"github.com/ldesfontaine/opencloud/internal/transport"
)

// journalise une action comme si openCloud s'était arrêté juste après l'avoir
// écrite, sans passer par le runner.
func journalAction(t *testing.T, database *store.Store, id string, state store.ActionState, digest, cursor string) store.Action {
	t.Helper()
	action := store.Action{
		ID:             id,
		MachineID:      store.LocalMachineID,
		Kind:           string(catalog.KindDiagnostiquer),
		Params:         map[string]string{},
		State:          store.StatePrepared,
		ScriptDigest:   digest,
		UnitName:       actiondir.UnitName(id),
		TimeoutSeconds: 600,
		CreatedAt:      time.Now().UTC(),
		LastCursor:     cursor,
	}
	if err := database.InsertAction(context.Background(), action); err != nil {
		t.Fatal(err)
	}
	if state == store.StateRunning {
		if err := database.MarkRunning(context.Background(), id, time.Now().UTC()); err != nil {
			t.Fatal(err)
		}
		action.State = store.StateRunning
	}
	return action
}

func preparedDigest(t *testing.T, actionCatalog *fakeCatalog) string {
	t.Helper()
	prepared, err := actionCatalog.Prepare(catalog.KindDiagnostiquer, nil)
	if err != nil {
		t.Fatal(err)
	}
	return prepared.ScriptDigest
}

// Coupure simulée après Launch : au démarrage suivant, l'action se conclut
// sans que personne n'intervienne, et rien n'est relancé.
func TestResume_RunningAction_IsFollowedFromItsCursor_AndConcludes(t *testing.T) {
	database := newTestStore(t)
	actionCatalog := newFakeCatalog()
	machineTransport := newFakeTransport()
	journalAction(t, database, "diagnostiquer-coupee", store.StateRunning, preparedDigest(t, actionCatalog), "c1")

	runner := newTestRunner(t, database, actionCatalog, &fakeTransports{transport: machineTransport})
	if err := runner.Resume(context.Background()); err != nil {
		t.Fatal(err)
	}

	concluded := waitForConclusion(t, database, "diagnostiquer-coupee")
	if concluded.State != store.StateApplied || concluded.Result != "rien à signaler" {
		t.Fatalf("action reprise = %+v", concluded)
	}
	launches, _ := machineTransport.counts()
	if launches != 0 {
		t.Fatalf("lancements = %d, attendu 0 : l'unité tournait déjà", launches)
	}
	if cursors := machineTransport.cursors(); len(cursors) != 1 || cursors[0] != "c1" {
		t.Fatalf("curseurs de reprise = %v, attendu [c1]", cursors)
	}
	lines, err := database.Lines(context.Background(), "diagnostiquer-coupee", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != 1 || lines[0].Text != "résultat: rien à signaler" {
		t.Fatalf("lignes = %+v, la reprise ne doit pas redoubler la sortie", lines)
	}
}

func TestResume_PreparedAction_IsDepositedLaunchedAndConcluded(t *testing.T) {
	database := newTestStore(t)
	actionCatalog := newFakeCatalog()
	machineTransport := newFakeTransport()
	journalAction(t, database, "diagnostiquer-preparee", store.StatePrepared, preparedDigest(t, actionCatalog), "")

	runner := newTestRunner(t, database, actionCatalog, &fakeTransports{transport: machineTransport})
	if err := runner.Resume(context.Background()); err != nil {
		t.Fatal(err)
	}

	concluded := waitForConclusion(t, database, "diagnostiquer-preparee")
	if concluded.State != store.StateApplied {
		t.Fatalf("état = %s, note = %q", concluded.State, concluded.Note)
	}
	launches, _ := machineTransport.counts()
	if launches != 1 {
		t.Fatalf("lancements = %d, attendu 1", launches)
	}
}

// L'unité existe peut-être déjà à la reprise : on suit, on ne relance pas.
func TestResume_PreparedAction_UnitAlreadyThere_IsFollowed(t *testing.T) {
	database := newTestStore(t)
	actionCatalog := newFakeCatalog()
	machineTransport := newFakeTransport()
	machineTransport.launchErr = transport.ErrAlreadyLaunched
	journalAction(t, database, "diagnostiquer-deja-la", store.StatePrepared, preparedDigest(t, actionCatalog), "")

	runner := newTestRunner(t, database, actionCatalog, &fakeTransports{transport: machineTransport})
	if err := runner.Resume(context.Background()); err != nil {
		t.Fatal(err)
	}

	concluded := waitForConclusion(t, database, "diagnostiquer-deja-la")
	if concluded.State != store.StateApplied {
		t.Fatalf("état = %s, note = %q", concluded.State, concluded.Note)
	}
}

func TestResume_ScriptChangedMeanwhile_IsRefused(t *testing.T) {
	database := newTestStore(t)
	actionCatalog := newFakeCatalog()
	machineTransport := newFakeTransport()
	journalAction(t, database, "diagnostiquer-vieille", store.StatePrepared, "sha256:une-autre-version", "")

	runner := newTestRunner(t, database, actionCatalog, &fakeTransports{transport: machineTransport})
	if err := runner.Resume(context.Background()); err != nil {
		t.Fatal(err)
	}

	concluded := waitForConclusion(t, database, "diagnostiquer-vieille")
	if concluded.State != store.StateRefused || !strings.Contains(concluded.Note, "le script a changé entre-temps") {
		t.Fatalf("état = %s, note = %q", concluded.State, concluded.Note)
	}
	if launches, follows := machineTransport.counts(); launches != 0 || follows != 0 {
		t.Fatalf("rien ne devait partir : %d lancements, %d suivis", launches, follows)
	}
}

// Injoignable, ce n'est pas perdu : l'information revient avec la machine.
func TestFollow_UnreachableThenBack_ConcludesWithoutIntervention(t *testing.T) {
	database := newTestStore(t)
	machineTransport := newFakeTransport()
	machineTransport.unreachableLeft = 3
	runner := newTestRunner(t, database, newFakeCatalog(), &fakeTransports{transport: machineTransport})

	action, err := runner.Enqueue(context.Background(), store.LocalMachineID, catalog.KindDiagnostiquer, nil)
	if err != nil {
		t.Fatal(err)
	}

	concluded := waitForConclusion(t, database, action.ID)
	if concluded.State != store.StateApplied {
		t.Fatalf("état = %s, note = %q", concluded.State, concluded.Note)
	}
	if _, follows := machineTransport.counts(); follows != 4 {
		t.Fatalf("suivis = %d, attendu 4 (trois échecs puis la reprise)", follows)
	}
}

// Au-delà du délai maximum plus la marge, le suivi est abandonné — et
// l'interface dit où aller voir. La machine, elle, fait ce qu'elle fait.
func TestFollow_UnreachablePastTheDeadline_IsAbandonedWithTheCommandToGoSee(t *testing.T) {
	database := newTestStore(t)
	actionCatalog := newFakeCatalog()
	machineTransport := newFakeTransport()
	machineTransport.unreachableLeft = 100
	journalAction(t, database, "diagnostiquer-perdue", store.StateRunning, preparedDigest(t, actionCatalog), "")

	runner := newTestRunner(t, database, actionCatalog, &fakeTransports{transport: machineTransport})
	// L'action a été lancée il y a longtemps : sa fenêtre de suivi est passée.
	runner.followGrace = -2 * time.Hour
	if err := runner.Resume(context.Background()); err != nil {
		t.Fatal(err)
	}

	concluded := waitForConclusion(t, database, "diagnostiquer-perdue")
	if concluded.State != store.StateFailed {
		t.Fatalf("état = %s", concluded.State)
	}
	if !strings.Contains(concluded.Note, "suivi abandonné") ||
		!strings.Contains(concluded.Note, "journalctl -u oc-action-diagnostiquer-perdue") {
		t.Fatalf("note = %q", concluded.Note)
	}
	if concluded.ExitCode != nil {
		t.Fatal("une action abandonnée n'a pas de code de retour")
	}
}

func TestFollow_UnitThatNeverConcludes_IsAbandonedAtTheDeadline(t *testing.T) {
	database := newTestStore(t)
	actionCatalog := newFakeCatalog()
	machineTransport := newFakeTransport()
	// Le journal ne dit jamais la fin : la porte reste fermée.
	machineTransport.gate = make(chan struct{})
	journalAction(t, database, "diagnostiquer-muette", store.StateRunning, preparedDigest(t, actionCatalog), "")

	runner := newTestRunner(t, database, actionCatalog, &fakeTransports{transport: machineTransport})
	runner.followGrace = -2 * time.Hour
	if err := runner.Resume(context.Background()); err != nil {
		t.Fatal(err)
	}

	concluded := waitForConclusion(t, database, "diagnostiquer-muette")
	if concluded.State != store.StateFailed {
		t.Fatalf("état = %s", concluded.State)
	}
	if !strings.Contains(concluded.Note, "suivi abandonné") ||
		!strings.Contains(concluded.Note, "journalctl -u oc-action-diagnostiquer-muette") {
		t.Fatalf("note = %q", concluded.Note)
	}
}
