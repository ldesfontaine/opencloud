package container_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ldesfontaine/opencloud/internal/container"
	"github.com/ldesfontaine/opencloud/internal/directory"
	"github.com/ldesfontaine/opencloud/internal/machine"
)

// Un annuaire de pacotille : il rattache selon le nom du conteneur, compte
// ses appels et peut échouer.
type fakeUnits struct {
	mu      sync.Mutex
	byName  map[string]container.UnitMatch
	calls   int
	seen    []int
	err     error
	delay   time.Duration
	running atomic.Int32
	overlap atomic.Bool
}

func (f *fakeUnits) MatchUnits(_ context.Context, _ string, containers []container.Container) (map[string]container.UnitMatch, error) {
	if f.running.Add(1) > 1 {
		f.overlap.Store(true)
	}
	defer f.running.Add(-1)
	time.Sleep(f.delay)
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	f.seen = append(f.seen, len(containers))
	if f.err != nil {
		return nil, f.err
	}
	matches := map[string]container.UnitMatch{}
	for _, item := range containers {
		if match, ok := f.byName[item.Name]; ok {
			matches[item.ID] = match
		}
	}
	return matches, nil
}

func (f *fakeUnits) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

// declareUnit écrit un service déclaré sur la machine openCloud, avec une
// seule unité : c'est ce que la clé étrangère de la fiche attend.
func (b *bench) declareUnit(t *testing.T, name string) string {
	t.Helper()
	serviceID, unitID := "svc_"+name, "unit_"+name
	declared := directory.Service{
		ID: serviceID, MachineID: machine.LocalID, Name: name, Nature: directory.NatureApplication,
		Origin: directory.OriginContainer, DeclaredBy: directory.ByOperator, CreatedAt: testNow, UpdatedAt: testNow,
		Units: []directory.Unit{{ID: unitID, ServiceID: serviceID, Name: name, Kind: directory.UnitContainer, Image: "nextcloud", ContainerName: name}},
	}
	if err := b.db.InsertDeclaredService(context.Background(), declared); err != nil {
		t.Fatal(err)
	}
	return unitID
}

func TestRecord_WritesTheUnitOfEachLiveContainer(t *testing.T) {
	b := newBench(t)
	webUnit := b.declareUnit(t, "web")
	units := &fakeUnits{byName: map[string]container.UnitMatch{"web": {UnitID: webUnit, Level: container.MatchName}}}
	b.tracker.SetUnits(units)

	b.record(t, inventory(running(webID, "web", ""), running(dbID, "db", "")))

	if web := b.get(t, webID); web.UnitID != webUnit || web.MatchLevel != container.MatchName {
		t.Fatalf("web = %+v", web)
	}
	if db := b.get(t, dbID); db.UnitID != "" || db.MatchLevel != container.MatchNone {
		t.Fatalf("db = %+v", db)
	}
	// Un événement sur la fiche, sans inventaire, garde le rattachement.
	b.record(t, container.Report{Events: []container.Event{{At: testNow, Action: "health_status", ContainerID: webID, Health: container.HealthHealthy}}})
	if web := b.get(t, webID); web.UnitID != webUnit || web.MatchLevel != container.MatchName {
		t.Fatalf("après un événement, web = %+v", web)
	}
}

// Un rapport qui ne porte aucune fiche vivante ne dérange pas l'annuaire :
// des mesures, ou la destruction d'un conteneur.
func TestRecord_DoesNotAskTheDirectoryWithoutALiveContainer(t *testing.T) {
	b := newBench(t)
	units := &fakeUnits{}
	b.tracker.SetUnits(units)

	b.record(t, container.Report{Engine: &container.EngineReport{Present: true}})
	b.record(t, container.Report{Stats: []container.Stat{{ContainerID: webID, SampledAt: testNow, CPUPercent: 1, MemUsed: 1, MemLimit: 1}}})
	if units.callCount() != 0 {
		t.Fatalf("%d appels pour des rapports sans fiche", units.callCount())
	}
	b.record(t, inventory(running(webID, "web", "")))
	b.record(t, container.Report{Events: []container.Event{{At: testNow, Action: container.ActionDestroy, ContainerID: webID}}})
	if units.callCount() != 1 {
		t.Fatalf("%d appels, un seul pour l'inventaire", units.callCount())
	}
}

