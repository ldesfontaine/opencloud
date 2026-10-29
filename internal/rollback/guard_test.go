package rollback

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/ldesfontaine/opencloud/internal/brand"
)

var epoch = time.Date(2026, 10, 29, 10, 0, 0, 0, time.UTC)

// fakeClock avance quand le test le dit : aucun test ne dort.
type fakeClock struct {
	mu      sync.Mutex
	now     time.Time
	waiting []waiter
}

type waiter struct {
	at      time.Time
	channel chan time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) After(delay time.Duration) <-chan time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	channel := make(chan time.Time, 1)
	c.waiting = append(c.waiting, waiter{at: c.now.Add(delay), channel: channel})
	return channel
}

// advance fait passer le temps et réveille ce qui attendait cet instant.
func (c *fakeClock) advance(delay time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(delay)
	remaining := c.waiting[:0]
	for _, w := range c.waiting {
		if !w.at.After(c.now) {
			w.channel <- c.now
			continue
		}
		remaining = append(remaining, w)
	}
	c.waiting = remaining
}

func (c *fakeClock) hasWaiter() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.waiting) > 0
}

// fakeChild est un binaire lancé : il finit tout seul avec son code, ou
// tient jusqu'à ce qu'on l'arrête.
type fakeChild struct {
	exit    chan Exit
	stopped chan struct{}
	once    sync.Once
}

func (c *fakeChild) Wait() Exit { return <-c.exit }

func (c *fakeChild) Stop() {
	c.once.Do(func() { close(c.stopped) })
	select {
	case c.exit <- Exit{Code: 0}:
	default:
	}
}

// launches note chaque lancement : la version que current visait alors.
type bench struct {
	t      *testing.T
	state  *os.Root
	bin    *os.Root
	clock  *fakeClock
	guard  *Guard
	launch []string
	// behave décide, pour la version lancée, de ce qui lui arrive.
	behave func(version string) behavior
	// children garde les binaires qui tiennent, pour les regarder.
	children []*fakeChild
	mu       sync.Mutex
}

type behavior struct {
	startErr error
	// exitCode et immediate : le binaire finit tout de suite avec ce code.
	immediate bool
	exitCode  int
}

func newBench(t *testing.T) *bench {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	state, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { state.Close() })
	if err := state.Mkdir(brand.AgentBinDir, 0o755); err != nil {
		t.Fatal(err)
	}
	bin, err := state.OpenRoot(brand.AgentBinDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { bin.Close() })
	for _, version := range []string{"v0.0.1", "v0.0.2"} {
		if err := bin.WriteFile(BinaryName(version), []byte("binary "+version), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := bin.Symlink(BinaryName("v0.0.2"), brand.AgentCurrentLink); err != nil {
		t.Fatal(err)
	}
	if err := bin.Symlink(BinaryName("v0.0.1"), brand.AgentPreviousLink); err != nil {
		t.Fatal(err)
	}
	b := &bench{t: t, state: state, bin: bin, clock: &fakeClock{now: epoch}}
	b.behave = func(string) behavior { return behavior{} }
	b.guard = &Guard{
		State: state, Bin: bin, Binary: filepath.Join(dir, brand.AgentBinDir, brand.AgentCurrentLink), Args: []string{"agent"},
		Launch: b.launcher, Now: b.clock.Now, After: b.clock.After, Logger: slog.New(slog.DiscardHandler),
	}
	return b
}

func (b *bench) launcher(_ string, _, environment []string) (Child, error) {
	target := LinkTarget(b.bin, brand.AgentCurrentLink)
	version, _ := VersionOf(target)
	b.mu.Lock()
	b.launch = append(b.launch, version)
	b.mu.Unlock()
	hasGuardEnv := false
	for _, entry := range environment {
		if entry == brand.AgentGuardEnv+"=1" {
			hasGuardEnv = true
		}
	}
	if !hasGuardEnv {
		b.t.Error("the agent was not told it runs under the guard")
	}
	what := b.behave(version)
	if what.startErr != nil {
		return nil, what.startErr
	}
	child := &fakeChild{exit: make(chan Exit, 1), stopped: make(chan struct{})}
	if what.immediate {
		child.exit <- Exit{Code: what.exitCode}
		return child, nil
	}
	b.mu.Lock()
	b.children = append(b.children, child)
	b.mu.Unlock()
	return child, nil
}

func (b *bench) child(index int) *fakeChild {
	b.mu.Lock()
	defer b.mu.Unlock()
	if index >= len(b.children) {
		return nil
	}
	return b.children[index]
}

func (b *bench) launched() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]string(nil), b.launch...)
}

