package restic

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/ldesfontaine/opencloud/internal/action"
	"github.com/ldesfontaine/opencloud/internal/agentwire"
	"github.com/ldesfontaine/opencloud/internal/audit"
	"github.com/ldesfontaine/opencloud/internal/machine"
	"github.com/ldesfontaine/opencloud/internal/refusal"
)

const (
	// Une vérification réussie tient une semaine : la nuit suivante n'en
	// relance pas. Une vérification ratée est rejouée dès la nuit d'après.
	verifyEvery      = 7 * 24 * time.Hour
	verifyRetryAfter = 20 * time.Hour
	// Un test de restauration plus vieux que ceci est périmé (alerte).
	VerifyStaleAfter = 14 * 24 * time.Hour
)

var (
	// La cible n'a pas restic.verify : l'écran propose de la ré-enrôler.
	ErrTargetVerifyRightMissing = refusal.Conflict("restic.target_verify_right_missing", "the agent of the target may not verify repositories: re-enroll it with restic.verify")
	ErrTargetVerifyUnavailable  = refusal.Conflict("restic.target_verify_unavailable", "the agent of the target holds restic.verify but cannot exercise it on this machine")
)

// verifyDue : jamais vérifié, ou la dernière réussite a plus d'une semaine, ou
// le dernier essai a échoué depuis plus de vingt heures.
func verifyDue(now time.Time, verify VerifyOutcome) bool {
	age := now.Sub(verify.At)
	switch {
	case verify.At.IsZero():
		return true
	case verify.State == StateSent:
		// Un ordre jamais conclu (serveur arrêté pendant l'attente) est rejoué.
		return age > runWaitLimit
	case verify.State == StateSucceeded:
		return age > verifyEvery
	}
	return age > verifyRetryAfter
}

// VerifyNow vérifie le dépôt de la sauvegarde tout de suite et rend l'ordre ;
// la cible rapporte ses comptes par son signal. Un dépôt qui reçoit déjà une
// sauvegarde, une rétention ou une vérification refuse.
func (s *Service) VerifyNow(ctx context.Context, sourceID, id string) (action.Run, error) {
	current, err := s.ownedBackup(ctx, sourceID, id)
	if err != nil {
		return action.Run{}, err
	}
	if !s.claimRepo(current.repo()) {
		return action.Run{}, ErrBackupRunning
	}
	link, run, err := s.startVerify(ctx, current.repo())
	if err != nil {
		s.releaseRepo(current.repo())
		return action.Run{}, err
	}
	s.auditVerify(ctx, current, run)
	followCtx := audit.WithActor(s.lifetime, audit.ActorFrom(ctx))
	s.background.Add(1)
	go func() {
		defer s.background.Done()
		defer s.releaseRepo(current.repo())
		s.followVerify(followCtx, link, run)
	}()
	return run, nil
}

func (s *Service) auditVerify(ctx context.Context, current Backup, run action.Run) {
	s.auditor.Record(ctx, audit.Event{
		Action: audit.ResticBackupVerify, ObjectID: current.ID, ObjectName: current.Name,
		Details: audit.Details{"run_id": run.ID, "source_machine_id": current.SourceMachineID, "target_machine_id": current.TargetMachineID},
	})
}

// verifyAfterNight vérifie, un dépôt après l'autre, ceux dont la vérification
// est due. Une cible sans le droit est passée : l'alerte « test de
// restauration périmé » dit pourquoi et le geste à faire.
func (s *Service) verifyAfterNight(ctx context.Context) {
	links, err := s.store.ListResticLinks(ctx)
	if err != nil {
		s.logger.Error("list restic links", "error", err)
		return
	}
	for _, link := range links {
		if ctx.Err() != nil {
			return
		}
		if !verifyDue(s.now(), link.Verify) || !s.claimRepo(link.repo()) {
			continue
		}
		s.verifyClaimed(ctx, link)
		s.releaseRepo(link.repo())
	}
}

func (s *Service) verifyClaimed(ctx context.Context, link Link) {
	link, run, err := s.startVerify(ctx, link.repo())
	if err != nil {
		if refused, ok := refusal.KeyOf(err); ok && !errors.Is(err, ErrBackupRunning) {
			s.logger.Info("restic verification skipped", "source", link.SourceMachineID, "target", link.TargetMachineID, "reason", refused)
			return
		}
		s.logger.Warn("restic verification not sent", "source", link.SourceMachineID, "target", link.TargetMachineID, "error", err)
		return
	}
	s.followVerify(ctx, link, run)
}

