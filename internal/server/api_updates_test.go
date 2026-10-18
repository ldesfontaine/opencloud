package server

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/ldesfontaine/opencloud/internal/container"
	"github.com/ldesfontaine/opencloud/internal/machine"
	"github.com/ldesfontaine/opencloud/internal/update"
)

// unitIDOf rend l'unité d'un service déclaré par son nom.
func (ts *testServer) unitIDOf(t *testing.T, serviceID, unitName string) string {
	t.Helper()
	found, err := ts.directory.GetService(context.Background(), serviceID)
	if err != nil {
		t.Fatal(err)
	}
	for _, unit := range found.Units {
		if unit.Name == unitName {
			return unit.ID
		}
	}
	t.Fatalf("unit %q not found in %+v", unitName, found.Units)
	return ""
}

// La politique se règle sur l'unité : épingler (hold) retire la mise à jour
// du compteur sans effacer le constat, ignorer pousse l'image à la machine,
// une politique inconnue ou une unité disparue est refusée avec sa clé.
func TestAPI_UnitUpdatePolicy_HoldIgnoreAndRefuseWithTheKey(t *testing.T) {
	server := newTestServer(t)
	server.enroll(t, "vps-paris-1", remoteID)
	server.seedContainers(t)
	cloudID := server.seedGaps(t)
	unitID := server.unitIDOf(t, cloudID, "nextcloud")
	var counts countsResponse
	decodeAPI(t, callAPI(server.Server, http.MethodGet, "/api/counts", nil), &counts)
	if counts.Containers.Updates != 2 {
		t.Fatalf("updates = %d, want 2", counts.Containers.Updates)
	}
	path := "/api/directory/units/" + unitID + "/update-policy"
	if got := callAPI(server.Server, http.MethodPut, path, unitPolicyRequest{Policy: "hold"}); got.Code != http.StatusNoContent {
		t.Fatalf("hold: %d %s", got.Code, got.Body.String())
	}
	decodeAPI(t, callAPI(server.Server, http.MethodGet, "/api/counts", nil), &counts)
	if counts.Containers.Updates != 1 {
		t.Fatalf("held: updates = %d, want 1", counts.Containers.Updates)
	}
	var response containerResponse
	decodeAPI(t, callAPI(server.Server, http.MethodGet, "/api/containers/"+nextcloudContainerID(), nil), &response)
	if response.Container.UpdatePolicy != "hold" || response.Container.DeclaredServiceID != cloudID || response.Container.ImageCheck == nil || response.Container.ImageCheck.Kind == "" {
		t.Fatalf("held container = %+v", response.Container)
	}
	if got := callAPI(server.Server, http.MethodPut, path, unitPolicyRequest{Policy: "ignore"}); got.Code != http.StatusNoContent {
		t.Fatalf("ignore: %d", got.Code)
	}
	if got := callAPI(server.Server, http.MethodPut, path, unitPolicyRequest{Policy: "pinned"}); got.Code != http.StatusUnprocessableEntity || errorCode(t, got) != "directory.update_policy_invalid" {
		t.Fatalf("bad policy: %d %s", got.Code, got.Body.String())
	}
	if got := callAPI(server.Server, http.MethodPut, "/api/directory/units/nope/update-policy", unitPolicyRequest{Policy: "watch"}); got.Code != http.StatusNotFound {
		t.Fatalf("unknown unit: %d", got.Code)
	}
	// Un conteneur non déclaré suit toujours : il n'a pas d'unité à régler.
	decodeAPI(t, callAPI(server.Server, http.MethodGet, "/api/containers/"+nextcloudDBContainerID(), nil), &response)
	if response.Container.UpdatePolicy != "watch" || response.Container.DeclaredServiceID != "" {
		t.Fatalf("undeclared container = %+v", response.Container)
	}
}

