package server

import (
	"context"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ldesfontaine/opencloud/internal/action"
	"github.com/ldesfontaine/opencloud/internal/agentwire"
	"github.com/ldesfontaine/opencloud/internal/audit"
	"github.com/ldesfontaine/opencloud/internal/container"
	"github.com/ldesfontaine/opencloud/internal/directory"
	"github.com/ldesfontaine/opencloud/internal/machine"
	"github.com/ldesfontaine/opencloud/internal/update"
)

// upgradeSetup déploie le service (l'agent dit l'ordre réussi), puis fait
// remonter ce que la machine montre : le conteneur de la base et le constat
// d'image qui propose 16.5. Rend le service, l'unité de la base et la session.
func upgradeSetup(t *testing.T, server *testServer, rights string) (serviceID, dbUnitID string, session *machine.Session) {
	t.Helper()
	serviceID, session = deploySetupSession(t, server, rights)
	sent := callAPI(server.Server, http.MethodPost, "/api/directory/services/"+serviceID+"/actions/deploy", map[string]any{})
	if sent.Code != http.StatusCreated {
		t.Fatalf("deploy: %d %s", sent.Code, sent.Body.String())
	}
	var run actionRunJSON
	decodeAPI(t, sent, &run)
	finished := agentwire.ActionReport{Results: []agentwire.ActionResult{{RunID: run.ID, State: agentwire.ActionSucceeded}}}
	if got := server.agentPost(session.Token, "/agent/signal", agentwire.SignalRequest{Actions: &finished}); got.Code != http.StatusNoContent {
		t.Fatalf("deploy result: %d %s", got.Code, got.Body.String())
	}
	database := container.Observed{
		ContainerID: strings.Repeat("b", 64), Name: "nextcloud-db-1", Group: "nextcloud", Image: "postgres:16.4",
		ComposeService: "db", ComposeDir: "/srv/workspace/nextcloud", State: container.StateRunning, CreatedAt: testNow,
	}
	inventory := container.Report{Complete: true, Inventory: []container.Observed{database}}
	checks := update.Report{Results: []update.Result{{
		Image: "postgres:16.4", CheckedAt: testNow, Outcome: update.OutcomeOK,
		LocalDigest: "sha256:" + strings.Repeat("1", 64), RemoteDigest: "sha256:" + strings.Repeat("1", 64), NewerTag: "16.5",
	}}}
	if got := server.agentPost(session.Token, "/agent/signal", agentwire.SignalRequest{Services: &inventory, Images: &checks}); got.Code != http.StatusNoContent {
		t.Fatalf("signal: %d %s", got.Code, got.Body.String())
	}
	return serviceID, server.unitIDOf(t, serviceID, "db"), session
}

func (ts *testServer) versionOf(t *testing.T, unitID string) string {
	t.Helper()
	unit, err := ts.db.GetUnit(context.Background(), unitID)
	if err != nil {
		t.Fatal(err)
	}
	return unit.Version
}