// arm écrit l'attente de confirmation d'une mise à jour vers v0.0.2.
func (b *bench) arm(kind Kind) Marker {
	b.t.Helper()
	marker := Marker{
		State: StatePending, Kind: kind, RunID: "run1", Target: "v0.0.2", Previous: "v0.0.1",
		StartedAt: epoch, Deadline: epoch.Add(GracePeriod),
	}
	if err := Save(b.state, marker); err != nil {
		b.t.Fatal(err)
	}
	return marker
}

func (b *bench) marker() Marker {
	b.t.Helper()
	marker, err := Load(b.state)
	if err != nil {
		b.t.Fatalf("marker: %v", err)
	}
	return marker
}

func (b *bench) current() string {
	return LinkTarget(b.bin, brand.AgentCurrentLink)
}

// run lance le lanceur jusqu'à sa fin ; un binaire qui tient est arrêté par
// l'annulation de ctx quand le test le décide.
func (b *bench) run(ctx context.Context) int {
	return b.guard.Run(ctx)
}

// runUntilStopped lance le lanceur, laisse le test agir pendant que le
// binaire tient, puis rend le code.
func (b *bench) runInBackground(ctx context.Context) <-chan int {
	done := make(chan int, 1)
	go func() { done <- b.run(ctx) }()
	return done
}

func waitFor(t *testing.T, what string, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for !condition() {
		if time.Now().After(deadline) {
			t.Fatalf("never happened: %s", what)
		}
		time.Sleep(time.Millisecond)
	}
}

func receive(t *testing.T, done <-chan int) int {
	t.Helper()
	select {
	case code := <-done:
		return code
	case <-time.After(30 * time.Second):
		t.Fatal("the guard never returned")
		return -1
	}
}

func TestGuard_WithoutAMarker_RunsCurrentAndPassesItsExitCode(t *testing.T) {
	for _, code := range []int{0, 1, 2, 3} {
		b := newBench(t)
		b.behave = func(string) behavior { return behavior{immediate: true, exitCode: code} }
		if got := b.run(context.Background()); got != code {
			t.Errorf("child exits %d, guard returns %d", code, got)
		}
		if launched := b.launched(); len(launched) != 1 || launched[0] != "v0.0.2" {
			t.Errorf("launched %v", launched)
		}
	}
}

func TestGuard_AStopRequestStopsTheChildAndExitsCleanly(t *testing.T) {
	b := newBench(t)
	ctx, cancel := context.WithCancel(context.Background())
	done := b.runInBackground(ctx)
	waitFor(t, "the child started", func() bool { return len(b.launched()) == 1 })
	cancel()
	if code := receive(t, done); code != 0 {
		t.Fatalf("code %d", code)
	}
	if b.child(0) == nil {
		t.Fatal("no child")
	}
	select {
	case <-b.child(0).stopped:
	default:
		t.Fatal("the child was not stopped")
	}
}

