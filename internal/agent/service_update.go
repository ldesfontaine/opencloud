package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/ldesfontaine/opencloud/internal/agentwire"
	"github.com/ldesfontaine/opencloud/internal/dockerapi"
	"github.com/ldesfontaine/opencloud/internal/fsx"
)

// Le filet d'une montée de version : l'agent note avant d'écrire, juge la
// santé du service lui-même, revient seul à la version d'avant si elle ne
// vient pas, et ne le tente qu'une fois. Le serveur n'est pas nécessaire pour
// revenir (comme le retour arrière de l'agent, internal/rollback).

const (
	previousComposeName = "compose.previous.yml"
	previousEnvName     = ".env.previous"
	markerPrefix        = "service-update-"
	markerSuffix        = ".json"
	maxMarkerBytes      = 16 << 10
	markerMode          = 0o600
	// Le rythme auquel la santé se relit pendant la fenêtre.
	healthPollInterval = 5 * time.Second
	// Les lignes lues au journal de chaque conteneur en défaut, avant de
	// garder les dernières de l'ensemble.
	journalTailPerContainer = agentwire.MaxJournalLines
)

// dockerEngine est ce que le filet lit de Docker : les conteneurs d'un projet
// et leur journal. Les tests le remplacent.
type dockerEngine interface {
	ListContainers(ctx context.Context) ([]dockerapi.Summary, error)
	Inspect(ctx context.Context, id string) (dockerapi.Container, error)
	// Tail rend les dernières lignes du journal d'un conteneur.
	Tail(ctx context.Context, id string, lines int) ([]string, error)
}

// engineClient donne à l'API Docker la lecture du journal que le filet veut.
type engineClient struct {
	*dockerapi.Client
}

func (e engineClient) Tail(ctx context.Context, id string, lines int) ([]string, error) {
	reader, err := e.Logs(ctx, id, dockerapi.LogsOptions{Tail: lines})
	if err != nil {
		return nil, err
	}
	defer reader.Close()
	var tail []string
	for {
		line, err := reader.Next()
		if line.Text != "" {
			tail = append(tail, line.Text)
		}
		if err != nil {
			// io.EOF ou une coupure du flux : ce qui a été lu suffit.
			return tail, nil
		}
	}
}

// updatePhase dit où en est la montée que le marqueur suit.
type updatePhase string

const (
	// La nouvelle version tourne, sa santé est jugée.
	phaseWatching updatePhase = "watching"
	// La version d'avant est remise, sa santé est jugée à son tour.
	phaseRestoring updatePhase = "restoring"
)

// Ce que le retour fait de l'.env : rien, le remettre depuis la copie, ou le
// retirer parce que la montée l'a créé.
const (
	envUntouched = ""
	envRestore   = "restore"
	envRemove    = "remove"
)

// updateMarker est écrit dans l'état de l'agent AVANT la première écriture du
// dossier du service.
type updateMarker struct {
	RunID    string                    `json:"run_id"`
	Project  string                    `json:"project"`
	Phase    updatePhase               `json:"phase"`
	Deadline time.Time                 `json:"deadline"`
	Safety   agentwire.ServiceRollback `json:"safety"`
	Env      string                    `json:"env,omitempty"`
	// Les redémarrages déjà comptés par conteneur avant la montée : seuls
	// ceux d'après sont un défaut.
	Restarts map[string]int `json:"restarts,omitempty"`
}

var errMarkerCorrupt = errors.New("service update marker unreadable")

func markerNameOf(project string) string {
	return markerPrefix + project + markerSuffix
}

func (m updateMarker) validate() error {
	switch {
	case m.RunID == "" || len(m.RunID) > 64:
		return errors.New("run id")
	case !agentwire.IsComposeProject(m.Project):
		return errors.New("project")
	case m.Phase != phaseWatching && m.Phase != phaseRestoring:
		return fmt.Errorf("phase %q", m.Phase)
	case !m.Safety.IsValid():
		return errors.New("safety")
	case m.Env != envUntouched && m.Env != envRestore && m.Env != envRemove:
		return fmt.Errorf("env %q", m.Env)
	}
	return nil
}

