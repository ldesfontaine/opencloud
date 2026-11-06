package agent

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ldesfontaine/opencloud/internal/agentwire"
	"github.com/ldesfontaine/opencloud/internal/container"
	"github.com/ldesfontaine/opencloud/internal/dockerapi"
)

// newerCompose est le compose de la montée : celui du serveur, avec une image
// de plus.
var newerCompose = func() string {
	compose, _, err := agentwire.RenderCompose("nextcloud", "svc_1", []agentwire.ComposeUnit{
		{Name: "app", Image: "nextcloud:29.0.5", Ports: []string{"8080:80"}, DataPath: "/var/www/html"},
		{Name: "db", Image: "postgres:16.4"},
	})
	if err != nil {
		panic(err)
	}
	return string(compose)
}()

// fakeEngine joue Docker : l'état des conteneurs dépend du nombre de Up faits
// et du temps écoulé depuis le dernier.
type fakeEngine struct {
	mu     sync.Mutex
	bench  *safetyBench
	states func(ups int, sinceUp time.Duration) []dockerapi.Container
	tails  map[string][]string
}

func (e *fakeEngine) current() []dockerapi.Container {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.states(len(e.bench.compose.upDirs), e.bench.now.Sub(e.bench.upAt))
}

func (e *fakeEngine) ListContainers(context.Context) ([]dockerapi.Summary, error) {
	var summaries []dockerapi.Summary
	for _, found := range e.current() {
		summaries = append(summaries, dockerapi.Summary{
			ID: found.ID, Labels: map[string]string{dockerapi.LabelComposeProject: "nextcloud"},
		})
	}
	// Un conteneur d'un autre projet ne compte pas.
	summaries = append(summaries, dockerapi.Summary{ID: "other", Labels: map[string]string{dockerapi.LabelComposeProject: "autre"}})
	return summaries, nil
}

func (e *fakeEngine) Inspect(_ context.Context, id string) (dockerapi.Container, error) {
	for _, found := range e.current() {
		if found.ID == id {
			return found, nil
		}
	}
	return dockerapi.Container{}, errors.New("unknown container")
}

func (e *fakeEngine) Tail(_ context.Context, id string, _ int) ([]string, error) {
	return e.tails[id], nil
}

// safetyBench : une machine sans Docker, un état d'agent, une horloge à la
// main (chaque pause l'avance).
type safetyBench struct {
	*dockerBench
	engine       *fakeEngine
	state        *os.Root
	stateDirPath string
	now          time.Time
	upAt         time.Time
	// pauses compte les attentes : un test qui attend sans fin le voit.
	pauses int
}

func newSafetyBench(t *testing.T) *safetyBench {
	t.Helper()
	b := &safetyBench{dockerBench: newDockerBench(t), now: time.Date(2026, 11, 6, 9, 0, 0, 0, time.UTC)}
	b.stateDirPath = cleanTempDir(t)
	state, err := os.OpenRoot(b.stateDirPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = state.Close() })
	b.state = state
	b.engine = &fakeEngine{bench: b, tails: map[string][]string{}}
	b.compose.onUp = func(int) error {
		b.upAt = b.now
		return nil
	}
	b.tools.engine, b.tools.stateDir, b.tools.watches = b.engine, state, &updateWatches{}
	b.tools.now = func() time.Time { return b.now }
	b.tools.pause = func(_ context.Context, duration time.Duration) error {
		b.pauses++
		if b.pauses > 10000 {
			return errors.New("the test waits forever")
		}
		b.now = b.now.Add(duration)
		return nil
	}
	b.compose.lines = nil
	// Le service est déjà déployé, en version ancienne.
	if _, err := b.tools.deployService(context.Background(), b.job(false)); err != nil {
		t.Fatal(err)
	}
	b.compose.upDirs, b.compose.projects = nil, nil
	return b
}

// upgradeJob est la montée : le compose neuf, le filet par défaut.
func (b *safetyBench) upgradeJob() dockerJob {
	job := b.job(true)
	job.params.Compose = newerCompose
	safety := agentwire.DefaultServiceRollback()
	job.params.Rollback = &safety
	job.runID = "run_up"
	return job
}

