package store

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/ldesfontaine/opencloud/internal/action"
	"github.com/ldesfontaine/opencloud/internal/agentwire"
	"github.com/ldesfontaine/opencloud/internal/machine"
)

func pendingRun(id string, found machine.Machine, at time.Time) action.Run {
	return action.Run{
		ID: id, Kind: agentwire.KindAgentPing, MachineID: found.ID, MachineName: found.Name, AgentID: found.Agent.ID,
		State: action.StateQueued, NotAfter: at.Add(action.OrderLifetime), RequestedBy: "operator:lucas", RequestedAt: at,
	}
}

// Le rang croît par agent et ne recule pas quand les runs sont purgés ; un
// agent révoqué n'en reçoit plus.
func TestInsertRun_TakesTheNextSeqOfTheAgentEvenAfterAPurge(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	found, err := db.GetMachine(ctx, enrolledMachine(t, db, "vps"))
	if err != nil {
		t.Fatal(err)
	}
	at := time.Unix(1_800_000_000, 0)
	first, err := db.InsertRun(ctx, pendingRun("r1", found, at))
	if err != nil || first.Seq != 1 {
		t.Fatalf("first %+v %v", first, err)
	}
	if err := db.PurgeRuns(ctx, at.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	second, err := db.InsertRun(ctx, pendingRun("r2", found, at.Add(2*time.Hour)))
	if err != nil || second.Seq != 2 {
		t.Fatalf("after the purge %+v %v", second, err)
	}
	if _, err := db.RevokeAgent(ctx, found.ID, at); err != nil {
		t.Fatal(err)
	}
	if _, err := db.InsertRun(ctx, pendingRun("r3", found, at)); !errors.Is(err, machine.ErrNoAgent) {
		t.Fatalf("revoked agent: %v", err)
	}
}

// La sortie s'ajoute à la suite, bornée par run, et part avec lui ; le run
// survit à sa machine, détaché.
func TestRunLogs_AreBoundedAndFollowTheirRun(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	found, err := db.GetMachine(ctx, enrolledMachine(t, db, "vps"))
	if err != nil {
		t.Fatal(err)
	}
	at := time.Unix(1_800_000_000, 0)
	if _, err := db.InsertRun(ctx, pendingRun("r1", found, at)); err != nil {
		t.Fatal(err)
	}
	lines := make([]action.LogLine, agentwire.MaxOutputLines-1)
	for index := range lines {
		lines[index] = action.LogLine{Stream: "stdout", Line: "x"}
	}
	accepted := action.Result{RunID: "r1", State: action.StateAccepted, Output: lines}
	if _, changed, err := db.ApplyRunResult(ctx, found.ID, accepted, at); err != nil || !changed {
		t.Fatalf("accepted: %v %v", changed, err)
	}
	done := action.Result{RunID: "r1", State: action.StateSucceeded, Output: lines[:3]}
	if _, changed, err := db.ApplyRunResult(ctx, found.ID, done, at); err != nil || !changed {
		t.Fatalf("succeeded: %v %v", changed, err)
	}
	runs, err := db.ListActionRuns(ctx, action.Query{Limit: 10})
	if err != nil || len(runs) != 1 || len(runs[0].Output) != agentwire.MaxOutputLines || runs[0].FinishedAt.IsZero() {
		t.Fatalf("runs %+v %v", runs, err)
	}
	// Un autre rapport de l'autre machine ne touche pas ce run.
	if _, changed, err := db.ApplyRunResult(ctx, "autre", action.Result{RunID: "r1", State: action.StateFailed}, at); err != nil || changed {
		t.Fatalf("another machine: %v %v", changed, err)
	}

	if err := db.DeleteMachine(ctx, found.ID); err != nil {
		t.Fatal(err)
	}
	runs, err = db.ListActionRuns(ctx, action.Query{Limit: 10})
	if err != nil || len(runs) != 1 || runs[0].MachineID != "" || runs[0].AgentID != "" || runs[0].MachineName != found.Name {
		t.Fatalf("after the machine left %+v %v", runs, err)
	}
	if err := db.PurgeRuns(ctx, at.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if left := count(t, db, `SELECT COUNT(*) FROM run_logs`); left != 0 {
		t.Fatalf("%d run logs left after the purge", left)
	}
}

// Seul un ordre accepté depuis longtemps échoue en timeout ; un ordre
// récent, un ordre qui attend encore son agent et un ordre fini ne bougent
// pas, et la machine touchée est rendue.
func TestTimeoutRuns_OnlyAcceptedAndOld(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	found, err := db.GetMachine(ctx, enrolledMachine(t, db, "vps"))
	if err != nil {
		t.Fatal(err)
	}
	at := time.Unix(1_800_000_000, 0)
	for index, id := range []string{"old-accepted", "recent-accepted", "old-sent", "old-done"} {
		requested := at
		if id == "recent-accepted" {
			requested = at.Add(2 * time.Hour)
		}
		if _, err := db.InsertRun(ctx, pendingRun(id, found, requested)); err != nil {
			t.Fatalf("%d: %v", index, err)
		}
	}
	for id, state := range map[string]action.State{
		"old-accepted": action.StateAccepted, "recent-accepted": action.StateAccepted,
		"old-sent": action.StateSent, "old-done": action.StateSucceeded,
	} {
		if err := db.SetRunState(ctx, id, state, "", time.Time{}); err != nil {
			t.Fatal(err)
		}
	}
	now := at.Add(3 * time.Hour)
	machines, err := db.TimeoutRuns(ctx, now.Add(-time.Hour), now)
	if err != nil || len(machines) != 1 || machines[0] != found.ID {
		t.Fatalf("machines %v %v", machines, err)
	}
	want := map[string]action.State{
		"old-accepted": action.StateFailed, "recent-accepted": action.StateAccepted,
		"old-sent": action.StateSent, "old-done": action.StateSucceeded,
	}
	for id, state := range want {
		run, err := db.GetActionRun(ctx, id)
		if err != nil || run.State != state {
			t.Errorf("%s: %+v %v, want %s", id, run, err, state)
		}
	}
	if run, _ := db.GetActionRun(ctx, "old-accepted"); run.Reason != action.ReasonTimeout || !run.FinishedAt.Equal(now) {
		t.Errorf("timed out run %+v", run)
	}
	if _, err := db.GetActionRun(ctx, "absent"); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("absent run: %v", err)
	}
}
