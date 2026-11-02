package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ldesfontaine/opencloud/internal/agentwire"
	"github.com/ldesfontaine/opencloud/internal/fsx"
	"github.com/ldesfontaine/opencloud/internal/restic"
)

// La rétention restic, appliquée par la cible chez elle : restic forget
// --prune sur le dossier du dépôt, avec le mot de passe du dépôt reçu dans le
// secret de l'ordre, écrit le temps de la commande puis effacé. La cible
// tient un plancher (retention-floor.json) : elle refuse ce qui garderait
// moins, quoi que le serveur demande. L'abaisser demande d'effacer ce
// fichier sur la machine.
const (
	// Une purge tient sous MaxRunDuration (une heure, côté serveur).
	resticRetainTimeout = 55 * time.Minute
	maxFloorFileBytes   = 1 << 10
	// restic tient un verrou pour périmé au bout de trente minutes sans
	// rafraîchissement : un verrou relayé plus vieux ne rend plus le dépôt
	// occupé.
	relayLockStale = 30 * time.Minute
)

// resticRetainJob est un ordre restic.retain jugé.
type resticRetainJob struct {
	params   agentwire.ResticRetainParams
	password string
	binary   string
	// repository : le dossier du dépôt, sous celui du droit restic.target.
	repository string
}

// acceptResticRetain juge la rétention vite (forme, secret, restic présent,
// plancher, dépôt libre), puis l'accepte : la purge est le travail du worker.
func (r *orderRunner) acceptResticRetain(body agentwire.OrderBody) (agentwire.ActionResult, func(ctx context.Context)) {
	dir, _ := resticTargetDir(r.identity.Capabilities())
	job, refused := r.restic.target.judgeRetain(body, dir, r.checks)
	if refused != nil {
		r.logger.Warn("order not accepted", "run_id", body.RunID, "kind", string(body.Kind), "reason", refused.Reason)
		return *refused, nil
	}
	return accepted(body), func(ctx context.Context) {
		output, err := r.restic.target.retain(ctx, job)
		r.results.push(r.finishRestic(body, output, err))
	}
}

// judgeRetain lit l'ordre et le refuse sans rien lancer s'il ne passe pas.
func (t *resticTarget) judgeRetain(body agentwire.OrderBody, dir string, checks hostChecks) (resticRetainJob, *agentwire.ActionResult) {
	refused := func(state agentwire.ActionState, reason string) *agentwire.ActionResult {
		return &agentwire.ActionResult{RunID: body.RunID, State: state, Reason: reason}
	}
	var params agentwire.ResticRetainParams
	if err := json.Unmarshal(body.Params, &params); err != nil {
		return resticRetainJob{}, refused(agentwire.ActionFailed, agentwire.ReasonBadOrder)
	}
	if err := params.Validate(); err != nil {
		if errors.Is(err, agentwire.ErrResticRetentionInvalid) {
			return resticRetainJob{}, refused(agentwire.ActionRefused, agentwire.ReasonRetentionInvalid)
		}
		return resticRetainJob{}, refused(agentwire.ActionFailed, agentwire.ReasonBadOrder)
	}
	var secret agentwire.ResticRetainSecret
	if err := json.Unmarshal(body.Secret, &secret); err != nil || secret.Validate() != nil {
		return resticRetainJob{}, refused(agentwire.ActionFailed, agentwire.ReasonBadOrder)
	}
	binary, err := checks.findRestic()
	if err != nil {
		return resticRetainJob{}, refused(agentwire.ActionRefused, agentwire.ReasonResticMissing)
	}
	if reason := t.retainFloorReason(params); reason != "" {
		return resticRetainJob{}, refused(agentwire.ActionRefused, reason)
	}
	if t.isRepoBusy(params.User) {
		return resticRetainJob{}, refused(agentwire.ActionRefused, agentwire.ReasonBusy)
	}
	repository := filepath.Join(dir, params.User)
	return resticRetainJob{params: params, password: secret.RepoPassword, binary: binary, repository: repository}, nil
}

