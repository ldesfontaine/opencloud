package agent

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os/exec"

	"github.com/ldesfontaine/opencloud/internal/agentwire"
	"github.com/ldesfontaine/opencloud/internal/machine"
)

// Les ordres restic. La préparation de la cible, ses utilisateurs et le lien
// de la source écrivent quelques fichiers de l'état : ils se font tout de
// suite, sous le verrou du juge. La sauvegarde et la rétention sont des
// ordres longs, faits par le worker. Les mots de passe arrivent dans le
// secret signé de l'ordre (body.Secret) ; aucun n'est journalisé ni rendu.

// useResticBinaries pose les chemins que l'opérateur a déclarés (-restic,
// -rest-server) ; vides, les binaires se cherchent dans le PATH.
func (r *orderRunner) useResticBinaries(resticBinary, restServerBinary string) {
	r.checks.findRestic = func() (string, error) { return findBinary(resticBinary, resticProgram, exec.LookPath) }
	r.checks.findRestServer = func() (string, error) { return findBinary(restServerBinary, restServerProgram, exec.LookPath) }
}

// startRestic relance rest-server au démarrage de l'agent, si la machine
// est une cible déjà préparée.
func (r *orderRunner) startRestic(ctx context.Context) {
	r.mu.Lock()
	dir, isTarget := resticTargetDir(r.identity.Capabilities())
	checks := r.checks
	r.mu.Unlock()
	if isTarget {
		r.restic.target.start(ctx, dir, checks)
	}
}

// resticTargetDir rend le dossier des dépôts : le chemin du droit
// restic.target, faux sans le droit.
func resticTargetDir(capabilities []machine.Capability) (string, bool) {
	for _, capability := range capabilities {
		if capability.Name == machine.CapabilityResticTarget && len(capability.Paths) == 1 {
			return capability.Paths[0], true
		}
	}
	return "", false
}

func (r *orderRunner) resticTargetSetup(body agentwire.OrderBody) agentwire.ActionResult {
	var params agentwire.ResticTargetSetupParams
	if err := json.Unmarshal(body.Params, &params); err != nil {
		return agentwire.ActionResult{RunID: body.RunID, State: agentwire.ActionFailed, Reason: agentwire.ReasonBadOrder}
	}
	if err := params.Validate(); err != nil {
		if errors.Is(err, agentwire.ErrResticRetentionInvalid) {
			return agentwire.ActionResult{RunID: body.RunID, State: agentwire.ActionRefused, Reason: agentwire.ReasonRetentionInvalid}
		}
		return agentwire.ActionResult{RunID: body.RunID, State: agentwire.ActionFailed, Reason: agentwire.ReasonBadOrder}
	}
	dir, _ := resticTargetDir(r.identity.Capabilities())
	output, err := r.restic.target.setup(params, dir, r.checks)
	return r.finishRestic(body, output, err)
}

// resticUser fait restic.user.add (le mot de passe REST dans le secret de
// l'ordre) et restic.user.remove (sans secret).
func (r *orderRunner) resticUser(body agentwire.OrderBody) agentwire.ActionResult {
	bad := agentwire.ActionResult{RunID: body.RunID, State: agentwire.ActionFailed, Reason: agentwire.ReasonBadOrder}
	if body.Kind == agentwire.KindResticUserRemove {
		var params agentwire.ResticUserRemoveParams
		if err := json.Unmarshal(body.Params, &params); err != nil || params.Validate() != nil || len(body.Secret) != 0 {
			return bad
		}
		output, err := r.restic.target.setUser(params.User, "")
		return r.finishRestic(body, output, err)
	}
	var params agentwire.ResticUserAddParams
	if err := json.Unmarshal(body.Params, &params); err != nil || params.Validate() != nil {
		return bad
	}
	var secret agentwire.ResticUserSecret
	if err := json.Unmarshal(body.Secret, &secret); err != nil || secret.Validate() != nil {
		return bad
	}
	output, err := r.restic.target.setUser(params.User, secret.RESTPassword)
	return r.finishRestic(body, output, err)
}

