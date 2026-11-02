package restic

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/ldesfontaine/opencloud/internal/action"
	"github.com/ldesfontaine/opencloud/internal/agentwire"
	"github.com/ldesfontaine/opencloud/internal/audit"
	"github.com/ldesfontaine/opencloud/internal/machine"
	"github.com/ldesfontaine/opencloud/internal/refusal"
)

// browseWait : combien de temps l'écran attend la cible. Une cible qui ne
// répond pas dans ce délai est dite injoignable ; l'ordre, lui, expire seul.
const browseWait = 45 * time.Second

var (
	// La cible n'a pas restic.browse : l'écran propose de la ré-enrôler.
	ErrTargetBrowseRightMissing = refusal.Conflict("restic.target_browse_right_missing", "the agent of the target may not browse repositories: re-enroll it with restic.browse")
	ErrTargetBrowseUnavailable  = refusal.Conflict("restic.target_browse_unavailable", "the agent of the target holds restic.browse but cannot exercise it on this machine")
	ErrBrowseRequest            = refusal.Invalid("restic.browse_request", "the snapshot or the folder is outside its form")
	ErrBrowseRepoEmpty          = refusal.Conflict("restic.browse_repo_empty", "the repository has no snapshot yet")
	ErrBrowseTimeout            = refusal.Conflict("restic.browse_timeout", "the target did not answer in time")
	ErrBrowseFailed             = refusal.Conflict("restic.browse_failed", "the target could not read the repository")
)

// BrowseRequest : ce qu'on lit. Snapshot vide, la liste des instantanés ; sinon
// le contenu du dossier Path de cet instantané.
type BrowseRequest struct {
	Snapshot string
	Path     string
}

// browseSlot attend le résultat d'un ordre de parcours. Il est créé par celui
// des deux qui arrive le premier : l'écran qui attend, ou le signal de la cible.
type browseSlot struct {
	done   chan struct{}
	once   sync.Once
	result agentwire.ActionResult
}

func (s *Service) browseSlot(runID string) *browseSlot {
	s.browseMu.Lock()
	defer s.browseMu.Unlock()
	if s.browses == nil {
		s.browses = map[string]*browseSlot{}
	}
	slot, found := s.browses[runID]
	if !found {
		slot = &browseSlot{done: make(chan struct{})}
		s.browses[runID] = slot
	}
	return slot
}

func (s *Service) dropBrowseSlot(runID string) {
	s.browseMu.Lock()
	delete(s.browses, runID)
	s.browseMu.Unlock()
}

// deliverBrowse donne un résultat fini à l'écran qui l'attend. Rend vrai si le
// résultat est celui d'un parcours. Les noms ne sont gardés nulle part : ni
// base, ni journal, seulement ce créneau, le temps de la requête.
func (s *Service) deliverBrowse(result agentwire.ActionResult) bool {
	s.browseMu.Lock()
	slot, found := s.browses[result.RunID]
	s.browseMu.Unlock()
	if !found {
		return false
	}
	if agentState(result.State) != StateRunning {
		slot.once.Do(func() {
			slot.result = result
			close(slot.done)
		})
	}
	return true
}

