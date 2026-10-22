package probe_test

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ldesfontaine/opencloud/internal/directory"
	"github.com/ldesfontaine/opencloud/internal/machine"
	"github.com/ldesfontaine/opencloud/internal/probe"
	"github.com/ldesfontaine/opencloud/internal/store"
	"github.com/ldesfontaine/opencloud/internal/store/storetest"
)

var testNow = time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC)

// Le service se teste sur la vraie base SQLite, temporaire, avec une
// horloge qu'on avance à la main et un moteur qui note ce qu'on lui donne.
type bench struct {
	service *probe.Service
	db      *store.DB
	now     time.Time
	runner  *recordingRunner
	changed []string
	mu      sync.Mutex
}

// recordingRunner tient lieu du flux de l'agent : il garde chaque jeu de
// sondes poussé, quelle que soit la machine.
type recordingRunner struct {
	mu          sync.Mutex
	assignments []probe.Assignment
}

func (r *recordingRunner) AssignProbes(_ string, assignment probe.Assignment) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.assignments = append(r.assignments, assignment)
	return nil
}

func (r *recordingRunner) last() probe.Assignment {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.assignments) == 0 {
		return probe.Assignment{}
	}
	return r.assignments[len(r.assignments)-1]
}

func (b *bench) ProbesChanged(machineID string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.changed = append(b.changed, machineID)
}

func newBench(t *testing.T) *bench {
	t.Helper()
	return newBenchLogging(t, slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError})))
}

func newBenchLogging(t *testing.T, logger *slog.Logger) *bench {
	t.Helper()
	db := storetest.Open(t)
	machines := machine.New(db, machine.NewSessions(), logger)
	storetest.EnsureLocalMachine(t, machines)
	fixture := &bench{db: db, now: testNow, runner: &recordingRunner{}}
	fixture.service = probe.New(db, logger)
	fixture.service.SetClock(func() time.Time { return fixture.now })
	fixture.service.SetWatcher(fixture)
	fixture.service.SetCommander(fixture.runner)
	return fixture
}

func (b *bench) create(t *testing.T, name, target string) probe.Probe {
	t.Helper()
	created, err := b.service.Create(context.Background(), probe.Definition{
		Name: name, Kind: probe.KindHTTP, Target: target, MachineID: machine.LocalID,
	})
	if err != nil {
		t.Fatalf("créer la sonde: %v", err)
	}
	return created
}

func (b *bench) record(t *testing.T, id string, at time.Time, outcome probe.Outcome) {
	t.Helper()
	b.recordResult(t, probe.Result{ProbeID: id, CheckedAt: at, Outcome: outcome, DurationMs: 42})
}

func (b *bench) recordResult(t *testing.T, result probe.Result) {
	t.Helper()
	if err := b.service.Record(context.Background(), machine.LocalID, probe.Report{Results: []probe.Result{result}}); err != nil {
		t.Fatalf("écrire l'essai: %v", err)
	}
}

func (b *bench) get(t *testing.T, id string) probe.Probe {
	t.Helper()
	found, err := b.db.GetProbe(context.Background(), id)
	if err != nil {
		t.Fatalf("relire la sonde: %v", err)
	}
	return found
}

func TestCreate_StartsNewAndHandsTheProbeToItsMachine(t *testing.T) {
	fixture := newBench(t)
	created := fixture.create(t, "site", "https://cloud.exemple.fr/")
	if created.Status != probe.StatusNew {
		t.Fatalf("attendu %q, obtenu %q", probe.StatusNew, created.Status)
	}
	if created.Interval != probe.DefaultInterval || created.Method != "GET" || created.ExpectedStatus != "2xx" {
		t.Fatalf("les défauts ne sont pas posés: %+v", created)
	}
	assignment := fixture.runner.last()
	if len(assignment.Probes) != 1 || assignment.Probes[0].ID != created.ID {
		t.Fatalf("le jeu poussé ne porte pas la sonde: %+v", assignment)
	}
}

func TestCreate_RefusesMoreProbesThanAMachineCarries(t *testing.T) {
	fixture := newBench(t)
	for index := 0; index < probe.MaxProbesPerMachine; index++ {
		fixture.create(t, "sonde", "https://cloud.exemple.fr/")
	}
	_, err := fixture.service.Create(context.Background(), probe.Definition{
		Name: "une de trop", Kind: probe.KindHTTP, Target: "https://a.fr/", MachineID: machine.LocalID,
	})
	if !errors.Is(err, probe.ErrTooMany) {
		t.Fatalf("attendu %v, obtenu %v", probe.ErrTooMany, err)
	}
}

// Des identifiants dans la cible finiraient en clair en base et dans le
// fichier de l'annuaire : la création les refuse, rien n'est écrit.
func TestCreate_RefusesATargetWithCredentials(t *testing.T) {
	fixture := newBench(t)
	_, err := fixture.service.Create(context.Background(), probe.Definition{
		Name: "api", Kind: probe.KindHTTP, Target: "https://svc:secret@api.exemple.fr/health", MachineID: machine.LocalID,
	})
	if !errors.Is(err, probe.ErrTargetCredentials) {
		t.Fatalf("attendu %v, obtenu %v", probe.ErrTargetCredentials, err)
	}
	if probes, err := fixture.db.ListProbes(context.Background(), ""); err != nil || len(probes) != 0 {
		t.Fatalf("rien ne devait être écrit: %+v %v", probes, err)
	}
}

