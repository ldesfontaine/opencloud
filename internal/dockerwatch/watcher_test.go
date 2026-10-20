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
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ldesfontaine/opencloud/internal/brand"
	"github.com/ldesfontaine/opencloud/internal/container"
	"github.com/ldesfontaine/opencloud/internal/dockerapi"
)

const (
	fixtureID   = "0082fc783011ead4e11be27f298f845fc39b25b371c0247ed7b7a798b8388513"
	networkedID = "0b8a6dace3720f0be321b35a86049366e9609bd998d8da680b249209360fa346"
)

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
	reports []container.Report
	wake    chan struct{}
}

func newSink() *recordingSink {
	return &recordingSink{wake: make(chan struct{}, 64)}
}

func (s *recordingSink) Deliver(report container.Report) {
	s.mu.Lock()
	s.reports = append(s.reports, report)
	s.mu.Unlock()
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

// waitFor attend qu'un rapport satisfasse le prédicat, une seconde au plus.
func (s *recordingSink) waitFor(t *testing.T, what string, match func(container.Report) bool) container.Report {
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
	// onEvents remplace le flux enregistré quand un test veut le sien.
	onEvents func(w http.ResponseWriter, r *http.Request)
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
	d.mux.HandleFunc("GET /v1.41/containers/"+networkedID+"/json", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(fixture(t, "inspect_networked.json")) })
	d.mux.HandleFunc("GET /v1.41/networks", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(fixture(t, "networks.json")) })
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
		if d.onEvents != nil {
			d.onEvents(w, r)
			return
		}
		_, _ = w.Write(fixture(t, "events.ndjson"))
		_, _ = w.Write(fixture(t, "events_network.ndjson"))
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
	if sink.count() != 1 || sink.reports[0].Engine == nil || sink.reports[0].Engine.Present || sink.reports[0].Engine.Reason != container.ReasonNoSocket {
		t.Fatalf("reports = %+v", sink.reports)
	}
}

// Le démon ne répond pas à la sonde ; on annule pendant qu'elle attend. Run
// part sans rien livrer : l'annulation est la nôtre, pas celle du démon.
func TestRun_CancelledDuringPing_DeliversNoReport(t *testing.T) {
	// Borne large : elle ne sert qu'à ne pas bloquer la CI si le code est cassé.
	const patience = 30 * time.Second
	pinging := make(chan struct{})
	var announce sync.Once
	client := serve(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		announce.Do(func() { close(pinging) })
		<-r.Context().Done()
	}))
	sink := newSink()
	watcher := newWatcher(client, sink)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stopped := make(chan struct{})
	go func() {
		watcher.Run(ctx)
		close(stopped)
	}()

	select {
	case <-pinging:
	case <-time.After(patience):
		t.Fatal("the ping never reached the daemon")
	}
	cancel()
	select {
	case <-stopped:
	case <-time.After(patience):
		t.Fatal("Run did not stop after the cancellation")
	}
	sink.mu.Lock()
	defer sink.mu.Unlock()
	if len(sink.reports) != 0 {
		t.Fatalf("reports = %+v", sink.reports)
	}
}

