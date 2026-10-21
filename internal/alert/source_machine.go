package alert

import (
	"context"

	"github.com/ldesfontaine/opencloud/internal/machine"
)

// Ce que le composant machine constate : une machine perdue, revenue,
// retirée. La machine openCloud comme les autres : c'est son agent qui se
// perd.

func (e *Engine) MachineLost(ctx context.Context, lost machine.Machine) {
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
	e.removed(ctx, Object{Kind: ObjectMachine, ID: machineID})
}