// Un binaire mort-né ne s'exécute même pas : le lanceur, lui, n'est pas ce
// binaire, il rebascule à l'échéance.
func TestGuard_AStillbornBinary_ReturnsToThePreviousVersionAtTheDeadline(t *testing.T) {
	b := newBench(t)
	b.arm(KindUpdate)
	b.behave = func(version string) behavior {
		if version == "v0.0.2" {
			return behavior{startErr: errors.New("exec format error")}
		}
		return behavior{}
	}
	// Avant l'échéance : chaque lancement échoue, systemd relance (code 1),
	// le lanceur compte les plantages et ne bascule pas.
	for attempt := 1; attempt <= 3; attempt++ {
		if code := b.run(context.Background()); code != 1 {
			t.Fatalf("attempt %d: code %d", attempt, code)
		}
		if b.current() != "opencloud-v0.0.2" {
			t.Fatalf("switched back too early: %s", b.current())
		}
		b.clock.advance(time.Minute)
	}
	if marker := b.marker(); marker.State != StatePending || marker.Crashes != 3 {
		t.Fatalf("marker %+v", marker)
	}
	// L'échéance est passée (trois minutes) : le lancement suivant rebascule.
	ctx, cancel := context.WithCancel(context.Background())
	done := b.runInBackground(ctx)
	waitFor(t, "the previous version started", func() bool { return len(b.launched()) == 4 })
	cancel()
	receive(t, done)
	if b.current() != "opencloud-v0.0.1" {
		t.Fatalf("current is %s", b.current())
	}
	marker := b.marker()
	if marker.State != StateRolledBack || marker.Cause != CauseCrash || marker.Target != "v0.0.2" || marker.Previous != "v0.0.1" || marker.RunID != "run1" {
		t.Fatalf("marker %+v", marker)
	}
	if last := b.launched()[3]; last != "v0.0.1" {
		t.Fatalf("launched %v", b.launched())
	}
}

func TestGuard_ABinaryThatLoops_ReturnsToThePreviousVersion(t *testing.T) {
	b := newBench(t)
	b.arm(KindUpdate)
	b.behave = func(version string) behavior {
		if version == "v0.0.2" {
			return behavior{immediate: true, exitCode: 1}
		}
		return behavior{}
	}
	if code := b.run(context.Background()); code != 1 {
		t.Fatalf("code %d", code)
	}
	// Un refus (2) avant l'échéance n'est qu'un plantage de plus : systemd ne
	// doit pas renoncer à relancer.
	b.behave = func(string) behavior { return behavior{immediate: true, exitCode: 2} }
	if code := b.run(context.Background()); code != 1 {
		t.Fatalf("a refusal before the deadline returned %d", code)
	}
	b.clock.advance(GracePeriod)
	b.behave = func(string) behavior { return behavior{} }
	ctx, cancel := context.WithCancel(context.Background())
	done := b.runInBackground(ctx)
	waitFor(t, "the previous version started", func() bool { return len(b.launched()) == 3 })
	cancel()
	receive(t, done)
	if marker := b.marker(); marker.State != StateRolledBack || marker.Cause != CauseCrash || marker.Crashes != 2 || b.current() != "opencloud-v0.0.1" {
		t.Fatalf("marker %+v, current %s", marker, b.current())
	}
}

// Il tourne mais ne rejoint jamais le serveur : à l'échéance le lanceur arrête
// le binaire, rebascule, et demande à systemd de relancer.
func TestGuard_ABinaryThatNeverConfirms_IsStoppedAndSwitchedBackAtTheDeadline(t *testing.T) {
	b := newBench(t)
	b.arm(KindUpdate)
	done := b.runInBackground(context.Background())
	waitFor(t, "the guard waits for the deadline", b.clock.hasWaiter)
	if b.current() != "opencloud-v0.0.2" {
		t.Fatal("switched back before the deadline")
	}
	b.clock.advance(GracePeriod)
	if code := receive(t, done); code != ExitRestart {
		t.Fatalf("code %d, want %d", code, ExitRestart)
	}
	select {
	case <-b.child(0).stopped:
	default:
		t.Fatal("the binary that never confirmed was not stopped")
	}
	marker := b.marker()
	if b.current() != "opencloud-v0.0.1" || marker.State != StateRolledBack || marker.Cause != CauseTimeout {
		t.Fatalf("marker %+v, current %s", marker, b.current())
	}
}