// Une sonde d'avant le refus, qui porte des identifiants, passe en pause au
// démarrage et n'en sort qu'avec une cible changée ; les autres ne bougent pas.
func TestPauseTargetsWithCredentials_HoldsOldProbesUntilTheTargetChanges(t *testing.T) {
	fixture := newBench(t)
	ctx := context.Background()
	clean := fixture.create(t, "site", "https://cloud.exemple.fr/")
	legacy := storetest.InsertProbe(t, fixture.db, "00000000000000a1", probe.Definition{
		Name: "api", Kind: probe.KindHTTP, Target: "https://svc:secret@api.exemple.fr/health", MachineID: machine.LocalID,
	}, testNow)
	if err := fixture.service.PauseTargetsWithCredentials(ctx); err != nil {
		t.Fatal(err)
	}
	if got := fixture.get(t, legacy.ID); !got.IsPaused() {
		t.Fatalf("la sonde à identifiants devait être en pause: %q", got.Status)
	}
	if got := fixture.get(t, clean.ID); got.IsPaused() {
		t.Fatal("la sonde sans identifiants ne devait pas bouger")
	}
	if assignment := fixture.runner.last(); len(assignment.Probes) != 1 || assignment.Probes[0].ID != clean.ID {
		t.Fatalf("le jeu ne devait plus porter que la sonde propre: %+v", assignment)
	}
	if err := fixture.service.Resume(ctx, legacy.ID); !errors.Is(err, probe.ErrTargetCredentials) {
		t.Fatalf("reprendre: attendu %v, obtenu %v", probe.ErrTargetCredentials, err)
	}
	definition := definitionOf(legacy)
	definition.Name = "api renommée"
	if _, err := fixture.service.Update(ctx, legacy.ID, definition); !errors.Is(err, probe.ErrTargetCredentials) {
		t.Fatalf("modifier sans changer la cible: attendu %v, obtenu %v", probe.ErrTargetCredentials, err)
	}
	definition.Target = "https://api.exemple.fr/health"
	fixture.update(t, legacy.ID, definition)
	if err := fixture.service.Resume(ctx, legacy.ID); err != nil {
		t.Fatalf("la cible changée, la sonde reprend: %v", err)
	}
	if err := fixture.service.PauseTargetsWithCredentials(ctx); err != nil {
		t.Fatal(err)
	}
	if got := fixture.get(t, legacy.ID); got.IsPaused() {
		t.Fatal("une cible changée ne se remet pas en pause au démarrage suivant")
	}
}

const remoteMachineID = "11111111-2222-4333-8444-555555555555"

// enrollRemote ajoute une seconde machine : une sonde peut surveiller un
// service déclaré sur une autre machine que la sienne.
func (b *bench) enrollRemote(t *testing.T) {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	machines := machine.New(b.db, machine.NewSessions(), logger)
	storetest.EnrollMachine(t, machines, remoteMachineID, "vps")
}

// addService écrit un service déclaré sur la machine donnée.
func (b *bench) addService(t *testing.T, id, machineID string) {
	t.Helper()
	err := b.db.InsertDeclaredService(context.Background(), directory.Service{
		ID: id, MachineID: machineID, Name: "nextcloud", Nature: directory.NatureApplication,
		Origin: directory.OriginContainer, DeclaredBy: directory.ByOperator, CreatedAt: testNow, UpdatedAt: testNow,
	})
	if err != nil {
		t.Fatalf("écrire le service: %v", err)
	}
}

func (b *bench) createWithService(machineID, serviceID string) (probe.Probe, error) {
	return b.service.Create(context.Background(), probe.Definition{
		Name: "site", Kind: probe.KindHTTP, Target: "https://cloud.exemple.fr/",
		MachineID: machineID, ServiceID: serviceID,
	})
}

func TestCreate_AttachesADeclaredServiceAndReadsItsMachine(t *testing.T) {
	fixture := newBench(t)
	fixture.addService(t, "svc_local", machine.LocalID)

	created, err := fixture.createWithService(machine.LocalID, "svc_local")
	if err != nil {
		t.Fatalf("créer la sonde: %v", err)
	}
	if created.ServiceID != "svc_local" || created.ServiceName != "nextcloud" || created.ServiceMachineName == "" {
		t.Fatalf("la sonde n'est pas rattachée: %+v", created)
	}
}

func TestCreate_UnknownDeclaredServiceRefused(t *testing.T) {
	fixture := newBench(t)

	_, err := fixture.createWithService(machine.LocalID, "inexistant")
	if !errors.Is(err, probe.ErrServiceInvalid) {
		t.Fatalf("attendu %v, obtenu %v", probe.ErrServiceInvalid, err)
	}
	if probes, _ := fixture.db.ListProbes(context.Background(), ""); len(probes) != 0 {
		t.Fatalf("une sonde refusée a été écrite: %+v", probes)
	}
}

// Le contrôle vit dans le composant : le fichier, qui crée une sonde sans
// passer par l'API, est tenu de même.
func TestCreate_UnknownMachineRefused(t *testing.T) {
	fixture := newBench(t)

	_, err := fixture.createWithService("inconnue", "")
	if !errors.Is(err, probe.ErrMachineInvalid) {
		t.Fatalf("attendu %v, obtenu %v", probe.ErrMachineInvalid, err)
	}
	if probes, _ := fixture.db.ListProbes(context.Background(), ""); len(probes) != 0 {
		t.Fatalf("une sonde refusée a été écrite: %+v", probes)
	}
}

// La sonde inconnue d'abord : introuvable, quoi que dise la définition.
func TestUpdate_UnknownMachineRefused(t *testing.T) {
	fixture := newBench(t)
	created := fixture.create(t, "site", "https://cloud.exemple.fr/")
	definition := definitionOf(created)
	definition.MachineID = "inconnue"

	if _, err := fixture.service.Update(context.Background(), created.ID, definition); !errors.Is(err, probe.ErrMachineInvalid) {
		t.Fatalf("attendu %v, obtenu %v", probe.ErrMachineInvalid, err)
	}
	if got := fixture.get(t, created.ID); got.MachineID != machine.LocalID {
		t.Fatalf("une édition refusée a été écrite: %+v", got)
	}
	if _, err := fixture.service.Update(context.Background(), "absente", definition); !errors.Is(err, probe.ErrNotFound) {
		t.Fatalf("attendu %v, obtenu %v", probe.ErrNotFound, err)
	}
}

