package alert

import (
	"context"

	"github.com/ldesfontaine/opencloud/internal/heartbeat"
)

// Ce que le composant heartbeat constate : une tâche en retard, en échec,
// revenue à l'heure, mise en pause, supprimée. Un ping en échec est un
// ping : le retard, lui, est levé.

func heartbeatObject(found heartbeat.Heartbeat) Object {
	return Object{Kind: ObjectHeartbeat, ID: found.ID, Name: found.Name}
}

func (e *Engine) Late(ctx context.Context, found heartbeat.Heartbeat) {
	e.Open(ctx, Fact{Kind: KindJobLate, Severity: SeverityAttention, Object: heartbeatObject(found), MachineID: found.MachineID})
}

func (e *Engine) Failed(ctx context.Context, found heartbeat.Heartbeat, exitCode int) {
	object := heartbeatObject(found)
	e.Resolve(ctx, KindJobLate, object)
	e.Open(ctx, Fact{Kind: KindJobFailed, Severity: SeverityDanger, Object: object, MachineID: found.MachineID, Details: Details{ExitCode: exitCode}})
}

func (e *Engine) Recovered(ctx context.Context, found heartbeat.Heartbeat) {
	e.Gone(ctx, heartbeatObject(found))
}

func (e *Engine) Paused(ctx context.Context, found heartbeat.Heartbeat) {
	e.Gone(ctx, heartbeatObject(found))
}

func (e *Engine) Removed(ctx context.Context, id string) {
	e.Gone(ctx, Object{Kind: ObjectHeartbeat, ID: id})
}
