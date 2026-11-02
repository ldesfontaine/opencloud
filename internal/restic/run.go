package restic

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/ldesfontaine/opencloud/internal/action"
	"github.com/ldesfontaine/opencloud/internal/agentwire"
	"github.com/ldesfontaine/opencloud/internal/audit"
	"github.com/ldesfontaine/opencloud/internal/machine"
)

// runWaitLimit borne le suivi d'un ordre : le serveur le dit fini de
// lui-même (expiré, ou échoué après MaxRunDuration), mais un suivi ne doit
// pas retenir le dépôt pour toujours.
const runWaitLimit = action.OrderLifetime + action.MaxRunDuration + 5*time.Minute

// Les états du dernier ordre d'une sauvegarde, tels que la base les garde.
const (
	StateSent      = "sent"
	StateRunning   = "running"
	StateSucceeded = "succeeded"
	StateFailed    = "failed"
	StateRefused   = "refused"
)

// reasonUnknown remplace toute raison que le serveur ne reconnaît pas : le
// texte d'un agent n'entre jamais en base tel quel.
const reasonUnknown = "unknown"

// Outcome est ce que la dernière exécution a dit : l'état de l'ordre, sa
// raison (un mot fermé), et le rapport de la source (chemin, raison du
// chemin, la cible visée, ce qui a été écrit). Path vide : pas de rapport.
type Outcome struct {
	RunID      string
	State      string
	Reason     string
	Report     agentwire.ResticRunReport
	FinishedAt time.Time
}

// IsFinished : l'ordre ne bougera plus.
func (o Outcome) IsFinished() bool {
	return o.State == StateSucceeded || o.State == StateFailed || o.State == StateRefused
}

// RunRecord est une ligne de l'historique d'une sauvegarde.
type RunRecord struct {
	RunID           string
	FinishedAt      time.Time
	State           string
	Reason          string
	Path            string
	PathReason      string
	BytesAdded      int64
	DurationSeconds int64
	// Portée service : ce que chaque partie a donné ; vide sinon.
	Parts []PartResult
}

// RunNow lance la sauvegarde tout de suite et rend l'ordre ; son issue
// arrive par le signal de la source. Un dépôt qui reçoit déjà une
// sauvegarde refuse.
func (s *Service) RunNow(ctx context.Context, sourceID, id string) (action.Run, error) {
	current, err := s.ownedBackup(ctx, sourceID, id)
	if err != nil {
		return action.Run{}, err
	}
	if !s.claimRepo(current.repo()) {
		return action.Run{}, ErrBackupRunning
	}
	run, err := s.start(ctx, current)
	if err != nil {
		s.releaseRepo(current.repo())
		return action.Run{}, err
	}
	// La requête finie, son contexte l'est aussi : le suivi garde l'acteur.
	followCtx := audit.WithActor(s.lifetime, audit.ActorFrom(ctx))
	s.background.Add(1)
	go func() {
		defer s.background.Done()
		defer s.releaseRepo(current.repo())
		s.follow(followCtx, current, run)
	}()
	return run, nil
}

// start juge la source, envoie restic.backup et note la tentative. Le dépôt
// est réservé par l'appelant.
func (s *Service) start(ctx context.Context, current Backup) (action.Run, error) {
	source, err := s.machines.Get(ctx, current.SourceMachineID)
	if err != nil {
		return action.Run{}, fmt.Errorf("get source machine: %w", err)
	}
	if err := judgeSourceRights(source, current.Scope); err != nil {
		return action.Run{}, err
	}
	content, err := s.contentToSend(ctx, current, source)
	if err != nil {
		return action.Run{}, err
	}
	params, err := backupParams(current, content)
	if err != nil {
		return action.Run{}, err
	}
	rightRefusal := ErrSourceRightMissing
	if current.Scope == agentwire.ResticScopeVM {
		rightRefusal = ErrSourceVMRightMissing
	}
	run, err := s.send(ctx, current.SourceMachineID, agentwire.KindResticBackup, params, rightRefusal)
	if err != nil {
		return action.Run{}, err
	}
	if err := s.store.NoteResticRunSent(ctx, current.ID, run.ID, s.now()); err != nil {
		return action.Run{}, fmt.Errorf("note restic run: %w", err)
	}
	s.logger.Info("restic backup sent", "backup_id", current.ID, "run_id", run.ID, "source", current.SourceMachineID)
	s.auditor.Record(ctx, audit.Event{
		Action: audit.ResticBackupRun, ObjectID: current.ID, ObjectName: current.Name,
		Details: audit.Details{"run_id": run.ID, "source_machine_id": current.SourceMachineID, "target_machine_id": current.TargetMachineID},
	})
	s.changed()
	return run, nil
}

// contentToSend rend, pour un service, son contenu du moment, relu dans
// l'inventaire à chaque envoi et jugé comme à la création ; rien pour un
// invité.
func (s *Service) contentToSend(ctx context.Context, current Backup, source machine.Status) (ServiceContent, error) {
	if current.Scope != agentwire.ResticScopeService {
		return ServiceContent{}, nil
	}
	service, err := s.store.ResticService(ctx, current.ServiceID)
	if errors.Is(err, ErrServiceNotFound) {
		return ServiceContent{}, ErrServiceUnknown
	}
	if err != nil {
		return ServiceContent{}, fmt.Errorf("get declared service: %w", err)
	}
	return s.judgedContent(ctx, service, source)
}

