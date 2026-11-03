package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ldesfontaine/opencloud/internal/directory"
	"github.com/ldesfontaine/opencloud/internal/server/servertest"
)

// Les connexions de l'hôte (étude services-modèle §2.3, signal 9, décision 5) :
// l'agent dit qui a joint quel port de sa machine ; le serveur rapproche ce
// port d'une instance du service qui le publie. Le lien est observé, sa source
// est un compte, une adresse ou Internet, jamais un service.

func (ts *testServer) postSignal(t *testing.T, session string, payload any) *httptest.ResponseRecorder {
	t.Helper()
	body, _ := json.Marshal(payload)
	request := httptest.NewRequest(http.MethodPost, "/agent/signal", bytes.NewReader(body))
	request.Header.Set("Authorization", "Bearer "+session)
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	ts.ServeHTTP(recorder, request)
	return recorder
}

func hostConnectionsSignal(connections ...map[string]any) map[string]any {
	return map[string]any{"host_connections": map[string]any{"available": true, "connections": connections}}
}

func TestAPI_HostConnections_ObservedLinksOnThePublishingService(t *testing.T) {
	server := newTestServer(t)
	enrolled, private := server.enroll(t, "vps-paris-1", remoteID)
	resp, session, _, cancel := servertest.OpenStream(t, server.Server, enrolled.ID, private)
	defer resp.Body.Close()
	defer cancel()
	dashboard := server.seedPublishingService(t, "3001:3000")

	signal := hostConnectionsSignal(
		map[string]any{"port": 3001, "origin": "account", "name": "backup"},
		map[string]any{"port": 3001, "origin": "address", "name": "192.168.1.20"},
		map[string]any{"port": 3001, "origin": "internet"},
		map[string]any{"port": 9100, "origin": "internet"},
	)
	if got := server.postSignal(t, session, signal); got.Code != http.StatusNoContent {
		t.Fatalf("signal : %d %s", got.Code, got.Body.String())
	}

	out := serviceLinksOf(t, server, dashboard)
	if len(out.Links) != 3 {
		t.Fatalf("liens : %+v (le port 9100 n'est publié par personne)", out.Links)
	}
	kinds := map[string]string{}
	for _, got := range out.Links {
		if got.Direction != "incoming" || got.Nature != "traffic" || got.Proof != "observed" || got.Signal != "host_connection" || got.Port != 3001 || got.Removable || got.Proposed || got.Stale {
			t.Errorf("lien = %+v", got)
		}
		if got.Peer.ServiceID != "" || got.Peer.ContainerID != "" || got.Self.ServiceID != dashboard {
			t.Errorf("extrémités = %+v / %+v : la source n'est pas un service", got.Self, got.Peer)
		}
		kinds[got.Peer.Kind] = got.Peer.Label
	}
	if kinds["account"] != "backup" || kinds["address"] != "192.168.1.20" || kinds["internet"] != "public" {
		t.Errorf("sources = %v", kinds)
	}
	// Une machine d'un autre service ne voit rien de ces connexions.
	_, _, proxy := server.seedLinkedServices(t)
	if other := serviceLinksOf(t, server, proxy); len(other.Links) != 0 {
		t.Errorf("le service d'une autre machine : %+v", other.Links)
	}
}

// Un conteneur sans port déclaré est trouvé par les ports qu'il publie
// aujourd'hui, tels que l'inventaire de l'agent les dit.
func TestAPI_HostConnections_ThePortsAContainerPublishesAreEnough(t *testing.T) {
	server := newTestServer(t)
	enrolled, private := server.enroll(t, "vps-paris-1", remoteID)
	resp, session, _, cancel := servertest.OpenStream(t, server.Server, enrolled.ID, private)
	defer resp.Body.Close()
	defer cancel()
	dashboard := server.seedPublishingService(t, "")
	inventory := map[string]any{
		"container_id": strings.Repeat("c", 64), "name": "dashboard", "image": "grafana", "state": "running", "created_at": testNow.Format("2006-01-02T15:04:05Z"),
		"ports": []map[string]any{{"ip": "0.0.0.0", "host_port": 3001, "container_port": 3000, "protocol": "tcp"}, {"ip": "0.0.0.0", "host_port": 8125, "container_port": 8125, "protocol": "udp"}},
	}
	signal := hostConnectionsSignal(map[string]any{"port": 3001, "origin": "internet"}, map[string]any{"port": 8125, "origin": "internet"})
	signal["services"] = map[string]any{"complete": true, "inventory": []any{inventory}}
	if got := server.postSignal(t, session, signal); got.Code != http.StatusNoContent {
		t.Fatalf("signal : %d %s", got.Code, got.Body.String())
	}
	out := serviceLinksOf(t, server, dashboard)
	if len(out.Links) != 1 || out.Links[0].Port != 3001 {
		t.Fatalf("liens : %+v (un port UDP n'est pas dans la table TCP)", out.Links)
	}
}