// La sonde qui regarde de l'extérieur surveille un service qui tourne
// ailleurs : l'ancienne règle « la machine de la sonde porte le service »
// n'a plus lieu d'être.
func TestCreate_DeclaredServiceOfAnotherMachineAccepted(t *testing.T) {
	fixture := newBench(t)
	fixture.enrollRemote(t)
	fixture.addService(t, "svc_remote", remoteMachineID)

	created, err := fixture.createWithService(machine.LocalID, "svc_remote")
	if err != nil {
		t.Fatalf("le service d'une autre machine doit passer: %v", err)
	}
	if created.MachineID != machine.LocalID || created.ServiceID != "svc_remote" || created.ServiceMachineName != "vps" {
		t.Fatalf("sonde = %+v", created)
	}
}

func TestRecord_WritesTheResultAndTheState(t *testing.T) {
	fixture := newBench(t)
	created := fixture.create(t, "site", "https://cloud.exemple.fr/")
	fixture.record(t, created.ID, testNow, probe.OutcomeUp)

	after := fixture.get(t, created.ID)
	if after.Status != probe.StatusUp || after.ConsecutiveSuccesses != 1 || after.LastDurationMs != 42 {
		t.Fatalf("l'état n'a pas suivi l'essai: %+v", after)
	}
	results, err := fixture.db.ListProbeResults(context.Background(), created.ID, 10)
	if err != nil || len(results) != 1 || results[0].Outcome != probe.OutcomeUp {
		t.Fatalf("l'essai n'est pas dans l'histoire: %+v (%v)", results, err)
	}
}

// Rejoué après une coupure, un essai s'écrit et ne touche à rien d'autre.
func TestRecord_ReplayedResultWritesHistoryOnly(t *testing.T) {
	fixture := newBench(t)
	created := fixture.create(t, "site", "https://cloud.exemple.fr/")
	fixture.record(t, created.ID, testNow.Add(-time.Minute), probe.OutcomeUp)
	fixture.recordResult(t, probe.Result{ProbeID: created.ID, CheckedAt: testNow, Outcome: probe.OutcomeDown, Replayed: true})

	after := fixture.get(t, created.ID)
	if after.Status != probe.StatusUp || after.ConsecutiveFailures != 0 {
		t.Fatalf("l'essai rejoué a bougé l'état: %+v", after)
	}
	results, _ := fixture.db.ListProbeResults(context.Background(), created.ID, 10)
	if len(results) != 2 {
		t.Fatalf("l'essai rejoué devait s'écrire: %d lignes", len(results))
	}
}

// Le même essai livré deux fois ne compte qu'une, sinon l'uptime mentirait.
func TestRecord_TheSameResultTwiceCountsOnce(t *testing.T) {
	fixture := newBench(t)
	created := fixture.create(t, "site", "https://cloud.exemple.fr/")
	fixture.record(t, created.ID, testNow, probe.OutcomeUp)
	fixture.record(t, created.ID, testNow, probe.OutcomeUp)

	results, _ := fixture.db.ListProbeResults(context.Background(), created.ID, 10)
	if len(results) != 1 {
		t.Fatalf("attendu un essai, obtenu %d", len(results))
	}
}

func TestRecord_RefusesAReportOutOfRange(t *testing.T) {
	fixture := newBench(t)
	created := fixture.create(t, "site", "https://cloud.exemple.fr/")
	err := fixture.service.Record(context.Background(), machine.LocalID, probe.Report{Results: []probe.Result{
		{ProbeID: created.ID, CheckedAt: testNow.Add(time.Hour), Outcome: probe.OutcomeUp},
	}})
	if !errors.Is(err, probe.ErrReportInvalid) {
		t.Fatalf("attendu %v, obtenu %v", probe.ErrReportInvalid, err)
	}
}

func TestPauseResume_TakeTheProbeOutOfItsMachineSetAndBack(t *testing.T) {
	fixture := newBench(t)
	created := fixture.create(t, "site", "https://cloud.exemple.fr/")
	fixture.record(t, created.ID, testNow, probe.OutcomeUp)

	if err := fixture.service.Pause(context.Background(), created.ID); err != nil {
		t.Fatal(err)
	}
	if got := fixture.get(t, created.ID); got.Status != probe.StatusPaused || got.ConsecutiveSuccesses != 0 {
		t.Fatalf("la pause n'a pas remis les compteurs: %+v", got)
	}
	if assignment := fixture.runner.last(); len(assignment.Probes) != 0 {
		t.Fatalf("une sonde en pause reste dans le jeu: %+v", assignment)
	}

	if err := fixture.service.Resume(context.Background(), created.ID); err != nil {
		t.Fatal(err)
	}
	if got := fixture.get(t, created.ID); got.Status != probe.StatusNew {
		t.Fatalf("la reprise devait repartir de Nouveau, obtenu %q", got.Status)
	}
	if assignment := fixture.runner.last(); len(assignment.Probes) != 1 {
		t.Fatalf("la sonde reprise n'est pas rendue à sa machine: %+v", assignment)
	}
}

func TestResume_RefusesAProbeThatIsNotPaused(t *testing.T) {
	fixture := newBench(t)
	created := fixture.create(t, "site", "https://cloud.exemple.fr/")
	if err := fixture.service.Resume(context.Background(), created.ID); !errors.Is(err, probe.ErrNotPaused) {
		t.Fatalf("attendu %v, obtenu %v", probe.ErrNotPaused, err)
	}
}

