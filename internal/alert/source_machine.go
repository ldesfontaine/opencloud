package alert

import (
	"context"
	"slices"

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

// Les causes d'un retour en arrière que l'alerte dit : le délai de
// confirmation passé, un plantage de la nouvelle version, ou une remise en
// état qui a échoué à son tour.
const (
	RollbackTimeout = "timeout"
	RollbackCrash   = "crash"
	RollbackFailed  = "failed"
)

// RollbackCauses rend la liste fermée, une phrase de catalogue chacune.
func RollbackCauses() []string {
	return []string{RollbackTimeout, RollbackCrash, RollbackFailed}
}

// AgentRolledBack ouvre l'alerte d'un agent revenu à sa version d'avant ;
// danger quand la remise en état a échoué (la machine n'a peut-être plus
// d'agent qui tourne), attention sinon. Une cause inconnue est dite comme un
// délai passé.
func (e *Engine) AgentRolledBack(ctx context.Context, machineID, machineName, target, cause string, failed bool) {
	severity := SeverityAttention
	if failed {
		severity = SeverityDanger
	}
	if !slices.Contains(RollbackCauses(), cause) {
		cause = RollbackTimeout
	}
	e.Open(ctx, Fact{
		Kind: KindAgentRolledBack, Severity: severity,
		Object:    Object{Kind: ObjectMachine, ID: machineID, Name: machineName},
		MachineID: machineID,
		Details:   Details{Reason: cause, Target: target},
	})
}

// AgentUpdated referme l'alerte : une mise à jour a été confirmée depuis.
func (e *Engine) AgentUpdated(ctx context.Context, machineID string) {
	e.Resolve(ctx, KindAgentRolledBack, Object{Kind: ObjectMachine, ID: machineID})
}
