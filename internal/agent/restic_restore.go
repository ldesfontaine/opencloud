package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ldesfontaine/opencloud/internal/agentwire"
	"github.com/ldesfontaine/opencloud/internal/machine"
	"github.com/ldesfontaine/opencloud/internal/restic"
)

// La restauration d'un fichier ou d'un dossier, chez la source : À CÔTÉ de
// l'original, jamais par-dessus, sans rien supprimer de ce qui existait. Le
// mot de passe du dépôt est celui du fichier du lien (0600), que la source
// détient déjà : l'ordre n'en porte aucun. restic restaure dans un dossier
// temporaire créé à côté de la destination (même disque), puis l'agent déplace
// le résultat à sa place ; le dossier temporaire est le seul chose effacée, et
// c'est lui qui l'a créé.
const (
	resticRestoreTimeout = 55 * time.Minute
	restoreTempPrefix    = ".restaure-tmp-"
)

type resticRestoreJob struct {
	params      agentwire.ResticRestoreParams
	binary      string
	destination string
}

// acceptResticRestore juge l'ordre vite, avant de rien écrire : sa forme, le
// droit (jugé plus haut), restic présent, le lien provisionné, la destination
// couverte par files.write (liens résolus) et libre. Un refus ne touche à rien.
func (r *orderRunner) acceptResticRestore(body agentwire.OrderBody) (agentwire.ActionResult, func(ctx context.Context)) {
	job, refused := judgeRestore(body, r.identity.Capabilities(), r.checks, r.restic.source.isProvisioned)
	if refused != nil {
		r.journal.notAccepted(body, refused.Reason)
		return *refused, nil
	}
	return accepted(body), func(ctx context.Context) {
		output, err := r.restic.source.restore(ctx, job)
		r.results.push(r.finishRestic(body, output, err))
	}
}

func judgeRestore(body agentwire.OrderBody, capabilities []machine.Capability, checks hostChecks, isProvisioned func(string) bool) (resticRestoreJob, *agentwire.ActionResult) {
	refused := func(state agentwire.ActionState, reason string) *agentwire.ActionResult {
		return &agentwire.ActionResult{RunID: body.RunID, State: state, Reason: reason}
	}
	var params agentwire.ResticRestoreParams
	if err := json.Unmarshal(body.Params, &params); err != nil || params.Validate() != nil || len(body.Secret) != 0 {
		return resticRestoreJob{}, refused(agentwire.ActionFailed, agentwire.ReasonBadOrder)
	}
	binary, err := checks.findRestic()
	if err != nil {
		return resticRestoreJob{}, refused(agentwire.ActionRefused, agentwire.ReasonResticMissing)
	}
	if !isProvisioned(params.Target) {
		return resticRestoreJob{}, refused(agentwire.ActionRefused, agentwire.ReasonResticNotProvisioned)
	}
	destination := agentwire.RestoreDestination(params.Path, params.Stamp)
	if reason := restoreDestinationReason(capabilities, destination); reason != "" {
		return resticRestoreJob{}, refused(agentwire.ActionRefused, reason)
	}
	return resticRestoreJob{params: params, binary: binary, destination: destination}, nil
}

// restoreDestinationReason : vide si la destination est couverte par files.write,
// son dossier résolu (liens suivis) l'est encore, et rien n'y existe déjà.
func restoreDestinationReason(capabilities []machine.Capability, destination string) string {
	if !machine.CoversPath(capabilities, machine.CapabilityFilesWrite, destination) {
		return agentwire.ReasonPathOutsideRight
	}
	parent, err := filepath.EvalSymlinks(filepath.Dir(destination))
	if err != nil {
		return agentwire.ReasonPathUnwritable
	}
	resolved := filepath.Join(parent, filepath.Base(destination))
	if !machine.CoversPath(capabilities, machine.CapabilityFilesWrite, resolved) {
		return agentwire.ReasonPathOutsideRight
	}
	if _, err := os.Lstat(resolved); err == nil {
		return agentwire.ReasonPathExists
	} else if !errors.Is(err, fs.ErrNotExist) {
		return agentwire.ReasonPathUnwritable
	}
	return ""
}

// restore lance restic dans un dossier temporaire voisin, puis déplace le
// résultat à la destination. Les mots de passe sont masqués dans toute sortie.
func (s *resticSource) restore(ctx context.Context, job resticRestoreJob) ([]agentwire.OutputLine, error) {
	ctx, cancel := context.WithTimeout(ctx, resticRestoreTimeout)
	defer cancel()
	files, secrets, err := s.prepareLink(job.params.Target)
	if err != nil {
		if isUntouched(err) {
			return nil, err
		}
		return nil, refuseWith(agentwire.ReasonPathUnwritable, err)
	}
	parent, err := filepath.EvalSymlinks(filepath.Dir(job.destination))
	if err != nil {
		return nil, refuseWith(agentwire.ReasonPathUnwritable, err)
	}
	destination := filepath.Join(parent, filepath.Base(job.destination))
	temp, err := os.MkdirTemp(parent, restoreTempPrefix)
	if err != nil {
		return nil, refuseWith(agentwire.ReasonPathUnwritable, err)
	}
	defer removeOwnTemp(temp, parent)

	choice := restic.Choose(ctx, s.dialer, s.loadTarget(job.params.Target), false)
	s.logger.Info("restic path chosen", "target", job.params.Target, "path", choice.Path, "reason", choice.Reason, "purpose", "restore")
	repository, release, err := s.repositoryFor(choice, job.params.Target, files)
	if err != nil {
		return nil, failWith(agentwire.ReasonResticFailed, err)
	}
	defer release()
	command, err := restic.RestoreSnapshot(job.binary, repository, job.params.Snapshot, temp, job.params.Path)
	if err != nil {
		return nil, failWith(agentwire.ReasonBadOrder, err)
	}
	tail := newLineTail(resticTailLines)
	code, runErr := s.runner.run(ctx, command, io.Discard, tail.writer("stderr"))
	tail.flush()
	lines := redactLines(tail.lines(), secrets, redactedSecret)
	if runErr != nil || code != 0 {
		return lines, failWith(agentwire.ReasonRestoreFailed, fmt.Errorf("restic restore exited with code %d: %v", code, runErr))
	}
	restored := filepath.Join(temp, filepath.FromSlash(job.params.Path))
	if _, err := os.Lstat(restored); err != nil {
		return lines, failWith(agentwire.ReasonPathNotInSnapshot, errors.New("the path is not in the snapshot"))
	}
	// Dernier jugement avant d'écrire : rien n'existe à la destination.
	if _, err := os.Lstat(destination); err == nil {
		return lines, refuseWith(agentwire.ReasonPathExists, errors.New("the destination appeared"))
	}
	if err := os.Rename(restored, destination); err != nil {
		return lines, failWith(agentwire.ReasonPathUnwritable, err)
	}
	return append(lines, stdout("restored "+destination)), nil
}

// removeOwnTemp efface le dossier temporaire que l'agent a créé, et lui seul :
// son nom porte le préfixe et son parent est celui de la destination.
func removeOwnTemp(temp, parent string) {
	if filepath.Dir(temp) == parent && strings.HasPrefix(filepath.Base(temp), restoreTempPrefix) {
		_ = os.RemoveAll(temp)
	}
}