func TestRun_InventoriesThenTranslatesEvents(t *testing.T) {
	sink := newSink()
	watcher := newWatcher(serve(t, newDaemon(t, false).mux), sink)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go watcher.Run(ctx)

	engine := sink.waitFor(t, "engine", func(r container.Report) bool { return r.Engine != nil })
	if !engine.Engine.Present || engine.Engine.Version != "29.8.0" {
		t.Fatalf("engine = %+v", engine.Engine)
	}
	inventory := sink.waitFor(t, "inventory", func(r container.Report) bool { return r.Complete })
	if len(inventory.Inventory) != 1 || !inventory.NetworksComplete || len(inventory.Networks) != 8 {
		t.Fatalf("inventory = %+v networks = %d", inventory.Inventory, len(inventory.Networks))
	}
	var back container.NetworkReport
	for _, network := range inventory.Networks {
		if network.Name == "ocfix_back" {
			back = network
		}
	}
	if !back.Internal || back.Driver != "bridge" || back.Group != "ocfix" || len(back.NetworkID) != 64 {
		t.Fatalf("back = %+v", back)
	}
	web := inventory.Inventory[0]
	if web.Name != "web" || web.Group != "fixture" || web.Image != "alpine:3.20" || web.State != container.StateRunning || web.StartedAt == nil {
		t.Fatalf("web = %+v", web)
	}
	if len(web.Ports) != 1 || web.Ports[0] != (container.Port{IP: "127.0.0.1", HostPort: 18080, ContainerPort: 80, Protocol: "tcp"}) {
		t.Fatalf("ports = %+v", web.Ports)
	}
	if web.NetworkMode != "bridge" || web.Privileged || len(web.Networks) != 1 || web.Networks[0].Name != "bridge" || web.Networks[0].IP != "172.17.0.2" || len(web.DependsOn) != 0 {
		t.Fatalf("networking = %+v", web)
	}
	die := sink.waitFor(t, "die", func(r container.Report) bool { return len(r.Events) == 1 && r.Events[0].Action == "die" })
	event := die.Events[0]
	// La fixture est un docker stop dont le délai expire : 137, mais demandé.
	if event.State != container.StateExited || event.ExitCode == nil || *event.ExitCode != 137 || event.Killed || event.Snippet != "" || event.Container == nil {
		t.Fatalf("die = %+v", event)
	}
	destroy := sink.waitFor(t, "destroy", func(r container.Report) bool {
		return len(r.Events) == 1 && r.Events[0].Action == container.ActionDestroy
	})
	if destroy.Events[0].Container != nil {
		t.Fatal("a destroyed container has no snapshot")
	}
	sink.waitFor(t, "pause", func(r container.Report) bool { return len(r.Events) == 1 && r.Events[0].State == container.StatePaused })
	// Puis la fixture réseau : un connect réinspecte le conteneur avec ses
	// réseaux, un create ou un destroy de réseau relit la liste.
	connect := sink.waitFor(t, "network connect", func(r container.Report) bool {
		return len(r.Events) == 1 && r.Events[0].Action == container.ActionNetworkConnect && r.Events[0].ContainerID == networkedID
	})
	if connect.Events[0].State != "" || connect.Events[0].Container == nil || len(connect.Events[0].Container.Networks) != 2 {
		t.Fatalf("connect = %+v", connect.Events[0])
	}
	relisted := sink.waitFor(t, "networks relisted", func(r container.Report) bool { return r.NetworksComplete && !r.Complete })
	if len(relisted.Networks) != 8 {
		t.Fatalf("relisted = %d", len(relisted.Networks))
	}
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
	// Run tourne encore : since se lit sous le verrou.
	watcher.mu.Lock()
	defer watcher.mu.Unlock()
	if watcher.since == "" {
		t.Fatal("since should follow the last event")
	}
}

// dockerEvent fait un événement de conteneur comme le démon le pousse.
func dockerEvent(id, action string, at time.Time, attributes map[string]string) dockerapi.Event {
	event := dockerapi.Event{Type: "container", Action: action, TimeNano: at.UnixNano()}
	event.Actor.ID = id
	event.Actor.Attributes = attributes
	return event
}