// loadMarker rend le marqueur d'un projet : found faux s'il n'y en a pas,
// errMarkerCorrupt (enveloppé) s'il existe et ne se lit pas.
func (t dockerTools) loadMarker(project string) (marker updateMarker, found bool, err error) {
	content, err := fsx.ReadPrivate(t.stateDir, markerNameOf(project), maxMarkerBytes)
	if errors.Is(err, fs.ErrNotExist) {
		return updateMarker{}, false, nil
	}
	if err != nil {
		return updateMarker{}, false, fmt.Errorf("%w: %w", errMarkerCorrupt, err)
	}
	if len(content) > maxMarkerBytes {
		return updateMarker{}, false, fmt.Errorf("%w: more than %d bytes", errMarkerCorrupt, maxMarkerBytes)
	}
	if err := json.Unmarshal(content, &marker); err != nil {
		return updateMarker{}, false, fmt.Errorf("%w: %w", errMarkerCorrupt, err)
	}
	if err := marker.validate(); err != nil {
		return updateMarker{}, false, fmt.Errorf("%w: %w", errMarkerCorrupt, err)
	}
	return marker, true, nil
}

func (t dockerTools) saveMarker(marker updateMarker) error {
	content, err := json.Marshal(marker)
	if err != nil {
		return fmt.Errorf("encode service update marker: %w", err)
	}
	return fsx.WriteAtomic(t.stateDir, markerNameOf(marker.Project), content, markerMode)
}

func (t dockerTools) removeMarker(project string) error {
	if err := t.stateDir.Remove(markerNameOf(project)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return fsx.SyncDir(t.stateDir, ".")
}

// updateWatches tient les fenêtres de santé ouvertes : docker.rollback n'a de
// sens que pour l'une d'elles.
type updateWatches struct {
	mu   sync.Mutex
	open map[string]chan struct{}
}

func (w *updateWatches) begin(project string) <-chan struct{} {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.open == nil {
		w.open = map[string]chan struct{}{}
	}
	vetoed := make(chan struct{})
	w.open[project] = vetoed
	return vetoed
}

func (w *updateWatches) end(project string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	delete(w.open, project)
}

// veto demande le retour de la fenêtre ouverte du projet ; faux s'il n'y en a
// pas, ou si le retour est déjà demandé.
func (w *updateWatches) veto(project string) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	vetoed, isOpen := w.open[project]
	if !isOpen {
		return false
	}
	select {
	case <-vetoed:
		return false
	default:
		close(vetoed)
		return true
	}
}

// armRollback garde la version d'avant et écrit le marqueur, avant toute
// écriture du service. Ce qu'il refuse ne laisse rien de plus que des copies.
func (t dockerTools) armRollback(dir *os.Root, job dockerJob) (updateMarker, error) {
	project := job.params.Project
	if t.stateDir == nil {
		return updateMarker{}, refuseWith(agentwire.ReasonStateUnwritable, errors.New("no state dir for the update marker"))
	}
	if _, found, err := t.loadMarker(project); found || err != nil {
		return updateMarker{}, refuseWith(agentwire.ReasonBusy, fmt.Errorf("an update of %s is already being judged", project))
	}
	if err := copyRegular(dir, composeFileName, previousComposeName, composeFileMode); err != nil {
		return updateMarker{}, refuseWith(agentwire.ReasonNoPrevious, err)
	}
	marker := updateMarker{
		RunID: job.runID, Project: project, Phase: phaseWatching, Safety: *job.params.Rollback,
		Deadline: t.now().Add(time.Duration(job.params.Rollback.HealthWindowSeconds) * time.Second),
		Restarts: t.restartCounts(context.Background(), project),
	}
	if job.hasEnv {
		marker.Env = envRemove
		if _, err := dir.Lstat(envFileName); err == nil {
			if err := copyRegular(dir, envFileName, previousEnvName, envFileMode); err != nil {
				return updateMarker{}, refuseWith(agentwire.ReasonPathUnwritable, err)
			}
			marker.Env = envRestore
		}
	}
	if err := t.saveMarker(marker); err != nil {
		return updateMarker{}, refuseWith(agentwire.ReasonStateUnwritable, err)
	}
	return marker, nil
}

