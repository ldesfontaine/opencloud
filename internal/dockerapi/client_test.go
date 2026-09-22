package dockerapi

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeDaemon sert les fixtures enregistrées sur un vrai démon (Docker
// 29.8, API 1.56) derrière une socket Unix temporaire.
func fakeDaemon(t *testing.T, handler http.Handler) *Client {
	t.Helper()
	socket := filepath.Join(shortTempDir(t), "docker.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewUnstartedServer(handler)
	server.Listener = listener
	server.Start()
	t.Cleanup(server.Close)
	return New(socket)
}

// Une socket Unix a un chemin court : t.TempDir peut dépasser la limite.
func shortTempDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "oc")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return dir
}

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	content, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return content
}

func serveFixture(t *testing.T, name string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(fixture(t, name))
	}
}

func fixtureMux(t *testing.T) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1.41/_ping", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Api-Version", "1.56")
		w.Header().Set("Server", "Docker/29.8.0 (linux)")
		_, _ = w.Write([]byte("OK"))
	})
	mux.HandleFunc("GET /v1.41/containers/json", serveFixture(t, "containers.json"))
	mux.HandleFunc("GET /v1.41/containers/0082fc783011ead4e11be27f298f845fc39b25b371c0247ed7b7a798b8388513/json", serveFixture(t, "inspect_running.json"))
	mux.HandleFunc("GET /v1.41/containers/exited/json", serveFixture(t, "inspect_exited.json"))
	mux.HandleFunc("GET /v1.41/containers/networked/json", serveFixture(t, "inspect_networked.json"))
	mux.HandleFunc("GET /v1.41/networks", serveFixture(t, "networks.json"))
	mux.HandleFunc("GET /v1.41/images/alpine:3.20/json", serveFixture(t, "image_alpine.json"))
	mux.HandleFunc("GET /v1.41/images/portfolio:latest/json", serveFixture(t, "image_local.json"))
	mux.HandleFunc("GET /v1.41/images/{name}/json", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"message":"No such image: nope"}`))
	})
	mux.HandleFunc("GET /v1.41/containers/{id}/json", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"message":"No such container: nope"}`))
	})
	mux.HandleFunc("GET /v1.41/containers/{id}/stats", serveFixture(t, "stats.json"))
	mux.HandleFunc("GET /v1.41/containers/{id}/logs", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("tail") != "5" || r.URL.Query().Get("timestamps") != "1" {
			t.Errorf("logs query: %s", r.URL.RawQuery)
		}
		_, _ = w.Write(fixture(t, "logs_multiplexed.bin"))
	})
	mux.HandleFunc("GET /v1.41/events", func(w http.ResponseWriter, r *http.Request) {
		if filters := r.URL.Query().Get("filters"); !strings.Contains(filters, "container") || !strings.Contains(filters, "network") {
			t.Errorf("events filters: %s", r.URL.RawQuery)
		}
		if r.URL.Query().Get("since") == "network" {
			_, _ = w.Write(fixture(t, "events_network.ndjson"))
			return
		}
		_, _ = w.Write(fixture(t, "events.ndjson"))
	})
	return mux
}

func TestPing_ReadsEngineVersions(t *testing.T) {
	client := fakeDaemon(t, fixtureMux(t))
	engine, err := client.Ping(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if engine.Version != "29.8.0" || engine.APIVersion != "1.56" {
		t.Fatalf("engine = %+v", engine)
	}
}

func TestPing_RefusesAnOlderAPI(t *testing.T) {
	client := fakeDaemon(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Api-Version", "1.24")
		_, _ = w.Write([]byte("OK"))
	}))
	_, err := client.Ping(context.Background())
	if !errors.Is(err, ErrVersionTooOld) {
		t.Fatalf("err = %v", err)
	}
}

func TestPing_WithoutSocket_SaysSo(t *testing.T) {
	client := New(filepath.Join(shortTempDir(t), "none.sock"))
	_, err := client.Ping(context.Background())
	if !errors.Is(err, ErrNoSocket) {
		t.Fatalf("err = %v", err)
	}
}