// Le conteneur recréé change d'identifiant, pas d'unité : il retrouve la
// politique que l'ancienne fiche avait perdue en disparaissant.
func TestAPI_UnitUpdatePolicy_SurvivesTheRecreationOfTheContainer(t *testing.T) {
	server := newTestServer(t)
	server.enroll(t, "vps-paris-1", remoteID)
	server.seedContainers(t)
	cloudID := server.seedGaps(t)
	unitID := server.unitIDOf(t, cloudID, "nextcloud")
	if got := callAPI(server.Server, http.MethodPut, "/api/directory/units/"+unitID+"/update-policy", unitPolicyRequest{Policy: "ignore"}); got.Code != http.StatusNoContent {
		t.Fatalf("ignore: %d", got.Code)
	}
	recreated := container.Observed{
		ContainerID: strings.Repeat("e", 64), Name: "nextcloud", Group: "nextcloud", Image: "nextcloud:29.0.4",
		ComposeService: "nextcloud", ComposeDir: "/srv/nextcloud", State: container.StateRunning, CreatedAt: testNow,
	}
	report := container.Report{Complete: true, Inventory: []container.Observed{recreated}}
	if err := server.containers.Record(context.Background(), remoteID, report); err != nil {
		t.Fatal(err)
	}
	var response containerResponse
	decodeAPI(t, callAPI(server.Server, http.MethodGet, "/api/containers/"+container.ID(remoteID, recreated.ContainerID), nil), &response)
	if response.Container.UpdatePolicy != "ignore" || response.Container.DeclaredServiceID != cloudID {
		t.Fatalf("recreated container = %+v", response.Container)
	}
}

// Vérifier maintenant vers une machine hors ligne est refusé avec la clé
// du catalogue ; la machine openCloud, elle, vérifie sans réseau.
func TestCheckUpdates_OfflineRefused_LocalAccepted(t *testing.T) {
	server := newTestServer(t)
	server.enroll(t, "vps-paris-1", remoteID)
	if got := callAPI(server.Server, http.MethodPost, "/api/machines/"+remoteID+"/actions/check-updates", nil); got.Code != http.StatusServiceUnavailable || errorCode(t, got) != "update.machine_offline" {
		t.Fatalf("offline: %d %s", got.Code, got.Body.String())
	}
	if got := callAPI(server.Server, http.MethodPost, "/api/machines/nope/actions/check-updates", nil); got.Code != http.StatusNotFound {
		t.Fatalf("unknown machine: %d", got.Code)
	}
	runner := &fakeUpdateRunner{}
	server.updates.SetLocalRunner(machine.LocalID, runner)
	if got := callAPI(server.Server, http.MethodPost, "/api/machines/local/actions/check-updates", nil); got.Code != http.StatusNoContent {
		t.Fatalf("local: %d %s", got.Code, got.Body.String())
	}
	if len(runner.assignments) != 1 || !runner.assignments[0].Now {
		t.Fatalf("local runner got %+v", runner.assignments)
	}
}

type fakeUpdateRunner struct{ assignments []update.Assignment }

func (f *fakeUpdateRunner) Assign(assignment update.Assignment) {
	f.assignments = append(f.assignments, assignment)
}

// Le signal porte la section images : elle s'écrit au nom de la machine
// de la session, un rapport hors de mesure est refusé en bloc.
func TestAgentSignal_RecordsImageChecks(t *testing.T) {
	server := newTestServer(t)
	enrolled, private := server.enroll(t, "vps-paris-1", remoteID)
	response, session, _, cancel := openStream(t, server, enrolled.ID, private)
	defer response.Body.Close()
	defer cancel()
	report := update.Report{Results: []update.Result{{
		Image: "nginx:1.27", CheckedAt: testNow.Add(-time.Minute), Outcome: update.OutcomeOK,
		LocalDigest: "sha256:" + strings.Repeat("1", 64), RemoteDigest: "sha256:" + strings.Repeat("1", 64), NewerTag: "1.29",
	}}}
	if got := server.agentPost(session, "/agent/signal", machine.SignalRequest{Images: &report}); got.Code != http.StatusNoContent {
		t.Fatalf("signal: %d %s", got.Code, got.Body.String())
	}
	checks, err := server.updates.Checks(context.Background(), remoteID)
	if err != nil || len(checks) != 1 {
		t.Fatalf("checks = %+v %v", checks, err)
	}
	for _, check := range checks {
		if check.Kind != update.KindMinor || check.NewerTag != "1.29" {
			t.Fatalf("check = %+v", check)
		}
	}
	bad := update.Report{Results: []update.Result{{Image: "nginx:1.27", CheckedAt: testNow, Outcome: "maybe"}}}
	if got := server.agentPost(session, "/agent/signal", machine.SignalRequest{Images: &bad}); got.Code != http.StatusBadRequest || !strings.Contains(got.Body.String(), "bad_images") {
		t.Fatalf("bad report: %d %s", got.Code, got.Body.String())
	}
}
