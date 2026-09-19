package service_test

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ldesfontaine/opencloud/internal/machine"
	"github.com/ldesfontaine/opencloud/internal/service"
	"github.com/ldesfontaine/opencloud/internal/store"
)

var testNow = time.Date(2026, 9, 19, 9, 0, 0, 0, time.UTC)

const (
	webID = "0082fc783011ead4e11be27f298f845fc39b25b371c0247ed7b7a798b8388513"
	dbID  = "1111111111111111111111111111111111111111111111111111111111111111"
)

type bench struct {
	tracker *service.Tracker
	db      *store.DB
	now     time.Time
	changed []string
	mu      sync.Mutex
}

func (b *bench) ServicesChanged(machineID string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.changed = append(b.changed, machineID)
}

// Le composant se teste sur la vraie base SQLite, temporaire, avec la
// machine openCloud en place et une horloge qu'on avance à la main.
func newBench(t *testing.T) *bench {
	t.Helper()
	root, err := os.OpenRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { root.Close() })
	db, err := store.Open(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	err = db.SaveLocalMachine(context.Background(), machine.Machine{
		ID: machine.LocalID, Name: "opencloud", Kind: machine.KindLocal, EnrolledAt: testNow, CreatedAt: testNow,
	})
	if err != nil {
		t.Fatal(err)
	}
	b := &bench{db: db, now: testNow}
	b.tracker = service.New(db, slog.New(slog.NewTextHandler(os.Stderr, nil)))
	b.tracker.SetClock(func() time.Time { return b.now })
	b.tracker.SetWatcher(b)
	return b
}

func (b *bench) record(t *testing.T, report service.Report) {
	t.Helper()
	if err := b.tracker.Record(context.Background(), machine.LocalID, report); err != nil {
		t.Fatal(err)
	}
}

func (b *bench) get(t *testing.T, containerID string) service.Service {
	t.Helper()
	item, err := b.tracker.Get(context.Background(), service.ID(machine.LocalID, containerID))
	if err != nil {
		t.Fatal(err)
	}
	return item
}

func (b *bench) transitions(t *testing.T, containerID string) []service.Transition {
	t.Helper()
	transitions, err := b.tracker.Transitions(context.Background(), service.ID(machine.LocalID, containerID), 50)
	if err != nil {
		t.Fatal(err)
	}
	return transitions
}

func running(id, name, group string) service.Container {
	started := testNow.Add(-time.Hour)
	return service.Container{
		ContainerID: id, Name: name, Group: group, Image: "nextcloud:29.0.4", ImageID: "sha256:abc",
		State: service.StateRunning, RestartCount: 0,
		Ports:     []service.Port{{IP: "127.0.0.1", HostPort: 18080, ContainerPort: 80, Protocol: "tcp"}},
		CreatedAt: testNow.Add(-2 * time.Hour), StartedAt: &started,
	}
}

func inventory(containers ...service.Container) service.Report {
	return service.Report{
		Engine:    &service.EngineReport{Present: true, Version: "29.8.0", APIVersion: "1.56"},
		Complete:  true,
		Inventory: containers,
	}
}

func TestRecord_InventoryCreatesTheServicesAndTheirFirstTransition(t *testing.T) {
	b := newBench(t)
	b.record(t, inventory(running(webID, "web", "fixture"), running(dbID, "db", "fixture")))

	web := b.get(t, webID)
	if web.Name != "web" || web.Group != "fixture" || web.State != service.StateRunning || web.Kind != service.KindContainer {
		t.Fatalf("web = %+v", web)
	}
	if len(web.Ports) != 1 || web.Ports[0].HostPort != 18080 || web.StartedAt.IsZero() || !web.FinishedAt.IsZero() {
		t.Fatalf("web = %+v", web)
	}
	if web.FirstSeenAt != testNow || web.LastSeenAt != testNow || web.IsArchived() {
		t.Fatalf("seen = %s / %s", web.FirstSeenAt, web.LastSeenAt)
	}
	transitions := b.transitions(t, webID)
	if len(transitions) != 1 || transitions[0].Action != "inventory" || transitions[0].PreviousState != "" || transitions[0].NewState != service.StateRunning {
		t.Fatalf("transitions = %+v", transitions)
	}
	engines, _ := b.tracker.Engines(context.Background())
	if len(engines) != 1 || !engines[0].Present || engines[0].Version != "29.8.0" {
		t.Fatalf("engines = %+v", engines)
	}
	if strings.Join(b.changed, ",") != machine.LocalID {
		t.Fatalf("changed = %v", b.changed)
	}
}

