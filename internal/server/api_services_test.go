package server

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ldesfontaine/opencloud/internal/machine"
	"github.com/ldesfontaine/opencloud/internal/probe"
	"github.com/ldesfontaine/opencloud/internal/service"
)

func (ts *testServer) agentPost(session, path string, payload any) *httptest.ResponseRecorder {
	var body bytes.Buffer
	_ = json.NewEncoder(&body).Encode(payload)
	request := httptest.NewRequest(http.MethodPost, path, &body)
	request.Header.Set("Authorization", "Bearer "+session)
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	ts.ServeHTTP(recorder, request)
	return recorder
}

// readCommand lit le flux de l'agent jusqu'à la commande attendue : il
// porte aussi le jeu de sondes, poussé dès l'ouverture et à chaque
// changement.
func readCommand(t *testing.T, stream *bufio.Reader, want string) string {
	t.Helper()
	for {
		name, data := readStreamEvent(t, stream)
		if name == want {
			return data
		}
		if name != probe.CommandProbes {
			t.Fatalf("commande inattendue %q en attendant %q", name, want)
		}
	}
}

// readStreamEvent lit le prochain événement nommé du flux de l'agent, en
// sautant les pings.
func readStreamEvent(t *testing.T, reader *bufio.Reader) (string, string) {
	t.Helper()
	var name, data string
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			t.Fatal(err)
		}
		line = strings.TrimRight(line, "\n")
		switch {
		case strings.HasPrefix(line, "event: "):
			name = strings.TrimPrefix(line, "event: ")
		case strings.HasPrefix(line, "data: "):
			data = strings.TrimPrefix(line, "data: ")
		case line == "" && name != "" && name != "ping":
			return name, data
		case line == "":
			name, data = "", ""
		}
	}
}

// Le signal porte la section services : elle s'écrit au nom de la machine
// de la session, un rapport hors de mesure est refusé, un agent sans
// section passe.
func TestAgentSignal_CarriesServicesAndRefusesBadReports(t *testing.T) {
	server := newTestServer(t)
	enrolled, private := server.enroll(t, "vps-paris-1", remoteID)
	resp, session, _, cancel := openStream(t, server, enrolled.ID, private)
	defer resp.Body.Close()
	defer cancel()

	report := service.Report{Complete: true, Inventory: []service.Container{{
		ContainerID: strings.Repeat("c", 64), Name: "web", Image: "nginx:1.27", State: service.StateRunning, CreatedAt: testNow,
	}}}
	if got := server.agentPost(session, "/agent/signal", machine.SignalRequest{Services: &report}); got.Code != http.StatusNoContent {
		t.Fatalf("signal: %d %s", got.Code, got.Body.String())
	}
	services, err := server.services.List(context.Background(), remoteID)
	if err != nil || len(services) != 1 || services[0].Name != "web" {
		t.Fatalf("services = %+v %v", services, err)
	}
	bad := service.Report{Complete: true, Inventory: []service.Container{{ContainerID: "short", Name: "x", State: service.StateRunning, CreatedAt: testNow}}}
	if got := server.agentPost(session, "/agent/signal", machine.SignalRequest{Services: &bad}); got.Code != http.StatusBadRequest || !strings.Contains(got.Body.String(), "bad_services") {
		t.Fatalf("bad report: %d %s", got.Code, got.Body.String())
	}
}

// Une demande de journaux part par le flux de l'agent, les lots reviennent
// par POST et sortent en SSE vers le navigateur ; un lot pour une requête
// inconnue est refusé.
func TestServiceLogs_RemoteRoundTrip(t *testing.T) {
	server := newTestServer(t)
	enrolled, private := server.enroll(t, "vps-paris-1", remoteID)
	resp, session, agentStream, cancel := openStream(t, server, enrolled.ID, private)
	defer resp.Body.Close()
	defer cancel()
	serviceID := server.seedServices(t)

	live := httptest.NewServer(server.Server)
	defer live.Close()
	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	request, _ := http.NewRequestWithContext(ctx, http.MethodGet, live.URL+"/api/services/"+serviceID+"/logs/stream?tail=20", nil)
	browser, err := live.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer browser.Body.Close()
	if browser.StatusCode != http.StatusOK || browser.Header.Get("Content-Type") != "text/event-stream" {
		t.Fatalf("stream: %d %s", browser.StatusCode, browser.Header.Get("Content-Type"))
	}

	data := readCommand(t, agentStream, service.CommandLogs)
	var command service.LogRequest
	if err := json.Unmarshal([]byte(data), &command); err != nil {
		t.Fatal(err)
	}
	if command.ID == "" || command.ContainerID != strings.Repeat("b", 64) || command.Tail != 20 || !command.Follow {
		t.Fatalf("command %+v", command)
	}

	if got := server.agentPost(session, "/agent/logs/unknown", service.LogBatch{}); got.Code != http.StatusNotFound {
		t.Fatalf("unknown request: %d", got.Code)
	}
	at := testNow
	batch := service.LogBatch{Lines: []service.LogLine{{At: at, Stream: "stdout", Text: "ready"}, {Stream: "stderr", Text: "oops"}}}
	if got := server.agentPost(session, "/agent/logs/"+command.ID, batch); got.Code != http.StatusNoContent {
		t.Fatalf("deliver: %d %s", got.Code, got.Body.String())
	}
	if got := server.agentPost(session, "/agent/logs/"+command.ID, service.LogBatch{Done: true}); got.Code != http.StatusNoContent {
		t.Fatalf("deliver done: %d", got.Code)
	}

	reader := bufio.NewReader(browser.Body)
	var events []string
	for len(events) < 3 {
		name, data := readStreamEvent(t, reader)
		events = append(events, name+" "+data)
	}
	if !strings.HasPrefix(events[0], `line {"at":"2026-09-12T12:00:00Z","stream":"stdout","text":"ready"}`) || !strings.Contains(events[1], `"stream":"stderr"`) || !strings.HasPrefix(events[2], "end ") {
		t.Fatalf("events = %v", events)
	}
	// Le serveur ne veut plus rien pour cette requête.
	if got := server.agentPost(session, "/agent/logs/"+command.ID, service.LogBatch{}); got.Code != http.StatusNotFound {
		t.Fatalf("after done: %d", got.Code)
	}
}