// copyRegular copie un fichier ordinaire du dossier du service ; un lien
// symbolique n'est jamais suivi.
func copyRegular(dir *os.Root, from, to string, mode os.FileMode) error {
	info, err := dir.Lstat(from)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Size() > agentwire.MaxComposeBytes {
		return fmt.Errorf("%s is not a regular file of a reasonable size", from)
	}
	content, err := dir.ReadFile(from)
	if err != nil {
		return err
	}
	return fsx.WriteAtomic(dir, to, content, mode)
}

// restoreFiles remet le compose.yml et l'.env d'avant la montée.
func restoreFiles(dir *os.Root, marker updateMarker) error {
	if err := copyRegular(dir, previousComposeName, composeFileName, composeFileMode); err != nil {
		return err
	}
	switch marker.Env {
	case envRestore:
		return copyRegular(dir, previousEnvName, envFileName, envFileMode)
	case envRemove:
		if err := dir.Remove(envFileName); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
	}
	return nil
}

// restartCounts lit les redémarrages de chaque conteneur du projet ; une
// lecture qui échoue rend une table vide, ce qui compte tout redémarrage.
func (t dockerTools) restartCounts(ctx context.Context, project string) map[string]int {
	containers, err := t.projectContainers(ctx, project)
	if err != nil {
		return nil
	}
	counts := map[string]int{}
	for _, container := range containers {
		counts[container.ID] = container.RestartCount
	}
	return counts
}

// projectContainers inspecte les conteneurs du projet, ceux d'un
// « docker compose run » exclus.
func (t dockerTools) projectContainers(ctx context.Context, project string) ([]dockerapi.Container, error) {
	summaries, err := t.engine.ListContainers(ctx)
	if err != nil {
		return nil, err
	}
	var containers []dockerapi.Container
	for _, summary := range summaries {
		if summary.Labels[dockerapi.LabelComposeProject] != project || summary.IsOneOff() {
			continue
		}
		container, err := t.engine.Inspect(ctx, summary.ID)
		if err != nil {
			return nil, err
		}
		containers = append(containers, container)
	}
	return containers, nil
}

type healthVerdict int

const (
	verdictWaiting healthVerdict = iota
	verdictHealthy
	verdictUnhealthy
)

// judgeContainers est la règle de santé de l'agent, la même que
// container.NeedsAttention pour ce qui est en marche (un test de parité les
// tient égales) : un conteneur qui redémarre, est mort ou est « unhealthy » va
// mal ; avec HEALTHCHECK il doit être « healthy » ; sans HEALTHCHECK il doit
// tenir en marche, sans redémarrage, pendant stable. Il rend aussi les
// conteneurs en défaut.
func judgeContainers(containers []dockerapi.Container, restarts map[string]int, now time.Time, stable time.Duration) (healthVerdict, []dockerapi.Container) {
	if len(containers) == 0 {
		return verdictWaiting, nil
	}
	var faulty []dockerapi.Container
	isWaiting := false
	for _, container := range containers {
		switch {
		case isFaulty(container, restarts):
			faulty = append(faulty, container)
		case isSettled(container, now, stable):
		default:
			isWaiting = true
		}
	}
	switch {
	case len(faulty) > 0:
		return verdictUnhealthy, faulty
	case isWaiting:
		return verdictWaiting, nil
	}
	return verdictHealthy, nil
}

func isFaulty(container dockerapi.Container, restarts map[string]int) bool {
	switch container.State.Status {
	case "restarting", "dead", "exited":
		return true
	}
	if container.State.Health != nil && container.State.Health.Status == "unhealthy" {
		return true
	}
	return !container.HasHealthcheck() && container.RestartCount > restarts[container.ID]
}

