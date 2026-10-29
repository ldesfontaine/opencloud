package action

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/ldesfontaine/opencloud/internal/agentwire"
	"github.com/ldesfontaine/opencloud/internal/audit"
	"github.com/ldesfontaine/opencloud/internal/machine"
	"github.com/ldesfontaine/opencloud/internal/release"
)

// Alerter est ce que le composant attend du moteur des alertes : l'agent d'une
// machine est revenu à sa version d'avant (ou n'a pas pu), et une mise à jour
// confirmée referme cette alerte.
type Alerter interface {
	AgentRolledBack(ctx context.Context, machineID, machineName, target, reason string, failed bool)
	AgentUpdated(ctx context.Context, machineID string)
}

// RollbackAgent envoie l'ordre agent.rollback : l'agent rebascule sur la
// version d'avant, l'éprouve et redémarre. Même droit que agent.update, même
// garde « une seule mise à jour à la fois », et il exige lui aussi un agent
// capable de revenir : le retour est confirmé ou annulé comme une mise à jour.
// Le serveur ne sait pas quelle est la version d'avant ; l'agent refuse
// (no_previous, previous_identical) quand il n'y en a pas.
func (s *Service) RollbackAgent(ctx context.Context, machineID string) (Run, error) {
	s.updateMu.Lock()
	defer s.updateMu.Unlock()
	target, err := s.machines.Get(ctx, machineID)
	if err != nil {
		return Run{}, err
	}
	if !target.Agent.IsActive() {
		return Run{}, machine.ErrNoAgent
	}
	if !target.Agent.CanRollBack() {
		return Run{}, ErrRollbackUnavailable
	}
	if err := s.refuseWhileUpdating(ctx); err != nil {
		return Run{}, err
	}
	return s.send(ctx, ServiceOrder{
		MachineID: machineID, Kind: agentwire.KindAgentRollback,
		AuditDetails: audit.Details{"from": target.Agent.Version},
	})
}

// refuseWhileUpdating refuse quand une mise à jour ou un retour arrière, de
// n'importe quelle machine, n'est pas fini : l'opérateur et un jeton passent
// par ce même contrôle.
func (s *Service) refuseWhileUpdating(ctx context.Context) error {
	_, found, err := s.store.FindUpdateInFlight(ctx, s.now().Add(-UpdateInFlightWindow))
	if err != nil {
		return fmt.Errorf("find update in flight: %w", err)
	}
	if found {
		return ErrUpdateInProgress
	}
	return nil
}

// UpdateInFlight dit quelle mise à jour est en vol, pour le plan.
func (s *Service) UpdateInFlight(ctx context.Context) (Run, bool, error) {
	return s.store.FindUpdateInFlight(ctx, s.now().Add(-UpdateInFlightWindow))
}

// isRollbackReport : l'agent dit que sa mise à jour n'a pas tenu, ou que le
// retour à la version d'avant n'a pas tenu non plus.
func isRollbackReport(kind agentwire.Kind, reason string) bool {
	if kind != agentwire.KindAgentUpdate && kind != agentwire.KindAgentRollback {
		return false
	}
	switch reason {
	case agentwire.ReasonRolledBackTimeout, agentwire.ReasonRolledBackCrash, agentwire.ReasonRollbackFailed:
		return true
	}
	return false
}

// noteRollback écrit ce qu'un agent revenu en arrière rapporte : l'événement
// d'audit (l'acteur est l'agent, que le signal porte) et l'alerte lisible. Le
// run est déjà fermé en échec par ApplyRunResult.
func (s *Service) noteRollback(ctx context.Context, run Run) {
	full, err := s.store.GetActionRun(ctx, run.ID)
	if err != nil {
		s.logger.Warn("read rolled back run", "run_id", run.ID, "error", err)
		full = run
	}
	target := updateTargetOf(full)
	current := ""
	if status, err := s.machines.Get(ctx, run.MachineID); err == nil {
		current = status.Agent.Version
	}
	details := audit.Details{
		"kind": string(run.Kind), "machine_id": run.MachineID, "agent_id": run.AgentID,
		"reason": run.Reason, "target": target, "version": current,
	}
	s.auditor.Record(ctx, audit.Event{Action: audit.ActionRolledBack, ObjectID: run.ID, ObjectName: run.MachineName, Details: details})
	if s.alerter != nil {
		cause := strings.TrimPrefix(run.Reason, "rolled_back_")
		failed := run.Reason == agentwire.ReasonRollbackFailed
		if failed {
			cause = "failed"
		}
		s.alerter.AgentRolledBack(ctx, run.MachineID, run.MachineName, target, cause, failed)
	}
}

// updateTargetOf rend la version qu'une mise à jour visait ; vide pour un
// retour arrière, qui n'en porte pas.
func updateTargetOf(run Run) string {
	var params agentwire.AgentUpdateParams
	if run.Kind != agentwire.KindAgentUpdate || json.Unmarshal(run.Params, &params) != nil {
		return ""
	}
	if _, err := release.ParseVersion(params.Version); err != nil {
		return ""
	}
	return params.Version
}