// Quand le navigateur part, l'agent reçoit l'ordre d'arrêter.
func TestServiceLogs_BrowserLeaving_StopsTheAgent(t *testing.T) {
	server := newTestServer(t)
	enrolled, private := server.enroll(t, "vps-paris-1", remoteID)
	resp, _, agentStream, cancel := openStream(t, server, enrolled.ID, private)
	defer resp.Body.Close()
	defer cancel()
	serviceID := server.seedServices(t)

	live := httptest.NewServer(server.Server)
	defer live.Close()
	ctx, stop := context.WithCancel(context.Background())
	request, _ := http.NewRequestWithContext(ctx, http.MethodGet, live.URL+"/api/services/"+serviceID+"/logs/stream", nil)
	browser, err := live.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	data := readCommand(t, agentStream, service.CommandLogs)
	browser.Body.Close()
	stop()
	stopData := readCommand(t, agentStream, service.CommandLogsStop)
	if !strings.Contains(data, jsonID(t, stopData)) {
		t.Fatalf("la commande d'arrêt ne porte pas la requête ouverte: %s", stopData)
	}
}

func jsonID(t *testing.T, data string) string {
	t.Helper()
	var request service.LogRequest
	if err := json.Unmarshal([]byte(data), &request); err != nil {
		t.Fatal(err)
	}
	return request.ID
}

// Sans flux, les journaux d'une machine distante sont un refus traduit ;
// la fiche d'un service inconnu est un 404.
func TestServiceLogs_OfflineMachineAndUnknownService(t *testing.T) {
	server := newTestServer(t)
	server.enroll(t, "vps-paris-1", remoteID)
	serviceID := server.seedServices(t)
	got := callAPI(server.Server, http.MethodGet, "/api/services/"+serviceID+"/logs", nil)
	if got.Code != http.StatusServiceUnavailable || errorCode(t, got) != "service.machine_offline" {
		t.Fatalf("offline: %d %s", got.Code, got.Body.String())
	}
	got = callAPI(server.Server, http.MethodGet, "/api/services/"+serviceID+"/logs/stream", nil)
	if got.Code != http.StatusServiceUnavailable {
		t.Fatalf("offline stream: %d", got.Code)
	}
	for _, path := range []string{"/api/services/nope", "/api/services/nope/transitions", "/api/services/nope/logs", "/api/machines/nope/services"} {
		if got := callAPI(server.Server, http.MethodGet, path, nil); got.Code != http.StatusNotFound {
			t.Fatalf("%s: %d", path, got.Code)
		}
	}
}

// La machine openCloud sert ses journaux en processus : le tirage unique
// passe par la source locale, sans agent.
type fakeLocalLogs struct{}

func (fakeLocalLogs) OpenLogs(_ context.Context, request service.LogRequest) (<-chan service.LogBatch, error) {
	batches := make(chan service.LogBatch, 1)
	batches <- service.LogBatch{Lines: []service.LogLine{{At: testNow, Stream: "stdout", Text: "local line " + request.ContainerID[:4]}}, Done: true}
	close(batches)
	return batches, nil
}

func TestServiceLogs_LocalMachineUsesTheInProcessSource(t *testing.T) {
	server := newTestServer(t)
	server.services.SetLocalLogSource(machine.LocalID, fakeLocalLogs{})
	report := service.Report{Complete: true, Inventory: []service.Container{{
		ContainerID: strings.Repeat("d", 64), Name: "traefik", Image: "traefik:3.1", State: service.StateRunning, CreatedAt: testNow,
	}}}
	if err := server.services.Record(context.Background(), machine.LocalID, report); err != nil {
		t.Fatal(err)
	}
	got := callAPI(server.Server, http.MethodGet, "/api/services/"+service.ID(machine.LocalID, strings.Repeat("d", 64))+"/logs?tail=5", nil)
	if got.Code != http.StatusOK {
		t.Fatalf("logs: %d %s", got.Code, got.Body.String())
	}
	var response logsResponse
	decodeAPI(t, got, &response)
	if len(response.Lines) != 1 || response.Lines[0].Text != "local line dddd" || response.Lines[0].At == nil {
		t.Fatalf("lines = %+v", response.Lines)
	}
}
