package deploy

import (
	"context"
	"fmt"
	"time"

	"github.com/ldesfontaine/opencloud/internal/action"
	"github.com/ldesfontaine/opencloud/internal/agentwire"
	"github.com/ldesfontaine/opencloud/internal/directory"
	"github.com/ldesfontaine/opencloud/internal/refusal"
)

// Les refus d'une montée de version avec filet.
var (
	ErrRollbackUnavailable = refusal.Conflict("deploy.rollback_unavailable", "the agent cannot return to the previous version of a service by itself: update it first")
	ErrUpdateInProgress    = refusal.Conflict("deploy.update_in_progress", "another version change on this machine is still being judged")
)

// UpdateState dit ce qu'une montée de version est devenue.
type UpdateState string

const (
	// L'agent juge encore la santé de la nouvelle version.
	UpdatePending UpdateState = "pending"
	// La nouvelle version tient.
	UpdateConfirmed UpdateState = "confirmed"
	// L'agent est revenu seul à la version d'avant, et elle va bien.
	UpdateRolledBack UpdateState = "rolled_back"
	// L'agent n'a pas pu revenir, ou la version d'avant ne va pas non plus.
	UpdateFailed UpdateState = "failed"
	// L'agent a refusé avant d'écrire : rien n'a bougé sur la machine.
	UpdateRefused UpdateState = "refused"
)

// ServiceUpdate est une montée de version demandée.
type ServiceUpdate struct {
	RunID       string
	ServiceID   string
	MachineID   string
	UnitID      string
	UnitName    string
	FromVersion string
	ToVersion   string
	State       UpdateState
	Reason      string
	RequestedAt time.Time
	SettledAt   time.Time
}

// UpdateStore est ce que le composant garde des montées de version.
type UpdateStore interface {
	InsertServiceUpdate(ctx context.Context, update ServiceUpdate) error
	// SettleServiceUpdate ferme la montée en attente de ce run ; faux s'il n'y
	// en a pas (déjà fermée, ou ordre sans filet).
	SettleServiceUpdate(ctx context.Context, runID string, state UpdateState, reason string, at time.Time) (ServiceUpdate, bool, error)
	// FindPendingServiceUpdate rend une montée en attente sur cette machine,
	// demandée depuis since.
	FindPendingServiceUpdate(ctx context.Context, machineID string, since time.Time) (ServiceUpdate, bool, error)
	// LatestServiceUpdate rend la dernière montée du service, quel que soit
	// son état.
	LatestServiceUpdate(ctx context.Context, serviceID string) (ServiceUpdate, bool, error)
}

// updateInFlightWindow : un run vit une heure (action) ; une montée en
// attente plus vieille n'a plus d'agent qui la juge.
const updateInFlightWindow = time.Hour

// refuseWhileJudging refuse une montée quand une autre est encore jugée sur
// la machine : l'agent n'en juge qu'une à la fois.
func (s *Service) refuseWhileJudging(ctx context.Context, machineID string) error {
	_, found, err := s.updates.FindPendingServiceUpdate(ctx, machineID, s.now().Add(-updateInFlightWindow))
	if err != nil {
		return fmt.Errorf("find pending service update: %w", err)
	}
	if found {
		return ErrUpdateInProgress
	}
	return nil
}

// recordUpdate garde la montée dont l'ordre est parti. L'échec n'annule pas
// l'ordre : l'agent juge et revient quand même, seul le rattrapage de
// l'annuaire manquerait, et il se dit au journal.
func (s *Service) recordUpdate(ctx context.Context, run action.Run, unit directory.Unit, fromVersion, toVersion string) {
	err := s.updates.InsertServiceUpdate(ctx, ServiceUpdate{
		RunID: run.ID, ServiceID: unit.ServiceID, MachineID: run.MachineID, UnitID: unit.ID, UnitName: unit.Name,
		FromVersion: fromVersion, ToVersion: toVersion, State: UpdatePending, RequestedAt: s.now(),
	})
	if err != nil {
		s.logger.Error("record service update", "run_id", run.ID, "service_id", unit.ServiceID, "error", err)
	}
}

