package alert_test

import (
	"context"
	"log/slog"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ldesfontaine/opencloud/internal/alert"
	"github.com/ldesfontaine/opencloud/internal/machine"
	"github.com/ldesfontaine/opencloud/internal/store"
)

var testNow = time.Date(2026, 9, 22, 10, 0, 0, 0, time.UTC)

// Le moteur se teste sur la vraie base SQLite, temporaire, avec une horloge
// qu'on avance à la main, un notifieur qui note ce qu'on lui confie, et
// une page de statut qu'on met en maintenance d'un mot.
type fixture struct {
	*alert.Engine
	db          *store.DB
	machines    *machine.Service
	clock       time.Time
	sender      *recordingSender
	watcher     *countingWatcher
	maintenance *fakeMaintenance
}

type recordingSender struct {
	mu   sync.Mutex
	jobs []alert.Job
}

func (s *recordingSender) Enqueue(job alert.Job) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.jobs = append(s.jobs, job)
}

// events rend « événement:canal » pour chaque livraison confiée.
func (s *recordingSender) events() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var events []string
	for _, job := range s.jobs {
		events = append(events, string(job.Delivery.Event)+":"+job.Channel.Name)
	}
	return strings.Join(events, ",")
}

type countingWatcher struct{ changes int }

func (w *countingWatcher) AlertsChanged() { w.changes++ }

type fakeMaintenance struct{ under map[string]bool }

func (m *fakeMaintenance) UnderMaintenance(_ context.Context, kind, id string) (bool, error) {
	return m.under[kind+":"+id], nil
}

func newFixture(t *testing.T) *fixture {
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
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	f := &fixture{Engine: alert.New(db, logger), db: db, clock: testNow, sender: &recordingSender{}, watcher: &countingWatcher{}, maintenance: &fakeMaintenance{under: map[string]bool{}}}
	f.SetClock(func() time.Time { return f.clock })
	f.SetSender(f.sender)
	f.SetWatcher(f.watcher)
	f.SetMaintenance(f.maintenance)
	// La machine openCloud existe toujours : les objets s'y rattachent.
	f.machines = machine.New(db, machine.NewSessions(), logger)
	f.machines.SetClock(func() time.Time { return f.clock })
	f.machines.SetAlerter(f.Engine)
	if err := f.machines.EnsureLocal(context.Background(), machine.LocalInfo{Hostname: "opencloud-host"}); err != nil {
		t.Fatal(err)
	}
	return f
}

func (f *fixture) advance(d time.Duration) {
	f.clock = f.clock.Add(d)
}

// channel crée un canal actif ; la cible n'est jamais appelée, le
// notifieur est un faux.
func (f *fixture) channel(t *testing.T, name string, minSeverity alert.Severity, notifyResolve bool) alert.Channel {
	t.Helper()
	channel, err := f.CreateChannel(context.Background(), alert.ChannelDefinition{
		Name: name, URL: "http://127.0.0.1:9/hook", Format: alert.FormatJSON, MinSeverity: minSeverity, NotifyResolve: notifyResolve, Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	return channel
}

// volumeFact est le fait le plus simple : un volume de la machine openCloud.
func volumeFact(severity alert.Severity, percent int) alert.Fact {
	return alert.Fact{
		Kind: alert.KindDiskFull, Severity: severity,
		Object:    alert.Object{Kind: alert.ObjectVolume, ID: alert.VolumeID(machine.LocalID, "/data"), Name: "/data"},
		MachineID: machine.LocalID,
		Details:   alert.Details{MountPoint: "/data", Percent: percent},
	}
}

func (f *fixture) open(t *testing.T, status alert.Status) []alert.Alert {
	t.Helper()
	alerts, err := f.List(context.Background(), status)
	if err != nil {
		t.Fatal(err)
	}
	return alerts
}

func (f *fixture) only(t *testing.T, status alert.Status) alert.Alert {
	t.Helper()
	alerts := f.open(t, status)
	if len(alerts) != 1 {
		t.Fatalf("%s alerts: %d, want 1: %+v", status, len(alerts), alerts)
	}
	return alerts[0]
}
