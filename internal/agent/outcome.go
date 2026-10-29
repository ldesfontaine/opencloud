package agent

import (
	"errors"
	"log/slog"
	"os"
	"strconv"
	"sync"

	"github.com/ldesfontaine/opencloud/internal/agentwire"
	"github.com/ldesfontaine/opencloud/internal/brand"
	"github.com/ldesfontaine/opencloud/internal/rollback"
)

// updateOutcome suit le marqueur d'une mise à jour du côté de l'agent : il la
// confirme quand le serveur a acquitté la nouvelle version, et rapporte au
// serveur ce que le lanceur a fait en son absence (retour à la version
// d'avant, échec de la remise en état).
type updateOutcome struct {
	stateDir *os.Root
	version  string
	results  *actionResults
	logger   *slog.Logger
	// Sous mu : le marqueur lu et écrit par le flux et par la boucle du signal.
	mu sync.Mutex
}

func newUpdateOutcome(stateDir *os.Root, version string, results *actionResults, logger *slog.Logger) *updateOutcome {
	return &updateOutcome{stateDir: stateDir, version: version, results: results, logger: logger}
}

// guardVersion lit ce que le lanceur dit de lui dans l'environnement ; 0 si
// l'agent ne tourne pas sous lui, ou sous un lanceur que ce binaire ne
// connaît pas. C'est ce qui permet de rapporter la capacité de retour arrière.
func guardVersion(environment string) int {
	version, err := strconv.Atoi(environment)
	if err != nil || version < 1 {
		return 0
	}
	return min(version, agentwire.RollbackVersion)
}

// GuardVersionFromEnv rend la version du lanceur sous lequel l'agent tourne.
func GuardVersionFromEnv() int {
	return guardVersion(os.Getenv(brand.AgentGuardEnv))
}

// load rend le marqueur, ou rien : un marqueur illisible est dit au journal et
// retiré, il ne bloque jamais l'agent.
func (o *updateOutcome) load() (rollback.Marker, bool) {
	marker, err := rollback.Load(o.stateDir)
	switch {
	case err == nil:
		return marker, true
	case errors.Is(err, rollback.ErrNoMarker):
		return rollback.Marker{}, false
	}
	o.logger.Error("update marker unreadable, removed", "error", err)
	if removeErr := rollback.Remove(o.stateDir); removeErr != nil {
		o.logger.Error("remove update marker", "error", removeErr)
	}
	return rollback.Marker{}, false
}

// announce, au démarrage, dit au journal et prépare pour le serveur ce que le
// lanceur a fait : l'ancienne version est revenue, ou la remise en état a
// échoué. Le marqueur reste jusqu'à la livraison du rapport.
func (o *updateOutcome) announce() {
	if o == nil {
		return
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	marker, found := o.load()
	if !found {
		return
	}
	switch marker.State {
	case rollback.StateRolledBack:
		o.logger.Error("the update was refused: back on the previous version",
			"run_id", marker.RunID, "target", marker.Target, "version", o.version, "cause", string(marker.Cause))
	case rollback.StateFailed:
		o.logger.Error("the update failed and so did the return to the previous version: this machine needs a hand",
			"run_id", marker.RunID, "target", marker.Target, "previous", marker.Previous, "cause", string(marker.Cause))
	case rollback.StateConfirmed:
	case rollback.StatePending:
		o.logger.Info("update waiting for the server's acknowledgement", "run_id", marker.RunID, "target", marker.Target)
		return
	}
	o.results.push(o.report(marker))
}

// confirm, une fois le flux ouvert (le serveur a vu la version et l'a
// acquittée), clôt la mise à jour en attente si c'est bien la version visée
// qui tourne. Une mise à jour se ferme du côté du serveur à ce retour même ;
// un retour arrière demandé n'a pas de version à attendre côté serveur, il le
// dit lui-même.
func (o *updateOutcome) confirm() {
	if o == nil {
		return
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	marker, found := o.load()
	if !found || marker.State != rollback.StatePending || marker.Target != o.version {
		return
	}
	if marker.Kind == rollback.KindUpdate {
		if err := rollback.Remove(o.stateDir); err != nil {
			o.logger.Error("confirm update", "run_id", marker.RunID, "error", err)
			return
		}
		o.logger.Info("update confirmed", "run_id", marker.RunID, "version", o.version)
		return
	}
	marker.State = rollback.StateConfirmed
	if err := rollback.Save(o.stateDir, marker); err != nil {
		o.logger.Error("confirm rollback", "run_id", marker.RunID, "error", err)
		return
	}
	o.logger.Info("rollback confirmed", "run_id", marker.RunID, "version", o.version)
	o.results.push(o.report(marker))
}

// delivered retire le marqueur quand le signal qui portait son rapport a été
// écrit par le serveur : avant, une coupure le ferait rejouer.
func (o *updateOutcome) delivered(report *agentwire.ActionReport) {
	if o == nil {
		return
	}
	if report == nil {
		return
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	marker, found := o.load()
	if !found || marker.State == rollback.StatePending {
		return
	}
	for _, result := range report.Results {
		if result.RunID == marker.RunID {
			if err := rollback.Remove(o.stateDir); err != nil {
				o.logger.Error("remove delivered update marker", "run_id", marker.RunID, "error", err)
			}
			return
		}
	}
}

// report est la réponse que le serveur lira pour l'ordre de la mise à jour :
// échoué avec la raison pour une remise en état, fait pour un retour arrière
// confirmé. Le détail tient en lignes de sortie.
func (o *updateOutcome) report(marker rollback.Marker) agentwire.ActionResult {
	result := agentwire.ActionResult{RunID: marker.RunID, State: agentwire.ActionFailed}
	lines := []string{"target " + marker.Target, "previous " + marker.Previous, "running " + o.version}
	switch marker.State {
	case rollback.StateConfirmed:
		result.State = agentwire.ActionSucceeded
		lines = []string{"rolled back to " + marker.Target, "left " + marker.Previous}
	case rollback.StateRolledBack:
		result.Reason = agentwire.ReasonRolledBackTimeout
		if marker.Cause == rollback.CauseCrash {
			result.Reason = agentwire.ReasonRolledBackCrash
		}
	default:
		result.Reason = agentwire.ReasonRollbackFailed
		lines = append(lines, "cause "+string(marker.Cause))
	}
	for _, line := range lines {
		result.Output = append(result.Output, agentwire.OutputLine{Stream: "stdout", Line: line})
	}
	return result
}