func TestGuard_AConfirmedUpdate_IsLeftAlone(t *testing.T) {
	b := newBench(t)
	b.arm(KindUpdate)
	ctx, cancel := context.WithCancel(context.Background())
	done := b.runInBackground(ctx)
	waitFor(t, "the guard waits for the deadline", b.clock.hasWaiter)
	// L'agent a rejoint le serveur, qui l'a acquitté : il retire le marqueur.
	if err := Remove(b.state); err != nil {
		t.Fatal(err)
	}
	b.clock.advance(GracePeriod)
	cancel()
	if code := receive(t, done); code != 0 {
		t.Fatalf("code %d", code)
	}
	if b.current() != "opencloud-v0.0.2" {
		t.Fatalf("a confirmed update was reverted: %s", b.current())
	}
	if _, err := Load(b.state); !errors.Is(err, ErrNoMarker) {
		t.Fatalf("marker came back: %v", err)
	}
}

// Une seule remise en état : si l'ancienne version échoue aussi, on s'arrête,
// l'état dit l'échec, et rien ne boucle.
func TestGuard_WhenThePreviousVersionFailsToo_StopsWithoutLooping(t *testing.T) {
	b := newBench(t)
	b.arm(KindUpdate)
	b.behave = func(string) behavior { return behavior{startErr: errors.New("no such file")} }
	b.clock.advance(GracePeriod)
	// Échéance passée : bascule sur v0.0.1, qui ne se lance pas non plus.
	if code := b.run(context.Background()); code != ExitRefused {
		t.Fatalf("code %d, want %d (systemd must not restart)", code, ExitRefused)
	}
	marker := b.marker()
	if marker.State != StateFailed || marker.Cause != CausePreviousCrash {
		t.Fatalf("marker %+v", marker)
	}
	if b.current() != "opencloud-v0.0.1" {
		t.Fatalf("current is %s", b.current())
	}
	// Un redémarrage par l'opérateur : on relance l'agent tel quel, on ne
	// rebascule plus, et un nouvel échec rend encore 2.
	for range 3 {
		if code := b.run(context.Background()); code != ExitRefused {
			t.Fatalf("code %d", code)
		}
	}
	if b.current() != "opencloud-v0.0.1" {
		t.Fatalf("the guard switched again: %s", b.current())
	}
	for _, version := range b.launched() {
		if version == "v0.0.2" {
			// Seul le tout premier lancement après l'échéance a eu lieu sur la
			// version d'avant ; la version refusée n'a pas été relancée.
			t.Fatalf("the refused version came back: %v", b.launched())
		}
	}
}

func TestGuard_WhenThePreviousBinaryIsGone_SaysSoAndKeepsCurrent(t *testing.T) {
	b := newBench(t)
	b.arm(KindUpdate)
	if err := b.bin.Remove(BinaryName("v0.0.1")); err != nil {
		t.Fatal(err)
	}
	b.clock.advance(GracePeriod)
	b.behave = func(string) behavior { return behavior{immediate: true, exitCode: 1} }
	b.run(context.Background())
	marker := b.marker()
	if marker.State != StateFailed || marker.Cause != CausePreviousMissing || b.current() != "opencloud-v0.0.2" {
		t.Fatalf("marker %+v, current %s", marker, b.current())
	}
}

func TestGuard_APreviousBinaryThatIsNotExecutable_IsNotSwitchedTo(t *testing.T) {
	b := newBench(t)
	b.arm(KindUpdate)
	if err := b.bin.Chmod(BinaryName("v0.0.1"), 0o600); err != nil {
		t.Fatal(err)
	}
	b.clock.advance(GracePeriod)
	b.behave = func(string) behavior { return behavior{immediate: true, exitCode: 1} }
	b.run(context.Background())
	if marker := b.marker(); marker.State != StateFailed || b.current() != "opencloud-v0.0.2" {
		t.Fatalf("marker %+v, current %s", marker, b.current())
	}
}