func (b *safetyBench) read(t *testing.T, parts ...string) string {
	t.Helper()
	content, err := os.ReadFile(b.path(append([]string{"nextcloud"}, parts...)...))
	if err != nil {
		t.Fatal(err)
	}
	return string(content)
}

func (b *safetyBench) markerExists() bool {
	_, err := b.state.Lstat(markerNameOf("nextcloud"))
	return err == nil
}

// dockerState fabrique un conteneur du projet.
func dockerState(id, status, health string, startedAgo time.Duration, restarts int, now time.Time) dockerapi.Container {
	found := dockerapi.Container{
		ID: id, Name: "/nextcloud-" + id + "-1", RestartCount: restarts,
		State: dockerapi.ContainerState{Status: status, StartedAt: now.Add(-startedAgo).Format(time.RFC3339Nano)},
	}
	if health != "" {
		found.Config.Healthcheck = &dockerapi.Healthcheck{Test: []string{"CMD", "true"}}
		found.State.Health = &dockerapi.Health{Status: health}
	}
	return found
}

func updateReasonOf(t *testing.T, err error) string {
	t.Helper()
	if err == nil {
		return ""
	}
	return deployReasonOf(t, err)
}

func TestSafeUpdate_HealthyWithHealthcheckIsConfirmed(t *testing.T) {
	b := newSafetyBench(t)
	b.engine.states = func(ups int, sinceUp time.Duration) []dockerapi.Container {
		health := "starting"
		if ups > 0 && sinceUp >= 20*time.Second {
			health = "healthy"
		}
		return []dockerapi.Container{dockerState("app", "running", health, sinceUp, 0, b.now)}
	}
	oldCompose := b.read(t, "compose.yml")

	output, err := b.tools.deployService(context.Background(), b.upgradeJob())
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if got := b.read(t, "compose.yml"); got != newerCompose {
		t.Fatalf("the new compose was not written")
	}
	if b.read(t, previousComposeName) != oldCompose {
		t.Fatal("the previous compose was not kept byte for byte")
	}
	if len(b.compose.upDirs) != 1 || b.markerExists() || !strings.Contains(lineTexts(output), "healthy") {
		t.Fatalf("ups %d, marker %v, output %q", len(b.compose.upDirs), b.markerExists(), lineTexts(output))
	}
}

func TestSafeUpdate_WithoutHealthcheckNeedsTheStableDuration(t *testing.T) {
	b := newSafetyBench(t)
	b.engine.states = func(_ int, sinceUp time.Duration) []dockerapi.Container {
		return []dockerapi.Container{dockerState("app", "running", "", sinceUp, 0, b.now)}
	}
	started := b.now
	if _, err := b.tools.deployService(context.Background(), b.upgradeJob()); err != nil {
		t.Fatalf("update: %v", err)
	}
	if waited := b.now.Sub(started); waited < 2*time.Minute {
		t.Fatalf("judged healthy after %s, before the stable duration", waited)
	}
}

