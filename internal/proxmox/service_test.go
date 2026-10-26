package proxmox

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ldesfontaine/opencloud/internal/audit"
	"github.com/ldesfontaine/opencloud/internal/machine"
)

// memoryStore tient un seul hôte par machine, comme la base.
type memoryStore struct {
	mu       sync.Mutex
	hosts    map[string]Credentials
	readings map[string]Reading
	failures map[string]string
	saves    int
}

func newMemoryStore() *memoryStore {
	return &memoryStore{hosts: map[string]Credentials{}, readings: map[string]Reading{}, failures: map[string]string{}}
}

func (s *memoryStore) GetProxmoxCredentials(_ context.Context, id string) (Credentials, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	host, found := s.hosts[id]
	if !found {
		return Credentials{}, ErrNotFound
	}
	return host, nil
}

func (s *memoryStore) SaveProxmoxHost(_ context.Context, credentials Credentials) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.saves++
	s.hosts[credentials.MachineID] = credentials
	delete(s.failures, credentials.MachineID)
	return nil
}

func (s *memoryStore) DeleteProxmoxHost(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, found := s.hosts[id]; !found {
		return ErrNotFound
	}
	delete(s.hosts, id)
	delete(s.readings, id)
	return nil
}

func (s *memoryStore) ListProxmoxHostIDs(context.Context) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ids := []string{}
	for id := range s.hosts {
		ids = append(ids, id)
	}
	return ids, nil
}

func (s *memoryStore) ReplaceProxmoxReading(_ context.Context, id string, reading Reading, _ time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, found := s.hosts[id]; !found {
		return ErrNotFound
	}
	s.readings[id] = reading
	delete(s.failures, id)
	return nil
}

func (s *memoryStore) NoteProxmoxFailure(_ context.Context, id, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, found := s.hosts[id]; !found {
		return ErrNotFound
	}
	s.failures[id] = key
	return nil
}

type fakeMachines []machine.Status

func (m fakeMachines) Get(_ context.Context, id string) (machine.Status, error) {
	for _, candidate := range m {
		if candidate.ID == id {
			return candidate, nil
		}
	}
	return machine.Status{}, machine.ErrNotFound
}

func (m fakeMachines) List(context.Context) ([]machine.Status, error) { return m, nil }

type recordingWatcher struct {
	mu  sync.Mutex
	ids []string
}

func (w *recordingWatcher) ProxmoxChanged(id string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.ids = append(w.ids, id)
}

func (w *recordingWatcher) count() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return len(w.ids)
}

type serviceBench struct {
	service *Service
	store   *memoryStore
	watcher *recordingWatcher
	api     *fakeAPI
}

func newServiceBench(t *testing.T) *serviceBench {
	t.Helper()
	bench := &serviceBench{store: newMemoryStore(), watcher: &recordingWatcher{}, api: newFakeAPI(t)}
	machines := fakeMachines{
		{Machine: machine.Machine{ID: "m1", Name: "Proxmox", Hostname: "pve"}},
		{Machine: machine.Machine{ID: "m2", Name: "Docker", Hostname: "docker1"}},
	}
	bench.service = New(bench.store, machines, slog.New(slog.NewTextHandler(io.Discard, nil)))
	bench.service.SetWatcher(bench.watcher)
	bench.service.SetClock(func() time.Time { return time.Unix(1_800_000_000, 0) })
	return bench
}

func (b *serviceBench) definition() Definition {
	return Definition{APIURL: b.api.URL, TokenID: testTokenID, TokenSecret: testTokenSecret, Pin: b.api.pin()}
}

func TestDeclare_ChecksTheTokenBeforeWritingAnything(t *testing.T) {
	bench := newServiceBench(t)
	definition := bench.definition()
	definition.TokenSecret = "wrong-secret-value"
	err := bench.service.Declare(context.Background(), "m1", definition)
	refusalOf(t, err, ErrUnauthorized)
	if bench.store.saves != 0 || len(bench.store.hosts) != 0 {
		t.Fatal("a refused token must write nothing")
	}
	if bench.watcher.count() != 0 {
		t.Fatal("a refused declaration must not wake the live view")
	}
}

func TestDeclare_RefusesAnInvalidInputWithoutCallingTheAPI(t *testing.T) {
	bench := newServiceBench(t)
	definition := bench.definition()
	definition.TokenID = "nope"
	refusalOf(t, bench.service.Declare(context.Background(), "m1", definition), ErrTokenIDInvalid)
	if len(bench.api.requested()) != 0 {
		t.Fatal("an invalid input reached the API")
	}
}

func TestDeclare_UnknownMachine(t *testing.T) {
	bench := newServiceBench(t)
	err := bench.service.Declare(context.Background(), "ghost", bench.definition())
	if !errors.Is(err, machine.ErrNotFound) {
		t.Fatalf("got %v", err)
	}
}

func TestDeclare_WritesTheHostAndReadsItAtOnce(t *testing.T) {
	bench := newServiceBench(t)
	if err := bench.service.Declare(context.Background(), "m1", bench.definition()); err != nil {
		t.Fatal(err)
	}
	host := bench.store.hosts["m1"]
	if host.TokenSecret != testTokenSecret || host.Version != "8.2.4" || host.VerifiedAt.Unix() != 1_800_000_000 {
		t.Errorf("saved host: %+v", host)
	}
	reading, read := bench.store.readings["m1"]
	if !read {
		t.Fatal("no first reading")
	}
	if reading.Node != "pve" {
		t.Errorf("node %q", reading.Node)
	}
	linked := map[int]string{}
	for _, guest := range reading.Guests {
		linked[guest.VMID] = guest.LinkedMachineID
	}
	if linked[5012] != "m2" || linked[200] != "" {
		t.Errorf("links: %v", linked)
	}
	if bench.watcher.count() < 2 {
		t.Errorf("the live view must hear the declaration and the reading, got %d", bench.watcher.count())
	}
}

