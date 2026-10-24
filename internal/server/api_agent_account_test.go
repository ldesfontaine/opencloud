package server

import (
	"context"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
)

// reconnectAs fait redire à l'agent de la machine distante son compte et les
// droits qu'il ne peut pas exercer, comme à l'ouverture d'un flux.
func reconnectAs(t *testing.T, server *testServer, rights, account, unavailable string) {
	t.Helper()
	current, err := server.machines.Get(context.Background(), remoteID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := server.machines.Connect(context.Background(), current.Machine, "192.0.2.1", "v0.0.1", rights, account, unavailable); err != nil {
		t.Fatal(err)
	}
}

// La fiche dit le compte de l'agent et, droit par droit, ce qu'il ne peut pas
// exercer. Un agent d'avant, qui ne dit rien, rend run_as null.
func TestMachine_SaysTheAccountAndWhatItCannotExercise(t *testing.T) {
	server := newTestServer(t)
	server.enroll(t, "vps-paris-1", remoteID)
	reconnectAs(t, server, "observe,docker.deploy,backup.receive=/srv/backups", "998:opencloud-agent", "docker.deploy=docker_denied,backup.receive=path_denied")

	recorder := callAPI(server.Server, http.MethodGet, "/api/machines/"+remoteID, nil)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status %d: %s", recorder.Code, recorder.Body.String())
	}
	compareGolden(t, filepath.Join("testdata", "machine_account.golden.json"), normalize(recorder.Body.String()))

	var fiche machineJSON
	decodeAPI(t, recorder, &fiche)
	if fiche.Agent == nil || fiche.Agent.RunAs == nil || fiche.Agent.RunAs.UID != 998 {
		t.Fatalf("agent %+v", fiche.Agent)
	}
	reasons := map[string]string{}
	for _, capability := range fiche.Agent.Capabilities {
		reasons[capability.Name] = capability.Unavailable
	}
	want := map[string]string{"observe": "", "docker.deploy": "docker_denied", "backup.receive": "path_denied"}
	for name, reason := range want {
		if reasons[name] != reason {
			t.Errorf("%s: %q, want %q", name, reasons[name], reason)
		}
	}
	// Ce que le jeton demandait n'a pas ce champ : le contrat du jeton ne bouge pas.
	if strings.Contains(recorder.Body.String(), `"requested":[{"name":"observe","paths":[],"unavailable"`) {
		t.Fatalf("the requested rights carry a reason: %s", recorder.Body.String())
	}
}

// Le plan de déploiement dit que le droit est coché mais pas exerçable, et
// pourquoi ; le geste reste permis : le serveur ne refuse jamais d'avance, c'est
// l'agent qui refuse, avec la raison, au moment de l'ordre.
func TestDeploy_AnUnavailableRightIsShownAndTheOrderStillGoes(t *testing.T) {
	server := newTestServer(t)
	id := deploySetup(t, server, "observe,docker.deploy,docker.update")
	reconnectAs(t, server, "observe,docker.deploy,docker.update", "998:opencloud-agent", "docker.deploy=docker_denied")
	path := "/api/directory/services/" + id + "/deploy-plan"

	plan := callAPI(server.Server, http.MethodGet, path, nil)
	if plan.Code != http.StatusOK {
		t.Fatalf("plan: %d %s", plan.Code, plan.Body.String())
	}
	compareGolden(t, filepath.Join("testdata", "deploy_plan_unavailable.golden.json"), normalize(plan.Body.String()))

	sent := callAPI(server.Server, http.MethodPost, "/api/directory/services/"+id+"/actions/deploy", map[string]any{})
	if sent.Code != http.StatusCreated {
		t.Fatalf("the server refused an order on the strength of the report: %d %s", sent.Code, sent.Body.String())
	}
}

// Même chose pour la destination d'une sauvegarde : affichée « indisponible »,
// l'envoi n'est pas refusé.
func TestBackups_AnUnavailableRightIsShownAndTheRunStillGoes(t *testing.T) {
	server := newTestServer(t)
	backupSetup(t, server)
	reconnectAs(t, server, "backup.receive=/srv/backups", "998:opencloud-agent", "backup.receive=path_unwritable")

	overview := callAPI(server.Server, http.MethodGet, "/api/backups", nil)
	if overview.Code != http.StatusOK {
		t.Fatalf("overview: %d %s", overview.Code, overview.Body.String())
	}
	compareGolden(t, filepath.Join("testdata", "backups_unavailable.golden.json"), normalizeBackups(overview.Body.String()))

	run := callAPI(server.Server, http.MethodPost, "/api/backups/plans/database/actions/run", map[string]any{})
	if run.Code != http.StatusCreated {
		t.Fatalf("the server refused a run on the strength of the report: %d %s", run.Code, run.Body.String())
	}
}

// Le droit retiré (ré-enrôlement avec moins de droits) reste « missing » : la
// raison d'un droit absent ne le fait pas passer pour « indisponible ».
func TestDeploy_AMissingRightStaysMissingWhateverTheReasons(t *testing.T) {
	server := newTestServer(t)
	id := deploySetup(t, server, "observe,docker.deploy")
	reconnectAs(t, server, "observe", "998:opencloud-agent", "docker.deploy=docker_denied")
	plan := callAPI(server.Server, http.MethodGet, "/api/directory/services/"+id+"/deploy-plan", nil)
	var decoded deployPlanJSON
	decodeAPI(t, plan, &decoded)
	if decoded.Right != "missing" || decoded.RightReason != "" {
		t.Fatalf("plan %+v", decoded)
	}
}