func TestRecord_MatchErrorKeepsThePreviousUnit(t *testing.T) {
	b := newBench(t)
	webUnit := b.declareUnit(t, "web")
	units := &fakeUnits{byName: map[string]container.UnitMatch{"web": {UnitID: webUnit, Level: container.MatchCompose}}}
	b.tracker.SetUnits(units)
	b.record(t, inventory(running(webID, "web", "")))

	units.err = errors.New("annuaire illisible")
	exited := running(webID, "web", "")
	exited.State = container.StateExited
	b.record(t, inventory(exited, running(dbID, "db", "")))

	web := b.get(t, webID)
	if web.State != container.StateExited {
		t.Fatalf("le rapport doit passer malgré l'échec: %+v", web)
	}
	if web.UnitID != webUnit || web.MatchLevel != container.MatchCompose {
		t.Fatalf("le rattachement précédent reste: %+v", web)
	}
	if db := b.get(t, dbID); db.UnitID != "" {
		t.Fatalf("db = %+v", db)
	}
}

func TestRecord_ArchivedContainerKeepsItsUnit(t *testing.T) {
	b := newBench(t)
	webUnit := b.declareUnit(t, "web")
	units := &fakeUnits{byName: map[string]container.UnitMatch{"web": {UnitID: webUnit, Level: container.MatchName}}}
	b.tracker.SetUnits(units)
	b.record(t, inventory(running(webID, "web", ""), running(dbID, "db", "")))

	b.now = b.now.Add(5 * time.Minute)
	b.record(t, inventory(running(dbID, "db", "")))

	web := b.get(t, webID)
	if !web.IsArchived() || web.UnitID != webUnit || web.MatchLevel != container.MatchName {
		t.Fatalf("web = %+v", web)
	}
	// Archivée, la fiche n'est plus recalculée : l'annuaire changerait d'avis
	// qu'elle garderait ce qu'elle était.
	units.byName["web"] = container.UnitMatch{}
	if err := b.tracker.Rematch(context.Background(), machine.LocalID); err != nil {
		t.Fatal(err)
	}
	if web := b.get(t, webID); web.UnitID != webUnit {
		t.Fatalf("après un nouveau rapprochement, web = %+v", web)
	}
}

func TestRematch_WritesOnlyWhatChangedAndTellsTheWatcher(t *testing.T) {
	b := newBench(t)
	webUnit := b.declareUnit(t, "web")
	units := &fakeUnits{byName: map[string]container.UnitMatch{}}
	b.tracker.SetUnits(units)
	b.record(t, inventory(running(webID, "web", ""), running(dbID, "db", "")))
	b.changed = nil

	// Rien n'a changé : rien n'est écrit, rien n'est annoncé.
	if err := b.tracker.Rematch(context.Background(), machine.LocalID); err != nil {
		t.Fatal(err)
	}
	if len(b.changed) != 0 {
		t.Fatalf("annoncé sans changement: %v", b.changed)
	}

	units.byName["web"] = container.UnitMatch{UnitID: webUnit, Level: container.MatchName}
	if err := b.tracker.Rematch(context.Background(), machine.LocalID); err != nil {
		t.Fatal(err)
	}
	if web := b.get(t, webID); web.UnitID != webUnit || web.MatchLevel != container.MatchName {
		t.Fatalf("web = %+v", web)
	}
	if len(b.changed) != 1 || b.changed[0] != machine.LocalID {
		t.Fatalf("changed = %v", b.changed)
	}

	// Le niveau seul qui change se réécrit aussi.
	b.changed = nil
	units.byName["web"] = container.UnitMatch{UnitID: webUnit, Level: container.MatchCompose}
	if err := b.tracker.Rematch(context.Background(), machine.LocalID); err != nil {
		t.Fatal(err)
	}
	if web := b.get(t, webID); web.MatchLevel != container.MatchCompose || len(b.changed) != 1 {
		t.Fatalf("web = %+v, changed = %v", web, b.changed)
	}

	// L'unité disparaît de l'annuaire : la clé étrangère rend la fiche non déclarée.
	if err := b.db.DeleteDeclaredService(context.Background(), "svc_web"); err != nil {
		t.Fatal(err)
	}
	if web := b.get(t, webID); web.UnitID != "" || web.MatchLevel != container.MatchNone {
		t.Fatalf("après la suppression, web = %+v", web)
	}
}