// SettleUpdate clôt la montée dont ce run est l'ordre, selon ce que l'agent a
// rapporté. Revenu à la version d'avant, refusé avant d'écrire, ou revenu
// sans succès : la machine porte l'ancien compose, l'annuaire reprend la
// version d'avant. Faux pour un run qui n'est pas une montée en attente.
func (s *Service) SettleUpdate(ctx context.Context, run action.Run) (action.UpdateEnd, bool, error) {
	state, restores, isFinal := stateOf(run)
	if !isFinal {
		return action.UpdateEnd{}, false, nil
	}
	update, found, err := s.updates.SettleServiceUpdate(ctx, run.ID, state, run.Reason, s.now())
	if err != nil || !found {
		return action.UpdateEnd{}, false, err
	}
	if restores {
		if err := s.units.SetUnitVersion(ctx, update.UnitID, update.FromVersion); err != nil {
			// L'unité a pu être retirée entre-temps : rien à remettre.
			s.logger.Warn("put the unit version back", "unit_id", update.UnitID, "version", update.FromVersion, "error", err)
		}
	}
	end := action.UpdateEnd{
		ServiceID: update.ServiceID, UnitName: update.UnitName, FromVersion: update.FromVersion, ToVersion: update.ToVersion,
		Confirmed: state == UpdateConfirmed, Failed: state == UpdateFailed, Reverted: state == UpdateRolledBack || state == UpdateFailed,
	}
	if service, err := s.services.GetDeclaredService(ctx, update.ServiceID); err == nil {
		end.ServiceName = service.Name
	}
	return end, true, nil
}

// SettleLateUpdate clôt la montée dont le rapport arrive après l'expiration du
// run. Contrairement à SettleUpdate, il ne remet pas la version d'avant dans
// l'annuaire : l'ordre est clos depuis longtemps, le serveur ne sait plus si
// la déclaration a été retouchée entre-temps.
func (s *Service) SettleLateUpdate(ctx context.Context, runID, reason string) (action.UpdateEnd, bool, error) {
	state := UpdateRolledBack
	if reason == agentwire.ReasonRollbackFailed {
		state = UpdateFailed
	}
	update, found, err := s.updates.SettleServiceUpdate(ctx, runID, state, reason, s.now())
	if err != nil || !found {
		return action.UpdateEnd{}, false, err
	}
	end := action.UpdateEnd{
		ServiceID: update.ServiceID, UnitName: update.UnitName, FromVersion: update.FromVersion, ToVersion: update.ToVersion,
		Failed: state == UpdateFailed, Reverted: true, Late: true,
	}
	if service, err := s.services.GetDeclaredService(ctx, update.ServiceID); err == nil {
		end.ServiceName = service.Name
	}
	return end, true, nil
}

// stateOf traduit un run docker.update fini en état de montée ; restores dit
// si la version d'avant revient dans l'annuaire. Faux pour un run qui n'a pas
// fini, ou qui n'est pas docker.update.
func stateOf(run action.Run) (state UpdateState, restores, isFinal bool) {
	if run.Kind != agentwire.KindDockerUpdate {
		return "", false, false
	}
	switch {
	case run.State == action.StateSucceeded:
		return UpdateConfirmed, false, true
	case run.State == action.StateRefused:
		return UpdateRefused, true, true
	case run.State == action.StateFailed && run.Reason == agentwire.ReasonRolledBackVersion:
		return UpdateRolledBack, true, true
	case run.State == action.StateFailed && run.Reason == agentwire.ReasonRollbackFailed:
		return UpdateFailed, true, true
	}
	return "", false, false
}

// RollBack demande à l'agent de revenir tout de suite à la version d'avant
// d'un service dont la montée est dans sa fenêtre de santé : le veto du
// serveur, quand une sonde liée au service échoue pendant la fenêtre. L'agent
// refuse (no_pending_update) hors fenêtre.
func (s *Service) RollBack(ctx context.Context, serviceID string) (action.Run, error) {
	service, err := s.services.GetDeclaredService(ctx, serviceID)
	if err != nil {
		return action.Run{}, err
	}
	params, err := jsonOf(agentwire.DockerRollbackParams{Project: service.ComposeProject})
	if err != nil {
		return action.Run{}, err
	}
	return s.sender.SendForService(ctx, action.ServiceOrder{
		MachineID: service.MachineID, ServiceID: serviceID, Kind: agentwire.KindDockerRollback, Params: params,
	})
}