// retainFloorReason juge une rétention contre le plancher de la cible ; sans
// fichier, le plancher par défaut. Un plancher illisible ne laisse rien
// passer.
func (t *resticTarget) retainFloorReason(params agentwire.ResticRetainParams) string {
	floor, err := t.readRetentionFloor()
	if errors.Is(err, fs.ErrNotExist) {
		floor, err = agentwire.DefaultResticRetentionFloor(), nil
	}
	if err != nil {
		t.logger.Warn("restic retention floor unreadable", "error", err)
		return agentwire.ReasonPathUnwritable
	}
	if !floor.Admits(params) {
		return agentwire.ReasonRetentionBelowFloor
	}
	return ""
}

// retain lance restic forget --prune. Le mot de passe du dépôt vit dans un
// fichier 0600 le temps de la commande, jamais dans un argument ni une
// variable ; il est effacé quoi qu'il arrive, et masqué dans la sortie.
func (t *resticTarget) retain(ctx context.Context, job resticRetainJob) ([]agentwire.OutputLine, error) {
	user := job.params.User
	if !t.claimMaintenance(user) {
		return nil, refuseWith(agentwire.ReasonBusy, errors.New("the repository is busy"))
	}
	defer t.releaseMaintenance(user)
	ctx, cancel := context.WithTimeout(ctx, resticRetainTimeout)
	defer cancel()
	passwordFile, remove, err := t.writeRepoPassword("retain-", user, job.password)
	if err != nil {
		return nil, failWith(agentwire.ReasonPathUnwritable, err)
	}
	defer remove()
	command, err := restic.ForgetRepository(job.binary, job.repository, passwordFile, job.params)
	if err != nil {
		return nil, failWith(agentwire.ReasonBadOrder, err)
	}
	tail := newLineTail(resticTailLines)
	code, err := t.runner.run(ctx, command, tail.writer("stdout"), tail.writer("stderr"))
	tail.flush()
	output := append([]agentwire.OutputLine{resticOutput("forget --prune " + user + " " + keptCounts(job.params))},
		redactLines(tail.lines(), []string{job.password}, redactedSecret)...)
	if err != nil {
		return output, failWith(agentwire.ReasonResticFailed, fmt.Errorf("run restic forget: %w", err))
	}
	if code != 0 {
		return output, failWith(agentwire.ReasonResticFailed, fmt.Errorf("restic forget exited with code %d", code))
	}
	return output, nil
}

// keptCounts dit ce que la rétention garde, pour la sortie de l'ordre.
func keptCounts(params agentwire.ResticRetainParams) string {
	return fmt.Sprintf("last %d daily %d weekly %d monthly %d yearly %d",
		params.KeepLast, params.KeepDaily, params.KeepWeekly, params.KeepMonthly, params.KeepYearly)
}

// writeRepoPassword écrit le mot de passe du dépôt dans l'état (0600), sous un
// nom propre à l'ordre qui s'en sert (prefix), et rend son chemin absolu, que
// restic lit, et de quoi l'effacer. Un reste d'un arrêt brutal est effacé
// d'abord.
func (t *resticTarget) writeRepoPassword(prefix, user, password string) (string, func(), error) {
	resticRoot, err := openResticDir(t.stateDir)
	if err != nil {
		return "", nil, err
	}
	name := prefix + user + ".password"
	if err := resticRoot.Remove(name); err != nil && !errors.Is(err, fs.ErrNotExist) {
		_ = resticRoot.Close() // on rend déjà une erreur plus grave : celle de fermeture est sans suite
		return "", nil, fmt.Errorf("remove leftover password file: %w", err)
	}
	if err := fsx.CreateExclusive(resticRoot, name, []byte(password), resticFileMode); err != nil {
		_ = resticRoot.Close() // on rend déjà une erreur plus grave : celle de fermeture est sans suite
		return "", nil, fmt.Errorf("write password file: %w", err)
	}
	remove := func() {
		if err := resticRoot.Remove(name); err != nil {
			t.logger.Error("remove restic password file", "error", err)
		}
		_ = resticRoot.Close() // on rend déjà une erreur plus grave : celle de fermeture est sans suite
	}
	absolute, err := absoluteStatePath(t.stateDir, resticDir, name)
	if err != nil {
		remove()
		return "", nil, err
	}
	return absolute, remove, nil
}

