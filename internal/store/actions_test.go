package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/ldesfontaine/opencloud/migrations"
)

func newActionStore(t *testing.T) *Store {
	t.Helper()
	return openTestStore(t, openTestRoot(t), migrations.Files)
}

func insertTestAction(t *testing.T, database *Store, id string, createdAt time.Time) Action {
	t.Helper()
	action := Action{
		ID:             id,
		MachineID:      LocalMachineID,
		Kind:           "diagnostiquer",
		Params:         map[string]string{"domain": "exemple.com"},
		State:          StatePrepared,
		ScriptDigest:   "sha256:abc",
		UnitName:       "oc-action-" + id,
		TimeoutSeconds: 600,
		CreatedAt:      createdAt,
	}
	if err := database.InsertAction(context.Background(), action); err != nil {
		t.Fatalf("insérer l'action : %v", err)
	}
	return action
}

func TestInsertAction_ThenRead_KeepsParamsAndDigest(t *testing.T) {
	database := newActionStore(t)
	created := time.Date(2026, 9, 6, 10, 0, 0, 0, time.UTC)
	insertTestAction(t, database, "diagnostiquer-1", created)

	action, err := database.Action(context.Background(), "diagnostiquer-1")
	if err != nil {
		t.Fatal(err)
	}

	if action.State != StatePrepared || action.ScriptDigest != "sha256:abc" {
		t.Fatalf("action = %+v", action)
	}
	if action.Params["domain"] != "exemple.com" {
		t.Fatalf("paramètres = %v", action.Params)
	}
	if !action.CreatedAt.Equal(created) {
		t.Fatalf("créée le %s, attendu %s", action.CreatedAt, created)
	}
	if action.ExitCode != nil || !action.FinishedAt.IsZero() {
		t.Fatal("une action préparée n'a ni code de retour ni date de fin")
	}
}

func TestAction_UnknownID_IsNotFound(t *testing.T) {
	database := newActionStore(t)

	_, err := database.Action(context.Background(), "absente")

	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, attendu ErrNotFound", err)
	}
}

func TestInsertAction_UnknownMachine_IsRefusedByTheForeignKey(t *testing.T) {
	database := newActionStore(t)
	action := insertTestAction(t, database, "diagnostiquer-1", time.Now().UTC())
	action.ID = "diagnostiquer-2"
	action.MachineID = "absente"

	if err := database.InsertAction(context.Background(), action); err == nil {
		t.Fatal("une action sans machine ne doit pas s'insérer")
	}
}

func TestActionsForMachine_ReturnsTheMostRecentFirst_AndHonoursTheLimit(t *testing.T) {
	database := newActionStore(t)
	base := time.Date(2026, 9, 6, 10, 0, 0, 0, time.UTC)
	insertTestAction(t, database, "diagnostiquer-1", base)
	insertTestAction(t, database, "diagnostiquer-2", base.Add(time.Minute))
	insertTestAction(t, database, "diagnostiquer-3", base.Add(2*time.Minute))

	actions, err := database.ActionsForMachine(context.Background(), LocalMachineID, 2)
	if err != nil {
		t.Fatal(err)
	}

	if len(actions) != 2 || actions[0].ID != "diagnostiquer-3" || actions[1].ID != "diagnostiquer-2" {
		t.Fatalf("actions = %v", actionIDs(actions))
	}
}

