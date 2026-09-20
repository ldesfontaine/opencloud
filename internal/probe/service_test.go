package probe_test

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/ldesfontaine/opencloud/internal/machine"
	"github.com/ldesfontaine/opencloud/internal/probe"
	"github.com/ldesfontaine/opencloud/internal/store"
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

// recordingRunner tient lieu du moteur de la machine openCloud.
type recordingRunner struct {
	mu          sync.Mutex
	assignments []probe.Assignment
}

func (r *recordingRunner) Assign(assignment probe.Assignment) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.assignments = append(r.assignments, assignment)
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
	root, err := os.OpenRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { root.Close() })
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	db, err := store.Open(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	machines := machine.New(db, machine.NewSessions(), logger)
	err = machines.EnsureLocal(context.Background(), machine.LocalInfo{
		Hostname: "opencloud-host", Address: "10.8.0.1", OS: "Debian 12", Arch: "amd64", Version: "v0.0.1",
	})
	if err != nil {
		t.Fatal(err)
	}
	fixture := &bench{db: db, now: testNow, runner: &recordingRunner{}}
	fixture.service = probe.New(db, logger)
	fixture.service.SetClock(func() time.Time { return fixture.now })
	fixture.service.SetWatcher(fixture)
	fixture.service.SetLocalRunner(machine.LocalID, fixture.runner)
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
	found, err := b.service.Get(context.Background(), id)
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

func TestRecord_WritesTheResultAndTheState(t *testing.T) {
	fixture := newBench(t)
	created := fixture.create(t, "site", "https://cloud.exemple.fr/")
	fixture.record(t, created.ID, testNow, probe.OutcomeUp)

	after := fixture.get(t, created.ID)
	if after.Status != probe.StatusUp || after.ConsecutiveSuccesses != 1 || after.LastDurationMs != 42 {
		t.Fatalf("l'état n'a pas suivi l'essai: %+v", after)
	}
	results, err := fixture.service.Results(context.Background(), created.ID, 10)
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
	results, _ := fixture.service.Results(context.Background(), created.ID, 10)
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

	results, _ := fixture.service.Results(context.Background(), created.ID, 10)
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
	if _, err := fixture.service.Get(context.Background(), created.ID); !errors.Is(err, probe.ErrNotFound) {
		t.Fatalf("attendu %v, obtenu %v", probe.ErrNotFound, err)
	}
	results, _ := fixture.service.Results(context.Background(), created.ID, 10)
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
	if results, _ := fixture.service.Results(context.Background(), created.ID, 10); len(results) != 0 {
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

	total, attention, err := fixture.service.Count(context.Background())
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
	if counted.Expiring != 1 {
		t.Fatalf("une seule échéance est sous le seuil, comptées %d", counted.Expiring)
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
