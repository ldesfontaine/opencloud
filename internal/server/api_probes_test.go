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
	"time"

	"github.com/ldesfontaine/opencloud/internal/machine"
	"github.com/ldesfontaine/opencloud/internal/probe"
	"github.com/ldesfontaine/opencloud/internal/update"
)

func (ts *testServer) createProbeVia(t *testing.T, request probeRequest) probeJSON {
	t.Helper()
	recorder := callAPI(ts.Server, http.MethodPost, "/api/probes", request)
	if recorder.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", recorder.Code, recorder.Body.String())
	}
	var created probeJSON
	decodeAPI(t, recorder, &created)
	return created
}

func TestAPI_CreateProbe_ReturnsItAndRefusesBadDefinitionsWithTheirKey(t *testing.T) {
	server := newTestServer(t)
	created := server.createProbeVia(t, probeRequest{
		Name: "site nextcloud", Kind: "http", Target: "https://cloud.exemple.fr/", MachineID: machine.LocalID,
	})
	if created.Status != string(probe.StatusNew) || created.IntervalSeconds != 60 || created.Method != "GET" {
		t.Fatalf("created %+v", created)
	}
	if created.MachineName == "" || !created.MachineOnline {
		t.Fatalf("la machine openCloud devait être nommée et en ligne: %+v", created)
	}
	if got, err := server.probes.Get(context.Background(), created.ID); err != nil || got.Name != "site nextcloud" {
		t.Fatalf("stored %+v %v", got, err)
	}

	cases := map[string]struct {
		request probeRequest
		key     string
	}{
		"nom vide":              {probeRequest{Name: " ", Kind: "http", Target: "https://a.fr/", MachineID: machine.LocalID}, "probe.name_invalid"},
		"type inconnu":          {probeRequest{Name: "x", Kind: "ping", Target: "a.fr", MachineID: machine.LocalID}, "probe.kind_invalid"},
		"cible illisible":       {probeRequest{Name: "x", Kind: "http", Target: "cloud.exemple.fr", MachineID: machine.LocalID}, "probe.target_invalid"},
		"cible interdite":       {probeRequest{Name: "x", Kind: "http", Target: "http://169.254.169.254/", MachineID: machine.LocalID}, "probe.target_forbidden"},
		"machine inconnue":      {probeRequest{Name: "x", Kind: "http", Target: "https://a.fr/", MachineID: "nope"}, "probe.machine_invalid"},
		"intervalle trop court": {probeRequest{Name: "x", Kind: "http", Target: "https://a.fr/", MachineID: machine.LocalID, IntervalSeconds: 5}, "probe.interval_invalid"},
		"méthode qui écrit":     {probeRequest{Name: "x", Kind: "http", Target: "https://a.fr/", MachineID: machine.LocalID, Method: "DELETE"}, "probe.method_invalid"},
		"code illisible":        {probeRequest{Name: "x", Kind: "http", Target: "https://a.fr/", MachineID: machine.LocalID, ExpectedStatus: "deux-cents"}, "probe.status_invalid"},
	}
	for name, testCase := range cases {
		got := callAPI(server.Server, http.MethodPost, "/api/probes", testCase.request)
		if got.Code != http.StatusUnprocessableEntity || errorCode(t, got) != testCase.key {
			t.Errorf("%s: %d %s", name, got.Code, got.Body.String())
		}
	}
}

func TestAPI_ProbeActions_PauseResumeDelete(t *testing.T) {
	server := newTestServer(t)
	created := server.createProbeVia(t, probeRequest{Name: "site", Kind: "http", Target: "https://a.fr/", MachineID: machine.LocalID})

	if got := callAPI(server.Server, http.MethodPost, "/api/probes/"+created.ID+"/actions/resume", nil); got.Code != http.StatusConflict || errorCode(t, got) != "probe.not_paused" {
		t.Fatalf("resume while running: %d %s", got.Code, got.Body.String())
	}
	if got := callAPI(server.Server, http.MethodPost, "/api/probes/"+created.ID+"/actions/pause", nil); got.Code != http.StatusNoContent {
		t.Fatalf("pause: %d %s", got.Code, got.Body.String())
	}
	if found, _ := server.probes.Get(context.Background(), created.ID); !found.IsPaused() {
		t.Fatalf("not paused: %+v", found)
	}
	if got := callAPI(server.Server, http.MethodPost, "/api/probes/"+created.ID+"/actions/resume", nil); got.Code != http.StatusNoContent {
		t.Fatalf("resume: %d %s", got.Code, got.Body.String())
	}
	if got := callAPI(server.Server, http.MethodDelete, "/api/probes/"+created.ID, nil); got.Code != http.StatusNoContent {
		t.Fatalf("delete: %d", got.Code)
	}
	if got := callAPI(server.Server, http.MethodGet, "/api/probes/"+created.ID, nil); got.Code != http.StatusNotFound {
		t.Fatalf("after delete: %d", got.Code)
	}
}

func TestAPI_MachineProbes_AnswersNotFoundForAnUnknownMachine(t *testing.T) {
	server := newTestServer(t)
	if got := callAPI(server.Server, http.MethodGet, "/api/machines/nope/probes", nil); got.Code != http.StatusNotFound {
		t.Fatalf("unknown machine: %d %s", got.Code, got.Body.String())
	}
}

