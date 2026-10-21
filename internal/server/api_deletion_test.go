package server

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/ldesfontaine/opencloud/internal/deletion"
	"github.com/ldesfontaine/opencloud/internal/heartbeat"
	"github.com/ldesfontaine/opencloud/internal/machine"
	"github.com/ldesfontaine/opencloud/internal/status"
)

// Le plan d'une machine se lit avant de la retirer ; retenue, le DELETE
// répond 409 avec la clé du premier bloqueur et le plan ; détachée, 204.
func TestAPI_DeleteMachine_ReadsThePlanThenRefusesThenRemoves(t *testing.T) {
	server := newTestServer(t)
	ctx := context.Background()
	server.enroll(t, "vps-paris-1", remoteID)
	watched := server.createProbeVia(t, probeRequest{Name: "site", Kind: "http", Target: "https://a.fr/", MachineID: remoteID})
	job, err := server.heartbeats.Create(ctx, heartbeat.Definition{Name: "sauvegarde", MachineID: remoteID, Interval: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	component, err := server.status.CreateComponent(ctx, status.ComponentDefinition{
		Name: "Nuage", Members: []status.MemberRef{{Kind: status.KindMachine, ID: remoteID}},
	})
	if err != nil {
		t.Fatal(err)
	}

	got := callAPI(server.Server, http.MethodGet, "/api/machines/"+remoteID+"/delete-plan", nil)
	if got.Code != http.StatusOK {
		t.Fatalf("plan: %d %s", got.Code, got.Body.String())
	}
	var plan deletionPlanJSON
	decodeAPI(t, got, &plan)
	if !plan.Blocked || len(plan.Blockers) != 3 || plan.Blockers[0].Kind != "probes" || plan.Blockers[0].Objects[0].ID != watched.ID ||
		plan.Blockers[1].Kind != "heartbeats" || plan.Blockers[2].Kind != "components" || plan.Blockers[2].Objects[0].Name != "Nuage" {
		t.Fatalf("plan = %+v", plan)
	}

	got = callAPI(server.Server, http.MethodDelete, "/api/machines/"+remoteID, nil)
	if got.Code != http.StatusConflict || errorCode(t, got) != "machine.has_probes" {
		t.Fatalf("retrait retenu: %d %s", got.Code, got.Body.String())
	}
	var refusal deletionRefusalJSON
	decodeAPI(t, got, &refusal)
	if !refusal.Plan.Blocked || len(refusal.Plan.Blockers) != 3 {
		t.Fatalf("le 409 doit porter le plan: %+v", refusal)
	}

	callAPI(server.Server, http.MethodDelete, "/api/probes/"+watched.ID, nil)
	callAPI(server.Server, http.MethodDelete, "/api/jobs/"+job.ID, nil)
	callAPI(server.Server, http.MethodDelete, "/api/status/components/"+component.ID, nil)
	if got := callAPI(server.Server, http.MethodDelete, "/api/machines/"+remoteID, nil); got.Code != http.StatusNoContent {
		t.Fatalf("détachée: %d %s", got.Code, got.Body.String())
	}
	if got := callAPI(server.Server, http.MethodGet, "/api/machines/"+remoteID+"/delete-plan", nil); got.Code != http.StatusNotFound {
		t.Fatalf("plan d'une machine retirée: %d", got.Code)
	}
	// La machine openCloud a son plan comme les autres : elle emporte son
	// agent.
	var local deletionPlanJSON
	decodeAPI(t, callAPI(server.Server, http.MethodGet, "/api/machines/"+machine.LocalID+"/delete-plan", nil), &local)
	if local.Blocked || len(local.Removes) == 0 || local.Removes[0].Kind != string(deletion.KindAgents) {
		t.Fatalf("machine openCloud: %+v", local)
	}
}

// Une sonde ou une tâche affichée par la page de statut est retenue : 409 et
// sa clé ; le plan dit aussi ce qui partirait. Retirée du composant, 204.
func TestAPI_DeleteProbeAndJob_ShownOnStatus_Answer409WithThePlan(t *testing.T) {
	server := newTestServer(t)
	ctx := context.Background()
	watched := server.createProbeVia(t, probeRequest{Name: "site", Kind: "http", Target: "https://a.fr/", MachineID: machine.LocalID})
	job, err := server.heartbeats.Create(ctx, heartbeat.Definition{Name: "sauvegarde", Interval: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	component, err := server.status.CreateComponent(ctx, status.ComponentDefinition{
		Name: "Nuage", Members: []status.MemberRef{{Kind: status.KindProbe, ID: watched.ID}, {Kind: status.KindHeartbeat, ID: job.ID}},
	})
	if err != nil {
		t.Fatal(err)
	}

	for _, object := range []struct{ path, key string }{
		{"/api/probes/" + watched.ID, "probe.shown_on_status"},
		{"/api/jobs/" + job.ID, "job.shown_on_status"},
	} {
		var plan deletionPlanJSON
		decodeAPI(t, callAPI(server.Server, http.MethodGet, object.path+"/delete-plan", nil), &plan)
		if !plan.Blocked || plan.Blockers[0].Kind != "components" || plan.Blockers[0].Objects[0].ID != component.ID {
			t.Fatalf("%s : plan = %+v", object.path, plan)
		}
		got := callAPI(server.Server, http.MethodDelete, object.path, nil)
		if got.Code != http.StatusConflict || errorCode(t, got) != object.key {
			t.Fatalf("%s : %d %s", object.path, got.Code, got.Body.String())
		}
	}

	var plan deletionPlanJSON
	decodeAPI(t, callAPI(server.Server, http.MethodGet, "/api/status/components/"+component.ID+"/delete-plan", nil), &plan)
	if plan.Blocked || len(plan.Removes) != 1 || plan.Removes[0].Kind != "members" || plan.Removes[0].Count != 2 {
		t.Fatalf("plan du composant = %+v", plan)
	}
	callAPI(server.Server, http.MethodDelete, "/api/status/components/"+component.ID, nil)
	for _, path := range []string{"/api/probes/" + watched.ID, "/api/jobs/" + job.ID} {
		if got := callAPI(server.Server, http.MethodDelete, path, nil); got.Code != http.StatusNoContent {
			t.Fatalf("%s détachée: %d %s", path, got.Code, got.Body.String())
		}
	}
}

// Le plan d'un projet ne retient rien : ses services se détachent.
func TestAPI_ProjectDeletePlan_DetachesItsServices(t *testing.T) {
	server := newTestServer(t)
	server.enroll(t, "vps-paris-1", remoteID)
	var project projectJSON
	decodeAPI(t, callAPI(server.Server, http.MethodPost, "/api/directory/projects", projectRequest{Name: "docs"}), &project)
	request := nextcloudRequest(remoteID)
	request.ProjectIDs = []string{project.ID}
	var created declaredServiceJSON
	decodeAPI(t, callAPI(server.Server, http.MethodPost, "/api/directory/services", request), &created)

	var plan deletionPlanJSON
	decodeAPI(t, callAPI(server.Server, http.MethodGet, "/api/directory/projects/"+project.ID+"/delete-plan", nil), &plan)
	if plan.Blocked || len(plan.Detaches) != 1 || plan.Detaches[0].Kind != "project_links" || plan.Detaches[0].Count != 1 {
		t.Fatalf("plan = %+v", plan)
	}
	var service deletionPlanJSON
	decodeAPI(t, callAPI(server.Server, http.MethodGet, "/api/directory/services/"+created.ID+"/delete-plan", nil), &service)
	if service.Blocked || len(service.Removes) != 1 || service.Removes[0].Kind != "units" {
		t.Fatalf("plan du service = %+v", service)
	}
	if got := callAPI(server.Server, http.MethodGet, "/api/directory/projects/prj_0000000000000000/delete-plan", nil); got.Code != http.StatusNotFound {
		t.Fatalf("projet inconnu: %d", got.Code)
	}
}