func TestPing_SocketWithoutListener_IsEngineDown(t *testing.T) {
	socket := filepath.Join(shortTempDir(t), "dead.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	// Fermer sans retirer le fichier : la socket existe, personne n'écoute.
	listener.(*net.UnixListener).SetUnlinkOnClose(false)
	listener.Close()
	_, err = New(socket).Ping(context.Background())
	if !errors.Is(err, ErrEngineDown) {
		t.Fatalf("err = %v", err)
	}
}

func TestPing_SocketDenied_SaysSo(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root lit tout")
	}
	client := fakeDaemon(t, fixtureMux(t))
	socket := client.socket
	if err := os.Chmod(socket, 0); err != nil {
		t.Fatal(err)
	}
	_, err := client.Ping(context.Background())
	if !errors.Is(err, ErrSocketDenied) {
		t.Fatalf("err = %v", err)
	}
}

func TestListContainers_ReadsTheFixture(t *testing.T) {
	client := fakeDaemon(t, fixtureMux(t))
	list, err := client.ListContainers(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 6 {
		t.Fatalf("len = %d", len(list))
	}
	first := list[0]
	if first.Name() != "opencloud-fixture" || first.State != "running" || first.Image != "alpine:3.20" {
		t.Fatalf("first = %+v", first)
	}
	if first.Labels[LabelComposeProject] != "fixture" || first.Labels[LabelComposeService] != "web" || first.IsOneOff() {
		t.Fatalf("labels = %v", first.Labels)
	}
	if len(first.Ports) != 1 || first.Ports[0].PublicPort != 18080 || first.Ports[0].IP != "127.0.0.1" {
		t.Fatalf("ports = %+v", first.Ports)
	}
}

func TestInspect_RunningAndExited(t *testing.T) {
	client := fakeDaemon(t, fixtureMux(t))
	running, err := client.Inspect(context.Background(), "0082fc783011ead4e11be27f298f845fc39b25b371c0247ed7b7a798b8388513")
	if err != nil {
		t.Fatal(err)
	}
	if running.ShortName() != "opencloud-fixture" || running.State.Status != "running" || running.Config.Image != "alpine:3.20" || running.HasHealthcheck() || running.Config.Tty {
		t.Fatalf("running = %+v", running)
	}
	if ParseTime(running.State.StartedAt).IsZero() || !ParseTime(running.State.FinishedAt).IsZero() {
		t.Fatalf("dates = %s / %s", running.State.StartedAt, running.State.FinishedAt)
	}
	if bindings := running.Network.Ports["80/tcp"]; len(bindings) != 1 || bindings[0].HostPort != "18080" {
		t.Fatalf("ports = %+v", running.Network.Ports)
	}
	exited, err := client.Inspect(context.Background(), "exited")
	if err != nil {
		t.Fatal(err)
	}
	if exited.State.Status != "exited" || ParseTime(exited.State.FinishedAt).IsZero() {
		t.Fatalf("exited = %+v", exited.State)
	}
}

// Un conteneur Compose sur deux réseaux, dont un interne, avec ses
// dépendances déclarées : ce que la feature réseau lit en plus.
func TestInspect_ReadsNetworksModeAndDependencies(t *testing.T) {
	client := fakeDaemon(t, fixtureMux(t))
	web, err := client.Inspect(context.Background(), "networked")
	if err != nil {
		t.Fatal(err)
	}
	if web.Host.NetworkMode != "ocfix_back" || web.Host.Privileged || len(web.Host.Links) != 0 {
		t.Fatalf("host = %+v", web.Host)
	}
	if web.Config.Labels[LabelComposeDependsOn] != "cache:service_started:false,db:service_started:false" {
		t.Fatalf("depends_on = %q", web.Config.Labels[LabelComposeDependsOn])
	}
	back, ok := web.Network.Networks["ocfix_back"]
	if !ok || back.NetworkID != "0af01ef1573714129ced483d26f2250c1de6ce40a2ae2b7911ecc9534c02c3bf" || back.IPAddress != "172.20.0.4" || len(back.Aliases) != 3 {
		t.Fatalf("back = %+v", back)
	}
	if bindings := web.Network.Ports["80/tcp"]; len(bindings) != 2 || bindings[0].HostIP != "0.0.0.0" || bindings[1].HostIP != "::" {
		t.Fatalf("ports = %+v", web.Network.Ports)
	}
}

func TestListNetworks_ReadsDriverAndInternal(t *testing.T) {
	client := fakeDaemon(t, fixtureMux(t))
	networks, err := client.ListNetworks(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	byName := make(map[string]Network, len(networks))
	for _, network := range networks {
		byName[network.Name] = network
	}
	if len(networks) != 8 || byName["host"].Driver != "host" || byName["none"].Driver != "null" || byName["bridge"].Internal {
		t.Fatalf("networks = %+v", networks)
	}
	back := byName["ocfix_back"]
	if !back.Internal || back.Driver != "bridge" || back.Labels[LabelComposeProject] != "ocfix" || len(back.ID) != 64 {
		t.Fatalf("back = %+v", back)
	}
}

// Les événements de réseau : l'acteur est le réseau, « container » dit qui
// se connecte ; un « create » de réseau n'a pas de conteneur.
func TestEvents_NetworkEventsNameTheContainer(t *testing.T) {
	client := fakeDaemon(t, fixtureMux(t))
	stream, err := client.Events(context.Background(), "network")
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	var connects, creates int
	for {
		event, err := stream.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if event.Type != "network" {
			continue
		}
		switch event.Action {
		case "connect", "disconnect":
			connects++
			if len(event.Actor.Attributes["container"]) != 64 || event.Actor.Attributes["name"] == "" {
				t.Fatalf("event = %+v", event)
			}
		case "create", "destroy":
			creates++
			if _, has := event.Actor.Attributes["container"]; has {
				t.Fatalf("event = %+v", event)
			}
		}
	}
	if connects != 5 || creates != 2 {
		t.Fatalf("connects = %d creates = %d", connects, creates)
	}
}

func TestInspect_Unknown_IsNotFound(t *testing.T) {
	client := fakeDaemon(t, fixtureMux(t))
	_, err := client.Inspect(context.Background(), "nope")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v", err)
	}
}

func TestStats_ReadsCountersAndMemory(t *testing.T) {
	client := fakeDaemon(t, fixtureMux(t))
	stats, err := client.Stats(context.Background(), "x")
	if err != nil {
		t.Fatal(err)
	}
	if stats.CPU.Usage.Total != 29336000 || stats.CPU.System != 1095314330000000 || stats.CPU.OnlineCPUs != 20 {
		t.Fatalf("cpu = %+v", stats.CPU)
	}
	if stats.Memory.Limit != 29199167488 || stats.MemoryUsed() != 2846720-40960 {
		t.Fatalf("memory = %+v", stats.Memory)
	}
	if stats.ReadAt().IsZero() {
		t.Fatal("read time missing")
	}
}

func TestEvents_ReplaysTheRecordedStream(t *testing.T) {
	client := fakeDaemon(t, fixtureMux(t))
	stream, err := client.Events(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	var actions []string
	var last Event
	for {
		event, err := stream.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		actions = append(actions, event.Action)
		last = event
	}
	if strings.Join(actions, ",") != "pause,unpause,kill,kill,stop,die,destroy" {
		t.Fatalf("actions = %v", actions)
	}
	if last.Since() != "1789802649.736266200" || last.At().Year() != 2026 {
		t.Fatalf("since = %s at = %s", last.Since(), last.At())
	}
	if actions[5] != "die" {
		t.Fatal("die expected in fifth position")
	}
}

func TestEvents_DieCarriesTheExitCode(t *testing.T) {
	client := fakeDaemon(t, fixtureMux(t))
	stream, err := client.Events(context.Background(), "1789802649.6")
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	for {
		event, err := stream.Next()
		if err != nil {
			t.Fatal("die not found")
		}
		if event.Action == "die" {
			if event.ExitCode() != 137 || event.Actor.Attributes["name"] != "opencloud-fixture" {
				t.Fatalf("die = %+v", event)
			}
			return
		}
	}
}

func TestLogs_DemultiplexesStdoutAndStderr(t *testing.T) {
	client := fakeDaemon(t, fixtureMux(t))
	reader, err := client.Logs(context.Background(), "x", LogsOptions{Tail: 5})
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	var lines []Line
	for {
		line, err := reader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		lines = append(lines, line)
	}
	if len(lines) != 5 {
		t.Fatalf("lines = %+v", lines)
	}
	if lines[0].Text != "tick 0" || lines[0].Stream != "stdout" || lines[0].At.IsZero() {
		t.Fatalf("first = %+v", lines[0])
	}
	if lines[1].Text != "an error line" || lines[1].Stream != "stderr" {
		t.Fatalf("second = %+v", lines[1])
	}
}

func TestLogs_TTYIsRawText(t *testing.T) {
	client := fakeDaemon(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("2026-09-19T07:24:02.000000000Z hello\n2026-09-19T07:24:03.000000000Z world"))
	}))
	reader, err := client.Logs(context.Background(), "x", LogsOptions{})
	if err != nil {
		t.Fatal(err)
	}
	first, _ := reader.Next()
	second, _ := reader.Next()
	_, err = reader.Next()
	if first.Text != "hello" || second.Text != "world" || err != io.EOF {
		t.Fatalf("%+v %+v %v", first, second, err)
	}
}