// Un retour arrière demandé à la main est confirmé ou annulé comme une mise à
// jour, mais ne déclenche pas de seconde remise en état.
func TestGuard_AManualRollbackThatIsNotConfirmed_DoesNotSwitchAgain(t *testing.T) {
	b := newBench(t)
	// Le retour a basculé sur v0.0.1, que le marqueur nomme comme cible.
	if err := SwitchLink(b.bin, brand.AgentCurrentLink, BinaryName("v0.0.1")); err != nil {
		t.Fatal(err)
	}
	marker := Marker{
		State: StatePending, Kind: KindRollback, RunID: "run2", Target: "v0.0.1", Previous: "v0.0.2",
		StartedAt: epoch, Deadline: epoch.Add(GracePeriod),
	}
	if err := Save(b.state, marker); err != nil {
		t.Fatal(err)
	}
	b.clock.advance(GracePeriod)
	ctx, cancel := context.WithCancel(context.Background())
	done := b.runInBackground(ctx)
	waitFor(t, "the agent started", func() bool { return len(b.launched()) == 1 })
	cancel()
	receive(t, done)
	got := b.marker()
	if got.State != StateFailed || got.Cause != CauseUnconfirmed || b.current() != "opencloud-v0.0.1" {
		t.Fatalf("marker %+v, current %s", got, b.current())
	}
}

func TestGuard_AMarkerThatDoesNotRead_IsSetAsideAndTheAgentStarts(t *testing.T) {
	cases := map[string]func(b *bench){
		"garbage": func(b *bench) {
			if err := b.state.WriteFile(brand.AgentUpdateMarker, []byte("{not json"), 0o600); err != nil {
				t.Fatal(err)
			}
		},
		"unknown state": func(b *bench) {
			if err := b.state.WriteFile(brand.AgentUpdateMarker, []byte(`{"state":"weird","kind":"update","run_id":"r","target":"v0.0.2","previous":"v0.0.1"}`), 0o600); err != nil {
				t.Fatal(err)
			}
		},
		"readable by others": func(b *bench) {
			b.arm(KindUpdate)
			if err := b.state.Chmod(brand.AgentUpdateMarker, 0o644); err != nil {
				t.Fatal(err)
			}
		},
		"a version that is a path": func(b *bench) {
			if err := b.state.WriteFile(brand.AgentUpdateMarker, []byte(`{"state":"pending","kind":"update","run_id":"r","target":"v0.0.2","previous":"../../etc/passwd"}`), 0o600); err != nil {
				t.Fatal(err)
			}
		},
	}
	for name, corrupt := range cases {
		t.Run(name, func(t *testing.T) {
			b := newBench(t)
			corrupt(b)
			b.clock.advance(time.Hour)
			b.behave = func(string) behavior { return behavior{immediate: true, exitCode: 0} }
			if code := b.run(context.Background()); code != 0 {
				t.Fatalf("code %d", code)
			}
			if launched := b.launched(); len(launched) != 1 || launched[0] != "v0.0.2" {
				t.Fatalf("launched %v", launched)
			}
			if _, err := Load(b.state); !errors.Is(err, ErrNoMarker) {
				t.Fatalf("the unreadable marker is still there: %v", err)
			}
			if b.current() != "opencloud-v0.0.2" {
				t.Fatalf("the guard switched on a marker it could not read: %s", b.current())
			}
		})
	}
}

// Une version d'avant qui a tenu au-delà du délai de pose n'est pas "cassée" :
// un plantage bien plus tard est un plantage ordinaire, systemd relance.
func TestGuard_APreviousVersionThatHeldForAWhile_CrashesLikeAnyAgent(t *testing.T) {
	b := newBench(t)
	marker := b.arm(KindUpdate)
	marker.State, marker.Cause, marker.FinishedAt = StateRolledBack, CauseTimeout, epoch.Add(GracePeriod)
	if err := Save(b.state, marker); err != nil {
		t.Fatal(err)
	}
	if err := SwitchLink(b.bin, brand.AgentCurrentLink, BinaryName("v0.0.1")); err != nil {
		t.Fatal(err)
	}
	b.behave = func(string) behavior { return behavior{} }
	done := b.runInBackground(context.Background())
	waitFor(t, "the child started", func() bool { return b.child(0) != nil })
	b.clock.advance(SettleAfter + time.Second)
	b.child(0).exit <- Exit{Code: 1}
	if code := receive(t, done); code != 1 {
		t.Fatalf("code %d", code)
	}
	if got := b.marker(); got.State != StateRolledBack {
		t.Fatalf("marker %+v", got)
	}
}