// backupParams : la cible par son nom de dépôt, la portée et sa référence ;
// pour un service, son dossier, ses volumes et ses bases.
func backupParams(current Backup, content ServiceContent) (json.RawMessage, error) {
	targetUser, err := agentwire.ResticUserFor(current.TargetMachineID)
	if err != nil {
		return nil, ErrTargetUnknown
	}
	params := agentwire.ResticBackupParams{Target: targetUser, Scope: current.Scope}
	if current.Scope == agentwire.ResticScopeVM {
		params.VMID, params.GuestType = current.VMID, current.GuestType
	} else {
		params.Service, params.Volumes = content.Folder, content.Volumes
		for _, database := range content.Databases {
			params.Databases = append(params.Databases, database.Container)
		}
	}
	if err := params.Validate(); err != nil {
		return nil, fmt.Errorf("restic backup params: %w", err)
	}
	return json.Marshal(params)
}

// follow attend la fin de l'ordre et dit s'il a réussi. Le rapport de la
// source arrive par son signal (RecordResults) ; un ordre fini sans réponse
// de l'agent (expiré, non livré, délai dépassé) se note ici.
func (s *Service) follow(ctx context.Context, current Backup, run action.Run) bool {
	waitCtx, cancel := context.WithTimeout(ctx, runWaitLimit)
	defer cancel()
	finished, err := s.sender.Wait(waitCtx, run.ID)
	if err != nil {
		if ctx.Err() == nil {
			s.logger.Warn("restic run not followed", "backup_id", current.ID, "run_id", run.ID, "error", err)
		}
		return false
	}
	// L'agent a répondu : son résultat, rapport compris, passe par le signal.
	if !isServerFinished(finished) {
		return finished.State == action.StateSucceeded
	}
	s.noteServerFinished(ctx, current, run, finished)
	return false
}

func (s *Service) noteServerFinished(ctx context.Context, current Backup, run, finished action.Run) {
	latest, err := s.store.GetResticBackup(ctx, current.ID)
	if err != nil || latest.LastRunID != run.ID || latest.Last.IsFinished() {
		return
	}
	outcome := Outcome{RunID: run.ID, State: StateFailed, Reason: runReason(finished.State, finished.Reason), FinishedAt: s.now()}
	if err := s.store.RecordResticOutcome(ctx, current.ID, outcome); err != nil {
		s.logger.Error("record restic outcome", "backup_id", current.ID, "error", err)
		return
	}
	s.changed()
}

// RecordResults lit les résultats d'ordres que l'agent d'une machine
// rapporte, et garde ceux d'une sauvegarde restic qu'il a reçue. Le rapport
// de chemin est rejugé contre les listes fermées : hors bornes, il est jeté,
// l'état de l'ordre reste.
func (s *Service) RecordResults(ctx context.Context, machineID string, results []agentwire.ActionResult) error {
	isChanged := false
	for _, result := range results {
		current, err := s.store.FindResticBackupByRun(ctx, machineID, result.RunID)
		if err != nil {
			continue
		}
		outcome := outcomeOf(result, s.now())
		if current.Last.IsFinished() && current.Last.RunID == result.RunID {
			continue
		}
		if err := s.store.RecordResticOutcome(ctx, current.ID, outcome); err != nil {
			return fmt.Errorf("record restic outcome: %w", err)
		}
		isChanged = true
		s.logger.Info("restic backup result", "backup_id", current.ID, "run_id", result.RunID, "state", outcome.State,
			"path", outcome.Report.Path, "path_reason", outcome.Report.Reason)
	}
	if isChanged {
		s.changed()
	}
	return nil
}

// outcomeOf traduit un résultat d'agent. Une raison hors de la liste fermée
// devient « unknown » ; un rapport de chemin hors bornes est retiré.
func outcomeOf(result agentwire.ActionResult, at time.Time) Outcome {
	state := agentState(result.State)
	outcome := Outcome{RunID: result.RunID, State: state, Reason: agentReason(result.Reason)}
	if result.Restic != nil && result.Restic.Validate() == nil {
		outcome.Report = *result.Restic
	}
	if outcome.IsFinished() {
		outcome.FinishedAt = at
	}
	return outcome
}

func agentState(state agentwire.ActionState) string {
	switch state {
	case agentwire.ActionSucceeded:
		return StateSucceeded
	case agentwire.ActionFailed:
		return StateFailed
	case agentwire.ActionRefused:
		return StateRefused
	}
	return StateRunning
}

// isServerFinished : le serveur a fini le run sans réponse de l'agent.
func isServerFinished(run action.Run) bool {
	return run.State == action.StateExpired || run.Reason == action.ReasonUndelivered || run.Reason == action.ReasonTimeout
}

// runReason : un ordre expiré se dit expiré, une raison du serveur ou de la
// liste fermée passe, tout autre texte devient « unknown ».
func runReason(state action.State, reason string) string {
	if state == action.StateExpired {
		return agentwire.ReasonExpired
	}
	if reason == action.ReasonUndelivered || reason == action.ReasonTimeout {
		return reason
	}
	return agentReason(reason)
}

// agentReason garde une raison de la liste fermée, avec le droit nommé pour
// « right_missing:<droit> ».
func agentReason(reason string) string {
	if reason == "" {
		return ""
	}
	if capability, found := strings.CutPrefix(reason, agentwire.ReasonRightMissing+":"); found {
		if slices.Contains(machine.CapabilityNames(), capability) {
			return reason
		}
		return reasonUnknown
	}
	if slices.Contains(agentwire.RefusalReasons(), reason) {
		return reason
	}
	return reasonUnknown
}
