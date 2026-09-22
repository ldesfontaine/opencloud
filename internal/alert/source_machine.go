package alert

import (
	"context"

	"github.com/ldesfontaine/opencloud/internal/machine"
)

// Ce que le composant machine constate : une machine perdue, revenue,
// retirée. La machine openCloud n'est jamais perdue, elle est ce processus.

func (e *Engine) MachineLost(ctx context.Context, lost machine.Machine) {
	if lost.IsLocal() {
		return
	}
	e.Open(ctx, Fact{
		Kind: KindMachineLost, Severity: SeverityDanger,
		Object:    Object{Kind: ObjectMachine, ID: lost.ID, Name: lost.Name},
		MachineID: lost.ID,
		Details:   Details{Since: lost.LastSeenAt},
	})
}

func (e *Engine) MachineBack(ctx context.Context, machineID string) {
	e.Resolve(ctx, KindMachineLost, Object{Kind: ObjectMachine, ID: machineID})
}

func (e *Engine) MachineRemoved(ctx context.Context, machineID string) {
	e.Gone(ctx, Object{Kind: ObjectMachine, ID: machineID})
}