func TestMarker_RefusesWhatIsNotAnUpdate(t *testing.T) {
	good := Marker{State: StatePending, Kind: KindUpdate, RunID: "r", Target: "v0.0.2", Previous: "v0.0.1"}
	if err := good.Validate(); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*Marker){
		"state":            func(m *Marker) { m.State = "x" },
		"kind":             func(m *Marker) { m.Kind = "x" },
		"no run":           func(m *Marker) { m.RunID = "" },
		"target":           func(m *Marker) { m.Target = "dev" },
		"previous":         func(m *Marker) { m.Previous = "v1" },
		"same version":     func(m *Marker) { m.Previous = m.Target },
		"previous as path": func(m *Marker) { m.Previous = "../x" },
	} {
		marker := good
		mutate(&marker)
		if err := marker.Validate(); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestMarker_SaveAndLoadRoundTripAndKeepNoSecret(t *testing.T) {
	b := newBench(t)
	saved := b.arm(KindUpdate)
	loaded := b.marker()
	if loaded.RunID != saved.RunID || loaded.Target != saved.Target || !loaded.Deadline.Equal(saved.Deadline) {
		t.Fatalf("loaded %+v", loaded)
	}
	info, err := b.state.Stat(brand.AgentUpdateMarker)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v %v", info, err)
	}
	content, err := b.state.ReadFile(brand.AgentUpdateMarker)
	if err != nil {
		t.Fatal(err)
	}
	// Le marqueur ne dit que l'état de la mise à jour : jamais un jeton, une clé
	// ni une adresse.
	for _, key := range []string{"token", "secret", "key", "password", "url", "address"} {
		if containsFold(string(content), key) {
			t.Errorf("the marker mentions %q: %s", key, content)
		}
	}
}

func containsFold(text, part string) bool {
	return len(part) > 0 && len(text) >= len(part) && indexFold(text, part) >= 0
}

func indexFold(text, part string) int {
	for i := 0; i+len(part) <= len(text); i++ {
		match := true
		for j := range len(part) {
			a, b := text[i+j], part[j]
			if a >= 'A' && a <= 'Z' {
				a += 'a' - 'A'
			}
			if a != b {
				match = false
				break
			}
		}
		if match {
			return i
		}
	}
	return -1
}

func TestArm_WritesPreviousBeforeTheMarkerAndLeavesCurrentAlone(t *testing.T) {
	b := newBench(t)
	if err := b.bin.Remove(brand.AgentPreviousLink); err != nil {
		t.Fatal(err)
	}
	marker := Marker{State: StatePending, Kind: KindUpdate, RunID: "r", Target: "v0.0.2", Previous: "v0.0.1", StartedAt: epoch, Deadline: epoch.Add(GracePeriod)}
	if err := Arm(b.state, b.bin, marker); err != nil {
		t.Fatal(err)
	}
	if LinkTarget(b.bin, brand.AgentPreviousLink) != "opencloud-v0.0.1" || b.current() != "opencloud-v0.0.2" {
		t.Fatalf("previous %s, current %s", LinkTarget(b.bin, brand.AgentPreviousLink), b.current())
	}
	if b.marker().RunID != "r" {
		t.Fatal("no marker")
	}
	if _, err := b.bin.Lstat(brand.AgentPreviousLink + ".new"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a temporary link stayed: %v", err)
	}
}

func TestVersionOf_OnlyReadsAnInstalledBinaryName(t *testing.T) {
	for name, want := range map[string]string{"opencloud-v0.0.2": "v0.0.2", "opencloud-dev": "", "other-v0.0.1": "", "opencloud-v0.0.1-rc1": "", "": ""} {
		got, ok := VersionOf(name)
		if got != want || ok != (want != "") {
			t.Errorf("%q: %q %v", name, got, ok)
		}
	}
}