func TestRecord_InventoryReplayed_ChangesNothing(t *testing.T) {
	b := newBench(t)
	b.record(t, inventory(running(webID, "web", "fixture")))
	b.now = b.now.Add(5 * time.Minute)
	b.record(t, inventory(running(webID, "web", "fixture")))

	web := b.get(t, webID)
	if web.FirstSeenAt != testNow || web.LastSeenAt != b.now {
		t.Fatalf("seen = %s / %s", web.FirstSeenAt, web.LastSeenAt)
	}
	if transitions := b.transitions(t, webID); len(transitions) != 1 {
		t.Fatalf("transitions = %+v", transitions)
	}
}

func TestRecord_InventoryWithoutAContainer_ArchivesIt(t *testing.T) {
	b := newBench(t)
	b.record(t, inventory(running(webID, "web", "fixture"), running(dbID, "db", "fixture")))
	b.now = b.now.Add(5 * time.Minute)
	b.record(t, inventory(running(webID, "web", "fixture")))

	db := b.get(t, dbID)
	if db.ArchivedAt != b.now {
		t.Fatalf("archived = %s", db.ArchivedAt)
	}
	live, _ := b.tracker.List(context.Background(), machine.LocalID)
	if len(live) != 1 || live[0].Name != "web" {
		t.Fatalf("live = %+v", live)
	}
	transitions := b.transitions(t, dbID)
	if len(transitions) != 2 || transitions[0].Action != "gone" || transitions[0].NewState != "" || transitions[0].PreviousState != service.StateRunning {
		t.Fatalf("transitions = %+v", transitions)
	}
}

func TestRecord_EmptyCompleteInventory_ArchivesEverything(t *testing.T) {
	b := newBench(t)
	b.record(t, inventory(running(webID, "web", "fixture")))
	b.record(t, inventory())
	if live, _ := b.tracker.List(context.Background(), ""); len(live) != 0 {
		t.Fatalf("live = %+v", live)
	}
}

func TestRecord_DieThenStart_WritesBothTransitionsInOrder(t *testing.T) {
	b := newBench(t)
	b.record(t, inventory(running(webID, "web", "fixture")))
	exitCode := 1
	restarted := running(webID, "web", "fixture")
	restarted.RestartCount = 1
	// Docker garde le dernier code de sortie sur un conteneur relancé.
	restarted.ExitCode = 1
	b.record(t, service.Report{Events: []service.Event{
		{At: testNow.Add(time.Second), Action: "die", ContainerID: webID, State: service.StateExited, ExitCode: &exitCode, Snippet: "panic: boom", Container: &restarted},
		{At: testNow.Add(2 * time.Second), Action: "start", ContainerID: webID, State: service.StateRunning, Container: &restarted},
	}})

	web := b.get(t, webID)
	if web.State != service.StateRunning || web.RestartCount != 1 || web.ExitCode != 1 {
		t.Fatalf("web = %+v", web)
	}
	transitions := b.transitions(t, webID)
	if len(transitions) != 3 {
		t.Fatalf("transitions = %+v", transitions)
	}
	die := transitions[1]
	if die.Action != "die" || die.PreviousState != service.StateRunning || die.NewState != service.StateExited || die.ExitCode == nil || *die.ExitCode != 1 || die.Snippet != "panic: boom" {
		t.Fatalf("die = %+v", die)
	}
	if start := transitions[0]; start.Action != "start" || start.PreviousState != service.StateExited || start.NewState != service.StateRunning {
		t.Fatalf("start = %+v", start)
	}
}

func TestRecord_EventWithoutStateChange_LeavesNoTransition(t *testing.T) {
	b := newBench(t)
	b.record(t, inventory(running(webID, "web", "fixture")))
	renamed := running(webID, "web2", "fixture")
	b.record(t, service.Report{Events: []service.Event{{At: testNow, Action: "rename", ContainerID: webID, Container: &renamed}}})
	if web := b.get(t, webID); web.Name != "web2" {
		t.Fatalf("web = %+v", web)
	}
	if transitions := b.transitions(t, webID); len(transitions) != 1 {
		t.Fatalf("transitions = %+v", transitions)
	}
}