// docker.update compte les ordres de redéploiement partis pour ce service.
func (ts *testServer) updateOrders(t *testing.T) int {
	t.Helper()
	runs, err := ts.db.ListActionRuns(context.Background(), action.Query{Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, run := range runs {
		if run.Kind == agentwire.KindDockerUpdate {
			count++
		}
	}
	return count
}

// Le geste écrit la version offerte et envoie le redéploiement : l'ordre porte
// le compose du nouveau tag, la fiche du service le nouveau tag, le journal
// dit l'unité et les deux versions.
func TestUpgrade_ChangesTheVersionAndRedeploysInOneGesture(t *testing.T) {
	server := newTestServer(t)
	serviceID, unitID, _ := upgradeSetup(t, server, "observe,docker.deploy,docker.update")

	sent := callAPI(server.Server, http.MethodPost, "/api/directory/units/"+unitID+"/actions/upgrade", map[string]any{"version": "16.5"})

	if sent.Code != http.StatusCreated {
		t.Fatalf("upgrade: %d %s", sent.Code, sent.Body.String())
	}
	var run actionRunJSON
	decodeAPI(t, sent, &run)
	if run.Kind != string(agentwire.KindDockerUpdate) || run.ServiceID == nil || *run.ServiceID != serviceID {
		t.Fatalf("run %+v", run)
	}
	if !strings.Contains(sent.Body.String(), "image: postgres:16.5") || strings.Contains(sent.Body.String(), "postgres:16.4") || strings.Contains(sent.Body.String(), `"env_sent":true`) {
		t.Fatalf("the order must carry the new compose and no .env: %s", sent.Body.String())
	}
	compareGolden(t, filepath.Join("testdata", "deploy_upgrade.golden.json"), normalize(sent.Body.String()))
	if got := server.versionOf(t, unitID); got != "16.5" {
		t.Fatalf("declared version %q", got)
	}

	events, err := server.db.ListAuditEvents(context.Background(), audit.Query{Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, event := range events {
		if event.Action == audit.ServiceUpdate && event.Details["via"] == "upgrade" {
			found = event.ObjectID == serviceID && event.Details["unit"] == "db" &&
				event.Details["version_before"] == "16.4" && event.Details["version_after"] == "16.5"
		}
	}
	if !found {
		t.Fatalf("no audit line for the upgrade: %+v", events)
	}
}

// Chaque refus arrive par sa clé, avec le statut de sa nature, et n'écrit
// rien : la version déclarée reste, aucun redéploiement ne part.
func TestUpgrade_RefusesWhatTheStateForbidsAndWritesNothing(t *testing.T) {
	server := newTestServer(t)
	serviceID, unitID, session := upgradeSetup(t, server, "observe,docker.deploy,docker.update")
	binary, err := server.directory.CreateService(context.Background(), directory.ServiceDefinition{
		MachineID: remoteID, Name: "caddy", Nature: directory.NatureInfra, Origin: directory.OriginBinary,
		Units: []directory.UnitDefinition{{Name: "caddy", SystemdUnit: "caddy.service"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	call := func(unit string, payload any, status int, key string) {
		t.Helper()
		got := callAPI(server.Server, http.MethodPost, "/api/directory/units/"+unit+"/actions/upgrade", payload)
		if got.Code != status || errorCode(t, got) != key {
			t.Fatalf("%s %v: %d %s, want %d %s", unit, payload, got.Code, got.Body.String(), status, key)
		}
		if server.versionOf(t, unitID) != "16.4" || server.updateOrders(t) != 0 {
			t.Fatalf("a refusal wrote or sent (%s)", key)
		}
	}
	version := map[string]any{"version": "16.5"}

	call("unt_inconnue", version, http.StatusNotFound, "directory.unit_not_found")
	call(unitID, map[string]any{"version": "16.5", "force": true}, http.StatusBadRequest, "bad_json")
	call(binary.Units[0].ID, version, http.StatusUnprocessableEntity, "directory.unit_not_container")
	// Une autre version que celle que l'agent propose, même proche.
	call(unitID, map[string]any{"version": "16.6"}, http.StatusConflict, "deploy.version_not_offered")
	call(unitID, map[string]any{"version": "16.5 "}, http.StatusConflict, "deploy.version_not_offered")
	call(unitID, map[string]any{"version": ""}, http.StatusConflict, "deploy.version_not_offered")

	// Épinglée, l'unité n'a plus d'offre.
	if got := callAPI(server.Server, http.MethodPut, "/api/directory/units/"+unitID+"/update-policy", unitPolicyRequest{Policy: "hold"}); got.Code != http.StatusNoContent {
		t.Fatalf("hold: %d", got.Code)
	}
	call(unitID, version, http.StatusConflict, "deploy.version_not_offered")
	if got := callAPI(server.Server, http.MethodPut, "/api/directory/units/"+unitID+"/update-policy", unitPolicyRequest{Policy: "watch"}); got.Code != http.StatusNoContent {
		t.Fatalf("watch: %d", got.Code)
	}

	// Une empreinte épinglée appartient à l'ancien tag.
	found, err := server.db.GetDeclaredService(context.Background(), serviceID)
	if err != nil {
		t.Fatal(err)
	}
	pinned := found.Definition()
	pinned.Units[1].Digest = "sha256:" + strings.Repeat("a", 64)
	if _, err := server.directory.UpdateService(context.Background(), serviceID, pinned); err != nil {
		t.Fatal(err)
	}
	call(unitID, version, http.StatusConflict, "deploy.unit_pinned_digest")
	pinned.Units[1].Digest = ""
	if _, err := server.directory.UpdateService(context.Background(), serviceID, pinned); err != nil {
		t.Fatal(err)
	}

	// La machine ne répond plus : l'ordre ne partirait pas, la version non plus.
	server.machines.Disconnect(session)
	call(unitID, version, http.StatusConflict, "action.machine_offline")
}

// Sans le droit docker.update, ou avant le premier déploiement, rien non plus.
func TestUpgrade_RefusesWithoutTheRightOrBeforeTheFirstDeployment(t *testing.T) {
	server := newTestServer(t)
	_, unitID, _ := upgradeSetup(t, server, "observe,docker.deploy")
	got := callAPI(server.Server, http.MethodPost, "/api/directory/units/"+unitID+"/actions/upgrade", map[string]any{"version": "16.5"})
	if got.Code != http.StatusConflict || errorCode(t, got) != "deploy.right_missing" || server.versionOf(t, unitID) != "16.4" {
		t.Fatalf("without the right: %d %s", got.Code, got.Body.String())
	}

	fresh := newTestServer(t)
	serviceID := deploySetup(t, fresh, "observe,docker.deploy,docker.update")
	freshUnit := fresh.unitIDOf(t, serviceID, "db")
	got = callAPI(fresh.Server, http.MethodPost, "/api/directory/units/"+freshUnit+"/actions/upgrade", map[string]any{"version": "16.5"})
	if got.Code != http.StatusConflict || errorCode(t, got) != "deploy.not_deployed" || fresh.versionOf(t, freshUnit) != "16.4" {
		t.Fatalf("before the first deployment: %d %s", got.Code, got.Body.String())
	}
}