func isSettled(container dockerapi.Container, now time.Time, stable time.Duration) bool {
	if container.State.Status != "running" {
		return false
	}
	if container.HasHealthcheck() {
		return container.State.Health != nil && container.State.Health.Status == "healthy"
	}
	startedAt, err := time.Parse(time.RFC3339Nano, container.State.StartedAt)
	return err == nil && now.Sub(startedAt) >= stable
}

// watchOutcome dit comment une fenêtre de santé s'est close.
type watchOutcome int

const (
	watchHealthy watchOutcome = iota
	watchUnhealthy
	watchVetoed
)

// watchHealth relit la santé du projet jusqu'à ce qu'elle soit jugée ou que
// l'échéance passe. Un contexte annulé (l'agent s'arrête) n'est pas un verdict :
// l'erreur revient, le marqueur reste, la reprise jugera.
func (t dockerTools) watchHealth(ctx context.Context, marker updateMarker, vetoed <-chan struct{}) (watchOutcome, []dockerapi.Container, error) {
	stable := time.Duration(marker.Safety.StableSeconds) * time.Second
	var faulty []dockerapi.Container
	for {
		containers, err := t.projectContainers(ctx, marker.Project)
		if ctx.Err() != nil {
			return watchUnhealthy, nil, ctx.Err()
		}
		verdict := verdictWaiting
		if err == nil {
			verdict, faulty = judgeContainers(containers, marker.Restarts, t.now(), stable)
		}
		switch verdict {
		case verdictHealthy:
			return watchHealthy, nil, nil
		case verdictUnhealthy:
			return watchUnhealthy, faulty, nil
		}
		if !t.now().Before(marker.Deadline) {
			return watchUnhealthy, faulty, nil
		}
		select {
		case <-vetoed:
			return watchVetoed, faulty, nil
		default:
		}
		if err := t.pause(ctx, healthPollInterval); err != nil {
			return watchUnhealthy, nil, err
		}
	}
}

// settleUpdate juge la montée puis, si elle ne tient pas, revient à la
// version d'avant, une seule fois. Il sert à l'ordre qui vient de lancer
// Compose et à la reprise d'un agent redémarré pendant la fenêtre ; le marqueur
// est retiré à la fin, sauf si l'agent s'arrête avant (la reprise le relit).
func (t dockerTools) settleUpdate(ctx context.Context, dir *os.Root, root string, marker updateMarker, composeFailed bool, output []agentwire.OutputLine) ([]agentwire.OutputLine, error) {
	var journal []string
	if marker.Phase == phaseWatching {
		outcome := watchUnhealthy
		var faulty []dockerapi.Container
		if !composeFailed {
			var err error
			vetoed := t.watches.begin(marker.Project)
			outcome, faulty, err = t.watchHealth(ctx, marker, vetoed)
			t.watches.end(marker.Project)
			if err != nil {
				// L'agent s'arrête : pas de verdict, le marqueur reste.
				return output, err
			}
		}
		if outcome == watchHealthy {
			output = append(output, stdout("healthy"))
			return output, t.finishMarker(marker.Project)
		}
		output = append(output, stdout(unhealthyLine(outcome, composeFailed)))
		journal = t.journalOf(ctx, marker.Project, faulty, dir)
		marker.Phase = phaseRestoring
		marker.Deadline = t.now().Add(time.Duration(marker.Safety.HealthWindowSeconds) * time.Second)
		if err := t.saveMarker(marker); err != nil {
			return output, t.rollbackFailed(marker.Project, journal, err)
		}
	}
	restored, err := t.restoreAndUp(ctx, dir, root, marker)
	output = append(output, restored...)
	if err != nil {
		return output, t.rollbackFailed(marker.Project, journal, err)
	}
	if err := t.finishMarker(marker.Project); err != nil {
		return output, err
	}
	output = append(output, stdout("restored previous version"))
	return output, &orderError{reason: agentwire.ReasonRolledBackVersion, cause: errors.New("the new version did not become healthy"), journal: journal}
}