func TestSafeUpdate_ReturnsToThePreviousVersionWhenTheNewOneFails(t *testing.T) {
	cases := []struct {
		name    string
		states  func(ups int, sinceUp time.Duration, now time.Time) []dockerapi.Container
		upError func(rank int) error
	}{
		{"unhealthy", func(ups int, since time.Duration, now time.Time) []dockerapi.Container {
			if ups == 1 {
				return []dockerapi.Container{dockerState("app", "running", "unhealthy", since, 0, now)}
			}
			return []dockerapi.Container{dockerState("app", "running", "healthy", since, 0, now)}
		}, nil},
		{"restart loop", func(ups int, since time.Duration, now time.Time) []dockerapi.Container {
			if ups == 1 {
				return []dockerapi.Container{dockerState("app", "restarting", "", since, 3, now)}
			}
			return []dockerapi.Container{dockerState("app", "running", "", since, 0, now)}
		}, nil},
		{"still starting at the deadline", func(ups int, since time.Duration, now time.Time) []dockerapi.Container {
			if ups == 1 {
				return []dockerapi.Container{dockerState("app", "running", "starting", since, 0, now)}
			}
			return []dockerapi.Container{dockerState("app", "running", "healthy", since, 0, now)}
		}, nil},
		{"compose failed", func(_ int, since time.Duration, now time.Time) []dockerapi.Container {
			return []dockerapi.Container{dockerState("app", "running", "healthy", since, 0, now)}
		}, func(rank int) error {
			if rank == 1 {
				return errors.New("pull access denied")
			}
			return nil
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			b := newSafetyBench(t)
			b.engine.states = func(ups int, since time.Duration) []dockerapi.Container { return c.states(ups, since, b.now) }
			if c.upError != nil {
				b.compose.onUp = func(rank int) error {
					b.upAt = b.now
					return c.upError(rank)
				}
			}
			oldCompose := b.read(t, "compose.yml")

			_, err := b.tools.deployService(context.Background(), b.upgradeJob())
			if got := updateReasonOf(t, err); got != agentwire.ReasonRolledBackVersion {
				t.Fatalf("reason %q (%v)", got, err)
			}
			if b.read(t, "compose.yml") != oldCompose {
				t.Fatal("the previous compose was not put back")
			}
			if len(b.compose.upDirs) != 2 || b.markerExists() {
				t.Fatalf("ups %d, marker %v", len(b.compose.upDirs), b.markerExists())
			}
		})
	}
}

func TestSafeUpdate_AFailedReturnStopsThereWithoutASecondTry(t *testing.T) {
	b := newSafetyBench(t)
	b.engine.states = func(_ int, since time.Duration) []dockerapi.Container {
		return []dockerapi.Container{dockerState("app", "running", "unhealthy", since, 0, b.now)}
	}
	_, err := b.tools.deployService(context.Background(), b.upgradeJob())
	if got := updateReasonOf(t, err); got != agentwire.ReasonRollbackFailed {
		t.Fatalf("reason %q (%v)", got, err)
	}
	if len(b.compose.upDirs) != 2 {
		t.Fatalf("%d Compose runs: the return is tried once", len(b.compose.upDirs))
	}
	if b.markerExists() {
		t.Fatal("the marker stayed: the agent would try again")
	}
}

func TestSafeUpdate_CarriesTheJournalWithoutTheEnvValues(t *testing.T) {
	b := newSafetyBench(t)
	b.engine.states = func(ups int, since time.Duration) []dockerapi.Container {
		if ups == 1 {
			return []dockerapi.Container{dockerState("app", "restarting", "", since, 2, b.now), dockerState("db", "running", "healthy", since, 0, b.now)}
		}
		return []dockerapi.Container{dockerState("app", "running", "", since, 0, b.now), dockerState("db", "running", "healthy", since, 0, b.now)}
	}
	var lines []string
	for index := range 30 {
		lines = append(lines, "line "+string(rune('a'+index%26)))
	}
	lines = append(lines, "connect postgres://nextcloud:s3cr3t-value-123@db/nextcloud failed")
	b.engine.tails["app"] = lines
	b.engine.tails["db"] = []string{"healthy db line must not appear"}

	job := b.upgradeJob()
	job.params.EnvSent, job.hasEnv, job.env = true, true, "DB_PASSWORD=s3cr3t-value-123\n"
	_, err := b.tools.deployService(context.Background(), job)
	var failed *orderError
	if !errors.As(err, &failed) || failed.reason != agentwire.ReasonRolledBackVersion {
		t.Fatalf("err %v", err)
	}
	joined := strings.Join(failed.journal, "\n")
	if len(failed.journal) > agentwire.MaxJournalLines || len(joined) > agentwire.MaxJournalBytes {
		t.Fatalf("journal too long: %d lines, %d bytes", len(failed.journal), len(joined))
	}
	if strings.Contains(joined, "s3cr3t-value-123") || !strings.Contains(joined, "[.env]") {
		t.Fatalf("the .env value was not masked:\n%s", joined)
	}
	if strings.Contains(joined, "healthy db line") {
		t.Fatal("the journal of a healthy container was taken")
	}
	result := dockerResultOf("run_up", nil, err)
	if result.State != agentwire.ActionFailed || result.Reason != agentwire.ReasonRolledBackVersion || len(result.Journal) == 0 {
		t.Fatalf("result %+v", result)
	}
}