// Ce que Docker émet réellement : docker kill envoie SIGKILL d'emblée ;
// docker stop envoie d'abord le signal d'arrêt, puis SIGKILL au bout du
// délai, puis « stop ». compose down et rm -f retirent le conteneur.
func TestHandleEvent_A137WithoutAStopRequestIsKilled(t *testing.T) {
	start := time.Date(2026, 10, 10, 16, 0, 0, 0, time.UTC)
	killSignal := map[string]string{"signal": "9"}
	termSignal := map[string]string{"signal": "15"}
	died := map[string]string{"exitCode": "137"}
	cases := []struct {
		name   string
		id     string
		events []dockerapi.Event
		killed bool
	}{
		{"docker kill", fixtureID, []dockerapi.Event{
			dockerEvent(fixtureID, "kill", start, killSignal),
			dockerEvent(fixtureID, "die", start, died),
		}, true},
		{"docker stop -t 0", fixtureID, []dockerapi.Event{
			dockerEvent(fixtureID, "kill", start, termSignal),
			dockerEvent(fixtureID, "kill", start, killSignal),
			dockerEvent(fixtureID, "stop", start, nil),
			dockerEvent(fixtureID, "die", start, died),
		}, false},
		{"docker stop whose delay expires", fixtureID, []dockerapi.Event{
			dockerEvent(fixtureID, "kill", start, termSignal),
			dockerEvent(fixtureID, "kill", start.Add(10*time.Second), killSignal),
			dockerEvent(fixtureID, "die", start.Add(10*time.Second), died),
		}, false},
		{"a stop long ago does not cover a later kill", fixtureID, []dockerapi.Event{
			dockerEvent(fixtureID, "stop", start, nil),
			dockerEvent(fixtureID, "kill", start.Add(time.Hour), killSignal),
			dockerEvent(fixtureID, "die", start.Add(time.Hour), died),
		}, true},
		{"a stop before a restart does not cover the next kill", fixtureID, []dockerapi.Event{
			dockerEvent(fixtureID, "stop", start, nil),
			dockerEvent(fixtureID, "start", start, nil),
			dockerEvent(fixtureID, "kill", start, killSignal),
			dockerEvent(fixtureID, "die", start, died),
		}, true},
		{"docker rm -f, the container is gone", "removed", []dockerapi.Event{
			dockerEvent("removed", "kill", start, killSignal),
			dockerEvent("removed", "die", start, died),
		}, false},
		{"SIGTERM is a clean stop", fixtureID, []dockerapi.Event{
			dockerEvent(fixtureID, "die", start, map[string]string{"exitCode": "143"}),
		}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sink := newSink()
			watcher := newWatcher(serve(t, newDaemon(t, false).mux), sink)
			for _, raw := range tc.events {
				watcher.handleEvent(context.Background(), raw)
			}
			die := sink.waitFor(t, "die", func(r container.Report) bool { return len(r.Events) == 1 && r.Events[0].Action == "die" })
			if die.Events[0].Killed != tc.killed {
				t.Fatalf("killed = %v, want %v", die.Events[0].Killed, tc.killed)
			}
			if tc.killed && die.Events[0].Snippet == "" {
				t.Fatal("a brutal kill keeps the end of the log")
			}
		})
	}
}

func TestRun_SecondMeasureGivesACPUPercent(t *testing.T) {
	sink := newSink()
	watcher := newWatcher(serve(t, newDaemon(t, true).mux), sink)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go watcher.Run(ctx)
	stats := sink.waitFor(t, "stats", func(r container.Report) bool { return len(r.Stats) > 0 })
	stat := stats.Stats[0]
	// 10 ms sur 1 s de système, 20 cœurs : 20 %.
	if stat.ContainerID != fixtureID || stat.CPUPercent < 19.9 || stat.CPUPercent > 20.1 || stat.MemUsed != 2846720-40960 || stat.MemLimit != 29199167488 {
		t.Fatalf("stat = %+v", stat)
	}
}

