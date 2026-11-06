package restic

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/ldesfontaine/opencloud/internal/action"
	"github.com/ldesfontaine/opencloud/internal/agentwire"
	"github.com/ldesfontaine/opencloud/internal/audit"
	"github.com/ldesfontaine/opencloud/internal/machine"
	"github.com/ldesfontaine/opencloud/internal/refusal"
)

var (
	// La source n'a pas restic.restore : l'écran propose de la ré-enrôler.
	ErrSourceRestoreRightMissing = refusal.Conflict("restic.source_restore_right_missing", "the agent of the source may not restore: re-enroll it with restic.restore").NeedsRight(machine.CapabilityResticRestore)
	ErrSourceRestoreUnavailable  = refusal.Conflict("restic.source_restore_unavailable", "the agent of the source holds restic.restore but cannot exercise it on this machine")
	// La destination n'est pas sous un dossier que files.write couvre.
	ErrRestoreOutsideRight = refusal.Conflict("restic.restore_outside_right", "the destination is not under a folder the agent may write (files.write)").NeedsRight(machine.CapabilityFilesWrite)
	ErrRestoreRequest      = refusal.Invalid("restic.restore_request", "the snapshot, the path or the stamp is outside its form")
)

// RestoreRequest : ce qu'on restaure. Stamp est l'horodatage AAAAMMJJ-HHMMSS que
// prend la destination, choisi par l'écran qui l'a montré avant de confirmer.
type RestoreRequest struct {
	Snapshot string
	Path     string
	Stamp    string
}

// Restore envoie à la SOURCE l'ordre de restaurer le chemin à côté de
// l'original, et rend l'ordre : le résultat (fait, ou refusé avec sa raison : la
// destination existe, le chemin n'est pas dans l'instantané…) arrive par le
// signal de la source. L'ordre ne porte aucun secret. L'audit garde qui, quoi,
// quel instantané et la destination exacte.
func (s *Service) Restore(ctx context.Context, sourceID, id string, request RestoreRequest) (action.Run, error) {
	current, err := s.ownedBackup(ctx, sourceID, id)
	if err != nil {
		return action.Run{}, err
	}
	target, err := agentwire.ResticUserFor(current.TargetMachineID)
	if err != nil {
		return action.Run{}, ErrTargetUnknown
	}
	params := agentwire.ResticRestoreParams{Target: target, Snapshot: request.Snapshot, Path: request.Path, Stamp: request.Stamp}
	if params.Validate() != nil {
		return action.Run{}, ErrRestoreRequest
	}
	source, err := s.machines.Get(ctx, current.SourceMachineID)
	if err != nil {
		return action.Run{}, fmt.Errorf("get source machine: %w", err)
	}
	destination := agentwire.RestoreDestination(request.Path, request.Stamp)
	if err := judgeRestore(source, destination); err != nil {
		return action.Run{}, err
	}
	encoded, err := json.Marshal(params)
	if err != nil {
		return action.Run{}, fmt.Errorf("encode restore: %w", err)
	}
	run, err := s.send(ctx, current.SourceMachineID, agentwire.KindResticRestore, encoded, ErrSourceRestoreRightMissing)
	if err != nil {
		return action.Run{}, err
	}
	s.auditor.Record(ctx, audit.Event{
		Action: audit.ResticBackupRestore, ObjectID: current.ID, ObjectName: current.Name,
		Details: audit.Details{
			"run_id": run.ID, "source_machine_id": current.SourceMachineID, "target_machine_id": current.TargetMachineID,
			"snapshot": request.Snapshot, "path": request.Path, "destination": destination,
		},
	})
	s.logger.Info("restic restore sent", "run_id", run.ID, "source", current.SourceMachineID)
	return run, nil
}

// judgeRestore : la source tient restic.restore et sait l'exercer, et
// files.write couvre la destination.
func judgeRestore(source machine.Status, destination string) error {
	switch source.Agent.Fit(machine.CapabilityResticRestore, "").State {
	case machine.RightOK:
	case machine.RightUnknown:
		return nil
	case machine.RightUnavailable:
		return ErrSourceRestoreUnavailable
	default:
		return ErrSourceRestoreRightMissing
	}
	if source.Agent.Fit(machine.CapabilityFilesWrite, destination).State == machine.RightMissing {
		return ErrRestoreOutsideRight
	}
	return nil
}
