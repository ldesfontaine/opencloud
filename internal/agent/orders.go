package agent

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"log/slog"
	"os"
	"sync"
	"time"

	"github.com/ldesfontaine/opencloud/internal/action"
	"github.com/ldesfontaine/opencloud/internal/agentwire"
	"github.com/ldesfontaine/opencloud/internal/anchor"
	"github.com/ldesfontaine/opencloud/internal/dockerapi"
	"github.com/ldesfontaine/opencloud/internal/machine"
)

// orderRunner juge chaque ordre reçu, exécute ceux qui passent et garde
// le résultat pour le signal suivant. Il vit avec l'agent, pas avec le
// flux : le rang du dernier ordre exécuté se garde dans identity.json, et
// un redémarrage ne rouvre pas la porte d'un rejeu.
//
// Un ordre passe si, dans cet ordre : l'agent a une clé du serveur ; la
// signature est de cette clé ; le corps se lit ; il vise cette machine et
// cet agent ; il n'est pas périmé ; son rang dépasse le dernier exécuté ;
// son genre est connu ; le fichier de l'agent porte le droit qu'il exige
// (action.RightOf) ; ses paramètres sont dans les ancres de confiance locales
// que le registre de internal/anchor lui applique. Sinon il est refusé, et le
// refus est rapporté. Les
// commandes d'observation ne passent pas ici : elles exigent observe, que le
// fichier porte toujours (machine.NormalizeCapabilities l'ajoute).
type orderRunner struct {
	stateDir *os.Root
	version  string
	results  *actionResults
	logger   *slog.Logger
	// journal écrit les lignes locales des ordres (journal.go).
	journal *orderJournal
	now     func() time.Time
	// Les ordres longs, que le juge accepte puis confie à worker ; fetch
	// leur donne les colis du serveur.
	worker *orderWorker
	fetch  *fetcher
	// La mise à jour de l'agent (agent.update) : la clé de version et la
	// relance viennent de Run.
	updater *selfUpdater
	// Docker et le disque, que les déploiements touchent ; les tests les
	// remplacent.
	docker dockerTools
	// Le dossier produce/ de l'état et la table des producteurs, que backup.produce
	// touche ; les tests y mettent les leurs.
	produce produceTools
	// Le jeton local et l'API de Proxmox, que les ordres proxmox.vm.* touchent.
	proxmox proxmoxTools
	// La cible et la source restic, que les ordres restic.* touchent.
	restic *resticTools
	// Ce que la machine répond au compte de l'agent : Docker, les dossiers, son
	// propre dossier bin/. Les mêmes questions que le rapport des droits
	// indisponibles ; un ordre est refusé, avant d'écrire, sur la même raison.
	checks hostChecks

	mu       sync.Mutex
	identity Identity
}

func newOrderRunner(stateDir *os.Root, identity Identity, version, dockerSocket string, results *actionResults, logger *slog.Logger) *orderRunner {
	updater := newSelfUpdater(stateDir, version, logger)
	checks := newHostChecks(dockerSocket, updater)
	checks.proxmoxToken = func() string {
		_, reason := readProxmoxToken(stateDir)
		return reason
	}
	if dockerSocket == "" {
		dockerSocket = dockerapi.DefaultSocket
	}
	runner := &orderRunner{
		stateDir: stateDir, identity: identity, version: version, results: results, logger: logger, now: time.Now,
		worker: newOrderWorker(), fetch: &fetcher{}, updater: updater, docker: newDockerTools(checks, stateDir, dockerSocket), checks: checks,
		produce: newProduceTools(stateDir), proxmox: newProxmoxTools(stateDir), restic: newResticTools(stateDir, logger),
	}
	runner.journal = newOrderJournal(func() *slog.Logger { return runner.logger })
	runner.restic.source.inventory = dockerapi.New(dockerSocket)
	return runner
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
		r.refuse(signed, body, reason)
		return
	}
	r.journal.received(body, "")
	result, job := r.execute(body)
	// « accepté » part avant que le worker ne puisse finir : la réponse
	// finale ne le précède jamais.
	r.results.push(result)
	if job == nil {
		// Un ordre fait tout de suite n'a plus de ligne à dire.
		r.journal.forget(body.RunID)
		return
	}
	if !r.worker.submit(job) {
		r.journal.refused(body, agentwire.ReasonBusy)
		r.results.push(agentwire.ActionResult{RunID: body.RunID, State: agentwire.ActionFailed, Reason: agentwire.ReasonBusy})
	}
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
	r.journal.remember(body.RunID, factsOf(signed.Body, r.identity.ServerKey))
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
	right, known := action.RightOf(body.Kind)
	if !known || !machine.HasCapability(r.identity.Capabilities(), right) {
		return body, agentwire.RightMissing(right)
	}
	// Les ancres de confiance locales : le registre dit quels paramètres de
	// quels ordres elles bornent. Relues du disque à chaque ordre, pour
	// qu'une ancre posée par « agent trust » vaille tout de suite.
	if finding := anchor.Judge(r.reloadAnchors(), string(body.Kind), body.Params); finding != nil {
		return body, agentwire.AnchorRefusal(*finding)
	}
	if !r.keepSeq(body.Seq) {
		return body, agentwire.ReasonStateUnwritable
	}
	return body, ""
}