// claimMaintenance réserve un dépôt pour une rétention ou une vérification :
// jamais deux à la fois, jamais pendant une sauvegarde relayée.
func (t *resticTarget) claimMaintenance(user string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.retaining[user] || t.isRelayLockedLocked(user) {
		return false
	}
	t.retaining[user] = true
	return true
}

func (t *resticTarget) releaseMaintenance(user string) {
	t.mu.Lock()
	delete(t.retaining, user)
	t.mu.Unlock()
}

// isRepoBusy : une rétention tourne déjà sur ce dépôt, ou une sauvegarde
// relayée y tient un verrou. Une sauvegarde directe, la cible ne la voit
// pas : le serveur sérialise, et restic refuse son verrou exclusif.
func (t *resticTarget) isRepoBusy(user string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.retaining[user] || t.isRelayLockedLocked(user)
}

func (t *resticTarget) isRelayLockedLocked(user string) bool {
	now := t.now()
	for name, postedAt := range t.relayLocks[user] {
		if now.Sub(postedAt) < relayLockStale {
			return true
		}
		delete(t.relayLocks[user], name)
	}
	return false
}

// noteRelayed retient les verrous que restic pose et retire par le relais :
// c'est ce qui dit à la cible qu'une sauvegarde relayée est en cours. Une
// requête que Allow refuserait ne compte pas.
func (t *resticTarget) noteRelayed(request agentwire.ResticRelayRequest) {
	name, isLock := strings.CutPrefix(request.Path, "/locks/")
	if !isLock || restic.Allow(request.Method, request.Path, request.Query) != nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	locks := t.relayLocks[request.Repo]
	if locks == nil {
		locks = map[string]time.Time{}
		t.relayLocks[request.Repo] = locks
	}
	switch request.Method {
	case http.MethodPost:
		locks[name] = t.now()
	case http.MethodDelete:
		delete(locks, name)
	}
}

// readRetentionFloor relit le plancher posé ; fs.ErrNotExist sans fichier.
func (t *resticTarget) readRetentionFloor() (agentwire.ResticRetentionFloor, error) {
	resticRoot, err := openResticDir(t.stateDir)
	if err != nil {
		return agentwire.ResticRetentionFloor{}, err
	}
	defer resticRoot.Close()
	return readRetentionFloorFrom(resticRoot)
}

func readRetentionFloorFrom(resticRoot *os.Root) (agentwire.ResticRetentionFloor, error) {
	content, err := fsx.ReadPrivate(resticRoot, retentionFloorFile, maxFloorFileBytes)
	if err != nil {
		return agentwire.ResticRetentionFloor{}, err
	}
	var floor agentwire.ResticRetentionFloor
	if err := json.Unmarshal(content, &floor); err != nil {
		return agentwire.ResticRetentionFloor{}, fmt.Errorf("parse retention floor: %w", err)
	}
	if err := floor.Validate(); err != nil {
		return agentwire.ResticRetentionFloor{}, fmt.Errorf("retention floor: %w", err)
	}
	return floor, nil
}

// nextRetentionFloor rend le plancher que restic.target.setup pose : celui
// demandé, sinon celui déjà posé, sinon celui par défaut. Un plancher qui en
// abaisserait un déjà posé est refusé ; un plancher illisible aussi, faute de
// pouvoir juger.
func nextRetentionFloor(resticRoot *os.Root, requested *agentwire.ResticRetentionFloor) (agentwire.ResticRetentionFloor, error) {
	current, err := readRetentionFloorFrom(resticRoot)
	hasCurrent := err == nil
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return agentwire.ResticRetentionFloor{}, refuseWith(agentwire.ReasonPathUnwritable, err)
	}
	switch {
	case requested == nil && hasCurrent:
		return current, nil
	case requested == nil:
		return agentwire.DefaultResticRetentionFloor(), nil
	case hasCurrent && requested.IsLowerThan(current):
		return agentwire.ResticRetentionFloor{}, refuseWith(agentwire.ReasonRetentionBelowFloor, errors.New("the retention floor cannot be lowered by an order"))
	}
	return *requested, nil
}