// Un rapport hors de ce qu'un agent peut avoir relevé est refusé en entier,
// avec sa clé ; ce que l'agent n'a pas dit ne devient pas un lien.
func TestAPI_HostConnections_ForgedReportIsRefused(t *testing.T) {
	server := newTestServer(t)
	enrolled, private := server.enroll(t, "vps-paris-1", remoteID)
	resp, session, _, cancel := servertest.OpenStream(t, server.Server, enrolled.ID, private)
	defer resp.Body.Close()
	defer cancel()
	dashboard := server.seedPublishingService(t, "3001:3000")

	forged := map[string]map[string]any{
		"a public address as a LAN address": {"port": 3001, "origin": "address", "name": "8.8.8.8"},
		"a visitor address on Internet":     {"port": 3001, "origin": "internet", "name": "8.8.8.8"},
		"an unknown origin":                 {"port": 3001, "origin": "process", "name": "nginx"},
		"an account that is a sentence":     {"port": 3001, "origin": "account", "name": "Jean Dupont"},
		"a port out of range":               {"port": 70000, "origin": "internet"},
	}
	for name, connection := range forged {
		got := server.postSignal(t, session, hostConnectionsSignal(connection))
		if got.Code != http.StatusBadRequest || !strings.Contains(got.Body.String(), "bad_host_connections") {
			t.Errorf("%s : %d %s", name, got.Code, got.Body.String())
		}
	}
	if out := serviceLinksOf(t, server, dashboard); len(out.Links) != 0 {
		t.Errorf("liens : %+v", out.Links)
	}
}

var seededHostSecrets = []string{"hunter2-Zq83", "tok_9f3a77c1d2e8", "p4ss-Xk29", "203.0.113.77", "nginx: worker", "/home/svc-user"}

// Banc de non-fuite, côté serveur (pendant de hostconn.TestRead_NoSeededValueLeavesTheAgent) :
// un agent mal écrit, ou hostile, glisse dans son rapport ce que la table ne
// donne pas (processus, ligne de commande, environnement, adresse d'un
// visiteur, dossier d'un compte). Aucune de ces valeurs ne doit se retrouver
// dans la base, ni dans une réponse de l'API (écrans, lectures du MCP), ni
// dans le journal d'audit.
func TestAPI_HostConnections_SeededValuesAreInNoOutput(t *testing.T) {
	server := newTestServer(t)
	enrolled, private := server.enroll(t, "vps-paris-1", remoteID)
	resp, session, _, cancel := servertest.OpenStream(t, server.Server, enrolled.ID, private)
	defer resp.Body.Close()
	defer cancel()
	dashboard := server.seedPublishingService(t, "3001:3000")

	signal := map[string]any{"host_connections": map[string]any{
		"available": true,
		"connections": []map[string]any{{
			"port": 3001, "origin": "account", "name": "backup",
			// Ce qu'un agent mal écrit enverrait en plus : le serveur ne le lit pas.
			"process": "nginx: worker", "cmdline": "psql postgres://svc-user:p4ss-Xk29@db/app", "env": []string{"API_TOKEN=tok_9f3a77c1d2e8"},
			"remote": "203.0.113.77", "home": "/home/svc-user", "password": "hunter2-Zq83",
		}},
		"process_table": map[string]any{"1234": "nginx: worker"},
	}}
	if got := server.postSignal(t, session, signal); got.Code != http.StatusNoContent {
		t.Fatalf("signal : %d %s", got.Code, got.Body.String())
	}
	out := serviceLinksOf(t, server, dashboard)
	if len(out.Links) != 1 || out.Links[0].Peer.Label != "backup" {
		t.Fatalf("le lien devait être écrit : %+v", out.Links)
	}

	snapshot := filepath.Join(t.TempDir(), "snapshot.db")
	if err := server.db.SnapshotDatabase(context.Background(), snapshot); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	outputs := map[string]string{"base": string(content)}
	for _, path := range []string{
		"/api/session", "/api/counts", "/api/machines", "/api/machines/" + remoteID, "/api/machines/" + remoteID + "/network", "/api/network",
		"/api/containers", "/api/directory/services", "/api/directory/gaps", "/api/audit", "/api/alerts", "/api/actions", "/api/jobs",
		"/api/directory/services/" + dashboard, "/api/directory/services/" + dashboard + "/links",
	} {
		got := callAPI(server.Server, http.MethodGet, path, nil)
		if got.Code != http.StatusOK {
			t.Fatalf("%s : %d %s", path, got.Code, got.Body.String())
		}
		outputs[path] = got.Body.String()
	}
	for _, route := range apiTokenRoutes {
		if strings.Contains(route, "{id}") {
			continue
		}
		path := strings.TrimPrefix(route, "GET ")
		if got := callAPI(server.Server, http.MethodGet, path, nil); got.Code == http.StatusOK {
			outputs["mcp "+path] = got.Body.String()
		}
	}
	for name, output := range outputs {
		for _, secret := range seededHostSecrets {
			if strings.Contains(output, secret) {
				t.Errorf("%q est dans %s", secret, name)
			}
		}
	}
	if links := outputs["/api/directory/services/"+dashboard+"/links"]; !strings.Contains(links, `"signal":"host_connection"`) || !strings.Contains(links, `"proof":"observed"`) {
		t.Errorf("la fiche devait montrer le lien vu : %s", links)
	}
}

// seedPublishingService déclare « dashboard », un conteneur de la machine
// distante, avec le port publié donné (vide : aucun port déclaré).
func (ts *testServer) seedPublishingService(t *testing.T, declaredPort string) string {
	t.Helper()
	var ports []string
	if declaredPort != "" {
		ports = []string{declaredPort}
	}
	created, err := ts.directory.CreateService(context.Background(), directoryServiceDashboard(ports))
	if err != nil {
		t.Fatal(err)
	}
	return created.ID
}

func directoryServiceDashboard(ports []string) directory.ServiceDefinition {
	return directory.ServiceDefinition{
		MachineID: remoteID, Name: "dashboard", Nature: directory.NatureApplication, Origin: directory.OriginContainer,
		Units: []directory.UnitDefinition{{Name: "dashboard", ContainerName: "dashboard", Image: "grafana", Ports: ports}},
	}
}