func TestDelete_TakesTheProbeAndItsHistoryAway(t *testing.T) {
	fixture := newBench(t)
	created := fixture.create(t, "site", "https://cloud.exemple.fr/")
	fixture.record(t, created.ID, testNow, probe.OutcomeUp)

	if err := fixture.service.Delete(context.Background(), created.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.db.GetProbe(context.Background(), created.ID); !errors.Is(err, probe.ErrNotFound) {
		t.Fatalf("attendu %v, obtenu %v", probe.ErrNotFound, err)
	}
	results, _ := fixture.db.ListProbeResults(context.Background(), created.ID, 10)
	if len(results) != 0 {
		t.Fatalf("l'histoire a survécu à la suppression: %d lignes", len(results))
	}
	if assignment := fixture.runner.last(); len(assignment.Probes) != 0 {
		t.Fatalf("la sonde supprimée reste dans le jeu: %+v", assignment)
	}
}

// La fenêtre de 24 h lit le brut, les autres l'agrégat journalier ; une
// fenêtre sans essai n'a pas de disponibilité, jamais un zéro.
func TestUptimes_ReadTheirOwnTableAndSayNothingWhenEmpty(t *testing.T) {
	fixture := newBench(t)
	created := fixture.create(t, "site", "https://cloud.exemple.fr/")
	fixture.record(t, created.ID, testNow.Add(-2*24*time.Hour), probe.OutcomeUp)
	fixture.record(t, created.ID, testNow.Add(-2*24*time.Hour-time.Minute), probe.OutcomeDown)
	fixture.record(t, created.ID, testNow.Add(-time.Hour), probe.OutcomeUp)
	if err := fixture.service.Rollup(context.Background()); err != nil {
		t.Fatal(err)
	}

	byWindow := map[string]probe.Uptime{}
	uptimes, err := fixture.service.Uptimes(context.Background(), created.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, uptime := range uptimes {
		byWindow[uptime.Window] = uptime
	}
	if got := byWindow["24h"]; got.Total != 1 || got.Success != 1 {
		t.Fatalf("la fenêtre de 24 h ne lit pas le brut: %+v", got)
	}
	if got := byWindow["7d"]; got.Total != 3 || got.Success != 2 {
		t.Fatalf("la fenêtre de 7 jours n'additionne pas l'agrégat: %+v", got)
	}
}

func TestUptimes_SayNothingRatherThanZeroWithoutAnyCheck(t *testing.T) {
	fixture := newBench(t)
	created := fixture.create(t, "site", "https://cloud.exemple.fr/")
	uptimes, err := fixture.service.Uptimes(context.Background(), created.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, uptime := range uptimes {
		if uptime.Total != 0 || uptime.Success != 0 {
			t.Fatalf("une sonde neuve a une disponibilité: %+v", uptime)
		}
	}
}

// Un essai dégradé nourrit l'uptime comme un succès : la cible répond.
func TestUptimes_DegradedCountsAsASuccess(t *testing.T) {
	fixture := newBench(t)
	created := fixture.create(t, "site", "https://cloud.exemple.fr/")
	fixture.record(t, created.ID, testNow.Add(-time.Minute), probe.OutcomeDegraded)

	uptimes, _ := fixture.service.Uptimes(context.Background(), created.ID)
	for _, uptime := range uptimes {
		if uptime.Window == "24h" && (uptime.Total != 1 || uptime.Success != 1) {
			t.Fatalf("l'essai dégradé n'a pas compté comme un succès: %+v", uptime)
		}
	}
}

// Repasser le rollup sur les mêmes jours donne le même agrégat.
func TestRollup_IsIdempotent(t *testing.T) {
	fixture := newBench(t)
	created := fixture.create(t, "site", "https://cloud.exemple.fr/")
	fixture.record(t, created.ID, testNow.Add(-2*24*time.Hour), probe.OutcomeUp)
	for round := 0; round < 3; round++ {
		if err := fixture.service.Rollup(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	days, err := fixture.service.Days(context.Background(), created.ID, 90*24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if len(days) != 1 || days[0].Total != 1 || days[0].Success != 1 {
		t.Fatalf("le rollup rejoué n'a pas donné le même agrégat: %+v", days)
	}
}

func TestPurge_ClearsRawResultsAndKeepsTheAggregate(t *testing.T) {
	fixture := newBench(t)
	created := fixture.create(t, "site", "https://cloud.exemple.fr/")
	fixture.record(t, created.ID, testNow.Add(-2*24*time.Hour), probe.OutcomeUp)
	if err := fixture.service.Rollup(context.Background()); err != nil {
		t.Fatal(err)
	}
	// Dix jours plus tard, le brut est hors rétention, l'agrégat non.
	fixture.now = testNow.Add(10 * 24 * time.Hour)
	if err := fixture.service.Purge(context.Background()); err != nil {
		t.Fatal(err)
	}
	if results, _ := fixture.db.ListProbeResults(context.Background(), created.ID, 10); len(results) != 0 {
		t.Fatalf("le brut devait être purgé: %d lignes", len(results))
	}
	days, _ := fixture.service.Days(context.Background(), created.ID, 90*24*time.Hour)
	if len(days) != 1 {
		t.Fatalf("l'agrégat devait rester: %+v", days)
	}
}

func TestCount_CountsWhatTheSidebarShows(t *testing.T) {
	fixture := newBench(t)
	online := fixture.create(t, "en ligne", "https://a.fr/")
	offline := fixture.create(t, "hors ligne", "https://b.fr/")
	fixture.record(t, online.ID, testNow, probe.OutcomeUp)
	fixture.record(t, offline.ID, testNow, probe.OutcomeDown)

	total, attention, err := fixture.db.CountProbes(context.Background())
	if err != nil || total != 2 || attention != 1 {
		t.Fatalf("attendu 2 sondes dont 1 à traiter, obtenu %d/%d (%v)", total, attention, err)
	}
}

func TestRecord_PublishesOnTheBus(t *testing.T) {
	fixture := newBench(t)
	created := fixture.create(t, "site", "https://cloud.exemple.fr/")
	fixture.mu.Lock()
	fixture.changed = nil
	fixture.mu.Unlock()
	fixture.record(t, created.ID, testNow, probe.OutcomeUp)
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	if len(fixture.changed) != 1 || fixture.changed[0] != machine.LocalID {
		t.Fatalf("le direct n'a pas été prévenu: %+v", fixture.changed)
	}
}

// Le compteur de la vue d'ensemble : ce que la sonde a vu, ce qui approche
// de sa fin, et l'échéance la plus proche.
func TestCertificates_CountWhatIsSeenAndWhatIsClose(t *testing.T) {
	fixture := newBench(t)
	far := fixture.create(t, "loin", "https://loin.exemple.fr/")
	soon := fixture.create(t, "bientot", "https://bientot.exemple.fr/")
	fixture.create(t, "jamais sondé", "https://muet.exemple.fr/")

	fixture.seeCertificate(t, far.ID, testNow.AddDate(0, 0, 80))
	fixture.seeCertificate(t, soon.ID, testNow.AddDate(0, 0, 12))

	counted, err := fixture.service.Certificates(context.Background())
	if err != nil {
		t.Fatalf("compter les certificats: %v", err)
	}
	if counted.Total != 2 {
		t.Fatalf("deux sondes ont vu un certificat, comptées %d", counted.Total)
	}
	if counted.Expiring != 1 || counted.Expired != 0 {
		t.Fatalf("une seule échéance sous le seuil, aucune passée: %+v", counted)
	}
	if !counted.Soonest.Equal(testNow.AddDate(0, 0, 12)) {
		t.Fatalf("la plus proche échéance attendue dans 12 jours, obtenue %s", counted.Soonest)
	}
}

// Une sonde en pause ne regarde plus : ce qu'elle a vu ne dit plus rien de
// la cible, et n'a donc rien à faire dans le compteur.
func TestCertificates_IgnoreAPausedProbe(t *testing.T) {
	fixture := newBench(t)
	paused := fixture.create(t, "en pause", "https://pause.exemple.fr/")
	fixture.seeCertificate(t, paused.ID, testNow.AddDate(0, 0, 3))
	if err := fixture.service.Pause(context.Background(), paused.ID); err != nil {
		t.Fatalf("mettre en pause: %v", err)
	}

	counted, err := fixture.service.Certificates(context.Background())
	if err != nil {
		t.Fatalf("compter les certificats: %v", err)
	}
	if counted.Total != 0 || counted.Expiring != 0 || !counted.Soonest.IsZero() {
		t.Fatalf("une sonde en pause ne compte pas: %+v", counted)
	}
}

// Ce qui approche de sa fin et ce qui l'a passée sont deux comptes : on ne
// renouvelle pas de la même façon un certificat mort et un qui va mourir.
// La plus proche échéance rendue est à venir, sans quoi elle ne dirait pas
// dans combien de jours.
func TestCertificates_SeparateWhatIsCloseFromWhatIsPast(t *testing.T) {
	fixture := newBench(t)
	dead := fixture.create(t, "mort", "https://mort.exemple.fr/")
	soon := fixture.create(t, "bientot", "https://bientot.exemple.fr/")
	fixture.seeCertificate(t, dead.ID, testNow.AddDate(0, 0, -4))
	fixture.seeCertificate(t, soon.ID, testNow.AddDate(0, 0, 12))

	counted, err := fixture.service.Certificates(context.Background())
	if err != nil {
		t.Fatalf("compter les certificats: %v", err)
	}
	if counted.Expired != 1 || counted.Expiring != 1 {
		t.Fatalf("un expiré et un qui approche: %+v", counted)
	}
	if !counted.Soonest.Equal(testNow.AddDate(0, 0, 12)) {
		t.Fatalf("la plus proche échéance doit être à venir, obtenue %s", counted.Soonest)
	}
}

// Un essai qui n'a vu aucun certificat n'efface pas celui qu'on avait : une
// coupure ne fait pas disparaître ce que la cible sert.
func TestCertificates_AFailedCheckKeepsTheLastOneSeen(t *testing.T) {
	fixture := newBench(t)
	site := fixture.create(t, "site", "https://cloud.exemple.fr/")
	fixture.seeCertificate(t, site.ID, testNow.AddDate(0, 0, 20))
	fixture.record(t, site.ID, testNow.Add(time.Minute), probe.OutcomeDown)

	found := fixture.get(t, site.ID)
	if found.Certificate == nil {
		t.Fatal("le certificat vu ne doit pas disparaître avec un essai muet")
	}
	if !found.Certificate.NotAfter.Equal(testNow.AddDate(0, 0, 20)) {
		t.Fatalf("échéance attendue inchangée, obtenue %s", found.Certificate.NotAfter)
	}
}

// seeCertificate rapporte un essai dégradé qui a vu un certificat
// d'autorité interne : le nom correspond, la chaîne ne remonte à rien de
// connu, et l'échéance reste parfaitement lisible.
func (b *bench) seeCertificate(t *testing.T, id string, notAfter time.Time) {
	t.Helper()
	b.recordResult(t, probe.Result{
		ProbeID: id, CheckedAt: b.now, Outcome: probe.OutcomeDegraded, DurationMs: 42,
		Reason: probe.ReasonTLSUntrusted,
		Certificate: &probe.Certificate{
			Subject: "cloud.exemple.fr", Issuer: "Autorité interne",
			NotBefore: b.now.AddDate(0, 0, -70), NotAfter: notAfter,
			Fingerprint: "ab", HostnameMatch: true,
		},
	})
}

// Des créations simultanées ne dépassent jamais le plafond : compter puis
// insérer se fait d'un seul tenant.
func TestCreate_ConcurrentCreations_StopAtTheCeiling(t *testing.T) {
	fixture := newBench(t)
	attempts := probe.MaxProbesPerMachine + 16
	var created sync.WaitGroup
	results := make(chan error, attempts)
	for range attempts {
		created.Add(1)
		go func() {
			defer created.Done()
			_, err := fixture.service.Create(context.Background(), probe.Definition{
				Name: "sonde", Kind: probe.KindHTTP, Target: "https://cloud.exemple.fr/", MachineID: machine.LocalID,
			})
			results <- err
		}()
	}
	created.Wait()
	close(results)
	accepted, tooMany := 0, 0
	for err := range results {
		switch {
		case err == nil:
			accepted++
		case errors.Is(err, probe.ErrTooMany):
			tooMany++
		default:
			t.Fatalf("erreur inattendue: %v", err)
		}
	}
	if accepted != probe.MaxProbesPerMachine || tooMany != 16 {
		t.Fatalf("acceptées %d, refusées %d", accepted, tooMany)
	}
}

// recordingAlerter note chaque sonde que le service montre au moteur des
// alertes, dans l'ordre.
type recordingAlerter struct {
	mu     sync.Mutex
	probes []probe.Probe
}

func (a *recordingAlerter) ProbeChanged(_ context.Context, found probe.Probe) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.probes = append(a.probes, found)
}

func (a *recordingAlerter) ProbeRemoved(context.Context, string) {}

func (a *recordingAlerter) last() probe.Probe {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.probes[len(a.probes)-1]
}

// recordingCommander tient lieu du flux d'une machine distante : il note le
// dernier jeu poussé à chacune.
type recordingCommander struct {
	mu   sync.Mutex
	sets map[string]probe.Assignment
}

func (c *recordingCommander) AssignProbes(machineID string, assignment probe.Assignment) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.sets == nil {
		c.sets = map[string]probe.Assignment{}
	}
	c.sets[machineID] = assignment
	return nil
}

func (c *recordingCommander) setOf(machineID string) (probe.Assignment, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	set, ok := c.sets[machineID]
	return set, ok
}

// definitionOf redonne la définition que la sonde a aujourd'hui : un test
// n'a plus qu'à changer le champ qui l'intéresse.
func definitionOf(found probe.Probe) probe.Definition {
	return probe.Definition{
		Name: found.Name, Kind: found.Kind, Target: found.Target, MachineID: found.MachineID,
		ServiceID: found.ServiceID, Interval: found.Interval, Timeout: found.Timeout,
		FailureThreshold: found.FailureThreshold, RecoveryThreshold: found.RecoveryThreshold,
		Method: found.Method, ExpectedStatus: found.ExpectedStatus, ExpectedBody: found.ExpectedBody,
		FollowRedirects: found.FollowRedirects, TLS: found.TLS,
	}
}

func (b *bench) update(t *testing.T, id string, definition probe.Definition) probe.Probe {
	t.Helper()
	updated, err := b.service.Update(context.Background(), id, definition)
	if err != nil {
		t.Fatalf("modifier la sonde: %v", err)
	}
	return updated
}

func (b *bench) countResults(t *testing.T, id string) int {
	t.Helper()
	results, err := b.db.ListProbeResults(context.Background(), id, 100)
	if err != nil {
		t.Fatal(err)
	}
	return len(results)
}

// Le nom, le rythme et les seuils ne changent pas ce que la sonde mesure :
// ni l'état, ni les compteurs, ni le certificat n'y touchent.
func TestUpdate_RhythmOnly_KeepsIDHistoryStateAndCertificate(t *testing.T) {
	fixture := newBench(t)
	created := fixture.create(t, "site", "https://cloud.exemple.fr/")
	fixture.seeCertificate(t, created.ID, testNow.AddDate(0, 0, 20))
	before := fixture.get(t, created.ID)

	definition := definitionOf(before)
	definition.Name = "site nextcloud"
	definition.Interval = 5 * time.Minute
	definition.FailureThreshold = 5
	definition.RecoveryThreshold = 4
	updated := fixture.update(t, created.ID, definition)

	if updated.ID != created.ID || !updated.CreatedAt.Equal(created.CreatedAt) {
		t.Fatalf("l'identité a changé: %+v", updated)
	}
	if updated.Name != "site nextcloud" || updated.Interval != 5*time.Minute || updated.FailureThreshold != 5 || updated.RecoveryThreshold != 4 {
		t.Fatalf("la définition n'a pas été remplacée: %+v", updated)
	}
	if updated.Status != before.Status || updated.ConsecutiveSuccesses != before.ConsecutiveSuccesses {
		t.Fatalf("l'état a bougé: avant %+v, après %+v", before, updated)
	}
	if updated.Certificate == nil || updated.Certificate.Fingerprint != "ab" {
		t.Fatalf("le certificat doit rester: %+v", updated.Certificate)
	}
	if !updated.RevisedAt.IsZero() {
		t.Fatalf("une édition de rythme n'est pas une révision: %s", updated.RevisedAt)
	}
	if got := fixture.countResults(t, created.ID); got != 1 {
		t.Fatalf("l'histoire doit rester: %d essais", got)
	}
	if assignment := fixture.runner.last(); len(assignment.Probes) != 1 || assignment.Probes[0].IntervalSeconds != 300 {
		t.Fatalf("la machine n'a pas reçu le nouveau rythme: %+v", assignment)
	}
}

// Une autre cible : l'état, les compteurs et le certificat ne disent plus
// rien d'elle, l'histoire reste.
func TestUpdate_NewTarget_ResetsStateAndCertificateKeepsHistory(t *testing.T) {
	fixture := newBench(t)
	created := fixture.create(t, "site", "https://cloud.exemple.fr/")
	fixture.seeCertificate(t, created.ID, testNow.AddDate(0, 0, 20))
	fixture.now = testNow.Add(time.Hour)

	definition := definitionOf(fixture.get(t, created.ID))
	definition.Target = "https://autre.exemple.fr/"
	updated := fixture.update(t, created.ID, definition)

	if updated.Status != probe.StatusNew || updated.ConsecutiveSuccesses != 0 || updated.ConsecutiveFailures != 0 {
		t.Fatalf("l'état devait repartir de zéro: %+v", updated)
	}
	if updated.Certificate != nil {
		t.Fatalf("le certificat de l'ancienne cible doit disparaître: %+v", updated.Certificate)
	}
	if !updated.RevisedAt.Equal(fixture.now) {
		t.Fatalf("révision attendue à %s, obtenue %s", fixture.now, updated.RevisedAt)
	}
	if got := fixture.countResults(t, created.ID); got != 1 {
		t.Fatalf("l'histoire doit rester: %d essais", got)
	}
	if assignment := fixture.runner.last(); len(assignment.Probes) != 1 || assignment.Probes[0].Target != "https://autre.exemple.fr/" {
		t.Fatalf("la machine n'a pas reçu la nouvelle cible: %+v", assignment)
	}
}

// Un réglage de l'essai remet l'état à zéro, mais le certificat reste : la
// cible est la même.
func TestUpdate_ExpectedBody_ResetsStateButKeepsTheCertificate(t *testing.T) {
	fixture := newBench(t)
	created := fixture.create(t, "site", "https://cloud.exemple.fr/")
	fixture.seeCertificate(t, created.ID, testNow.AddDate(0, 0, 20))

	definition := definitionOf(fixture.get(t, created.ID))
	definition.ExpectedBody = "bienvenue"
	updated := fixture.update(t, created.ID, definition)

	if updated.Status != probe.StatusNew || updated.RevisedAt.IsZero() {
		t.Fatalf("l'état devait repartir de zéro: %+v", updated)
	}
	if updated.Certificate == nil {
		t.Fatal("la cible n'a pas changé : le certificat doit rester")
	}
}

// Un essai pris avec l'ancien réglage arrive après l'édition : il va dans
// l'histoire, pas dans l'état tout juste remis à zéro.
func TestUpdate_ResultCheckedBeforeRevision_WritesHistoryOnly(t *testing.T) {
	fixture := newBench(t)
	created := fixture.create(t, "site", "https://cloud.exemple.fr/")
	fixture.now = testNow.Add(time.Minute)
	definition := definitionOf(fixture.get(t, created.ID))
	definition.Target = "https://autre.exemple.fr/"
	fixture.update(t, created.ID, definition)

	fixture.record(t, created.ID, testNow, probe.OutcomeDown)
	after := fixture.get(t, created.ID)
	if after.Status != probe.StatusNew || after.ConsecutiveFailures != 0 {
		t.Fatalf("un essai de l'ancien réglage a bougé l'état: %+v", after)
	}
	if got := fixture.countResults(t, created.ID); got != 1 {
		t.Fatalf("l'essai doit être dans l'histoire: %d essais", got)
	}

	fixture.record(t, created.ID, fixture.now, probe.OutcomeUp)
	if after := fixture.get(t, created.ID); after.Status != probe.StatusUp {
		t.Fatalf("un essai du nouveau réglage doit compter, obtenu %q", after.Status)
	}
}

func TestUpdate_MovingMachine_HandsTheSetToBoth(t *testing.T) {
	fixture := newBench(t)
	fixture.enrollRemote(t)
	commander := &recordingCommander{}
	fixture.service.SetCommander(commander)
	created := fixture.create(t, "site", "https://cloud.exemple.fr/")

	definition := definitionOf(created)
	definition.MachineID = remoteMachineID
	updated := fixture.update(t, created.ID, definition)

	if updated.MachineID != remoteMachineID || updated.Status != probe.StatusNew {
		t.Fatalf("la sonde n'a pas changé de machine: %+v", updated)
	}
	if previous, pushed := commander.setOf(machine.LocalID); !pushed || len(previous.Probes) != 0 {
		t.Fatalf("l'ancienne machine garde la sonde: %+v", previous)
	}
	set, pushed := commander.setOf(remoteMachineID)
	if !pushed || len(set.Probes) != 1 || set.Probes[0].ID != created.ID {
		t.Fatalf("la nouvelle machine n'a pas reçu la sonde: %+v", set)
	}
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	if !slices.Contains(fixture.changed, machine.LocalID) || !slices.Contains(fixture.changed, remoteMachineID) {
		t.Fatalf("les deux machines doivent être signalées: %v", fixture.changed)
	}
}

func TestUpdate_ReplaysValidation(t *testing.T) {
	fixture := newBench(t)
	fixture.enrollRemote(t)
	created := fixture.create(t, "site", "https://cloud.exemple.fr/")

	cases := []struct {
		name   string
		change func(*probe.Definition)
		want   error
	}{
		{"nom vide", func(d *probe.Definition) { d.Name = " " }, probe.ErrNameInvalid},
		{"type inconnu", func(d *probe.Definition) { d.Kind = "udp" }, probe.ErrKindInvalid},
		{"cible illisible", func(d *probe.Definition) { d.Target = "pas une url" }, probe.ErrTargetInvalid},
		{"cible lien-local", func(d *probe.Definition) { d.Target = "http://169.254.169.254/" }, probe.ErrTargetForbidden},
		{"machine absente", func(d *probe.Definition) { d.MachineID = "" }, probe.ErrMachineInvalid},
		{"intervalle trop court", func(d *probe.Definition) { d.Interval = time.Second }, probe.ErrIntervalInvalid},
		{"délai plus long que l'intervalle", func(d *probe.Definition) { d.Timeout = 2 * d.Interval }, probe.ErrTimeoutInvalid},
		{"seuil hors limites", func(d *probe.Definition) { d.FailureThreshold = 11 }, probe.ErrThresholdInvalid},
		{"méthode interdite", func(d *probe.Definition) { d.Method = "DELETE" }, probe.ErrMethodInvalid},
		{"code attendu illisible", func(d *probe.Definition) { d.ExpectedStatus = "beaucoup" }, probe.ErrStatusInvalid},
		{"texte attendu trop long", func(d *probe.Definition) { d.ExpectedBody = strings.Repeat("x", probe.MaxBodyLength+1) }, probe.ErrBodyInvalid},
		{"service absent de l'annuaire", func(d *probe.Definition) { d.ServiceID = "svc_absent" }, probe.ErrServiceInvalid},
	}
	for _, c := range cases {
		definition := definitionOf(created)
		c.change(&definition)
		if _, err := fixture.service.Update(context.Background(), created.ID, definition); !errors.Is(err, c.want) {
			t.Errorf("%s: attendu %v, obtenu %v", c.name, c.want, err)
		}
	}
	if got := fixture.get(t, created.ID); got.Name != "site" || got.Target != created.Target {
		t.Fatalf("une édition refusée a été écrite: %+v", got)
	}

	if _, err := fixture.service.Update(context.Background(), "inconnue", definitionOf(created)); !errors.Is(err, probe.ErrNotFound) {
		t.Fatalf("attendu %v, obtenu %v", probe.ErrNotFound, err)
	}
}

// Changer de machine compte sur la nouvelle : pleine, elle refuse.
func TestUpdate_MovingToAFullMachine_IsRefused(t *testing.T) {
	fixture := newBench(t)
	fixture.enrollRemote(t)
	for index := 0; index < probe.MaxProbesPerMachine; index++ {
		_, err := fixture.service.Create(context.Background(), probe.Definition{
			Name: "sonde", Kind: probe.KindHTTP, Target: "https://a.fr/", MachineID: remoteMachineID,
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	created := fixture.create(t, "site", "https://cloud.exemple.fr/")

	definition := definitionOf(created)
	definition.MachineID = remoteMachineID
	if _, err := fixture.service.Update(context.Background(), created.ID, definition); !errors.Is(err, probe.ErrTooMany) {
		t.Fatalf("attendu %v, obtenu %v", probe.ErrTooMany, err)
	}
	if got := fixture.get(t, created.ID); got.MachineID != machine.LocalID {
		t.Fatalf("la sonde a bougé malgré le refus: %+v", got)
	}

	// Une sonde qui reste sur la machine pleine n'y prend pas une place de plus.
	carried, err := fixture.db.ListProbes(context.Background(), remoteMachineID)
	if err != nil || len(carried) == 0 {
		t.Fatalf("lire les sondes de la machine pleine: %v", err)
	}
	renamed := definitionOf(carried[0])
	renamed.Name = "renommée"
	fixture.update(t, carried[0].ID, renamed)
}

func TestUpdate_Paused_StaysPausedAndOutOfTheSet(t *testing.T) {
	fixture := newBench(t)
	created := fixture.create(t, "site", "https://cloud.exemple.fr/")
	fixture.record(t, created.ID, testNow, probe.OutcomeUp)
	if err := fixture.service.Pause(context.Background(), created.ID); err != nil {
		t.Fatal(err)
	}

	definition := definitionOf(fixture.get(t, created.ID))
	definition.Target = "https://autre.exemple.fr/"
	updated := fixture.update(t, created.ID, definition)

	if updated.Status != probe.StatusPaused {
		t.Fatalf("une sonde en pause le reste, obtenu %q", updated.Status)
	}
	if updated.Target != "https://autre.exemple.fr/" || updated.RevisedAt.IsZero() {
		t.Fatalf("la définition devait être remplacée et révisée: %+v", updated)
	}
	if assignment := fixture.runner.last(); len(assignment.Probes) != 0 {
		t.Fatalf("une sonde en pause revient dans le jeu: %+v", assignment)
	}
}

// Remise à zéro : le moteur des alertes voit « Nouveau » et lève tout. Sans
// remise à zéro, il voit l'état d'avant, et ne fait que renommer.
func TestUpdate_Reset_TellsTheAlerterNew(t *testing.T) {
	fixture := newBench(t)
	alerter := &recordingAlerter{}
	fixture.service.SetAlerter(alerter)
	created := fixture.create(t, "site", "https://cloud.exemple.fr/")
	fixture.record(t, created.ID, testNow, probe.OutcomeDown)

	definition := definitionOf(fixture.get(t, created.ID))
	definition.Name = "renommée"
	fixture.update(t, created.ID, definition)
	if seen := alerter.last(); seen.Status != probe.StatusDown || seen.Name != "renommée" {
		t.Fatalf("le moteur doit voir la sonde renommée, toujours hors ligne: %+v", seen)
	}

	definition.Target = "https://autre.exemple.fr/"
	fixture.update(t, created.ID, definition)
	if seen := alerter.last(); seen.Status != probe.StatusNew {
		t.Fatalf("le moteur doit voir « Nouveau », obtenu %q", seen.Status)
	}
}

// editingStore édite la sonde juste avant que le rapport s'écrive : le
// cas d'un essai lu avec l'ancienne sonde, appliqué après l'édition.
type editingStore struct {
	*store.DB
	beforeApply func()
}

func (s *editingStore) ApplyProbeResults(ctx context.Context, changes probe.Changes) ([]probe.Probe, error) {
	if s.beforeApply != nil {
		s.beforeApply()
	}
	return s.DB.ApplyProbeResults(ctx, changes)
}

// Une sonde révisée entre la lecture et l'écriture garde son état remis à
// zéro, et le moteur des alertes ne voit rien de la sonde d'avant : ni
// « hors ligne » qui n'a plus lieu d'être, ni son ancien certificat.
func TestRecord_ProbeRevisedBetweenReadAndWrite_TellsTheAlerterNothing(t *testing.T) {
	fixture := newBench(t)
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	alerter := &recordingAlerter{}
	wrapper := &editingStore{DB: fixture.db}
	recording := probe.New(wrapper, logger)
	recording.SetClock(func() time.Time { return fixture.now })
	recording.SetAlerter(alerter)

	created := fixture.create(t, "site", "https://cloud.exemple.fr/")
	wrapper.beforeApply = func() {
		definition := definitionOf(fixture.get(t, created.ID))
		definition.Target = "https://autre.exemple.fr/"
		fixture.update(t, created.ID, definition)
	}
	// Lu avant l'édition : le plan ne sait pas encore qu'elle viendra, seule
	// la garde de l'écriture retient l'état calculé.
	fixture.now = testNow.Add(time.Minute)
	result := probe.Result{ProbeID: created.ID, CheckedAt: fixture.now, Outcome: probe.OutcomeDown, DurationMs: 5}
	if err := recording.Record(context.Background(), machine.LocalID, probe.Report{Results: []probe.Result{result}}); err != nil {
		t.Fatal(err)
	}

	alerter.mu.Lock()
	defer alerter.mu.Unlock()
	if len(alerter.probes) != 0 {
		t.Fatalf("le moteur a reçu la sonde d'avant l'édition: %+v", alerter.probes)
	}
	if after := fixture.get(t, created.ID); after.Status != probe.StatusNew || after.Target != "https://autre.exemple.fr/" {
		t.Fatalf("l'état remis à zéro a été écrasé: %+v", after)
	}
}
