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
	"time"

	"github.com/ldesfontaine/opencloud/internal/link"
	"github.com/ldesfontaine/opencloud/internal/server/servertest"
)

// Le banc de non-fuite de l'environnement, côté serveur (étude
// services-modèle §2.4 ; son pendant côté agent est dockerwatch,
// TestInventory_NoSeededSecretNorVariableNameLeavesTheAgent). Un agent qui a le
// droit observe.env envoie son signal ; cet agent-ci est mal écrit (ou
// hostile) et y glisse l'environnement brut. Aucune valeur secrète ni aucun nom
// de variable ne doit se retrouver dans la base, ni dans aucune réponse de
// l'API : celles des écrans comme les lectures qu'un jeton d'API (le MCP)
// ouvre, ni dans le journal d'audit.

var seededEnvironmentSecrets = []string{"hunter2-Zq83", "tok_9f3a77c1d2e8", "p4ss-Xk29", "svc-user", "DB_PASSWORD", "API_TOKEN", "DATABASE_URL"}

func TestAPI_Environment_SeededSecretsAreInNoOutput(t *testing.T) {
	server := newTestServer(t)
	enrolled, private := server.enroll(t, "vps-paris-1", remoteID)
	resp, session, _, cancel := servertest.OpenStreamWithRights(t, server.Server, enrolled.ID, private, "observe,observe.env")
	defer resp.Body.Close()
	defer cancel()
	app, database, _ := server.seedLinkedServices(t)

	created := testNow.Add(-24 * time.Hour).Format(time.RFC3339)
	web := map[string]any{
		"container_id": strings.Repeat("a", 64), "name": "nextcloud", "group": "nextcloud", "compose_service": "nextcloud", "image": "nextcloud", "state": "running", "created_at": created,
		"env_targets": []map[string]any{{"name": "nextcloud-db", "group": "nextcloud", "port": 5432}},
		// Ce qu'un agent mal écrit enverrait en plus : le serveur ne le lit pas.
		"env":         []string{"DB_PASSWORD=hunter2-Zq83", "API_TOKEN=tok_9f3a77c1d2e8"},
		"environment": map[string]string{"DATABASE_URL": "postgres://svc-user:p4ss-Xk29@nextcloud-db/app"},
	}
	db := map[string]any{"container_id": strings.Repeat("b", 64), "name": "nextcloud-db", "group": "nextcloud", "compose_service": "nextcloud-db", "image": "postgres", "state": "running", "created_at": created}
	body, _ := json.Marshal(map[string]any{"services": map[string]any{"complete": true, "inventory": []any{web, db}}})
	request := httptest.NewRequest(http.MethodPost, "/agent/signal", bytes.NewReader(body))
	request.Header.Set("Authorization", "Bearer "+session)
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	server.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("signal: %d %s", recorder.Code, recorder.Body.String())
	}

	stored, err := server.db.ListLinks(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	var environment []link.Link
	for _, found := range stored {
		if found.Signal == link.SignalEnvironment {
			environment = append(environment, found)
		}
	}
	if len(environment) != 1 || environment[0].From.Key != "compose:nextcloud/nextcloud" || environment[0].To.Key != "compose:nextcloud/nextcloud-db" || environment[0].Port != 5432 {
		t.Fatalf("le lien de l'environnement devait être écrit : %+v", stored)
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
	paths := []string{
		"/api/session", "/api/counts", "/api/machines", "/api/machines/" + remoteID, "/api/machines/" + remoteID + "/network", "/api/machines/" + remoteID + "/resources",
		"/api/network", "/api/containers", "/api/directory/services", "/api/directory/gaps", "/api/audit", "/api/alerts", "/api/actions", "/api/jobs",
		"/api/directory/services/" + app + "/links", "/api/directory/services/" + database + "/links",
	}
	for _, path := range paths {
		got := callAPI(server.Server, http.MethodGet, path, nil)
		if got.Code != http.StatusOK {
			t.Fatalf("%s : %d %s", path, got.Code, got.Body.String())
		}
		outputs[path] = got.Body.String()
	}
	// Les lectures du MCP : les mêmes routes, par un jeton d'API (liste fermée).
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
		for _, secret := range seededEnvironmentSecrets {
			if strings.Contains(output, secret) {
				t.Errorf("%q est dans %s", secret, name)
			}
		}
	}
	// Ce qui doit y être : le lien, avec son signal et son port, rien de plus.
	if links := outputs["/api/directory/services/"+app+"/links"]; !strings.Contains(links, `"signal":"environment"`) || !strings.Contains(links, `"port":5432`) {
		t.Errorf("la fiche du service devait montrer le lien lu : %s", links)
	}
}