// reloadAnchors relit les ancres du fichier d'identité et rend ce que l'agent
// en applique. Un fichier qui ne se lit pas garde les ancres connues : ce que
// l'agent a appliqué jusque-là ne s'élargit jamais sur une erreur de lecture.
// Appelé sous le verrou de l'agent.
func (r *orderRunner) reloadAnchors() anchor.State {
	// Sans dossier d'état (les bancs de la boucle du flux), l'identité en mémoire fait foi.
	if r.stateDir == nil {
		return r.identity.AnchorState()
	}
	if stored, err := LoadIdentity(r.stateDir); err == nil {
		r.identity.Anchors = stored.Anchors
	} else {
		r.logger.Warn("reload anchors", "error", err)
	}
	return r.identity.AnchorState()
}

// currentAnchors rend les ancres du fichier, pour l'ouverture du flux et pour
// savoir si une pose locale demande de la rouvrir.
func (r *orderRunner) currentAnchors() anchor.State {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.reloadAnchors()
}

// keepSeq écrit le nouveau rang ; un échec d'écriture laisse l'ancien en
// mémoire comme sur disque.
func (r *orderRunner) keepSeq(seq int64) bool {
	updated := r.identity
	updated.LastOrderSeq = seq
	saved, err := saveKeepingAnchors(r.stateDir, updated)
	if err != nil {
		r.logger.Error("keep order seq", "seq", seq, "error", err)
		return false
	}
	r.identity = saved
	return true
}

// execute fait ce que l'ordre demande. agent.ping ne change rien : il dit
// que l'ordre est arrivé, vérifié, et quel agent l'a pris. agent.server_key
// épingle la clé neuve du serveur. Un ordre long (backup.receive,
// backup.prune, agent.update) est jugé ici, vite, puis accepté : le travail
// est rendu à part, que le worker fera à son tour. Un genre dont l'exécution
// n'est pas écrite est refusé, même avec son droit.
//
// Le secret de l'ordre (body.Secret) est signé avec lui ; seul l'exécuteur du
// genre qui en a besoin le lit, et rien ne l'écrit.
func (r *orderRunner) execute(body agentwire.OrderBody) (agentwire.ActionResult, func(ctx context.Context)) {
	switch body.Kind {
	case agentwire.KindAgentPing:
		r.journal.executed(body)
		return agentwire.ActionResult{
			RunID: body.RunID, State: agentwire.ActionSucceeded,
			Output: []agentwire.OutputLine{{Stream: "stdout", Line: "pong " + r.version}},
		}, nil
	case agentwire.KindServerKey:
		return r.pinServerKey(body), nil
	case agentwire.KindBackupReceive:
		return r.acceptBackupReceive(body)
	case agentwire.KindBackupPrune:
		return r.acceptBackupPrune(body)
	case agentwire.KindBackupProduce:
		return r.acceptBackupProduce(body)
	case agentwire.KindAgentUpdate:
		return r.acceptAgentUpdate(body)
	case agentwire.KindAgentRollback:
		return r.acceptAgentRollback(body)
	case agentwire.KindProxmoxStart, agentwire.KindProxmoxShutdown, agentwire.KindProxmoxSnapshot, agentwire.KindProxmoxSnapshotDelete:
		return r.acceptProxmox(body)
	case agentwire.KindDockerDeploy:
		return r.acceptDocker(body, false)
	case agentwire.KindDockerUpdate:
		return r.acceptDocker(body, true)
	case agentwire.KindDockerRollback:
		return r.requestServiceRollback(body), nil
	case agentwire.KindResticTargetSetup:
		return r.resticTargetSetup(body), nil
	case agentwire.KindResticUserAdd, agentwire.KindResticUserRemove:
		return r.resticUser(body), nil
	case agentwire.KindResticProvision:
		return r.resticProvision(body), nil
	case agentwire.KindResticBackup:
		return r.acceptResticBackup(body)
	case agentwire.KindResticRetain:
		return r.acceptResticRetain(body)
	case agentwire.KindResticVerify:
		return r.acceptResticVerify(body)
	case agentwire.KindResticBrowse:
		return r.acceptResticBrowse(body)
	case agentwire.KindResticRestore:
		return r.acceptResticRestore(body)
	}
	r.journal.refused(body, agentwire.ReasonNotImplemented)
	return agentwire.ActionResult{RunID: body.RunID, State: agentwire.ActionRefused, Reason: agentwire.ReasonNotImplemented}, nil
}