func TestDeclare_ARedeclarationReplacesTheConnection(t *testing.T) {
	bench := newServiceBench(t)
	if err := bench.service.Declare(context.Background(), "m1", bench.definition()); err != nil {
		t.Fatal(err)
	}
	if err := bench.service.Declare(context.Background(), "m1", bench.definition()); err != nil {
		t.Fatal(err)
	}
	if bench.store.saves != 2 || len(bench.store.hosts) != 1 {
		t.Fatalf("saves %d, hosts %d", bench.store.saves, len(bench.store.hosts))
	}
}

func TestRefresh_AFailureIsKeptOnTheHostAndTheLastReadingStays(t *testing.T) {
	bench := newServiceBench(t)
	if err := bench.service.Declare(context.Background(), "m1", bench.definition()); err != nil {
		t.Fatal(err)
	}
	before := len(bench.store.readings["m1"].Guests)

	bench.api.mu.Lock()
	bench.api.handler = func(w http.ResponseWriter, _ *http.Request) bool {
		http.Error(w, "no", http.StatusUnauthorized)
		return true
	}
	bench.api.mu.Unlock()
	err := bench.service.Refresh(context.Background(), "m1")
	refusalOf(t, err, ErrUnauthorized)
	if bench.store.failures["m1"] != "proxmox.unauthorized" {
		t.Errorf("failure key %q", bench.store.failures["m1"])
	}
	if len(bench.store.readings["m1"].Guests) != before {
		t.Error("a failed reading must not replace the last one")
	}

	bench.api.mu.Lock()
	bench.api.handler = nil
	bench.api.mu.Unlock()
	if err := bench.service.Refresh(context.Background(), "m1"); err != nil {
		t.Fatal(err)
	}
	if _, failed := bench.store.failures["m1"]; failed {
		t.Error("a good reading must clear the failure")
	}
}

func TestRefresh_AnEmptiedSecretIsAnUnauthorizedToken(t *testing.T) {
	bench := newServiceBench(t)
	if err := bench.service.Declare(context.Background(), "m1", bench.definition()); err != nil {
		t.Fatal(err)
	}
	host := bench.store.hosts["m1"]
	host.TokenSecret = ""
	bench.store.hosts["m1"] = host
	requests := len(bench.api.requested())

	err := bench.service.Refresh(context.Background(), "m1")
	refusalOf(t, err, ErrUnauthorized)
	if len(bench.api.requested()) != requests {
		t.Error("an empty token must not be sent to the API")
	}
	if bench.store.failures["m1"] != "proxmox.unauthorized" {
		t.Errorf("failure key %q", bench.store.failures["m1"])
	}
}

func TestRefresh_NoHostIsNotFound(t *testing.T) {
	bench := newServiceBench(t)
	refusalOf(t, bench.service.Refresh(context.Background(), "m1"), ErrNotFound)
}

func TestRemove_TakesTheHostAndItsReading(t *testing.T) {
	bench := newServiceBench(t)
	if err := bench.service.Declare(context.Background(), "m1", bench.definition()); err != nil {
		t.Fatal(err)
	}
	if err := bench.service.Remove(context.Background(), "m1"); err != nil {
		t.Fatal(err)
	}
	if len(bench.store.hosts) != 0 || len(bench.store.readings) != 0 {
		t.Fatal("the host or its reading remains")
	}
	refusalOf(t, bench.service.Remove(context.Background(), "m1"), ErrNotFound)
	if err := bench.service.Remove(context.Background(), "ghost"); !errors.Is(err, machine.ErrNotFound) {
		t.Fatalf("got %v", err)
	}
}

func TestWatch_ReadsEveryDeclaredHostAtStartAndStopsWithItsContext(t *testing.T) {
	bench := newServiceBench(t)
	if err := bench.service.Declare(context.Background(), "m1", bench.definition()); err != nil {
		t.Fatal(err)
	}
	delete(bench.store.readings, "m1")

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		bench.service.Watch(ctx)
		close(done)
	}()
	deadline := time.After(10 * time.Second)
	for {
		bench.store.mu.Lock()
		_, read := bench.store.readings["m1"]
		bench.store.mu.Unlock()
		if read {
			break
		}
		select {
		case <-deadline:
			t.Fatal("the watch did not read the host at start")
		case <-time.After(10 * time.Millisecond):
		}
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the watch did not stop")
	}
}

func TestEvents_NeverCarryTheSecretNorTheAddress(t *testing.T) {
	definition, err := validDefinition().Normalize()
	if err != nil {
		t.Fatal(err)
	}
	event := setEvent(machine.Machine{ID: "m1", Name: "Proxmox"}, definition)
	if event.Action != audit.MachineProxmoxSet {
		t.Fatalf("action %q", event.Action)
	}
	for key, value := range event.Details {
		if strings.Contains(key, "secret") || strings.Contains(value, testTokenSecret) || strings.Contains(value, "https://") {
			t.Errorf("detail %s=%s", key, value)
		}
	}
	if event.Details["host"] != "pve.lan:8006" || event.Details["token_id"] != testTokenID {
		t.Errorf("details: %v", event.Details)
	}
}