// Le flux qui s'ouvre repart avec le jeu de sondes de sa machine, et une
// sonde créée ensuite part sur le flux déjà ouvert : sans l'un ni l'autre,
// un agent ne sonderait que ce qu'il avait au démarrage.
func TestAgentStream_PushesTheProbeSetOnConnectAndOnChange(t *testing.T) {
	server := newTestServer(t)
	enrolled, private := server.enroll(t, "vps-paris-1", remoteID)
	first := server.createProbeVia(t, probeRequest{Name: "site", Kind: "http", Target: "https://a.fr/", MachineID: remoteID})

	response, _, stream, cancel := openStream(t, server, enrolled.ID, private)
	defer response.Body.Close()
	defer cancel()

	assignment := nextAssignment(t, stream)
	if len(assignment.Probes) != 1 || assignment.Probes[0].ID != first.ID || assignment.Probes[0].Target != "https://a.fr/" {
		t.Fatalf("le jeu poussé à la connexion ne porte pas la sonde: %+v", assignment)
	}

	second := server.createProbeVia(t, probeRequest{Name: "base", Kind: "tcp", Target: "10.8.0.2:5432", MachineID: remoteID})
	assignment = nextAssignment(t, stream)
	if len(assignment.Probes) != 2 {
		t.Fatalf("le jeu poussé après la création ne porte pas les deux sondes: %+v", assignment)
	}

	if got := callAPI(server.Server, http.MethodPost, "/api/probes/"+second.ID+"/actions/pause", nil); got.Code != http.StatusNoContent {
		t.Fatalf("pause: %d", got.Code)
	}
	assignment = nextAssignment(t, stream)
	if len(assignment.Probes) != 1 || assignment.Probes[0].ID != first.ID {
		t.Fatalf("une sonde en pause reste dans le jeu poussé: %+v", assignment)
	}
}

// nextAssignment lit la prochaine commande « probes » du flux de l'agent.
func nextAssignment(t *testing.T, stream *bufio.Reader) probe.Assignment {
	t.Helper()
	name, data := nextCommand(t, stream, 3*time.Second)
	// Le flux qui s'ouvre porte aussi les exclusions d'images : on passe.
	if name == update.CommandImages {
		name, data = nextCommand(t, stream, 3*time.Second)
	}
	if name != probe.CommandProbes {
		t.Fatalf("attendu la commande %q, obtenu %q", probe.CommandProbes, name)
	}
	var assignment probe.Assignment
	if err := json.Unmarshal([]byte(data), &assignment); err != nil {
		t.Fatal(err)
	}
	return assignment
}

// nextCommand lit la prochaine commande du flux, en sautant la session et
// les pings, sans jamais laisser le test attendre sans fin : une commande
// qui ne vient pas doit faire échouer, pas bloquer.
func nextCommand(t *testing.T, stream *bufio.Reader, within time.Duration) (string, string) {
	t.Helper()
	type event struct{ name, data string }
	arrived := make(chan event, 1)
	go func() {
		var name, data string
		for {
			line, err := stream.ReadString('\n')
			if err != nil {
				return
			}
			line = strings.TrimRight(line, "\n")
			switch {
			case strings.HasPrefix(line, "event: "):
				name = strings.TrimPrefix(line, "event: ")
			case strings.HasPrefix(line, "data: "):
				data = strings.TrimPrefix(line, "data: ")
			case line == "" && name != "" && name != "ping" && name != "session":
				arrived <- event{name: name, data: data}
				return
			case line == "":
				name, data = "", ""
			}
		}
	}()
	select {
	case got := <-arrived:
		return got.name, got.data
	case <-time.After(within):
		t.Fatal("aucune commande reçue sur le flux de l'agent")
		return "", ""
	}
}

// Le signal porte les essais des sondes à côté des mesures ; un essai hors
// de ce qu'un agent peut avoir observé refuse le lot entier.
func TestAgentSignal_CarriesProbeResultsAndRefusesBadOnes(t *testing.T) {
	server := newTestServer(t)
	enrolled, private := server.enroll(t, "vps-paris-1", remoteID)
	created := server.createProbeVia(t, probeRequest{Name: "site", Kind: "http", Target: "https://a.fr/", MachineID: remoteID})
	response, session, _, cancel := openStream(t, server, enrolled.ID, private)
	defer response.Body.Close()
	defer cancel()

	signal := func(report probe.Report) *httptest.ResponseRecorder {
		var body bytes.Buffer
		_ = json.NewEncoder(&body).Encode(machine.SignalRequest{Probes: &report})
		request := httptest.NewRequest(http.MethodPost, "/agent/signal", &body)
		request.Header.Set("Authorization", "Bearer "+session)
		request.Header.Set("Content-Type", "application/json")
		recorder := httptest.NewRecorder()
		server.ServeHTTP(recorder, request)
		return recorder
	}

	code := 200
	good := probe.Report{Results: []probe.Result{{ProbeID: created.ID, CheckedAt: testNow, Outcome: probe.OutcomeUp, DurationMs: 118, Code: &code}}}
	if got := signal(good); got.Code != http.StatusNoContent {
		t.Fatalf("signal: %d %s", got.Code, got.Body.String())
	}
	stored, err := server.probes.Get(context.Background(), created.ID)
	if err != nil || stored.Status != probe.StatusUp || stored.LastDurationMs != 118 {
		t.Fatalf("l'essai n'a pas été écrit: %+v %v", stored, err)
	}

	bad := probe.Report{Results: []probe.Result{{ProbeID: created.ID, CheckedAt: testNow.Add(time.Hour), Outcome: probe.OutcomeUp}}}
	got := signal(bad)
	if got.Code != http.StatusBadRequest {
		t.Fatalf("un essai daté du futur devait refuser le lot: %d %s", got.Code, got.Body.String())
	}
}
