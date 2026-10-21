package store

import (
	"context"
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
	if changed, err := db.ApplyRunResult(ctx, found.ID, accepted, at); err != nil || !changed {
		t.Fatalf("accepted: %v %v", changed, err)
	}
	done := action.Result{RunID: "r1", State: action.StateSucceeded, Output: lines[:3]}
	if changed, err := db.ApplyRunResult(ctx, found.ID, done, at); err != nil || !changed {
		t.Fatalf("succeeded: %v %v", changed, err)
	}
	runs, err := db.ListActionRuns(ctx, action.Query{Limit: 10})
	if err != nil || len(runs) != 1 || len(runs[0].Output) != agentwire.MaxOutputLines || runs[0].FinishedAt.IsZero() {
		t.Fatalf("runs %+v %v", runs, err)
	}
	// Un autre rapport de l'autre machine ne touche pas ce run.
	if changed, err := db.ApplyRunResult(ctx, "autre", action.Result{RunID: "r1", State: action.StateFailed}, at); err != nil || changed {
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