func (r *orderRunner) resticProvision(body agentwire.OrderBody) agentwire.ActionResult {
	bad := agentwire.ActionResult{RunID: body.RunID, State: agentwire.ActionFailed, Reason: agentwire.ReasonBadOrder}
	var params agentwire.ResticProvisionParams
	if err := json.Unmarshal(body.Params, &params); err != nil || params.Validate() != nil {
		return bad
	}
	var secret agentwire.ResticProvisionSecret
	if err := json.Unmarshal(body.Secret, &secret); err != nil || secret.Validate() != nil {
		return bad
	}
	output, err := r.restic.source.provision(params, secret)
	return r.finishRestic(body, output, err)
}

// finishRestic rapporte un ordre restic fait tout de suite, et réveille le
// rapport : la fiche voit le changement sans attendre la période.
func (r *orderRunner) finishRestic(body agentwire.OrderBody, output []agentwire.OutputLine, err error) agentwire.ActionResult {
	if err != nil {
		r.journal.failed(body, err)
	} else {
		r.journal.executed(body)
	}
	r.restic.nudge()
	result := resultOf(body.RunID, output, err)
	if result.State == agentwire.ActionFailed && isUntouched(err) {
		result.State = agentwire.ActionRefused
	}
	return result
}

// acceptResticBackup juge la sauvegarde vite, puis l'accepte : sonder la
// cible et lancer restic est le travail du worker. Droits, racine et
// vérifications sont lus ici, sous le verrou.
func (r *orderRunner) acceptResticBackup(body agentwire.OrderBody) (agentwire.ActionResult, func(ctx context.Context)) {
	checks := r.checks
	job, refused := readResticBackup(body, r.identity.Capabilities(), r.identity.Root(), checks)
	if refused == nil && !r.restic.source.isProvisioned(job.params.Target) {
		refused = &agentwire.ActionResult{RunID: body.RunID, State: agentwire.ActionRefused, Reason: agentwire.ReasonResticNotProvisioned}
	}
	if refused != nil {
		r.journal.notAccepted(body, refused.Reason)
		return *refused, nil
	}
	return accepted(body), func(ctx context.Context) {
		report, output, err := r.restic.source.backup(ctx, job, checks)
		result := r.finishRestic(body, output, err)
		if report.Path != "" {
			result.Restic = &report
		}
		r.results.push(result)
	}
}

// serveRelay passe une requête relayée à la cible ; sans le droit
// restic.target, elle est refusée sans toucher rest-server. Il se lance à
// part du flux, qui continue de lire.
func (r *orderRunner) serveRelay(ctx context.Context, request agentwire.ResticRelayRequest) {
	server := relayLink{fetch: r.fetch}
	r.mu.Lock()
	isTarget := machine.HasCapability(r.identity.Capabilities(), machine.CapabilityResticTarget)
	r.mu.Unlock()
	if !isTarget {
		r.logger.Warn("restic relay refused", "repo", request.Repo, "reason", agentwire.RightMissing(machine.CapabilityResticTarget))
		r.restic.target.answerRelay(ctx, server, request.ID, http.StatusForbidden, relayRightMissing)
		return
	}
	r.restic.target.serveRelay(ctx, server, request)
}

// resticReport rend la section restic du signal, faux pour un agent qui n'a
// aucun des deux droits.
func (r *orderRunner) resticReport(ctx context.Context) (agentwire.ResticReport, bool) {
	r.mu.Lock()
	capabilities, checks := r.identity.Capabilities(), r.checks
	r.mu.Unlock()
	var report agentwire.ResticReport
	if dir, isTarget := resticTargetDir(capabilities); isTarget {
		target := r.restic.target.report(ctx, dir, checks)
		report.Target = &target
	}
	if machine.HasCapability(capabilities, machine.CapabilityResticProduce) {
		source := r.restic.source.report(ctx, checks)
		report.Source = &source
	}
	return report, report.Target != nil || report.Source != nil
}
