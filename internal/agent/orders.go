package agent

import (
	"crypto/ed25519"
	"encoding/json"
	"log/slog"
	"os"
	"sync"
	"time"

	"github.com/ldesfontaine/opencloud/internal/agentwire"
)

// orderRunner juge chaque ordre reçu, exécute ceux qui passent et garde
// le résultat pour le signal suivant. Il vit avec l'agent, pas avec le
// flux : le rang du dernier ordre exécuté se garde dans identity.json, et
// un redémarrage ne rouvre pas la porte d'un rejeu.
//
// Un ordre passe si, dans cet ordre : l'agent a une clé du serveur ; la
// signature est de cette clé ; le corps se lit ; il vise cette machine et
// cet agent ; il n'est pas périmé ; son rang dépasse le dernier exécuté ;
// son genre est connu. Sinon il est refusé, et le refus est rapporté.
type orderRunner struct {
	stateDir *os.Root
	version  string
	results  *actionResults
	logger   *slog.Logger
	now      func() time.Time

	mu       sync.Mutex
	identity Identity
}

func newOrderRunner(stateDir *os.Root, identity Identity, version string, results *actionResults, logger *slog.Logger) *orderRunner {
	return &orderRunner{stateDir: stateDir, identity: identity, version: version, results: results, logger: logger, now: time.Now}
}

// receive juge et exécute un ordre, un à la fois : deux ordres arrivés
// ensemble ne se disputent pas le rang.
func (r *orderRunner) receive(data []byte) {
	r.mu.Lock()
	defer r.mu.Unlock()
	signed, err := agentwire.DecodeSignedOrder(data)
	if err != nil {
		// Sans enveloppe lisible, pas de run à qui répondre.
		r.logger.Warn("order refused", "reason", agentwire.ReasonBadOrder, "error", err)
		return
	}
	body, reason := r.judge(signed)
	if reason != "" {
		r.refuse(runIDOf(signed.Body), reason)
		return
	}
	r.results.push(r.execute(body))
}

// judge rend le corps d'un ordre qui passe, ou la raison de son refus. Le
// rang est écrit sur disque avant de rendre la main : un ordre exécuté ne
// se rejoue plus, même après un redémarrage.
func (r *orderRunner) judge(signed agentwire.SignedOrder) (agentwire.OrderBody, string) {
	if len(r.identity.ServerKey) != ed25519.PublicKeySize {
		return agentwire.OrderBody{}, agentwire.ReasonServerKeyMissing
	}
	if !ed25519.Verify(r.identity.ServerKey, agentwire.OrderSigningBytes(signed.Body), signed.Signature) {
		return agentwire.OrderBody{}, agentwire.ReasonBadSignature
	}
	var body agentwire.OrderBody
	if err := json.Unmarshal(signed.Body, &body); err != nil || body.RunID == "" {
		return agentwire.OrderBody{}, agentwire.ReasonBadOrder
	}
	switch {
	case body.MachineID != r.identity.MachineID:
		return body, agentwire.ReasonWrongMachine
	case body.AgentID != r.identity.AgentID:
		return body, agentwire.ReasonWrongAgent
	case !r.now().Before(time.Unix(body.NotAfter, 0)):
		return body, agentwire.ReasonExpired
	case body.Seq <= r.identity.LastOrderSeq:
		return body, agentwire.ReasonReplayed
	case !body.Kind.IsOrder():
		return body, agentwire.ReasonUnknownKind
	}
	if !r.keepSeq(body.Seq) {
		return body, agentwire.ReasonStateUnwritable
	}
	return body, ""
}

// keepSeq écrit le nouveau rang ; un échec d'écriture laisse l'ancien en
// mémoire comme sur disque.
func (r *orderRunner) keepSeq(seq int64) bool {
	updated := r.identity
	updated.LastOrderSeq = seq
	if err := SaveIdentity(r.stateDir, updated); err != nil {
		r.logger.Error("keep order seq", "seq", seq, "error", err)
		return false
	}
	r.identity = updated
	return true
}

// execute fait ce que l'ordre demande. agent.ping ne change rien : il dit
// que l'ordre est arrivé, vérifié, et quel agent l'a pris.
func (r *orderRunner) execute(body agentwire.OrderBody) agentwire.ActionResult {
	r.logger.Info("order executed", "run_id", body.RunID, "kind", string(body.Kind), "seq", body.Seq)
	return agentwire.ActionResult{
		RunID: body.RunID, State: agentwire.ActionSucceeded,
		Output: []agentwire.OutputLine{{Stream: "stdout", Line: "pong " + r.version}},
	}
}

func (r *orderRunner) refuse(runID, reason string) {
	r.logger.Warn("order refused", "run_id", runID, "reason", reason)
	if runID == "" {
		return
	}
	r.results.push(agentwire.ActionResult{RunID: runID, State: agentwire.ActionRefused, Reason: reason})
}

// runIDOf lit l'identifiant d'un corps qui n'a pas passé le jugement : le
// refus se rapporte à ce run. Un faux serveur ne gagne rien à le choisir :
// le rapport repart vers le serveur qui tient le flux.
func runIDOf(body []byte) string {
	var unverified struct {
		RunID string `json:"run_id"`
	}
	if json.Unmarshal(body, &unverified) != nil || len(unverified.RunID) > maxRunIDBytes {
		return ""
	}
	return unverified.RunID
}

// Un identifiant de run plus long n'en est pas un : le serveur refuserait
// le rapport entier.
const maxRunIDBytes = 64

// actionResults garde les résultats entre deux signaux et réveille la
// boucle du flux : la réponse à un ordre n'attend pas les 30 s du signal.
type actionResults struct {
	mu      sync.Mutex
	pending []agentwire.ActionResult
	ready   chan struct{}
}

func newActionResults() *actionResults {
	return &actionResults{ready: make(chan struct{}, 1)}
}

func (a *actionResults) push(result agentwire.ActionResult) {
	a.mu.Lock()
	a.pending = appendBounded(a.pending, []agentwire.ActionResult{result}, agentwire.MaxActionResults)
	a.mu.Unlock()
	select {
	case a.ready <- struct{}{}:
	default:
	}
}

// take vide le tampon ; nil s'il n'y a rien à dire.
func (a *actionResults) take() *agentwire.ActionReport {
	a.mu.Lock()
	defer a.mu.Unlock()
	if len(a.pending) == 0 {
		return nil
	}
	taken := agentwire.ActionReport{Results: a.pending}
	a.pending = nil
	return &taken
}

// restore remet devant ce qu'un signal n'a pas pu livrer.
func (a *actionResults) restore(report *agentwire.ActionReport) {
	if report == nil {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.pending = appendBounded(report.Results, a.pending, agentwire.MaxActionResults)
}
