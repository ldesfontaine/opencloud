package probe

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ldesfontaine/opencloud/internal/store"
	"github.com/ldesfontaine/opencloud/internal/transport"
	"github.com/ldesfontaine/opencloud/migrations"
)

// fakeProber rejoue ce qu'une machine aurait répondu : SSH est lent et
// extérieur, il ne rentre pas dans un test.
type fakeProber struct {
	mu       sync.Mutex
	probes   int
	probeErr error
	reply    transport.LauncherReply
	replyErr error
	entered  chan struct{}
	release  chan struct{}
}

func (f *fakeProber) Probe(context.Context) error {
	f.mu.Lock()
	f.probes++
	f.mu.Unlock()

	if f.entered != nil {
		f.entered <- struct{}{}
		<-f.release
	}
	return f.probeErr
}

func (f *fakeProber) CheckLauncher(context.Context) (transport.LauncherReply, error) {
	return f.reply, f.replyErr
}

func (f *fakeProber) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.probes
}

// fakeTransports : une machine sans entrée ici n'est pas enrôlée.
type fakeTransports struct {
	byMachine map[string]*fakeProber
}

func (f fakeTransports) For(_ context.Context, machine store.Machine) (Prober, error) {
	prober, found := f.byMachine[machine.ID]
	if !found {
		return nil, errors.New("machine non enrôlée")
	}
	return prober, nil
}

func newTestStore(t *testing.T) *store.Store {
	t.Helper()

	root, err := os.OpenRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { root.Close() })

	database, err := store.Open(context.Background(), root, migrations.Files, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	return database
}

func newTestChecker(t *testing.T, database *store.Store, probers map[string]*fakeProber) *Checker {
	t.Helper()

	checker := New(database, fakeTransports{byMachine: probers}, time.Millisecond, slog.New(slog.DiscardHandler))
	checker.now = func() time.Time { return time.Date(2026, 9, 6, 10, 0, 0, 0, time.UTC) }
	return checker
}

// répondant : la machine répond, et son lanceur refuse comme il doit le faire
// appelé sans identifiant.
func respondingProber() *fakeProber {
	return &fakeProber{reply: transport.LauncherReply{ExitCode: 2, Output: "usage: oc-launch <id>"}}
}

