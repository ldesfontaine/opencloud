package restic

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/ldesfontaine/opencloud/internal/action"
	"github.com/ldesfontaine/opencloud/internal/agentwire"
	"github.com/ldesfontaine/opencloud/internal/refusal"
)

// retainAfterNight applique la rétention de chaque dépôt qui a reçu un
// instantané cette nuit, un dépôt après l'autre : une seule purge à la fois,
// et jamais pendant une sauvegarde du même dépôt (claimRepo).
func (s *Service) retainAfterNight(ctx context.Context, saved []Backup) {
	backups, err := s.store.ListResticBackups(ctx)
	if err != nil {
		s.logger.Error("list restic backups", "error", err)
		return
	}
	retained := map[repoKey]bool{}
	for _, passed := range saved {
		key := passed.repo()
		if retained[key] || ctx.Err() != nil {
			continue
		}
		retained[key] = true
		s.retainRepo(ctx, key, widestRetention(sharingRepo(backups, key)))
	}
}

func sharingRepo(backups []Backup, key repoKey) []Backup {
	sharing := []Backup{}
	for _, candidate := range backups {
		if candidate.repo() == key {
			sharing = append(sharing, candidate)
		}
	}
	return sharing
}

// widestRetention : les sauvegardes d'un dépôt le partagent, la purge garde
// le plus large de chaque compte. Aucune ne perd ce qu'elle a déclaré.
func widestRetention(backups []Backup) Retention {
	var widest Retention
	for _, candidate := range backups {
		kept := candidate.Retention
		widest.Last = max(widest.Last, kept.Last)
		widest.Daily = max(widest.Daily, kept.Daily)
		widest.Weekly = max(widest.Weekly, kept.Weekly)
		widest.Monthly = max(widest.Monthly, kept.Monthly)
		widest.Yearly = max(widest.Yearly, kept.Yearly)
	}
	return widest
}

// retainRepo envoie restic.retain à la cible, avec le mot de passe du dépôt
// dans le secret de l'ordre, attend sa fin et la note sur le lien. Une
// rétention sous le plancher rapporté ne part pas : elle est notée refusée,
// comme la cible l'aurait fait.
func (s *Service) retainRepo(ctx context.Context, key repoKey, retention Retention) {
	if !s.claimRepo(key) {
		s.logger.Warn("restic retention skipped, repository busy", "source", key.source, "target", key.target)
		return
	}
	defer s.releaseRepo(key)
	link, err := s.store.GetResticLink(ctx, key.source, key.target)
	if err != nil {
		s.logger.Error("get restic link", "source", key.source, "target", key.target, "error", err)
		return
	}
	run, err := s.sendRetain(ctx, link, retention)
	if err != nil {
		s.noteRetain(ctx, link, RetainOutcome{State: StateRefused, Reason: retainRefusal(err), At: s.now()})
		return
	}
	s.noteRetain(ctx, link, RetainOutcome{RunID: run.ID, State: StateSent, At: s.now()})
	waitCtx, cancel := context.WithTimeout(ctx, runWaitLimit)
	defer cancel()
	finished, err := s.sender.Wait(waitCtx, run.ID)
	if err != nil {
		s.logger.Warn("restic retention not followed", "run_id", run.ID, "error", err)
		return
	}
	s.noteRetain(ctx, link, RetainOutcome{
		RunID: run.ID, State: runState(finished.State), Reason: runReason(finished.State, finished.Reason), At: s.now(),
	})
}

// errRetainBelowFloor : la rétention garderait moins que le plancher que la
// cible a rapporté ; notée sous la raison que la cible aurait rendue.
var errRetainBelowFloor = errors.New("restic retention below the floor of the target")

func (s *Service) sendRetain(ctx context.Context, link Link, retention Retention) (action.Run, error) {
	if link.Secrets.isLost() {
		return action.Run{}, ErrLinkSecretLost
	}
	target, err := s.store.GetResticTarget(ctx, link.TargetMachineID)
	if err != nil {
		return action.Run{}, fmt.Errorf("get restic target: %w", err)
	}
	user, err := agentwire.ResticUserFor(link.SourceMachineID)
	if err != nil {
		return action.Run{}, ErrSourceUnknown
	}
	params := retention.retainParams(user)
	if params.Validate() != nil {
		return action.Run{}, ErrRetentionInvalid
	}
	if floor := target.Reported.RetentionFloor; floor != nil && !floor.Admits(params) {
		return action.Run{}, errRetainBelowFloor
	}
	encoded, err := json.Marshal(params)
	if err != nil {
		return action.Run{}, fmt.Errorf("encode retain: %w", err)
	}
	secret := agentwire.ResticRetainSecret{RepoPassword: link.Secrets.Repo}
	run, err := s.sendSecret(ctx, link.TargetMachineID, agentwire.KindResticRetain, encoded, secret, ErrTargetRightMissing)
	if err != nil {
		return action.Run{}, err
	}
	s.logger.Info("restic retention sent", "run_id", run.ID, "source", link.SourceMachineID, "target", link.TargetMachineID)
	return run, nil
}

// retainRefusal : la raison de la cible pour un plancher, la clé d'un
// refus, sinon « unknown » ; jamais un texte.
func retainRefusal(cause error) string {
	if errors.Is(cause, errRetainBelowFloor) {
		return agentwire.ReasonRetentionBelowFloor
	}
	if key, isRefusal := refusal.KeyOf(cause); isRefusal {
		return key
	}
	return reasonUnknown
}

func (s *Service) noteRetain(ctx context.Context, link Link, outcome RetainOutcome) {
	if outcome.State == StateRefused || outcome.State == StateFailed {
		s.logger.Warn("restic retention not applied", "source", link.SourceMachineID, "target", link.TargetMachineID, "reason", outcome.Reason)
	}
	if err := s.store.NoteResticRetain(ctx, link.ID, outcome); err != nil {
		s.logger.Error("note restic retention", "link_id", link.ID, "error", err)
		return
	}
	s.changed()
}

// runState : l'état d'un ordre fini, dans les mots de la base ; un ordre
// expiré a échoué.
func runState(state action.State) string {
	switch state {
	case action.StateSucceeded:
		return StateSucceeded
	case action.StateRefused:
		return StateRefused
	}
	return StateFailed
}
