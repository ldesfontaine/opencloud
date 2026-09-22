package server

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/ldesfontaine/opencloud/internal/machine"
	"github.com/ldesfontaine/opencloud/internal/service"
	"github.com/ldesfontaine/opencloud/internal/update"
)

// Épingler retire la mise à jour du compteur sans effacer le constat ;
// exclure pousse l'image à la machine ; une politique inconnue est refusée.
func TestUpdatePolicy_PinExcludeAndRefuse(t *testing.T) {
	server := newTestServer(t)
	server.enroll(t, "vps-paris-1", remoteID)
	serviceID := server.seedServices(t)
	var counts countsResponse
	decodeAPI(t, callAPI(server.Server, http.MethodGet, "/api/counts", nil), &counts)
	if counts.Services.Updates != 2 {
		t.Fatalf("updates = %d, want 2", counts.Services.Updates)
	}
	if got := callAPI(server.Server, http.MethodPut, "/api/services/"+serviceID+"/update-policy", updatePolicyRequest{Policy: "pinned"}); got.Code != http.StatusNoContent {
		t.Fatalf("pin: %d %s", got.Code, got.Body.String())
	}
	decodeAPI(t, callAPI(server.Server, http.MethodGet, "/api/counts", nil), &counts)
	if counts.Services.Updates != 1 {
		t.Fatalf("pinned: updates = %d, want 1", counts.Services.Updates)
	}
	var response serviceResponse
	decodeAPI(t, callAPI(server.Server, http.MethodGet, "/api/services/"+serviceID, nil), &response)
	if response.Service.UpdatePolicy != "pinned" || response.Service.ImageCheck == nil || response.Service.ImageCheck.Kind != "digest" {
		t.Fatalf("pinned service = %+v", response.Service)
	}
	if got := callAPI(server.Server, http.MethodPut, "/api/services/"+serviceID+"/update-policy", updatePolicyRequest{Policy: "excluded"}); got.Code != http.StatusNoContent {
		t.Fatalf("exclude: %d", got.Code)
	}
	if got := callAPI(server.Server, http.MethodPut, "/api/services/"+serviceID+"/update-policy", updatePolicyRequest{Policy: "later"}); got.Code != http.StatusBadRequest || errorCode(t, got) != "update.policy_invalid" {
		t.Fatalf("bad policy: %d %s", got.Code, got.Body.String())
	}
	if got := callAPI(server.Server, http.MethodPut, "/api/services/nope/update-policy", updatePolicyRequest{Policy: ""}); got.Code != http.StatusNotFound {
		t.Fatalf("unknown service: %d", got.Code)
	}
	// La politique survit au rapport suivant de l'agent, qui redonne le
	// conteneur entier.
	db := service.Container{
		ContainerID: strings.Repeat("b", 64), Name: "nextcloud-db", Group: "nextcloud", Image: "postgres:16.4",
		ComposeService: "nextcloud-db", ComposeDir: "/srv/nextcloud", State: service.StateRunning, CreatedAt: testNow.Add(-48 * time.Hour),
	}
	restarted := service.Report{Events: []service.Event{{At: testNow, Action: "start", ContainerID: db.ContainerID, State: service.StateRunning, Container: &db}}}
	if err := server.services.Record(context.Background(), remoteID, restarted); err != nil {
		t.Fatal(err)
	}
	decodeAPI(t, callAPI(server.Server, http.MethodGet, "/api/services/"+serviceID, nil), &response)
	if response.Service.UpdatePolicy != "excluded" {
		t.Fatalf("policy after inventory = %q", response.Service.UpdatePolicy)
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
