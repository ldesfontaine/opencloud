package container_test

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ldesfontaine/opencloud/internal/container"
	"github.com/ldesfontaine/opencloud/internal/machine"
	"github.com/ldesfontaine/opencloud/internal/seal"
	"github.com/ldesfontaine/opencloud/internal/store"
)

var testNow = time.Date(2026, 9, 19, 9, 0, 0, 0, time.UTC)

const (
	webID = "0082fc783011ead4e11be27f298f845fc39b25b371c0247ed7b7a798b8388513"
	dbID  = "1111111111111111111111111111111111111111111111111111111111111111"
)

type bench struct {
	tracker *container.Tracker
	db      *store.DB
	now     time.Time
	changed []string
	mu      sync.Mutex
}

func (b *bench) ContainersChanged(machineID string) {
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
	db.SetSealer(seal.Generate())
	t.Cleanup(func() { db.Close() })
	err = db.SaveLocalMachine(context.Background(), machine.Machine{
		ID: machine.LocalID, Name: "opencloud", Kind: machine.KindLocal, EnrolledAt: testNow, CreatedAt: testNow,
	})
	if err != nil {
		t.Fatal(err)
	}
	b := &bench{db: db, now: testNow}
	b.tracker = container.New(db, slog.New(slog.NewTextHandler(os.Stderr, nil)))
	b.tracker.SetClock(func() time.Time { return b.now })
	b.tracker.SetWatcher(b)
	return b
}

func (b *bench) record(t *testing.T, report container.Report) {
	t.Helper()
	if err := b.tracker.Record(context.Background(), machine.LocalID, report); err != nil {
		t.Fatal(err)
	}
}

func (b *bench) get(t *testing.T, containerID string) container.Container {
	t.Helper()
	item, err := b.tracker.Get(context.Background(), container.ID(machine.LocalID, containerID))
	if err != nil {
		t.Fatal(err)
	}
	return item
}

func (b *bench) transitions(t *testing.T, containerID string) []container.Transition {
	t.Helper()
	transitions, err := b.tracker.Transitions(context.Background(), container.ID(machine.LocalID, containerID), 50)
	if err != nil {
		t.Fatal(err)
	}
	return transitions
}

func running(id, name, group string) container.Observed {
	started := testNow.Add(-time.Hour)
	return container.Observed{
		ContainerID: id, Name: name, Group: group, Image: "nextcloud:29.0.4", ImageID: "sha256:abc",
		State: container.StateRunning, RestartCount: 0,
		Ports:     []container.Port{{IP: "127.0.0.1", HostPort: 18080, ContainerPort: 80, Protocol: "tcp"}},
		CreatedAt: testNow.Add(-2 * time.Hour), StartedAt: &started,
	}
}

func inventory(containers ...container.Observed) container.Report {
	return container.Report{
		Engine:    &container.EngineReport{Present: true, Version: "29.8.0", APIVersion: "1.56"},
		Complete:  true,
		Inventory: containers,
	}
}

