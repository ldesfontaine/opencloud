package server

import (
	"context"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ldesfontaine/opencloud/internal/agentwire"
	"github.com/ldesfontaine/opencloud/internal/audit"
)

// Un mot de passe de test, pas un secret.
const deployEnvLine = "POSTGRES_PASSWORD=cle-de-test-0001" // gitleaks:allow

// deploySetup déclare l'application Compose de la machine distante dont
// l'agent a rapporté ses droits et sa racine, et rend l'identifiant du service.
func deploySetup(t *testing.T, server *testServer, rights string) (serviceID string) {
	t.Helper()
	enrolled, _ := server.enroll(t, "vps-paris-1", remoteID)
	session, err := server.machines.Connect(context.Background(), enrolled, "192.0.2.1", "v0.0.1", rights)
	if err != nil {
		t.Fatal(err)
	}
	if err := server.machines.NoteRootDir(context.Background(), session.AgentID, "/srv/workspace"); err != nil {
		t.Fatal(err)
	}
	request := nextcloudRequest(remoteID)
	request.Units[0].DataPath = "/var/www/html"
	created := callAPI(server.Server, http.MethodPost, "/api/directory/services", request)
	if created.Code != http.StatusCreated {
		t.Fatalf("service: %d %s", created.Code, created.Body.String())
	}
	var service declaredServiceJSON
	decodeAPI(t, created, &service)
	if service.Units[0].DataPath != "/var/www/html" {
		t.Fatalf("data_path %+v", service.Units)
	}
	return service.ID
}

// Le plan dit où le service ira et ce qu'il y aura ; l'ordre parti, la fiche
// le rend avec son service, sans le moindre octet de l'.env.
func TestDeploy_PlanThenOrderMatchGoldenFiles(t *testing.T) {
	server := newTestServer(t)
	id := deploySetup(t, server, "observe,docker.deploy,docker.update")
	base := "/api/directory/services/" + id
	path := base + "/deploy-plan"

	plan := callAPI(server.Server, http.MethodGet, path, nil)
	if plan.Code != http.StatusOK {
		t.Fatalf("plan: %d %s", plan.Code, plan.Body.String())
	}
	compareGolden(t, filepath.Join("testdata", "deploy_plan.golden.json"), normalize(plan.Body.String()))

	sent := callAPI(server.Server, http.MethodPost, base+"/actions/deploy", map[string]any{"env": deployEnvLine + "\n"})
	if sent.Code != http.StatusCreated {
		t.Fatalf("deploy: %d %s", sent.Code, sent.Body.String())
	}
	var run actionRunJSON
	decodeAPI(t, sent, &run)
	if run.Kind != string(agentwire.KindDockerDeploy) || run.ServiceID == nil || *run.ServiceID != id {
		t.Fatalf("run %+v", run)
	}
	after := callAPI(server.Server, http.MethodGet, path, nil)
	runs := callAPI(server.Server, http.MethodGet, "/api/actions", nil)
	if !strings.Contains(runs.Body.String(), `"env_sent":true`) {
		t.Fatalf("the run does not say an .env left: %s", runs.Body.String())
	}
	for _, body := range []string{sent.Body.String(), after.Body.String(), runs.Body.String()} {
		if strings.Contains(body, "cle-de-test-0001") {
			t.Fatalf("the .env leaks: %s", body)
		}
	}
	compareGolden(t, filepath.Join("testdata", "deploy_plan_sent.golden.json"), normalize(after.Body.String()))

	events, err := server.db.ListAuditEvents(context.Background(), audit.Query{Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range events {
		if strings.Contains(fmt.Sprint(event.Details), "cle-de-test-0001") {
			t.Fatalf("the audit leaks the .env: %+v", event)
		}
	}
}

// Chaque refus arrive par sa clé, avec le statut de sa nature.
func TestDeploy_MapsEachRefusalToItsKey(t *testing.T) {
	server := newTestServer(t)
	call := func(method, path string, payload any, status int, key string) {
		t.Helper()
		got := callAPI(server.Server, method, path, payload)
		if got.Code != status || (key != "" && errorCode(t, got) != key) {
			t.Fatalf("%s %s: %d %s, want %d %s", method, path, got.Code, got.Body.String(), status, key)
		}
	}
	base := "/api/directory/services/"
	call(http.MethodGet, base+"svc_inconnu/deploy-plan", nil, http.StatusNotFound, "not_found")
	call(http.MethodPost, base+"svc_inconnu/actions/deploy", map[string]any{}, http.StatusNotFound, "not_found")
	call(http.MethodPost, base+"svc_inconnu/actions/redeploy", map[string]any{}, http.StatusNotFound, "not_found")

	id := deploySetup(t, server, "observe")
	call(http.MethodPost, base+id+"/actions/deploy", map[string]any{}, http.StatusConflict, "deploy.right_missing")
	call(http.MethodPost, base+id+"/actions/redeploy", map[string]any{}, http.StatusConflict, "deploy.right_missing")
	call(http.MethodPost, base+id+"/actions/deploy", map[string]any{"unknown": true}, http.StatusBadRequest, "bad_json")
}

// Avec le droit : un .env illisible est refusé avant tout ordre, un
// redéploiement attend un premier déploiement, et celui-ci ne se fait qu'une
// fois tant que le dossier n'a pas été effacé.
func TestDeploy_RefusesWhatTheStateForbids(t *testing.T) {
	server := newTestServer(t)
	id := deploySetup(t, server, "observe,docker.deploy,docker.update")
	base := "/api/directory/services/" + id
	call := func(path string, payload any, status int, key string) {
		t.Helper()
		got := callAPI(server.Server, http.MethodPost, base+path, payload)
		if got.Code != status || (key != "" && errorCode(t, got) != key) {
			t.Fatalf("%s: %d %s, want %d %s", path, got.Code, got.Body.String(), status, key)
		}
	}
	call("/actions/redeploy", map[string]any{}, http.StatusConflict, "deploy.not_deployed")
	call("/actions/deploy", map[string]any{"env": "A=\x00"}, http.StatusUnprocessableEntity, "deploy.env_invalid")
	call("/actions/deploy", map[string]any{"env": strings.Repeat("A", 70<<10)}, http.StatusUnprocessableEntity, "deploy.env_too_large")
	// Rien n'est parti pour ces refus.
	if runs := callAPI(server.Server, http.MethodGet, "/api/actions", nil); strings.Contains(runs.Body.String(), "docker.") {
		t.Fatalf("an order left: %s", runs.Body.String())
	}
}