func TestLogs_LineAcrossTwoFrames(t *testing.T) {
	frame := func(kind byte, text string) []byte {
		header := []byte{kind, 0, 0, 0, 0, 0, 0, byte(len(text))}
		return append(header, text...)
	}
	body := append(frame(1, "2026-09-19T07:24:02.000000000Z ab"), frame(1, "cd\n")...)
	body = append(body, frame(2, "2026-09-19T07:24:02.000000000Z err")...)
	client := fakeDaemon(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(body) }))
	reader, err := client.Logs(context.Background(), "x", LogsOptions{})
	if err != nil {
		t.Fatal(err)
	}
	first, _ := reader.Next()
	second, _ := reader.Next()
	_, err = reader.Next()
	if first.Text != "abcd" || second.Text != "err" || second.Stream != "stderr" || err != io.EOF {
		t.Fatalf("%+v %+v %v", first, second, err)
	}
}

func TestLogs_FollowStopsWithTheContext(t *testing.T) {
	client := fakeDaemon(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("2026-09-19T07:24:02.000000000Z first\n"))
		http.NewResponseController(w).Flush()
		<-r.Context().Done()
	}))
	ctx, cancel := context.WithCancel(context.Background())
	reader, err := client.Logs(ctx, "x", LogsOptions{Follow: true})
	if err != nil {
		t.Fatal(err)
	}
	if line, _ := reader.Next(); line.Text != "first" {
		t.Fatalf("line = %+v", line)
	}
	go func() { time.Sleep(50 * time.Millisecond); cancel() }()
	if _, err := reader.Next(); err == nil {
		t.Fatal("expected the cancelled context to end the stream")
	}
}

func TestInspectImage_ReadsThePulledDigest(t *testing.T) {
	client := fakeDaemon(t, fixtureMux(t))
	image, err := client.InspectImage(context.Background(), "alpine:3.20")
	if err != nil {
		t.Fatal(err)
	}
	if image.PulledDigest("alpine") != "sha256:d9e853e87e55526f6b2917df91a2115c36dd7c696a35be12163d44e6e2a4b6bc" {
		t.Fatalf("digest = %q", image.PulledDigest("alpine"))
	}
	if image.PulledDigest("nginx") != "" {
		t.Fatal("another repository must not match")
	}
	local, err := client.InspectImage(context.Background(), "portfolio:latest")
	if err != nil || local.PulledDigest("portfolio") != "" {
		t.Fatalf("local image: %q %v", local.PulledDigest("portfolio"), err)
	}
	if _, err := client.InspectImage(context.Background(), "nope:1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing image: %v", err)
	}
}