func TestPendingActions_KeepsPreparedAndRunning_InTheOrderTheyArrived(t *testing.T) {
	database := newActionStore(t)
	base := time.Date(2026, 9, 6, 10, 0, 0, 0, time.UTC)
	insertTestAction(t, database, "diagnostiquer-1", base)
	insertTestAction(t, database, "diagnostiquer-2", base.Add(time.Minute))
	insertTestAction(t, database, "diagnostiquer-3", base.Add(2*time.Minute))

	if err := database.MarkRunning(context.Background(), "diagnostiquer-2", base.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := database.Conclude(context.Background(), "diagnostiquer-3", StateApplied, exitCode(0), "fait", ""); err != nil {
		t.Fatal(err)
	}

	pending, err := database.PendingActions(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	if len(pending) != 2 || pending[0].ID != "diagnostiquer-1" || pending[1].ID != "diagnostiquer-2" {
		t.Fatalf("en attente = %v", actionIDs(pending))
	}
	if pending[1].State != StateRunning || pending[1].LaunchedAt.IsZero() {
		t.Fatalf("action en cours = %+v", pending[1])
	}
}

func TestConclude_WritesStateExitCodeResultAndNote(t *testing.T) {
	database := newActionStore(t)
	insertTestAction(t, database, "diagnostiquer-1", time.Now().UTC())

	err := database.Conclude(context.Background(), "diagnostiquer-1", StateFailed, exitCode(1),
		"trois écarts", "suivi abandonné")
	if err != nil {
		t.Fatal(err)
	}

	action, err := database.Action(context.Background(), "diagnostiquer-1")
	if err != nil {
		t.Fatal(err)
	}
	if action.State != StateFailed || action.Result != "trois écarts" || action.Note != "suivi abandonné" {
		t.Fatalf("action = %+v", action)
	}
	if action.ExitCode == nil || *action.ExitCode != 1 || action.FinishedAt.IsZero() {
		t.Fatalf("code de retour = %v, fin = %s", action.ExitCode, action.FinishedAt)
	}
}

func TestConclude_WithoutExitCode_LeavesItEmpty(t *testing.T) {
	database := newActionStore(t)
	insertTestAction(t, database, "diagnostiquer-1", time.Now().UTC())

	err := database.Conclude(context.Background(), "diagnostiquer-1", StateFailed, nil, "", "machine injoignable")
	if err != nil {
		t.Fatal(err)
	}

	action, err := database.Action(context.Background(), "diagnostiquer-1")
	if err != nil {
		t.Fatal(err)
	}
	if action.ExitCode != nil {
		t.Fatalf("code de retour = %d, attendu aucun", *action.ExitCode)
	}
}

func TestConclude_UnknownAction_IsNotFound(t *testing.T) {
	database := newActionStore(t)

	err := database.Conclude(context.Background(), "absente", StateApplied, exitCode(0), "", "")

	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, attendu ErrNotFound", err)
	}
}

func TestAppendLine_NumbersLinesAndAdvancesTheCursor(t *testing.T) {
	database := newActionStore(t)
	ctx := context.Background()
	insertTestAction(t, database, "diagnostiquer-1", time.Now().UTC())
	at := time.Date(2026, 9, 6, 10, 0, 0, 0, time.UTC)

	first, err := database.AppendLine(ctx, "diagnostiquer-1", at, "étape: disque", "cursor-1")
	if err != nil {
		t.Fatal(err)
	}
	second, err := database.AppendLine(ctx, "diagnostiquer-1", at.Add(time.Second), "résultat: rien à signaler", "cursor-2")
	if err != nil {
		t.Fatal(err)
	}

	if first.Seq != 1 || second.Seq != 2 {
		t.Fatalf("séquences = %d puis %d", first.Seq, second.Seq)
	}
	action, err := database.Action(ctx, "diagnostiquer-1")
	if err != nil {
		t.Fatal(err)
	}
	if action.LastCursor != "cursor-2" {
		t.Fatalf("curseur = %q", action.LastCursor)
	}
}

func TestLines_AfterSeq_ReturnsOnlyWhatFollows(t *testing.T) {
	database := newActionStore(t)
	ctx := context.Background()
	insertTestAction(t, database, "diagnostiquer-1", time.Now().UTC())
	at := time.Date(2026, 9, 6, 10, 0, 0, 0, time.UTC)
	for _, text := range []string{"une", "deux", "trois"} {
		if _, err := database.AppendLine(ctx, "diagnostiquer-1", at, text, "cursor-"+text); err != nil {
			t.Fatal(err)
		}
	}

	lines, err := database.Lines(ctx, "diagnostiquer-1", 1)
	if err != nil {
		t.Fatal(err)
	}

	if len(lines) != 2 || lines[0].Text != "deux" || lines[1].Text != "trois" {
		t.Fatalf("lignes = %+v", lines)
	}
	if !lines[0].At.Equal(at) {
		t.Fatalf("horodatage = %s, attendu %s", lines[0].At, at)
	}
}

func TestAppendLine_UnknownAction_IsNotFound(t *testing.T) {
	database := newActionStore(t)

	_, err := database.AppendLine(context.Background(), "absente", time.Now().UTC(), "une", "cursor")

	if err == nil {
		t.Fatal("une ligne sans action ne doit pas s'insérer")
	}
}

func exitCode(code int) *int {
	return &code
}

func actionIDs(actions []Action) []string {
	var ids []string
	for _, action := range actions {
		ids = append(ids, action.ID)
	}
	return ids
}