func TestRecord_HealthChange_IsATransitionOfItsOwn(t *testing.T) {
	b := newBench(t)
	healthy := running(webID, "web", "")
	healthy.Health = service.HealthHealthy
	b.record(t, inventory(healthy))
	b.record(t, service.Report{Events: []service.Event{{At: testNow, Action: "health_status", ContainerID: webID, Health: service.HealthUnhealthy}}})
	web := b.get(t, webID)
	if web.Health != service.HealthUnhealthy || web.State != service.StateRunning || !service.NeedsAttention(web) {
		t.Fatalf("web = %+v", web)
	}
	transitions := b.transitions(t, webID)
	if transitions[0].PreviousHealth != service.HealthHealthy || transitions[0].NewHealth != service.HealthUnhealthy || transitions[0].NewState != service.StateRunning {
		t.Fatalf("transition = %+v", transitions[0])
	}
}

func TestRecord_DestroyArchivesAndAReplayedEventIsMarked(t *testing.T) {
	b := newBench(t)
	b.record(t, inventory(running(webID, "web", "")))
	b.record(t, service.Report{Events: []service.Event{{At: testNow.Add(time.Minute), Action: service.ActionDestroy, ContainerID: webID, Replayed: true}}})
	web := b.get(t, webID)
	if web.ArchivedAt != testNow.Add(time.Minute) {
		t.Fatalf("web = %+v", web)
	}
	transitions := b.transitions(t, webID)
	if transitions[0].Action != "destroy" || !transitions[0].Replayed || transitions[0].NewState != "" {
		t.Fatalf("transition = %+v", transitions[0])
	}
	// Un second destroy ne fait rien.
	b.record(t, service.Report{Events: []service.Event{{At: testNow.Add(2 * time.Minute), Action: service.ActionDestroy, ContainerID: webID}}})
	if transitions := b.transitions(t, webID); len(transitions) != 2 {
		t.Fatalf("transitions = %+v", transitions)
	}
}

func TestRecord_UnknownContainerEventWithoutSnapshot_IsIgnored(t *testing.T) {
	b := newBench(t)
	b.record(t, service.Report{Events: []service.Event{{At: testNow, Action: "start", ContainerID: webID, State: service.StateRunning}}})
	if _, err := b.tracker.Get(context.Background(), service.ID(machine.LocalID, webID)); !errors.Is(err, service.ErrNotFound) {
		t.Fatalf("err = %v", err)
	}
	if len(b.changed) != 1 {
		t.Fatalf("changed = %v", b.changed)
	}
}