func unhealthyLine(outcome watchOutcome, composeFailed bool) string {
	switch {
	case composeFailed:
		return "compose failed: going back"
	case outcome == watchVetoed:
		return "rollback requested by the server: going back"
	}
	return "not healthy in time: going back"
}

// restoreAndUp remet les fichiers d'avant, relance Compose et juge la version
// revenue. Elle ne rend rien si elle va bien.
func (t dockerTools) restoreAndUp(ctx context.Context, dir *os.Root, root string, marker updateMarker) ([]agentwire.OutputLine, error) {
	if err := restoreFiles(dir, marker); err != nil {
		return nil, err
	}
	secrets := envSecrets(envOf(dir, dockerJob{}))
	lines, err := t.compose.Up(ctx, path.Join(root, marker.Project), marker.Project)
	tail := redactLines(lines[max(len(lines)-agentSummaryLines*2, 0):], secrets, redactedText)
	if err != nil {
		return tail, err
	}
	// Les conteneurs de la version d'avant sont recréés : leurs redémarrages
	// repartent de zéro.
	marker.Restarts = nil
	outcome, _, err := t.watchHealth(ctx, marker, nil)
	if err != nil {
		return tail, err
	}
	if outcome != watchHealthy {
		return tail, errors.New("the previous version is not healthy either")
	}
	return tail, nil
}

// finishMarker retire le marqueur : la montée est jugée.
func (t dockerTools) finishMarker(project string) error {
	if err := t.removeMarker(project); err != nil {
		return failWith(agentwire.ReasonStateUnwritable, err)
	}
	return nil
}

// rollbackFailed clôt la montée quand la version d'avant n'a pas pu être
// remise, ou ne tient pas non plus : on s'arrête là, jamais de seconde
// remise en état. Le marqueur est retiré, l'agent n'y reviendrait pas.
func (t dockerTools) rollbackFailed(project string, journal []string, cause error) error {
	if err := t.removeMarker(project); err != nil {
		cause = errors.Join(cause, err)
	}
	return &orderError{reason: agentwire.ReasonRollbackFailed, cause: cause, journal: journal}
}

// journalOf rend les dernières lignes de journal des conteneurs en défaut,
// les valeurs de l'.env masquées. Sans conteneur désigné (Compose a échoué),
// ceux du projet.
func (t dockerTools) journalOf(ctx context.Context, project string, faulty []dockerapi.Container, dir *os.Root) []string {
	if len(faulty) == 0 {
		faulty, _ = t.projectContainers(ctx, project)
	}
	var lines []string
	for _, container := range faulty {
		lines = append(lines, t.containerJournal(ctx, container)...)
	}
	secrets := envSecrets(envOf(dir, dockerJob{}))
	redacted := make([]string, 0, len(lines))
	for _, line := range lines {
		for _, secret := range secrets {
			line = strings.ReplaceAll(line, secret, redactedText)
		}
		redacted = append(redacted, line)
	}
	return fitJournal(redacted)
}

func (t dockerTools) containerJournal(ctx context.Context, container dockerapi.Container) []string {
	tail, err := t.engine.Tail(ctx, container.ID, journalTailPerContainer)
	if err != nil {
		return nil
	}
	var lines []string
	for _, raw := range tail {
		if text := cleanLine(raw); text != "" {
			lines = append(lines, container.ShortName()+" | "+text)
		}
	}
	return lines
}

// fitJournal garde les dernières lignes, vingt au plus et deux Kio : ce qui
// part dans une notification est court.
func fitJournal(lines []string) []string {
	lines = lines[max(len(lines)-agentwire.MaxJournalLines, 0):]
	total := 0
	for index := len(lines) - 1; index >= 0; index-- {
		total += len(lines[index]) + 1
		if total > agentwire.MaxJournalBytes {
			return lines[index+1:]
		}
	}
	return lines
}