// startVerify envoie restic.verify à la cible, avec le mot de passe du dépôt
// dans le secret de l'ordre, et note la tentative. Le dépôt est réservé par
// l'appelant.
func (s *Service) startVerify(ctx context.Context, key repoKey) (Link, action.Run, error) {
	link, err := s.store.GetResticLink(ctx, key.source, key.target)
	if err != nil {
		return Link{}, action.Run{}, fmt.Errorf("get restic link: %w", err)
	}
	if link.Secrets.isLost() {
		return link, action.Run{}, ErrLinkSecretLost
	}
	target, err := s.machines.Get(ctx, link.TargetMachineID)
	if err != nil {
		return link, action.Run{}, fmt.Errorf("get target machine: %w", err)
	}
	if err := judgeVerifyRight(target); err != nil {
		return link, action.Run{}, err
	}
	user, err := agentwire.ResticUserFor(link.SourceMachineID)
	if err != nil {
		return link, action.Run{}, ErrSourceUnknown
	}
	params, err := json.Marshal(agentwire.ResticVerifyParams{User: user})
	if err != nil {
		return link, action.Run{}, fmt.Errorf("encode verify: %w", err)
	}
	secret := agentwire.ResticVerifySecret{RepoPassword: link.Secrets.Repo}
	run, err := s.sendSecret(ctx, link.TargetMachineID, agentwire.KindResticVerify, params, secret, ErrTargetVerifyRightMissing)
	if err != nil {
		return link, action.Run{}, err
	}
	link.Verify = VerifyOutcome{RunID: run.ID, State: StateSent, At: s.now(), SuccessAt: link.Verify.SuccessAt}
	if err := s.store.NoteResticVerify(ctx, link.ID, link.Verify); err != nil {
		return link, action.Run{}, fmt.Errorf("note restic verify: %w", err)
	}
	s.logger.Info("restic verification sent", "run_id", run.ID, "source", link.SourceMachineID, "target", link.TargetMachineID)
	s.changed()
	return link, run, nil
}

// judgeVerifyRight : la cible tient restic.verify et sait l'exercer.
func judgeVerifyRight(target machine.Status) error {
	switch RightOf(target, machine.CapabilityResticVerify).State {
	case RightOK, RightUnknown:
		return nil
	case RightUnavailable:
		return ErrTargetVerifyUnavailable
	}
	return ErrTargetVerifyRightMissing
}

// followVerify attend la fin de l'ordre. Les comptes arrivent par le signal
// de la cible (RecordResults) ; un ordre fini sans réponse de l'agent
// (expiré, non livré, délai dépassé) se note ici.
func (s *Service) followVerify(ctx context.Context, link Link, run action.Run) {
	waitCtx, cancel := context.WithTimeout(ctx, runWaitLimit)
	defer cancel()
	finished, err := s.sender.Wait(waitCtx, run.ID)
	if err != nil {
		if ctx.Err() == nil {
			s.logger.Warn("restic verification not followed", "run_id", run.ID, "error", err)
		}
		return
	}
	if !isServerFinished(finished) {
		return
	}
	latest, err := s.store.FindResticLinkByVerifyRun(ctx, link.TargetMachineID, run.ID)
	if err != nil || latest.Verify.State != StateSent {
		return
	}
	s.noteVerify(ctx, latest, VerifyOutcome{
		RunID: run.ID, State: StateFailed, Reason: runReason(finished.State, finished.Reason), At: s.now(), SuccessAt: latest.Verify.SuccessAt,
	})
}

// recordVerifyResult garde ce que la cible a rapporté d'une vérification.
// Les comptes sont rejugés contre leurs bornes ; hors bornes, ils sont jetés
// et l'ordre compte pour un échec inconnu. Rend vrai si le résultat était
// celui d'une vérification.
func (s *Service) recordVerifyResult(ctx context.Context, machineID string, result agentwire.ActionResult) bool {
	link, err := s.store.FindResticLinkByVerifyRun(ctx, machineID, result.RunID)
	if err != nil {
		return false
	}
	state := agentState(result.State)
	if state == StateRunning || link.Verify.State != StateSent {
		return true
	}
	outcome := VerifyOutcome{RunID: result.RunID, State: state, Reason: agentReason(result.Reason), At: s.now(), SuccessAt: link.Verify.SuccessAt}
	if state == StateSucceeded {
		if result.Verify == nil || result.Verify.Validate() != nil {
			outcome.State, outcome.Reason = StateFailed, reasonUnknown
		} else {
			outcome.Snapshots, outcome.Files, outcome.Bytes = result.Verify.Snapshots, result.Verify.Files, result.Verify.Bytes
		}
	}
	s.noteVerify(ctx, link, outcome)
	return true
}

func (s *Service) noteVerify(ctx context.Context, link Link, outcome VerifyOutcome) {
	if outcome.State != StateSucceeded {
		s.logger.Warn("restic verification failed", "source", link.SourceMachineID, "target", link.TargetMachineID, "reason", outcome.Reason)
	}
	if err := s.store.NoteResticVerify(ctx, link.ID, outcome); err != nil {
		s.logger.Error("note restic verification", "link_id", link.ID, "error", err)
		return
	}
	s.changed()
	// L'alerte « test de restauration périmé » suit tout de suite.
	s.Supervise(ctx)
}