// Browse fait lire à la cible le dépôt de la sauvegarde, pour l'explorateur.
// La requête attend la réponse de la cible : rien n'est gardé, le rapport est
// rejugé contre ses bornes. Lire ne demande pas le dépôt : une sauvegarde ou une
// rétention en cours n'empêche pas de parcourir.
func (s *Service) Browse(ctx context.Context, sourceID, id string, request BrowseRequest) (agentwire.ResticBrowseReport, error) {
	current, err := s.ownedBackup(ctx, sourceID, id)
	if err != nil {
		return agentwire.ResticBrowseReport{}, err
	}
	user, err := agentwire.ResticUserFor(current.SourceMachineID)
	if err != nil {
		return agentwire.ResticBrowseReport{}, ErrSourceUnknown
	}
	params := agentwire.ResticBrowseParams{User: user, Snapshot: request.Snapshot, Path: request.Path}
	if params.Validate() != nil {
		return agentwire.ResticBrowseReport{}, ErrBrowseRequest
	}
	link, err := s.store.GetResticLink(ctx, current.SourceMachineID, current.TargetMachineID)
	if err != nil {
		return agentwire.ResticBrowseReport{}, fmt.Errorf("get restic link: %w", err)
	}
	if link.Secrets.isLost() {
		return agentwire.ResticBrowseReport{}, ErrLinkSecretLost
	}
	target, err := s.machines.Get(ctx, link.TargetMachineID)
	if err != nil {
		return agentwire.ResticBrowseReport{}, fmt.Errorf("get target machine: %w", err)
	}
	if err := judgeBrowseRight(target); err != nil {
		return agentwire.ResticBrowseReport{}, err
	}
	encoded, err := json.Marshal(params)
	if err != nil {
		return agentwire.ResticBrowseReport{}, fmt.Errorf("encode browse: %w", err)
	}
	run, err := s.sendSecret(ctx, link.TargetMachineID, agentwire.KindResticBrowse, encoded, agentwire.ResticBrowseSecret{RepoPassword: link.Secrets.Repo}, ErrTargetBrowseRightMissing)
	if err != nil {
		return agentwire.ResticBrowseReport{}, err
	}
	s.auditBrowse(ctx, current, request, run)
	return s.awaitBrowse(ctx, run)
}

func (s *Service) auditBrowse(ctx context.Context, current Backup, request BrowseRequest, run action.Run) {
	details := audit.Details{"run_id": run.ID, "target_machine_id": current.TargetMachineID}
	if request.Snapshot != "" {
		details["snapshot"] = request.Snapshot
		details["path"] = request.Path
	}
	s.auditor.Record(ctx, audit.Event{
		Action: audit.ResticBackupBrowse, ObjectID: current.ID, ObjectName: current.Name, Details: details,
	})
}

func (s *Service) awaitBrowse(ctx context.Context, run action.Run) (agentwire.ResticBrowseReport, error) {
	slot := s.browseSlot(run.ID)
	defer s.dropBrowseSlot(run.ID)
	waitCtx, cancel := context.WithTimeout(ctx, browseWait)
	defer cancel()
	finished := make(chan action.Run, 1)
	go func() {
		if done, err := s.sender.Wait(waitCtx, run.ID); err == nil {
			finished <- done
		}
	}()
	for {
		select {
		case <-slot.done:
			return browseOutcome(slot.result)
		case done := <-finished:
			// Fini sans résultat à lire : refusé, expiré, non livré. Un succès
			// laisse le résultat arriver.
			if done.State != action.StateSucceeded {
				return agentwire.ResticBrowseReport{}, browseRefusal(runReason(done.State, done.Reason))
			}
		case <-waitCtx.Done():
			return agentwire.ResticBrowseReport{}, ErrBrowseTimeout
		}
	}
}

func browseOutcome(result agentwire.ActionResult) (agentwire.ResticBrowseReport, error) {
	if agentState(result.State) != StateSucceeded {
		return agentwire.ResticBrowseReport{}, browseRefusal(agentReason(result.Reason))
	}
	if result.Browse == nil || result.Browse.Validate() != nil {
		return agentwire.ResticBrowseReport{}, ErrBrowseFailed
	}
	return *result.Browse, nil
}

func browseRefusal(reason string) error {
	switch reason {
	case agentwire.ReasonRepoEmpty:
		return ErrBrowseRepoEmpty
	case agentwire.RightMissing(machine.CapabilityResticBrowse), agentwire.RightMissing(machine.CapabilityResticTarget):
		return ErrTargetBrowseRightMissing
	case action.ReasonTimeout, action.ReasonUndelivered, agentwire.ReasonExpired:
		return ErrBrowseTimeout
	}
	return ErrBrowseFailed
}

// judgeBrowseRight : la cible tient restic.browse et sait l'exercer.
func judgeBrowseRight(target machine.Status) error {
	switch RightOf(target, machine.CapabilityResticBrowse).State {
	case RightOK, RightUnknown:
		return nil
	case RightUnavailable:
		return ErrTargetBrowseUnavailable
	}
	return ErrTargetBrowseRightMissing
}