// coversBackupPath : le chemin est celui du droit backup.receive du fichier
// de l'agent, ou dessous. Le droit est lu ici, sous le verrou : le worker ne
// touche pas à l'identité.
func (r *orderRunner) coversBackupPath() coveringPath {
	capabilities := r.identity.Capabilities()
	return func(path string) (string, bool) {
		return machine.CoveringPath(capabilities, machine.CapabilityBackupReceive, path)
	}
}

func accepted(body agentwire.OrderBody) agentwire.ActionResult {
	return agentwire.ActionResult{RunID: body.RunID, State: agentwire.ActionAccepted}
}

func (r *orderRunner) acceptBackupReceive(body agentwire.OrderBody) (agentwire.ActionResult, func(ctx context.Context)) {
	params, granted, refused := readBackupReceive(body, r.coversBackupPath(), r.checks.writable)
	if refused != nil {
		r.journal.notAccepted(body, refused.Reason)
		return *refused, nil
	}
	return accepted(body), func(ctx context.Context) {
		output, err := receiveBackup(ctx, r.fetch, granted, params)
		r.finishLong(body, output, err)
	}
}

func (r *orderRunner) acceptBackupPrune(body agentwire.OrderBody) (agentwire.ActionResult, func(ctx context.Context)) {
	params, granted, refused := readBackupPrune(body, r.coversBackupPath(), r.checks.writable)
	if refused != nil {
		r.journal.notAccepted(body, refused.Reason)
		return *refused, nil
	}
	return accepted(body), func(context.Context) {
		output, err := pruneBackups(granted, params)
		r.finishLong(body, output, err)
	}
}

// acceptBackupProduce juge l'ordre vite (le droit nomme le producteur, l'agent
// le connaît, les paramètres sont de la bonne forme), puis l'accepte : lancer
// le producteur, emballer et téléverser est le travail du worker. Les droits
// sont lus ici, sous le verrou.
func (r *orderRunner) acceptBackupProduce(body agentwire.OrderBody) (agentwire.ActionResult, func(ctx context.Context)) {
	job, refused := readBackupProduce(body, r.identity.Capabilities(), r.produce.producers)
	if refused != nil {
		r.journal.notAccepted(body, refused.Reason)
		return *refused, nil
	}
	return accepted(body), func(ctx context.Context) {
		output, err := r.produce.produce(ctx, r.checks, r.fetch, job, r.now())
		r.finishProduce(body, output, err)
	}
}

// acceptProxmox juge un geste sur un invité vite (paramètres, jeton local),
// puis l'accepte : appeler l'API et suivre la tâche de Proxmox est le travail
// du worker. Il se juge et se rapporte comme une production.
func (r *orderRunner) acceptProxmox(body agentwire.OrderBody) (agentwire.ActionResult, func(ctx context.Context)) {
	job, refused := r.proxmox.readProxmoxOrder(body)
	if refused != nil {
		r.journal.notAccepted(body, refused.Reason)
		return *refused, nil
	}
	return accepted(body), func(ctx context.Context) {
		output, err := r.proxmox.run(ctx, job)
		r.finishProduce(body, output, err)
	}
}

// acceptDocker juge un déploiement vite, puis l'accepte : le dossier, les
// prérequis, les écritures et Compose sont le travail du worker. La racine
// des services est celle du fichier de l'agent, lue ici sous le verrou.
func (r *orderRunner) acceptDocker(body agentwire.OrderBody, update bool) (agentwire.ActionResult, func(ctx context.Context)) {
	job, refused := readDockerOrder(body, r.identity.Root(), update)
	if refused != nil {
		r.journal.notAccepted(body, refused.Reason)
		return *refused, nil
	}
	return accepted(body), func(ctx context.Context) {
		output, err := r.docker.deployService(ctx, job)
		if isAgentStopping(err) {
			// Rien à rapporter : la montée reprendra au démarrage, par son marqueur.
			r.logger.Warn("agent stopping during a service update, it will resume", "run_id", body.RunID)
			return
		}
		r.finishDocker(body, output, err)
	}
}