func TestRecord_StatsBecomeTheCurrentValue(t *testing.T) {
	b := newBench(t)
	b.record(t, inventory(running(webID, "web", "")))
	b.record(t, service.Report{Stats: []service.Stat{
		{ContainerID: webID, SampledAt: testNow.Add(-30 * time.Second), CPUPercent: 12.5, MemUsed: 1 << 30, MemLimit: 8 << 30},
		{ContainerID: webID, SampledAt: testNow, CPUPercent: 15, MemUsed: 2 << 30, MemLimit: 8 << 30},
		{ContainerID: dbID, SampledAt: testNow, CPUPercent: 1, MemUsed: 1, MemLimit: 1},
	}})
	currents, err := b.tracker.CurrentAll(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(currents) != 1 || !currents[0].Available || currents[0].Sample.CPUPercent != 15 || currents[0].Sample.MemUsed != 2<<30 {
		t.Fatalf("currents = %+v", currents)
	}
	b.now = b.now.Add(2 * time.Minute)
	currents, _ = b.tracker.CurrentAll(context.Background())
	if currents[0].Available {
		t.Fatal("a two-minute-old sample is stale")
	}
}

func TestRecord_RefusesAnOutOfRangeReport(t *testing.T) {
	b := newBench(t)
	bad := running("short", "web", "")
	err := b.tracker.Record(context.Background(), machine.LocalID, inventory(bad))
	if !errors.Is(err, service.ErrReportInvalid) {
		t.Fatalf("err = %v", err)
	}
	absent := service.Report{Engine: &service.EngineReport{Present: false}}
	if err := b.tracker.Record(context.Background(), machine.LocalID, absent); !errors.Is(err, service.ErrReportInvalid) {
		t.Fatalf("err = %v", err)
	}
	if len(b.changed) != 0 {
		t.Fatalf("changed = %v", b.changed)
	}
}

func TestRecord_EngineAbsent_IsAFactNotAnError(t *testing.T) {
	b := newBench(t)
	b.record(t, service.Report{Engine: &service.EngineReport{Present: false, Reason: service.ReasonNoSocket}})
	engines, _ := b.tracker.Engines(context.Background())
	if len(engines) != 1 || engines[0].Present || engines[0].Reason != service.ReasonNoSocket {
		t.Fatalf("engines = %+v", engines)
	}
}

func TestCount_CountsLiveServicesAndThoseNeedingAttention(t *testing.T) {
	b := newBench(t)
	crashed := running(dbID, "db", "")
	crashed.State, crashed.ExitCode = service.StateExited, 1
	stopped := running("2222222222222222222222222222222222222222222222222222222222222222", "old", "")
	stopped.State, stopped.ExitCode = service.StateExited, 143
	b.record(t, inventory(running(webID, "web", ""), crashed, stopped))
	total, attention, err := b.tracker.Count(context.Background())
	if err != nil || total != 3 || attention != 1 {
		t.Fatalf("total = %d attention = %d err = %v", total, attention, err)
	}
}

func TestPurge_DropsOldTransitionsArchivedServicesAndSamples(t *testing.T) {
	b := newBench(t)
	b.record(t, inventory(running(webID, "web", ""), running(dbID, "db", "")))
	b.record(t, service.Report{Stats: []service.Stat{{ContainerID: webID, SampledAt: testNow, CPUPercent: 1, MemUsed: 1, MemLimit: 1}}})
	b.record(t, service.Report{Events: []service.Event{{At: testNow, Action: service.ActionDestroy, ContainerID: dbID}}})
	b.now = testNow.Add(31 * 24 * time.Hour)
	if err := b.tracker.Purge(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := b.tracker.Get(context.Background(), service.ID(machine.LocalID, dbID)); !errors.Is(err, service.ErrNotFound) {
		t.Fatalf("archived service should be gone: %v", err)
	}
	if transitions := b.transitions(t, webID); len(transitions) != 1 {
		t.Fatalf("a 31-day-old transition of a live service stays: %+v", transitions)
	}
	if currents, _ := b.tracker.CurrentAll(context.Background()); len(currents) != 0 {
		t.Fatalf("samples should be purged: %+v", currents)
	}
	b.now = testNow.Add(91 * 24 * time.Hour)
	_ = b.tracker.Purge(context.Background())
	if transitions := b.transitions(t, webID); len(transitions) != 0 {
		t.Fatalf("transitions = %+v", transitions)
	}
}

func TestDeleteMachine_TakesItsServicesAlong(t *testing.T) {
	b := newBench(t)
	remote := machine.Machine{ID: "11111111-1111-4111-8111-111111111111", Name: "vps", Kind: machine.KindRemote, PublicKey: make([]byte, 32), EnrolledAt: testNow, CreatedAt: testNow}
	if err := b.db.InsertToken(context.Background(), machine.Token{ID: "t1", Hash: "h", Prefix: "p", Name: "vps", CreatedAt: testNow, ExpiresAt: testNow.Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	if _, err := b.db.Enroll(context.Background(), "h", remote, testNow); err != nil {
		t.Fatal(err)
	}
	if err := b.tracker.Record(context.Background(), remote.ID, inventory(running(webID, "web", ""))); err != nil {
		t.Fatal(err)
	}
	if err := b.db.DeleteMachine(context.Background(), remote.ID); err != nil {
		t.Fatal(err)
	}
	if live, _ := b.tracker.List(context.Background(), ""); len(live) != 0 {
		t.Fatalf("live = %+v", live)
	}
	if engines, _ := b.tracker.Engines(context.Background()); len(engines) != 0 {
		t.Fatalf("engines = %+v", engines)
	}
}