func TestRematch_WithoutADirectoryDoesNothing(t *testing.T) {
	b := newBench(t)
	b.record(t, inventory(running(webID, "web", "")))
	b.changed = nil
	if err := b.tracker.Rematch(context.Background(), machine.LocalID); err != nil {
		t.Fatal(err)
	}
	b.tracker.RematchAll(context.Background())
	if len(b.changed) != 0 {
		t.Fatalf("changed = %v", b.changed)
	}
}

func TestRematchAll_VisitsEachMachineOnce(t *testing.T) {
	b := newBench(t)
	units := &fakeUnits{}
	b.tracker.SetUnits(units)
	b.record(t, inventory(running(webID, "web", ""), running(dbID, "db", "")))

	b.tracker.RematchAll(context.Background())

	if units.callCount() != 2 || units.seen[1] != 2 {
		t.Fatalf("appels = %d, vus = %v: un pour l'inventaire, un pour la machine", units.callCount(), units.seen)
	}
}

// Un rapport et un nouveau rapprochement de la même machine ne se mêlent
// pas : le second lirait des fiches que le premier est en train de changer.
func TestRecordAndRematch_AreSerialisedPerMachine(t *testing.T) {
	b := newBench(t)
	units := &fakeUnits{delay: 5 * time.Millisecond}
	b.tracker.SetUnits(units)
	b.record(t, inventory(running(webID, "web", "")))

	var wg sync.WaitGroup
	for range 8 {
		wg.Add(2)
		go func() {
			defer wg.Done()
			_ = b.tracker.Record(context.Background(), machine.LocalID, inventory(running(webID, "web", "")))
		}()
		go func() {
			defer wg.Done()
			_ = b.tracker.Rematch(context.Background(), machine.LocalID)
		}()
	}
	wg.Wait()

	if units.overlap.Load() {
		t.Fatal("deux rapprochements de la même machine se sont chevauchés")
	}
}

// La politique de mise à jour vit sur l'unité : le conteneur recréé change
// d'identifiant, pas d'unité, et la lit sans rien avoir à recopier. Une fiche
// que rien ne rattache se suit.
func TestRecord_RecreatedContainerReadsThePolicyOfItsUnit(t *testing.T) {
	b := newBench(t)
	ctx := context.Background()
	webUnit := b.declareUnit(t, "web")
	units := &fakeUnits{byName: map[string]container.UnitMatch{"web": {UnitID: webUnit, Level: container.MatchName}}}
	b.tracker.SetUnits(units)
	b.record(t, inventory(running(webID, "web", ""), running(dbID, "db", "")))
	if err := b.db.SetUnitUpdatePolicy(ctx, webUnit, container.PolicyHold); err != nil {
		t.Fatal(err)
	}
	if web := b.get(t, webID); web.UpdatePolicy != container.PolicyHold || web.DeclaredServiceID != "svc_web" {
		t.Fatalf("web = %+v", web)
	}
	if db := b.get(t, dbID); db.UpdatePolicy != container.PolicyWatch || db.DeclaredServiceID != "" {
		t.Fatalf("un conteneur non déclaré se suit: %+v", db)
	}

	// Le conteneur est recréé sous un autre identifiant, au même nom.
	const recreatedID = "5ee4f5d1c2b3a49786950a1b2c3d4e5f60718293a4b5c6d7e8f9011223344556"
	b.now = b.now.Add(5 * time.Minute)
	b.record(t, inventory(running(recreatedID, "web", ""), running(dbID, "db", "")))

	recreated := b.get(t, recreatedID)
	if recreated.UnitID != webUnit || recreated.UpdatePolicy != container.PolicyHold {
		t.Fatalf("le conteneur recréé doit retrouver la politique de son unité: %+v", recreated)
	}
	if old := b.get(t, webID); !old.IsArchived() || old.UpdatePolicy != container.PolicyHold {
		t.Fatalf("la fiche archivée lit la même unité: %+v", old)
	}
}
