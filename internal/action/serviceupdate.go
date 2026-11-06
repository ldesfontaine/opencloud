package action

import (
	"context"

	"github.com/ldesfontaine/opencloud/internal/agentwire"
	"github.com/ldesfontaine/opencloud/internal/audit"
)

// UpdateEnd est ce qu'une montée de version est devenue, pour le journal et
// les alertes.
type UpdateEnd struct {
	ServiceID   string
	ServiceName string
	UnitName    string
	FromVersion string
	ToVersion   string
	// La nouvelle version tient.
	Confirmed bool
	// L'agent n'a pas pu revenir, ou la version d'avant ne va pas non plus.
	Failed bool
	// La machine a été remise à la version d'avant (revenue ou tentée).
	Reverted bool
	// Le rapport est arrivé après l'expiration du run : l'annuaire n'a pas
	// été remis.
	Late bool
}

// ServiceUpdates est ce que le composant attend de celui qui tient les montées
// de version : fermer celle dont ce run est l'ordre. Faux pour un run qui n'est
// pas une montée en attente.
type ServiceUpdates interface {
	SettleUpdate(ctx context.Context, run Run) (UpdateEnd, bool, error)
	// SettleLateUpdate ferme la montée dont le rapport arrive après
	// l'expiration du run : l'annuaire n'est pas touché.
	SettleLateUpdate(ctx context.Context, runID, reason string) (UpdateEnd, bool, error)
}

// settleServiceUpdate écrit ce qu'une montée de version devient : l'audit et
// l'alerte d'un retour arrière, la fermeture de l'alerte quand la montée tient.
// Une erreur se dit au journal : le run est déjà fermé.
func (s *Service) settleServiceUpdate(ctx context.Context, run Run, result Result) {
	if s.updates == nil {
		return
	}
	end, found, err := s.updates.SettleUpdate(ctx, run)
	if err != nil {
		s.logger.Warn("settle service update", "run_id", run.ID, "error", err)
		return
	}
	if !found {
		return
	}
	if end.Confirmed {
		if s.alerter != nil {
			s.alerter.ServiceUpdated(ctx, end.ServiceID)
		}
		return
	}
	if !end.Reverted {
		return
	}
	s.announceRollback(ctx, run, end, result.Journal)
}

// announceRollback écrit l'audit et l'alerte d'une montée revenue en arrière.
func (s *Service) announceRollback(ctx context.Context, run Run, end UpdateEnd, journal []string) {
	details := audit.Details{
		"kind": string(agentwire.KindDockerUpdate), "machine_id": run.MachineID, "service_id": end.ServiceID, "service": end.ServiceName,
		"reason": run.Reason, "target": end.ToVersion, "version": end.FromVersion,
	}
	if end.Late {
		details["late"] = "true"
	}
	s.auditor.Record(ctx, audit.Event{Action: audit.ActionRolledBack, ObjectID: run.ID, ObjectName: run.MachineName, Details: details})
	if s.alerter != nil {
		s.alerter.ServiceRolledBack(ctx, end.ServiceID, end.ServiceName, run.MachineID, run.MachineName, end.FromVersion, end.ToVersion, end.Failed, end.Late, journal)
	}
}

// settleLateServiceReport traite le rapport d'une montée que le serveur avait
// déjà clos (run expiré ou coupé) : la machine, elle, est revenue en arrière.
// L'annuaire n'est PAS remis (le serveur ne sait plus ce qui tourne), mais
// l'alerte part, dite « rapport tardif ». Un rapport rejoué ne refait rien : la
// montée n'est plus en attente.
func (s *Service) settleLateServiceReport(ctx context.Context, machineID string, result Result) {
	if s.updates == nil || result.State != StateFailed ||
		(result.Reason != agentwire.ReasonRolledBackVersion && result.Reason != agentwire.ReasonRollbackFailed) {
		return
	}
	run, err := s.store.GetActionRun(ctx, result.RunID)
	if err != nil || run.MachineID != machineID || run.Kind != agentwire.KindDockerUpdate {
		return
	}
	end, found, err := s.updates.SettleLateUpdate(ctx, run.ID, result.Reason)
	if err != nil {
		s.logger.Warn("settle late service update", "run_id", run.ID, "error", err)
		return
	}
	if found {
		run.Reason = result.Reason
		s.announceRollback(ctx, run, end, result.Journal)
	}
}
