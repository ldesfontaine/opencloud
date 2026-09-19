package dockerwatch

import (
	"context"
	"encoding/json"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ldesfontaine/opencloud/internal/dockerapi"
	"github.com/ldesfontaine/opencloud/internal/service"
)

const fixtureID = "0082fc783011ead4e11be27f298f845fc39b25b371c0247ed7b7a798b8388513"

// Les fixtures sont celles du client, enregistrées sur un vrai démon.
func fixture(t *testing.T, name string) []byte {
	t.Helper()
	content, err := os.ReadFile(filepath.Join("..", "dockerapi", "testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return content
}

type recordingSink struct {
	mu      sync.Mutex
	reports []service.Report
	wake    chan struct{}
}

func newSink() *recordingSink {
	return &recordingSink{wake: make(chan struct{}, 64)}
}

func (s *recordingSink) Deliver(report service.Report) {
	s.mu.Lock()
	s.reports = append(s.reports, report)
	s.mu.Unlock()
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

// waitFor attend qu'un rapport satisfasse le prédicat, une seconde au plus.
func (s *recordingSink) waitFor(t *testing.T, what string, match func(service.Report) bool) service.Report {
	t.Helper()
	deadline := time.After(2 * time.Second)
	for {
		s.mu.Lock()
		for _, report := range s.reports {
			if match(report) {
				s.mu.Unlock()
				return report
			}
		}
		s.mu.Unlock()
		select {
		case <-s.wake:
		case <-deadline:
			t.Fatalf("no report matched: %s", what)
		}
	}
}

func (s *recordingSink) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.reports)
}

type daemon struct {
	mux   *http.ServeMux
	stats atomic.Int64
	// events tient le flux ouvert après les fixtures quand hold est vrai.
	hold bool
}

func newDaemon(t *testing.T, hold bool) *daemon {
	d := &daemon{mux: http.NewServeMux(), hold: hold}
	d.mux.HandleFunc("GET /v1.41/_ping", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Api-Version", "1.56")
		w.Header().Set("Server", "Docker/29.8.0 (linux)")
		_, _ = w.Write([]byte("OK"))
	})
	d.mux.HandleFunc("GET /v1.41/containers/json", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(fixture(t, "containers.json")) })
	d.mux.HandleFunc("GET /v1.41/containers/"+fixtureID+"/json", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(fixture(t, "inspect_running.json")) })
	d.mux.HandleFunc("GET /v1.41/containers/{id}/json", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"message":"no such container"}`))
	})
	d.mux.HandleFunc("GET /v1.41/containers/{id}/stats", func(w http.ResponseWriter, r *http.Request) {
		// Chaque appel avance les compteurs : 10 ms de processeur par
		// seconde de système, sur 20 cœurs.
		call := d.stats.Add(1)
		var stats map[string]any
		_ = json.Unmarshal(fixture(t, "stats.json"), &stats)
		cpu := stats["cpu_stats"].(map[string]any)
		cpu["cpu_usage"].(map[string]any)["total_usage"] = float64(call) * 10_000_000
		cpu["system_cpu_usage"] = float64(call) * 1_000_000_000
		_ = json.NewEncoder(w).Encode(stats)
	})
	d.mux.HandleFunc("GET /v1.41/containers/{id}/logs", func(w http.ResponseWriter, r *http.Request) {
		if r.PathValue("id") == "missing" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = w.Write(fixture(t, "logs_multiplexed.bin"))
		if r.URL.Query().Get("follow") == "1" {
			http.NewResponseController(w).Flush()
			<-r.Context().Done()
		}
	})
	d.mux.HandleFunc("GET /v1.41/events", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(fixture(t, "events.ndjson"))
		if d.hold {
			http.NewResponseController(w).Flush()
			<-r.Context().Done()
		}
	})
	return d
}

func serve(t *testing.T, handler http.Handler) *dockerapi.Client {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "oc")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	socket := filepath.Join(dir, "docker.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewUnstartedServer(handler)
	server.Listener = listener
	server.Start()
	t.Cleanup(server.Close)
	return dockerapi.New(socket)
}

func newWatcher(client *dockerapi.Client, sink Sink) *Watcher {
	return New(client, sink, Options{
		Logger: slog.New(slog.NewTextHandler(os.Stderr, nil)), SampleInterval: 20 * time.Millisecond,
		InventoryInterval: time.Hour, ProbeInterval: 20 * time.Millisecond,
	})
}

func TestRun_WithoutDocker_ReportsTheAbsenceOnce(t *testing.T) {
	sink := newSink()
	watcher := newWatcher(dockerapi.New("/tmp/none-oc.sock"), sink)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	watcher.Run(ctx)
	if sink.count() != 1 || sink.reports[0].Engine == nil || sink.reports[0].Engine.Present || sink.reports[0].Engine.Reason != service.ReasonNoSocket {
		t.Fatalf("reports = %+v", sink.reports)
	}
}

func TestRun_InventoriesThenTranslatesEvents(t *testing.T) {
	sink := newSink()
	watcher := newWatcher(serve(t, newDaemon(t, false).mux), sink)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go watcher.Run(ctx)

	engine := sink.waitFor(t, "engine", func(r service.Report) bool { return r.Engine != nil })
	if !engine.Engine.Present || engine.Engine.Version != "29.8.0" {
		t.Fatalf("engine = %+v", engine.Engine)
	}
	inventory := sink.waitFor(t, "inventory", func(r service.Report) bool { return r.Complete })
	if len(inventory.Inventory) != 1 {
		t.Fatalf("inventory = %+v", inventory.Inventory)
	}
	web := inventory.Inventory[0]
	if web.Name != "web" || web.Group != "fixture" || web.Image != "alpine:3.20" || web.State != service.StateRunning || web.StartedAt == nil {
		t.Fatalf("web = %+v", web)
	}
	if len(web.Ports) != 1 || web.Ports[0] != (service.Port{IP: "127.0.0.1", HostPort: 18080, ContainerPort: 80, Protocol: "tcp"}) {
		t.Fatalf("ports = %+v", web.Ports)
	}
	die := sink.waitFor(t, "die", func(r service.Report) bool { return len(r.Events) == 1 && r.Events[0].Action == "die" })
	event := die.Events[0]
	if event.State != service.StateExited || event.ExitCode == nil || *event.ExitCode != 137 || event.Snippet != "" || event.Container == nil {
		t.Fatalf("die = %+v", event)
	}
	destroy := sink.waitFor(t, "destroy", func(r service.Report) bool { return len(r.Events) == 1 && r.Events[0].Action == service.ActionDestroy })
	if destroy.Events[0].Container != nil {
		t.Fatal("a destroyed container has no snapshot")
	}
	sink.waitFor(t, "pause", func(r service.Report) bool { return len(r.Events) == 1 && r.Events[0].State == service.StatePaused })
	// kill et stop ne font pas d'événement.
	sink.mu.Lock()
	defer sink.mu.Unlock()
	for _, report := range sink.reports {
		for _, event := range report.Events {
			if event.Action == "kill" || event.Action == "stop" {
				t.Fatalf("unexpected event %+v", event)
			}
		}
	}
	if watcher.since == "" {
		t.Fatal("since should follow the last event")
	}
}

func TestRun_SecondMeasureGivesACPUPercent(t *testing.T) {
	sink := newSink()
	watcher := newWatcher(serve(t, newDaemon(t, true).mux), sink)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go watcher.Run(ctx)
	stats := sink.waitFor(t, "stats", func(r service.Report) bool { return len(r.Stats) > 0 })
	stat := stats.Stats[0]
	// 10 ms sur 1 s de système, 20 cœurs : 20 %.
	if stat.ContainerID != fixtureID || stat.CPUPercent < 19.9 || stat.CPUPercent > 20.1 || stat.MemUsed != 2846720-40960 || stat.MemLimit != 29199167488 {
		t.Fatalf("stat = %+v", stat)
	}
}

func TestOpenLogs_BatchesThenDone(t *testing.T) {
	watcher := newWatcher(serve(t, newDaemon(t, true).mux), newSink())
	batches, err := watcher.OpenLogs(context.Background(), service.LogRequest{ContainerID: fixtureID, Tail: 5})
	if err != nil {
		t.Fatal(err)
	}
	var lines []service.LogLine
	for batch := range batches {
		lines = append(lines, batch.Lines...)
		if batch.Error != "" {
			t.Fatalf("error = %s", batch.Error)
		}
	}
	if len(lines) != 5 || lines[1].Stream != "stderr" || lines[0].Text != "tick 0" {
		t.Fatalf("lines = %+v", lines)
	}
	if watcher.follows != 0 {
		t.Fatal("follow not released")
	}
}

func TestOpenLogs_UnknownContainerAndBusyMachine(t *testing.T) {
	watcher := newWatcher(serve(t, newDaemon(t, true).mux), newSink())
	batches, _ := watcher.OpenLogs(context.Background(), service.LogRequest{ContainerID: "missing"})
	if batch := <-batches; batch.Error != LogsNotFound {
		t.Fatalf("batch = %+v", batch)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	for i := 0; i < service.MaxFollowsPerMachine; i++ {
		if _, err := watcher.OpenLogs(ctx, service.LogRequest{ContainerID: fixtureID, Follow: true}); err != nil {
			t.Fatal(err)
		}
	}
	batches, _ = watcher.OpenLogs(ctx, service.LogRequest{ContainerID: fixtureID, Follow: true})
	if batch := <-batches; batch.Error != LogsBusy {
		t.Fatalf("batch = %+v", batch)
	}
}

func TestTranslate_MapsActionsToStates(t *testing.T) {
	cases := map[string]struct {
		state  service.State
		health service.Health
		wanted bool
	}{
		"start": {service.StateRunning, "", true}, "unpause": {service.StateRunning, "", true},
		"pause": {service.StatePaused, "", true}, "create": {service.StateCreated, "", true},
		"die": {service.StateExited, "", true}, "destroy": {"", "", true}, "rename": {"", "", true},
		"health_status: unhealthy": {"", service.HealthUnhealthy, true},
		"kill":                     {"", "", false}, "stop": {"", "", false}, "exec_start: sh": {"", "", false}, "oom": {"", "", false},
	}
	for action, expected := range cases {
		event, wanted := translate(dockerapi.Event{Action: action})
		if wanted != expected.wanted || event.State != expected.state || event.Health != expected.health {
			t.Errorf("%s → %+v wanted=%v", action, event, wanted)
		}
	}
}

func TestPublishedPorts_SortsAndSkipsUnpublished(t *testing.T) {
	ports := publishedPorts(map[string][]dockerapi.PortBinding{
		"443/tcp": {{HostIP: "0.0.0.0", HostPort: "443"}},
		"80/tcp":  {{HostIP: "0.0.0.0", HostPort: "80"}, {HostIP: "::", HostPort: "80"}},
		"53/udp":  nil,
	})
	if len(ports) != 3 || ports[0].HostPort != 80 || ports[2].ContainerPort != 443 || ports[0].IP != "0.0.0.0" {
		t.Fatalf("ports = %+v", ports)
	}
}