func TestOpenLogs_BatchesThenDone(t *testing.T) {
	watcher := newWatcher(serve(t, newDaemon(t, true).mux), newSink())
	batches, err := watcher.OpenLogs(context.Background(), container.LogRequest{ContainerID: fixtureID, Tail: 5})
	if err != nil {
		t.Fatal(err)
	}
	var lines []container.LogLine
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
	batches, _ := watcher.OpenLogs(context.Background(), container.LogRequest{ContainerID: "missing"})
	if batch := <-batches; batch.Error != LogsNotFound {
		t.Fatalf("batch = %+v", batch)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	for i := 0; i < container.MaxFollowsPerMachine; i++ {
		if _, err := watcher.OpenLogs(ctx, container.LogRequest{ContainerID: fixtureID, Follow: true}); err != nil {
			t.Fatal(err)
		}
	}
	batches, _ = watcher.OpenLogs(ctx, container.LogRequest{ContainerID: fixtureID, Follow: true})
	if batch := <-batches; batch.Error != LogsBusy {
		t.Fatalf("batch = %+v", batch)
	}
}

func TestTranslate_MapsActionsToStates(t *testing.T) {
	cases := map[string]struct {
		state  container.State
		health container.Health
		wanted bool
	}{
		"start": {container.StateRunning, "", true}, "unpause": {container.StateRunning, "", true},
		"pause": {container.StatePaused, "", true}, "create": {container.StateCreated, "", true},
		"die": {container.StateExited, "", true}, "destroy": {"", "", true}, "rename": {"", "", true},
		"health_status: unhealthy": {"", container.HealthUnhealthy, true},
		"kill":                     {"", "", false}, "stop": {"", "", false}, "exec_start: sh": {"", "", false}, "oom": {"", "", false},
	}
	for action, expected := range cases {
		event, wanted := translate(dockerapi.Event{Action: action})
		if wanted != expected.wanted || event.State != expected.state || event.Health != expected.health {
			t.Errorf("%s → %+v wanted=%v", action, event, wanted)
		}
	}
}

func TestTranslate_NetworkEventsNameTheContainer(t *testing.T) {
	connect := dockerapi.Event{Type: "network", Action: "connect"}
	connect.Actor.ID = "net"
	connect.Actor.Attributes = map[string]string{"container": networkedID, "name": "ocfix_back"}
	event, wanted := translate(connect)
	if !wanted || event.Action != container.ActionNetworkConnect || event.ContainerID != networkedID || event.State != "" {
		t.Fatalf("connect → %+v %v", event, wanted)
	}
	create := dockerapi.Event{Type: "network", Action: "create"}
	if _, wanted := translate(create); wanted {
		t.Fatal("a network create is not a container event")
	}
}

// Le conteneur Compose de la fixture : deux réseaux par nom, l'id court
// retiré des alias, les dépendances du label dans l'ordre du label.
func TestToContainer_ReadsNetworksAndDependencies(t *testing.T) {
	var inspected dockerapi.Container
	if err := json.Unmarshal(fixture(t, "inspect_networked.json"), &inspected); err != nil {
		t.Fatal(err)
	}
	observed := toContainer(inspected)
	if observed.NetworkMode != "ocfix_back" || observed.Privileged || observed.Group != "ocfix" || observed.Name != "web" {
		t.Fatalf("container = %+v", observed)
	}
	if len(observed.Networks) != 2 || observed.Networks[0].Name != "ocfix_back" || observed.Networks[1].Name != "ocfix_front" {
		t.Fatalf("networks = %+v", observed.Networks)
	}
	back := observed.Networks[0]
	if back.IP != "172.20.0.4" || len(back.NetworkID) != 64 || strings.Join(back.Aliases, ",") != "ocfix-web-1,web" {
		t.Fatalf("back = %+v", back)
	}
	if len(observed.DependsOn) != 2 || observed.DependsOn[0] != (container.Dependency{Name: "cache", Source: container.DependencyCompose}) || observed.DependsOn[1].Name != "db" {
		t.Fatalf("depends = %+v", observed.DependsOn)
	}
	if len(observed.Ports) != 2 || !observed.Ports[0].IsPublic() {
		t.Fatalf("ports = %+v", observed.Ports)
	}
	inspected.Host.Links = []string{"/legacy-db:/web/db"}
	if deps := dependencies(inspected); len(deps) != 3 || deps[2] != (container.Dependency{Name: "legacy-db", Source: container.DependencyLink}) {
		t.Fatalf("links = %+v", deps)
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

// Docker ne donne pas encore d'identifiant de réseau à un conteneur tout
// juste créé : le rattachement est écarté, le conteneur reste.
func TestToContainer_SkipsANetworkWithoutIdentifier(t *testing.T) {
	var inspected dockerapi.Container
	if err := json.Unmarshal(fixture(t, "inspect_created.json"), &inspected); err != nil {
		t.Fatal(err)
	}
	observed := toContainer(inspected)
	if observed.Name != "rd-test" || observed.State != container.StateCreated || len(observed.Networks) != 0 {
		t.Fatalf("container = %+v", observed)
	}
}

// Seuls nos labels partent : un label Traefik, Compose ou d'une autre marque
// ne quitte jamais la machine, et le préfixe est retiré.
func TestToContainer_ReadsOnlyOwnLabels(t *testing.T) {
	var inspected dockerapi.Container
	if err := json.Unmarshal(fixture(t, "inspect_networked.json"), &inspected); err != nil {
		t.Fatal(err)
	}
	// La fixture porte déjà des labels Compose : ils restent chez elle.
	if len(inspected.Config.Labels) == 0 {
		t.Fatal("la fixture doit porter des labels Compose")
	}
	inspected.Config.Labels[brand.LabelPrefix+container.LabelService] = "svc_0123456789abcdef"
	inspected.Config.Labels[brand.LabelPrefix+container.LabelUnit] = "web"
	inspected.Config.Labels["traefik.http.routers.web.rule"] = "Host(`example.org`)"
	inspected.Config.Labels["org.opencontainers.image.source"] = "https://example.org/repo"
	// Même racine que le préfixe, sans le point : ce n'est pas notre label.
	inspected.Config.Labels[brand.ID+"y.service"] = "svc_autre"

	got := toContainer(inspected).Labels
	want := map[string]string{container.LabelService: "svc_0123456789abcdef", container.LabelUnit: "web"}
	if len(got) != len(want) || got[container.LabelService] != want[container.LabelService] || got[container.LabelUnit] != want[container.LabelUnit] {
		t.Fatalf("labels = %v, want %v", got, want)
	}

	delete(inspected.Config.Labels, brand.LabelPrefix+container.LabelService)
	delete(inspected.Config.Labels, brand.LabelPrefix+container.LabelUnit)
	if labels := toContainer(inspected).Labels; labels != nil {
		t.Fatalf("sans label de la marque, labels = %v, want nil", labels)
	}
}

func TestToContainer_BoundsOwnLabels(t *testing.T) {
	prefix := brand.LabelPrefix
	t.Run("keys and values out of bounds are dropped", func(t *testing.T) {
		got := ownLabels(map[string]string{
			prefix + "service":                     "svc_ok",
			prefix + "unit":                        "web",
			prefix + "Majuscule":                   "refusé : hors de l'alphabet",
			prefix + "-tiret":                      "refusé : commence par un tiret",
			prefix + "":                            "refusé : suffixe vide",
			prefix + strings.Repeat("k", 64):       "refusé : suffixe trop long",
			prefix + strings.Repeat("k", 63):       "accepté : 63 caractères",
			prefix + "vide":                        "",
			prefix + "long":                        strings.Repeat("v", container.MaxNameLength+1),
			prefix + "limite":                      strings.Repeat("v", container.MaxNameLength),
			prefix + "avec.des_points-et-tirets-1": "accepté",
		})
		for _, suffix := range []string{"service", "unit", strings.Repeat("k", 63), "limite", "avec.des_points-et-tirets-1"} {
			if _, kept := got[suffix]; !kept {
				t.Errorf("le suffixe %q devait rester", suffix)
			}
		}
		if len(got) != 5 {
			t.Errorf("labels = %d, want 5 : %v", len(got), got)
		}
	})
	t.Run("seventeen keys are cut to sixteen, the two read ones first", func(t *testing.T) {
		labels := map[string]string{}
		for index := range 17 {
			labels[prefix+"a"+string(rune('a'+index))] = "x"
		}
		labels[prefix+container.LabelService] = "svc_ok"
		labels[prefix+container.LabelUnit] = "web"
		got := ownLabels(labels)
		if len(got) != container.MaxLabelsPerContainer {
			t.Fatalf("labels = %d, want %d", len(got), container.MaxLabelsPerContainer)
		}
		if got[container.LabelService] != "svc_ok" || got[container.LabelUnit] != "web" {
			t.Fatalf("les deux labels lus doivent passer avant la coupe : %v", got)
		}
	})
	t.Run("a label that cannot be kept is never a silent absence", func(t *testing.T) {
		// Rien de lisible mais des labels de la marque écartés : la clé réservée
		// dit au serveur que le conteneur est étiqueté, mal. Sans elle, il
		// retomberait sur Compose ou sur son nom.
		cases := map[string]map[string]string{
			"uppercase suffix":      {prefix + "Service": "svc_0123456789abcdef", prefix + "unit": ""},
			"uppercase prefix":      {strings.ToUpper(prefix) + "service": "svc_0123456789abcdef"},
			"mixed case prefix":     {strings.ToUpper(prefix[:1]) + prefix[1:] + "service": "svc_0123456789abcdef"},
			"value too long":        {prefix + "service": strings.Repeat("v", container.MaxNameLength+1)},
			"control character":     {prefix + "service": "svc_0123456789abcdef\n"},
			"empty value":           {prefix + "unit": ""},
			"only an extra is kept": {prefix + "extra": "ok", prefix + "Service": "svc_0123456789abcdef"},
			"half a pair is kept":   {prefix + "unit": "web", prefix + "Service": "svc_0123456789abcdef"},
		}
		for name, labels := range cases {
			got := ownLabels(labels)
			if _, flagged := got[container.LabelInvalid]; !flagged {
				t.Errorf("%s : labels = %v, want the reserved invalid key", name, got)
			}
			if got[container.LabelService] != "" {
				t.Errorf("%s : labels = %v, the service was discarded", name, got)
			}
		}
		if got := ownLabels(cases["uppercase suffix"]); got[container.LabelInvalid] != "2" {
			t.Errorf("la valeur compte les clés écartées : %v", got)
		}
	})
	t.Run("a complete pair is not flagged, junk or not", func(t *testing.T) {
		got := ownLabels(map[string]string{prefix + "service": "svc_0123456789abcdef", prefix + "unit": "web", prefix + "Autre": "x", prefix + "extra": "ok"})
		if _, flagged := got[container.LabelInvalid]; flagged {
			t.Fatalf("labels = %v, the pair is readable: a junk key beside it costs nothing", got)
		}
		if got[container.LabelService] == "" || got[container.LabelUnit] == "" || got["extra"] != "ok" {
			t.Fatalf("labels = %v", got)
		}
	})
	t.Run("a pair with nothing discarded is the server's to judge", func(t *testing.T) {
		got := ownLabels(map[string]string{prefix + "service": "svc_0123456789abcdef"})
		if _, flagged := got[container.LabelInvalid]; flagged || len(got) != 1 {
			t.Fatalf("labels = %v, an incomplete pair is reported as such by the server", got)
		}
	})
	t.Run("an image cannot write the reserved key", func(t *testing.T) {
		got := ownLabels(map[string]string{prefix + "service": "svc_0123456789abcdef", prefix + "unit": "web", prefix + container.LabelInvalid: "0"})
		if _, kept := got[container.LabelInvalid]; kept {
			t.Fatalf("labels = %v, the reserved key is the agent's alone", got)
		}
		alone := ownLabels(map[string]string{prefix + container.LabelInvalid: "0"})
		if alone[container.LabelInvalid] != "1" {
			t.Fatalf("labels = %v, a lone reserved key is one discarded key", alone)
		}
	})
	t.Run("the flag holds a slot when all sixteen are taken", func(t *testing.T) {
		labels := map[string]string{prefix + "Majuscule": "écarté"}
		for index := range 20 {
			labels[prefix+"a"+string(rune('a'+index))] = "x"
		}
		got := ownLabels(labels)
		if len(got) != container.MaxLabelsPerContainer {
			t.Fatalf("labels = %d, want %d", len(got), container.MaxLabelsPerContainer)
		}
		if _, flagged := got[container.LabelInvalid]; !flagged {
			t.Fatalf("labels = %v, want the reserved key", got)
		}
	})
	t.Run("nothing left gives nil", func(t *testing.T) {
		if got := ownLabels(map[string]string{"traefik.enable": "true"}); got != nil {
			t.Fatalf("labels = %v, want nil", got)
		}
		if got := ownLabels(nil); got != nil {
			t.Fatalf("labels = %v, want nil", got)
		}
	})
}