func machineState(t *testing.T, database *store.Store, id string) store.Machine {
	t.Helper()

	machine, err := database.Machine(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return machine
}

func TestNow_UneMachineQuiRepondEtDontLeLanceurRefuseEstJoignable(t *testing.T) {
	database := newTestStore(t)
	checker := newTestChecker(t, database, map[string]*fakeProber{store.LocalMachineID: respondingProber()})

	if err := checker.Now(context.Background(), store.LocalMachineID); err != nil {
		t.Fatalf("Now : %v", err)
	}

	machine := machineState(t, database, store.LocalMachineID)
	if machine.ProbeState != store.ProbeReachable {
		t.Errorf("état = %q, attendu %q", machine.ProbeState, store.ProbeReachable)
	}
	if machine.ProbeNote != "" {
		t.Errorf("note = %q, attendue vide", machine.ProbeNote)
	}
	if !machine.ProbedAt.Equal(time.Date(2026, 9, 6, 10, 0, 0, 0, time.UTC)) {
		t.Errorf("date = %s", machine.ProbedAt)
	}
}

func TestNow_UneMachineInjoignableEstEnEchecSSHEtLaNoteLeDit(t *testing.T) {
	database := newTestStore(t)
	prober := respondingProber()
	prober.probeErr = errors.New("connexion refusée")
	checker := newTestChecker(t, database, map[string]*fakeProber{store.LocalMachineID: prober})

	if err := checker.Now(context.Background(), store.LocalMachineID); err != nil {
		t.Fatalf("Now : %v", err)
	}

	machine := machineState(t, database, store.LocalMachineID)
	if machine.ProbeState != store.ProbeSSHFailed {
		t.Errorf("état = %q, attendu %q", machine.ProbeState, store.ProbeSSHFailed)
	}
	if !strings.Contains(machine.ProbeNote, "connexion refusée") {
		t.Errorf("note = %q", machine.ProbeNote)
	}
}

func TestNow_UnLanceurQuiNeRefusePasEstUnEchecDuLanceur(t *testing.T) {
	database := newTestStore(t)
	prober := respondingProber()
	prober.reply = transport.LauncherReply{ExitCode: 0}
	checker := newTestChecker(t, database, map[string]*fakeProber{store.LocalMachineID: prober})

	if err := checker.Now(context.Background(), store.LocalMachineID); err != nil {
		t.Fatalf("Now : %v", err)
	}

	machine := machineState(t, database, store.LocalMachineID)
	if machine.ProbeState != store.ProbeLauncherFailed {
		t.Errorf("état = %q, attendu %q", machine.ProbeState, store.ProbeLauncherFailed)
	}
	if !strings.Contains(machine.ProbeNote, "répond 0 au lieu de 2") {
		t.Errorf("note = %q", machine.ProbeNote)
	}
}

func TestNow_UneNoteTropLongueEstBornee(t *testing.T) {
	database := newTestStore(t)
	prober := respondingProber()
	prober.probeErr = errors.New(strings.Repeat("a", 5*maxNoteBytes))
	checker := newTestChecker(t, database, map[string]*fakeProber{store.LocalMachineID: prober})

	if err := checker.Now(context.Background(), store.LocalMachineID); err != nil {
		t.Fatalf("Now : %v", err)
	}

	machine := machineState(t, database, store.LocalMachineID)
	if len(machine.ProbeNote) > maxNoteBytes+20 {
		t.Errorf("note de %d octets, non bornée", len(machine.ProbeNote))
	}
}

func TestNow_UneMachineNonEnroleeNestPasSondee(t *testing.T) {
	database := newTestStore(t)
	checker := newTestChecker(t, database, nil)

	if err := checker.Now(context.Background(), store.LocalMachineID); err == nil {
		t.Fatal("le bouton doit dire ce qui l'a empêché")
	}

	machine := machineState(t, database, store.LocalMachineID)
	if machine.ProbeState != "" || !machine.ProbedAt.IsZero() {
		t.Errorf("une machine non enrôlée ne se sonde pas : %+v", machine)
	}
}

func TestRun_SondeChaqueMachineEnroleeEtLaisseLesAutresIntactes(t *testing.T) {
	database := newTestStore(t)
	declareMachine(t, database, "temoin")
	prober := respondingProber()
	checker := newTestChecker(t, database, map[string]*fakeProber{store.LocalMachineID: prober})

	ctx, stop := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		checker.Run(ctx)
		close(done)
	}()

	waitFor(t, func() bool {
		return machineState(t, database, store.LocalMachineID).ProbeState == store.ProbeReachable
	})
	stop()
	<-done

	if got := machineState(t, database, "temoin"); got.ProbeState != "" {
		t.Errorf("la machine non enrôlée a été sondée : %+v", got)
	}
}

func TestNow_PendantLaBoucle_NeSondeJamaisDeuxFoisLaMemeMachineALaFois(t *testing.T) {
	database := newTestStore(t)
	prober := respondingProber()
	prober.entered = make(chan struct{})
	prober.release = make(chan struct{})
	checker := newTestChecker(t, database, map[string]*fakeProber{store.LocalMachineID: prober})
	// Un seul balayage : celui du démarrage. Le suivant n'arrivera jamais.
	checker.interval = time.Hour

	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	go checker.Run(ctx)

	// La boucle tient la machine : le bouton attend, il ne double pas la sonde.
	<-prober.entered

	pressed := make(chan error, 1)
	go func() { pressed <- checker.Now(context.Background(), store.LocalMachineID) }()

	time.Sleep(20 * time.Millisecond)
	if prober.count() != 1 {
		t.Fatalf("%d sondes en même temps sur la même machine", prober.count())
	}

	prober.release <- struct{}{}
	<-prober.entered
	prober.release <- struct{}{}
	if err := <-pressed; err != nil {
		t.Fatalf("Now : %v", err)
	}
	if prober.count() != 2 {
		t.Errorf("%d sondes, attendu celle de la boucle puis celle du bouton", prober.count())
	}
}

func declareMachine(t *testing.T, database *store.Store, id string) {
	t.Helper()

	machine := store.Machine{
		ID: id, Name: id, Address: "192.168.1.10", Port: 22,
		Account: "opencloud", CreatedAt: time.Now().UTC(),
	}
	if err := database.InsertMachine(context.Background(), machine); err != nil {
		t.Fatal(err)
	}
}

func waitFor(t *testing.T, condition func() bool) {
	t.Helper()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("la condition attendue n'est jamais arrivée")
}