func TestSafeUpdate_PutsTheEnvBackOrRemovesTheOneItCreated(t *testing.T) {
	failing := func(b *safetyBench) {
		b.engine.states = func(ups int, since time.Duration) []dockerapi.Container {
			if ups == 1 {
				return []dockerapi.Container{dockerState("app", "running", "unhealthy", since, 0, b.now)}
			}
			return []dockerapi.Container{dockerState("app", "running", "healthy", since, 0, b.now)}
		}
	}
	t.Run("an existing .env comes back", func(t *testing.T) {
		b := newSafetyBench(t)
		failing(b)
		if err := os.WriteFile(b.path("nextcloud", ".env"), []byte("OLD=old-value-1\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		job := b.upgradeJob()
		job.params.EnvSent, job.hasEnv, job.env = true, true, "NEW=new-value-2\n"
		_, _ = b.tools.deployService(context.Background(), job)
		if got := b.read(t, ".env"); got != "OLD=old-value-1\n" {
			t.Fatalf(".env %q", got)
		}
	})
	t.Run("an .env the update created goes away", func(t *testing.T) {
		b := newSafetyBench(t)
		failing(b)
		job := b.upgradeJob()
		job.params.EnvSent, job.hasEnv, job.env = true, true, "NEW=new-value-2\n"
		_, _ = b.tools.deployService(context.Background(), job)
		if _, err := os.Lstat(b.path("nextcloud", ".env")); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("the new .env stayed: %v", err)
		}
	})
}

func TestSafeUpdate_AWriteFailurePutsTheVersionBackWithoutRunningCompose(t *testing.T) {
	b := newSafetyBench(t)
	b.engine.states = func(int, time.Duration) []dockerapi.Container { return nil }
	oldCompose := b.read(t, "compose.yml")
	b.tools.writeFiles = func(dir *os.Root, job dockerJob) ([]agentwire.OutputLine, error) {
		_, _ = writeServiceFiles(dir, job)
		return nil, failWith(agentwire.ReasonPathUnwritable, errors.New("disk error"))
	}
	if _, err := b.tools.deployService(context.Background(), b.upgradeJob()); err == nil {
		t.Fatal("expected the write failure")
	}
	if b.read(t, "compose.yml") != oldCompose || len(b.compose.upDirs) != 0 || b.markerExists() {
		t.Fatal("the failed write left the new version, a Compose run or a marker")
	}
}

func TestSafeUpdate_RefusesWhileAnotherWindowIsOpenAndWithoutAState(t *testing.T) {
	b := newSafetyBench(t)
	b.engine.states = func(int, time.Duration) []dockerapi.Container { return nil }
	if err := b.tools.saveMarker(updateMarker{RunID: "run_old", Project: "nextcloud", Phase: phaseWatching, Safety: agentwire.DefaultServiceRollback()}); err != nil {
		t.Fatal(err)
	}
	_, err := b.tools.deployService(context.Background(), b.upgradeJob())
	if got := updateReasonOf(t, err); got != agentwire.ReasonBusy || !isUntouched(err) {
		t.Fatalf("an open window: %v", err)
	}
	if b.read(t, "compose.yml") == newerCompose {
		t.Fatal("a refused update wrote the compose")
	}

	b.tools.stateDir = nil
	_, err = b.tools.deployService(context.Background(), b.upgradeJob())
	if got := updateReasonOf(t, err); got != agentwire.ReasonStateUnwritable {
		t.Fatalf("no state dir: %v", err)
	}
}

func TestSafeUpdate_ADockerRollbackOnlyAnswersAnOpenWindow(t *testing.T) {
	b := newSafetyBench(t)
	if b.tools.watches.veto("nextcloud") {
		t.Fatal("a veto with no window open was accepted")
	}
	sawVeto := make(chan struct{})
	b.engine.states = func(ups int, since time.Duration) []dockerapi.Container {
		// Sain à tous égards : seul le veto peut ramener la version.
		if ups == 1 && since > 10*time.Second && b.tools.watches.veto("nextcloud") {
			close(sawVeto)
		}
		health := "starting"
		if ups > 1 {
			health = "healthy"
		}
		return []dockerapi.Container{dockerState("app", "running", health, since, 0, b.now)}
	}
	_, err := b.tools.deployService(context.Background(), b.upgradeJob())
	select {
	case <-sawVeto:
	default:
		t.Fatal("the veto found no open window")
	}
	if got := updateReasonOf(t, err); got != agentwire.ReasonRolledBackVersion {
		t.Fatalf("reason %q", got)
	}
	if b.tools.watches.veto("nextcloud") {
		t.Fatal("the window stayed open after the update")
	}
}

func TestResume_FinishesAnInterruptedWindowAndDropsAnUnreadableMarker(t *testing.T) {
	t.Run("a watching marker is judged again", func(t *testing.T) {
		b := newSafetyBench(t)
		b.engine.states = func(ups int, since time.Duration) []dockerapi.Container {
			if ups == 0 {
				return []dockerapi.Container{dockerState("app", "running", "unhealthy", since, 0, b.now)}
			}
			return []dockerapi.Container{dockerState("app", "running", "healthy", since, 0, b.now)}
		}
		// L'agent est tombé après avoir écrit la nouvelle version.
		if err := copyRegular(mustOpen(t, b.path("nextcloud")), composeFileName, previousComposeName, composeFileMode); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(b.path("nextcloud", composeFileName), []byte(newerCompose), 0o644); err != nil {
			t.Fatal(err)
		}
		marker := updateMarker{RunID: "run_up", Project: "nextcloud", Phase: phaseWatching, Safety: agentwire.DefaultServiceRollback(), Deadline: b.now.Add(time.Minute)}
		if err := b.tools.saveMarker(marker); err != nil {
			t.Fatal(err)
		}
		output, err := b.tools.resumeSettle(context.Background(), b.root, marker)
		if got := updateReasonOf(t, err); got != agentwire.ReasonRolledBackVersion {
			t.Fatalf("reason %q (%v) %q", got, err, lineTexts(output))
		}
		if b.read(t, "compose.yml") == newerCompose || b.markerExists() {
			t.Fatal("the resumed update did not go back")
		}
	})
	t.Run("an unreadable marker is dropped and nothing is touched", func(t *testing.T) {
		b := newSafetyBench(t)
		b.engine.states = func(int, time.Duration) []dockerapi.Container { return nil }
		if err := os.WriteFile(b.stateDirPath+"/"+markerNameOf("nextcloud"), []byte("{not json"), 0o600); err != nil {
			t.Fatal(err)
		}
		runner := &orderRunner{stateDir: b.state, docker: b.tools, worker: newOrderWorker(), results: newActionResults(), logger: slog.New(slog.DiscardHandler)}
		runner.resumeUpdates()
		if b.markerExists() {
			t.Fatal("the unreadable marker stayed")
		}
		if len(b.compose.upDirs) != 0 || len(runner.worker.queue) != 0 {
			t.Fatal("an unreadable marker triggered an action")
		}
		if b.read(t, "compose.yml") == newerCompose {
			t.Fatal("the compose changed")
		}
	})
}

func TestMarker_JudgesWhatItReads(t *testing.T) {
	good := updateMarker{RunID: "run_1", Project: "nextcloud", Phase: phaseWatching, Safety: agentwire.DefaultServiceRollback()}
	if err := good.validate(); err != nil {
		t.Fatal(err)
	}
	for name, bad := range map[string]updateMarker{
		"no run":         {Project: "nextcloud", Phase: phaseWatching, Safety: agentwire.DefaultServiceRollback()},
		"project climbs": {RunID: "r", Project: "../x", Phase: phaseWatching, Safety: agentwire.DefaultServiceRollback()},
		"unknown phase":  {RunID: "r", Project: "nextcloud", Phase: "x", Safety: agentwire.DefaultServiceRollback()},
		"window too big": {RunID: "r", Project: "nextcloud", Phase: phaseWatching, Safety: agentwire.ServiceRollback{HealthWindowSeconds: 7200, StableSeconds: 120}},
		"unknown env":    {RunID: "r", Project: "nextcloud", Phase: phaseWatching, Safety: agentwire.DefaultServiceRollback(), Env: "rm -rf"},
	} {
		if bad.validate() == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestReadDockerOrder_JudgesTheSafetyNet(t *testing.T) {
	read := func(update bool, rollback *agentwire.ServiceRollback) bool {
		params, _ := json.Marshal(agentwire.DockerParams{ServiceID: "svc_1", Project: "nextcloud", Compose: testCompose, DataDirs: []string{"app"}, Rollback: rollback})
		_, refused := readDockerOrder(agentwire.OrderBody{RunID: "run_1", Params: params}, "/srv/workspace", update)
		return refused == nil
	}
	valid := agentwire.DefaultServiceRollback()
	if !read(true, &valid) || !read(true, nil) {
		t.Fatal("a valid update was refused")
	}
	if read(false, &valid) {
		t.Fatal("a first deployment cannot carry a safety net")
	}
	for name, bad := range map[string]agentwire.ServiceRollback{
		"window above 30 minutes":  {HealthWindowSeconds: 3600, StableSeconds: 120},
		"window below 2 minutes":   {HealthWindowSeconds: 30, StableSeconds: 60},
		"stable below a minute":    {HealthWindowSeconds: 300, StableSeconds: 5},
		"stable beyond the window": {HealthWindowSeconds: 300, StableSeconds: 300},
	} {
		if read(true, &bad) {
			t.Errorf("%s: accepted", name)
		}
	}
}

// La règle de santé de l'agent tient celle du serveur pour ce qui tourne :
// un conteneur qui va mal pour l'un va mal pour l'autre.
func TestJudgeContainers_MatchesTheServerRule(t *testing.T) {
	now := time.Date(2026, 11, 6, 9, 0, 0, 0, time.UTC)
	cases := []struct {
		name   string
		docker dockerapi.Container
		server container.Container
		unwell bool
	}{
		{"unhealthy", dockerState("a", "running", "unhealthy", time.Hour, 0, now), container.Container{State: container.StateRunning, Health: container.HealthUnhealthy}, true},
		{"restarting", dockerState("a", "restarting", "", time.Hour, 5, now), container.Container{State: container.StateRestarting}, true},
		{"dead", dockerState("a", "dead", "", time.Hour, 0, now), container.Container{State: container.StateDead}, true},
		{"healthy", dockerState("a", "running", "healthy", time.Hour, 0, now), container.Container{State: container.StateRunning, Health: container.HealthHealthy}, false},
		{"plain running", dockerState("a", "running", "", time.Hour, 0, now), container.Container{State: container.StateRunning}, false},
	}
	for _, c := range cases {
		verdict, _ := judgeContainers([]dockerapi.Container{c.docker}, nil, now, 2*time.Minute)
		if got := verdict == verdictUnhealthy; got != c.unwell || container.NeedsAttention(c.server) != c.unwell {
			t.Errorf("%s: agent %v, server %v, want %v", c.name, got, container.NeedsAttention(c.server), c.unwell)
		}
	}
}

func TestJudgeContainers_IgnoresRestartsThatAreOlderThanTheUpdate(t *testing.T) {
	now := time.Date(2026, 11, 6, 9, 0, 0, 0, time.UTC)
	old := dockerState("db", "running", "", time.Hour, 4, now)
	if verdict, _ := judgeContainers([]dockerapi.Container{old}, map[string]int{"db": 4}, now, time.Minute); verdict != verdictHealthy {
		t.Fatalf("a container that restarted before the update: %v", verdict)
	}
	if verdict, _ := judgeContainers([]dockerapi.Container{old}, map[string]int{"db": 3}, now, time.Minute); verdict != verdictUnhealthy {
		t.Fatalf("a container that restarted during the update: %v", verdict)
	}
	if verdict, _ := judgeContainers(nil, nil, now, time.Minute); verdict != verdictWaiting {
		t.Fatalf("no container: %v", verdict)
	}
}

func TestFitJournal_KeepsTheLastLinesWithinTheLimits(t *testing.T) {
	var many []string
	for range 50 {
		many = append(many, strings.Repeat("x", 300))
	}
	fitted := fitJournal(many)
	if len(fitted) > agentwire.MaxJournalLines || len(strings.Join(fitted, "\n")) > agentwire.MaxJournalBytes {
		t.Fatalf("%d lines, %d bytes", len(fitted), len(strings.Join(fitted, "\n")))
	}
	if got := fitJournal([]string{"a", "b"}); len(got) != 2 {
		t.Fatalf("short journal %v", got)
	}
}

func mustOpen(t *testing.T, dir string) *os.Root {
	t.Helper()
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = root.Close() })
	return root
}

// docker.rollback passe par le juge comme tout ordre : il exige docker.update,
// et hors fenêtre de santé ouverte l'agent le refuse sans rien faire.
func TestDockerRollbackOrder_NeedsTheRightAndAnOpenWindow(t *testing.T) {
	params := agentwire.DockerRollbackParams{Project: "nextcloud"}

	without := newDockerFlow(t, "docker.deploy")
	body := longOrder(1, agentwire.KindDockerRollback, params)
	without.runner.receive(without.signed(t, body))
	results := without.until(t, body.RunID)
	if len(results) != 1 || results[0].State != agentwire.ActionRefused || results[0].Reason != agentwire.RightMissing("docker.update") {
		t.Fatalf("without the right: %+v", results)
	}

	f := newDockerFlow(t, "docker.update")
	f.runner.docker.watches = &updateWatches{}
	body = longOrder(1, agentwire.KindDockerRollback, params)
	f.runner.receive(f.signed(t, body))
	results = f.until(t, body.RunID)
	if len(results) != 1 || results[0].State != agentwire.ActionRefused || results[0].Reason != agentwire.ReasonNoPendingUpdate {
		t.Fatalf("with no window: %+v", results)
	}

	vetoed := f.runner.docker.watches.begin("nextcloud")
	body = longOrder(2, agentwire.KindDockerRollback, params)
	f.runner.receive(f.signed(t, body))
	results = f.until(t, body.RunID)
	if len(results) != 1 || results[0].State != agentwire.ActionSucceeded {
		t.Fatalf("with a window: %+v", results)
	}
	select {
	case <-vetoed:
	default:
		t.Fatal("the open window did not hear the veto")
	}

	bad := longOrder(3, agentwire.KindDockerRollback, agentwire.DockerRollbackParams{Project: "../etc"})
	f.runner.receive(f.signed(t, bad))
	if results = f.until(t, bad.RunID); len(results) != 1 || results[0].Reason != agentwire.ReasonBadOrder {
		t.Fatalf("a project that climbs: %+v", results)
	}
}

// Un agent qui s'arrête pendant la fenêtre ne rend aucun verdict : le marqueur
// reste, le service n'est pas touché, la reprise jugera.
func TestSafeUpdate_AnAgentThatStopsDuringTheWindowLeavesTheMarkerAndNoVerdict(t *testing.T) {
	b := newSafetyBench(t)
	b.engine.states = func(_ int, since time.Duration) []dockerapi.Container {
		return []dockerapi.Container{dockerState("app", "running", "starting", since, 0, b.now)}
	}
	ctx, stop := context.WithCancel(context.Background())
	b.tools.pause = func(context.Context, time.Duration) error {
		stop()
		return ctx.Err()
	}
	_, err := b.tools.deployService(ctx, b.upgradeJob())
	if !isAgentStopping(err) {
		t.Fatalf("err %v", err)
	}
	if !b.markerExists() || len(b.compose.upDirs) != 1 {
		t.Fatalf("marker %v, ups %d: the stop must keep the marker and not go back", b.markerExists(), len(b.compose.upDirs))
	}
	if b.read(t, "compose.yml") != newerCompose {
		t.Fatal("the stop changed the compose")
	}
}