func TestRecord_InventoryCreatesTheContainersAndTheirFirstTransition(t *testing.T) {
	b := newBench(t)
	b.record(t, inventory(running(webID, "web", "fixture"), running(dbID, "db", "fixture")))

	web := b.get(t, webID)
	if web.Name != "web" || web.Group != "fixture" || web.State != container.StateRunning || web.Kind != container.KindContainer {
		t.Fatalf("web = %+v", web)
	}
	if len(web.Ports) != 1 || web.Ports[0].HostPort != 18080 || web.StartedAt.IsZero() || !web.FinishedAt.IsZero() {
		t.Fatalf("web = %+v", web)
	}
	if web.FirstSeenAt != testNow || web.LastSeenAt != testNow || web.IsArchived() {
		t.Fatalf("seen = %s / %s", web.FirstSeenAt, web.LastSeenAt)
	}
	transitions := b.transitions(t, webID)
	if len(transitions) != 1 || transitions[0].Action != "inventory" || transitions[0].PreviousState != "" || transitions[0].NewState != container.StateRunning {
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
	if len(transitions) != 2 || transitions[0].Action != "gone" || transitions[0].NewState != "" || transitions[0].PreviousState != container.StateRunning {
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
	b.record(t, container.Report{Events: []container.Event{
		{At: testNow.Add(time.Second), Action: "die", ContainerID: webID, State: container.StateExited, ExitCode: &exitCode, Snippet: "panic: boom", Container: &restarted},
		{At: testNow.Add(2 * time.Second), Action: "start", ContainerID: webID, State: container.StateRunning, Container: &restarted},
	}})

	web := b.get(t, webID)
	if web.State != container.StateRunning || web.RestartCount != 1 || web.ExitCode != 1 {
		t.Fatalf("web = %+v", web)
	}
	transitions := b.transitions(t, webID)
	if len(transitions) != 3 {
		t.Fatalf("transitions = %+v", transitions)
	}
	die := transitions[1]
	if die.Action != "die" || die.PreviousState != container.StateRunning || die.NewState != container.StateExited || die.ExitCode == nil || *die.ExitCode != 1 || die.Snippet != "panic: boom" {
		t.Fatalf("die = %+v", die)
	}
	if start := transitions[0]; start.Action != "start" || start.PreviousState != container.StateExited || start.NewState != container.StateRunning {
		t.Fatalf("start = %+v", start)
	}
}

func TestRecord_EventWithoutStateChange_LeavesNoTransition(t *testing.T) {
	b := newBench(t)
	b.record(t, inventory(running(webID, "web", "fixture")))
	renamed := running(webID, "web2", "fixture")
	b.record(t, container.Report{Events: []container.Event{{At: testNow, Action: "rename", ContainerID: webID, Container: &renamed}}})
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
	healthy.Health = container.HealthHealthy
	b.record(t, inventory(healthy))
	b.record(t, container.Report{Events: []container.Event{{At: testNow, Action: "health_status", ContainerID: webID, Health: container.HealthUnhealthy}}})
	web := b.get(t, webID)
	if web.Health != container.HealthUnhealthy || web.State != container.StateRunning || !container.NeedsAttention(web) {
		t.Fatalf("web = %+v", web)
	}
	transitions := b.transitions(t, webID)
	if transitions[0].PreviousHealth != container.HealthHealthy || transitions[0].NewHealth != container.HealthUnhealthy || transitions[0].NewState != container.StateRunning {
		t.Fatalf("transition = %+v", transitions[0])
	}
}

func TestRecord_DestroyArchivesAndAReplayedEventIsMarked(t *testing.T) {
	b := newBench(t)
	b.record(t, inventory(running(webID, "web", "")))
	b.record(t, container.Report{Events: []container.Event{{At: testNow.Add(time.Minute), Action: container.ActionDestroy, ContainerID: webID, Replayed: true}}})
	web := b.get(t, webID)
	if web.ArchivedAt != testNow.Add(time.Minute) {
		t.Fatalf("web = %+v", web)
	}
	transitions := b.transitions(t, webID)
	if transitions[0].Action != "destroy" || !transitions[0].Replayed || transitions[0].NewState != "" {
		t.Fatalf("transition = %+v", transitions[0])
	}
	// Un second destroy ne fait rien.
	b.record(t, container.Report{Events: []container.Event{{At: testNow.Add(2 * time.Minute), Action: container.ActionDestroy, ContainerID: webID}}})
	if transitions := b.transitions(t, webID); len(transitions) != 2 {
		t.Fatalf("transitions = %+v", transitions)
	}
}

func TestRecord_UnknownContainerEventWithoutSnapshot_IsIgnored(t *testing.T) {
	b := newBench(t)
	b.record(t, container.Report{Events: []container.Event{{At: testNow, Action: "start", ContainerID: webID, State: container.StateRunning}}})
	if _, err := b.tracker.Get(context.Background(), container.ID(machine.LocalID, webID)); !errors.Is(err, container.ErrNotFound) {
		t.Fatalf("err = %v", err)
	}
	if len(b.changed) != 1 {
		t.Fatalf("changed = %v", b.changed)
	}
}

func TestRecord_StatsBecomeTheCurrentValue(t *testing.T) {
	b := newBench(t)
	b.record(t, inventory(running(webID, "web", "")))
	b.record(t, container.Report{Stats: []container.Stat{
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

// Les réseaux voyagent avec l'inventaire : ceux de la machine remplacent
// la liste précédente, ceux de chaque conteneur remplacent ses
// appartenances ; un événement réseau réinspecte sans transition.
func TestRecord_NetworksReplaceTheListAndTheAttachments(t *testing.T) {
	b := newBench(t)
	backNet, frontNet := strings.Repeat("3", 64), strings.Repeat("4", 64)
	web := running(webID, "web", "fixture")
	web.NetworkMode, web.Privileged = "fixture_back", true
	web.Networks = []container.Attachment{{NetworkID: backNet, Name: "fixture_back", IP: "172.20.0.2", Aliases: []string{"web"}}, {NetworkID: frontNet, Name: "fixture_front", IP: "172.22.0.2"}}
	web.DependsOn = []container.Dependency{{Name: "db", Source: container.DependencyCompose}}
	report := inventory(web, running(dbID, "db", "fixture"))
	report.NetworksComplete = true
	report.Networks = []container.NetworkReport{
		{NetworkID: backNet, Name: "fixture_back", Driver: "bridge", Internal: true, Group: "fixture"},
		{NetworkID: frontNet, Name: "fixture_front", Driver: "bridge", Group: "fixture"},
	}
	b.record(t, report)

	stored := b.get(t, webID)
	if stored.NetworkMode != "fixture_back" || !stored.Privileged || len(stored.Networks) != 2 || stored.Networks[0].Name != "fixture_back" || stored.Networks[0].Aliases[0] != "web" || stored.Networks[1].IP != "172.22.0.2" {
		t.Fatalf("web = %+v", stored)
	}
	if len(stored.DependsOn) != 1 || stored.DependsOn[0] != (container.Dependency{Name: "db", Source: container.DependencyCompose}) {
		t.Fatalf("depends = %+v", stored.DependsOn)
	}
	topology, err := b.tracker.Topology(context.Background(), machine.LocalID)
	if err != nil {
		t.Fatal(err)
	}
	if len(topology.Groups) != 1 || topology.Groups[0].Name != "fixture_back" || !topology.Groups[0].Internal || len(topology.Groups[0].Members) != 1 {
		t.Fatalf("groups = %+v", topology.Groups)
	}
	if len(topology.Edges) != 1 || topology.Edges[0].Kind != container.EdgeDepends || topology.Edges[0].To != container.ID(machine.LocalID, dbID) {
		t.Fatalf("edges = %+v", topology.Edges)
	}

	// Le conteneur quitte le réseau de devant ; la machine perd ce réseau.
	web.Networks = web.Networks[:1]
	b.now = b.now.Add(time.Minute)
	b.record(t, container.Report{Events: []container.Event{{At: b.now, Action: container.ActionNetworkDisconnect, ContainerID: webID, Container: &web}}})
	b.record(t, container.Report{NetworksComplete: true, Networks: report.Networks[:1]})
	stored = b.get(t, webID)
	if len(stored.Networks) != 1 || len(b.transitions(t, webID)) != 1 {
		t.Fatalf("after disconnect = %+v, transitions = %d", stored.Networks, len(b.transitions(t, webID)))
	}
	networks, err := b.db.ListNetworks(context.Background(), machine.LocalID)
	if err != nil || len(networks) != 1 || networks[0].Name != "fixture_back" {
		t.Fatalf("networks = %+v %v", networks, err)
	}
}

func TestRecord_KeepsAContainerWhoseNetworkHasNoIdentifier(t *testing.T) {
	b := newBench(t)
	web := running(webID, "web", "fixture")
	web.Networks = []container.Attachment{{NetworkID: "", Name: "bridge"}}
	b.record(t, inventory(web, running(dbID, "db", "fixture")))
	if got := b.get(t, webID); got.Name != "web" || len(got.Networks) != 0 {
		t.Fatalf("web = %+v", got)
	}
	if got := b.get(t, dbID); got.Name != "db" {
		t.Fatalf("db = %+v", got)
	}
}

func TestRecord_DiscardsTheInvalidContainerAndKeepsTheOthers(t *testing.T) {
	b := newBench(t)
	broken := running("short", "broken", "fixture")
	report := inventory(running(webID, "web", "fixture"), broken)
	report.NetworksComplete = true
	report.Networks = []container.NetworkReport{{NetworkID: dbID, Name: "", Driver: "bridge"}, {NetworkID: dbID, Name: "back", Driver: "bridge"}}
	b.record(t, report)
	if got := b.get(t, webID); got.Name != "web" {
		t.Fatalf("web = %+v", got)
	}
	containers, _ := b.tracker.List(context.Background(), machine.LocalID)
	if len(containers) != 1 {
		t.Fatalf("containers = %+v", containers)
	}
	networks, _ := b.db.ListNetworks(context.Background(), machine.LocalID)
	if len(networks) != 1 || networks[0].Name != "back" {
		t.Fatalf("networks = %+v", networks)
	}
}

func TestRecord_DiscardsWhatMakesBadNetworking(t *testing.T) {
	bad := func(mutate func(inspected *container.Observed)) container.Observed {
		web := running(webID, "web", "fixture")
		mutate(&web)
		return web
	}
	cases := map[string]container.Observed{
		"network id": bad(func(c *container.Observed) { c.Networks = []container.Attachment{{NetworkID: "short", Name: "x"}} }),
		"network ip": bad(func(c *container.Observed) {
			c.Networks = []container.Attachment{{NetworkID: dbID, Name: "x", IP: "not-an-ip"}}
		}),
		"empty alias": bad(func(c *container.Observed) {
			c.Networks = []container.Attachment{{NetworkID: dbID, Name: "x", Aliases: []string{""}}}
		}),
	}
	for name, inspected := range cases {
		b := newBench(t)
		b.record(t, inventory(inspected))
		if got := b.get(t, webID); len(got.Networks) != 0 {
			t.Errorf("%s: networks = %+v", name, got.Networks)
		}
	}
	b := newBench(t)
	dependency := bad(func(c *container.Observed) { c.DependsOn = []container.Dependency{{Name: "db", Source: "guess"}} })
	b.record(t, inventory(dependency, running(dbID, "db", "fixture")))
	containers, _ := b.tracker.List(context.Background(), machine.LocalID)
	if len(containers) != 1 || containers[0].Name != "db" {
		t.Fatalf("containers = %+v", containers)
	}
}

func TestRecord_RefusesAnOutOfRangeReport(t *testing.T) {
	b := newBench(t)
	var crowd []container.Observed
	for range container.MaxContainersPerReport + 1 {
		crowd = append(crowd, running(webID, "web", ""))
	}
	if err := b.tracker.Record(context.Background(), machine.LocalID, inventory(crowd...)); !errors.Is(err, container.ErrReportInvalid) {
		t.Fatalf("err = %v", err)
	}
	absent := container.Report{Engine: &container.EngineReport{Present: false}}
	if err := b.tracker.Record(context.Background(), machine.LocalID, absent); !errors.Is(err, container.ErrReportInvalid) {
		t.Fatalf("err = %v", err)
	}
	if len(b.changed) != 0 {
		t.Fatalf("changed = %v", b.changed)
	}
}

func TestRecord_EngineAbsent_IsAFactNotAnError(t *testing.T) {
	b := newBench(t)
	b.record(t, container.Report{Engine: &container.EngineReport{Present: false, Reason: container.ReasonNoSocket}})
	engines, _ := b.tracker.Engines(context.Background())
	if len(engines) != 1 || engines[0].Present || engines[0].Reason != container.ReasonNoSocket {
		t.Fatalf("engines = %+v", engines)
	}
}

func TestCount_CountsLiveContainersAndThoseNeedingAttention(t *testing.T) {
	b := newBench(t)
	crashed := running(dbID, "db", "")
	crashed.State, crashed.ExitCode = container.StateExited, 1
	stopped := running("2222222222222222222222222222222222222222222222222222222222222222", "old", "")
	stopped.State, stopped.ExitCode = container.StateExited, 143
	b.record(t, inventory(running(webID, "web", ""), crashed, stopped))
	total, attention, err := b.tracker.Count(context.Background())
	if err != nil || total != 3 || attention != 1 {
		t.Fatalf("total = %d attention = %d err = %v", total, attention, err)
	}
}

func TestPurge_DropsOldTransitionsArchivedContainersAndSamples(t *testing.T) {
	b := newBench(t)
	b.record(t, inventory(running(webID, "web", ""), running(dbID, "db", "")))
	b.record(t, container.Report{Stats: []container.Stat{{ContainerID: webID, SampledAt: testNow, CPUPercent: 1, MemUsed: 1, MemLimit: 1}}})
	b.record(t, container.Report{Events: []container.Event{{At: testNow, Action: container.ActionDestroy, ContainerID: dbID}}})
	b.now = testNow.Add(31 * 24 * time.Hour)
	if err := b.tracker.Purge(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := b.tracker.Get(context.Background(), container.ID(machine.LocalID, dbID)); !errors.Is(err, container.ErrNotFound) {
		t.Fatalf("archived container should be gone: %v", err)
	}
	if transitions := b.transitions(t, webID); len(transitions) != 1 {
		t.Fatalf("a 31-day-old transition of a live container stays: %+v", transitions)
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

func TestDeleteMachine_TakesItsContainersAlong(t *testing.T) {
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