// requestServiceRollback transmet le veto du serveur à la fenêtre de santé
// ouverte du projet : la montée elle-même revient à la version d'avant et
// rapporte. Hors fenêtre, rien à défaire.
func (r *orderRunner) requestServiceRollback(body agentwire.OrderBody) agentwire.ActionResult {
	var params agentwire.DockerRollbackParams
	if err := json.Unmarshal(body.Params, &params); err != nil || !agentwire.IsComposeProject(params.Project) {
		r.journal.notAccepted(body, agentwire.ReasonBadOrder)
		return agentwire.ActionResult{RunID: body.RunID, State: agentwire.ActionFailed, Reason: agentwire.ReasonBadOrder}
	}
	if !r.docker.watches.veto(params.Project) {
		r.journal.notAccepted(body, agentwire.ReasonNoPendingUpdate)
		return agentwire.ActionResult{RunID: body.RunID, State: agentwire.ActionRefused, Reason: agentwire.ReasonNoPendingUpdate}
	}
	r.journal.executed(body)
	return agentwire.ActionResult{
		RunID: body.RunID, State: agentwire.ActionSucceeded,
		Output: []agentwire.OutputLine{stdout("rollback requested for " + params.Project)},
	}
}

// finishDocker rapporte la fin d'un déploiement. Le .env ne passe ici que par
// le job : ni le résultat ni le journal ne le portent.
func (r *orderRunner) finishDocker(body agentwire.OrderBody, output []agentwire.OutputLine, err error) {
	if err != nil {
		r.journal.failed(body, err)
	} else {
		r.journal.executed(body)
	}
	r.results.push(dockerResultOf(body.RunID, output, err))
}

// finishLong rapporte la fin d'un ordre long.
func (r *orderRunner) finishLong(body agentwire.OrderBody, output []agentwire.OutputLine, err error) {
	if err != nil {
		r.journal.failed(body, err)
	} else {
		r.journal.executed(body)
	}
	r.results.push(resultOf(body.RunID, output, err))
}

// finishProduce rapporte la fin d'une production. Ce que l'agent a refusé avant
// d'écrire (rien ne se lance, pas de place) est « refusé » ; un producteur ou un
// envoi qui échoue est « échoué ».
func (r *orderRunner) finishProduce(body agentwire.OrderBody, output []agentwire.OutputLine, err error) {
	if err != nil {
		r.journal.failed(body, err)
	} else {
		r.journal.executed(body)
	}
	result := resultOf(body.RunID, output, err)
	if result.State == agentwire.ActionFailed && isUntouched(err) {
		result.State = agentwire.ActionRefused
	}
	r.results.push(result)
}

// pinServerKey remplace la clé du serveur épinglée par celle que l'ordre
// livre. L'ordre a été signé par la clé épinglée jusqu'ici : c'est elle qui
// passe la main. Les ordres suivants sont vérifiés avec la neuve.
func (r *orderRunner) pinServerKey(body agentwire.OrderBody) agentwire.ActionResult {
	var params agentwire.ServerKeyParams
	if err := json.Unmarshal(body.Params, &params); err != nil {
		return agentwire.ActionResult{RunID: body.RunID, State: agentwire.ActionFailed, Reason: agentwire.ReasonBadOrder}
	}
	key, err := base64.StdEncoding.DecodeString(params.PublicKey)
	if err != nil || len(key) != ed25519.PublicKeySize {
		return agentwire.ActionResult{RunID: body.RunID, State: agentwire.ActionFailed, Reason: agentwire.ReasonBadOrder}
	}
	updated := r.identity
	updated.ServerKey = key
	saved, err := saveKeepingAnchors(r.stateDir, updated)
	if err != nil {
		r.logger.Error("pin server key", "run_id", body.RunID, "error", err)
		return agentwire.ActionResult{RunID: body.RunID, State: agentwire.ActionFailed, Reason: agentwire.ReasonStateUnwritable}
	}
	r.identity = saved
	fingerprint := agentwire.KeyFingerprint(key)
	r.logger.Info("server key pinned", "run_id", body.RunID, "server_key", fingerprint)
	return agentwire.ActionResult{
		RunID: body.RunID, State: agentwire.ActionSucceeded,
		Output: []agentwire.OutputLine{{Stream: "stdout", Line: "pinned " + fingerprint}},
	}
}

// serverKey rend la clé du serveur épinglée en ce moment : l'ouverture du
// flux la redit, et une rotation l'a peut-être changée depuis le démarrage.
func (r *orderRunner) serverKey() []byte {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.identity.ServerKey
}

// refuse dit le refus d'un ordre : au journal local, avec ce qu'on en sait
// (les empreintes du corps signé, les paramètres si le corps se lit), et au
// serveur s'il y a un run à qui répondre.
func (r *orderRunner) refuse(signed agentwire.SignedOrder, body agentwire.OrderBody, reason string) {
	runID := body.RunID
	if runID != "" {
		r.journal.received(body, reason)
	} else {
		runID = runIDOf(signed.Body)
		r.logger.Warn("order refused", "run_id", runID, "reason", reason, "body_sha256", factsOf(signed.Body, nil).bodySHA256)
	}
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