// safeUpdate est une montée de version avec filet : garder la version
// d'avant, écrire, lancer Compose, juger.
func (t dockerTools) safeUpdate(ctx context.Context, base *os.Root, job dockerJob, summary []agentwire.OutputLine) ([]agentwire.OutputLine, error) {
	dir, err := base.OpenRoot(job.params.Project)
	if err != nil {
		return summary, failWith(agentwire.ReasonPathUnwritable, err)
	}
	defer dir.Close()
	marker, err := t.armRollback(dir, job)
	if err != nil {
		return summary, err
	}
	written, err := t.writeFiles(dir, job)
	summary = append(summary, written...)
	if err != nil {
		// Rien n'a encore tourné : la version d'avant est remise telle quelle.
		if restoreErr := restoreFiles(dir, marker); restoreErr != nil {
			return summary, t.rollbackFailed(marker.Project, nil, errors.Join(err, restoreErr))
		}
		return summary, errors.Join(err, t.removeMarker(marker.Project))
	}
	lines, upErr := t.compose.Up(ctx, path.Join(job.root, job.params.Project), job.params.Project)
	lines = redactLines(lines, envSecrets(envOf(dir, job)), redactedText)
	output := append(summary, lines[max(len(lines)-(agentwire.MaxOutputLines-len(summary)), 0):]...)
	return t.settleUpdate(ctx, dir, job.root, marker, upErr != nil, output)
}

// resumeUpdates reprend les montées qu'un redémarrage de l'agent a coupées
// pendant leur fenêtre. Un marqueur illisible est écarté bruyamment, sans
// aucune action sur le service.
func (r *orderRunner) resumeUpdates() {
	if r.stateDir == nil {
		return
	}
	entries, err := fs.ReadDir(r.stateDir.FS(), ".")
	if err != nil {
		r.logger.Error("list service update markers", "error", err)
		return
	}
	var names []string
	for _, entry := range entries {
		if name := entry.Name(); strings.HasPrefix(name, markerPrefix) && strings.HasSuffix(name, markerSuffix) {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	for _, name := range names {
		project := strings.TrimSuffix(strings.TrimPrefix(name, markerPrefix), markerSuffix)
		r.resumeUpdate(project)
	}
}

func (r *orderRunner) resumeUpdate(project string) {
	marker, found, err := r.docker.loadMarker(project)
	if err != nil || !found {
		r.logger.Error("service update marker unreadable, dropped", "project", project, "error", err)
		if removeErr := r.docker.removeMarker(project); removeErr != nil {
			r.logger.Error("remove service update marker", "project", project, "error", removeErr)
		}
		return
	}
	root := r.identity.Root()
	r.logger.Warn("resuming an interrupted service update", "project", project, "run_id", marker.RunID, "phase", string(marker.Phase))
	submitted := r.worker.submit(func(workCtx context.Context) {
		output, err := r.docker.resumeSettle(workCtx, root, marker)
		if isAgentStopping(err) {
			return
		}
		r.results.push(dockerResultOf(marker.RunID, output, err))
	})
	if !submitted {
		r.logger.Error("service update not resumed: worker busy", "project", project)
	}
}

// resumeSettle rouvre le dossier du projet et finit de juger la montée.
func (t dockerTools) resumeSettle(ctx context.Context, root string, marker updateMarker) ([]agentwire.OutputLine, error) {
	base, err := os.OpenRoot(root)
	if err != nil {
		return nil, t.rollbackFailed(marker.Project, nil, err)
	}
	defer base.Close()
	dir, err := base.OpenRoot(marker.Project)
	if err != nil {
		return nil, t.rollbackFailed(marker.Project, nil, err)
	}
	defer dir.Close()
	// La fenêtre d'avant la coupure a peut-être passé : la reprise juge ce
	// qui tourne maintenant, avec le temps qui reste.
	return t.settleUpdate(ctx, dir, root, marker, false, []agentwire.OutputLine{stdout("resumed after an agent restart")})
}

// isAgentStopping : l'erreur vient de l'arrêt de l'agent, pas du service.
func isAgentStopping(err error) bool {
	return errors.Is(err, context.Canceled)
}
