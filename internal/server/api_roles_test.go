package server

import (
	"context"
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ldesfontaine/opencloud/internal/agentwire"
	"github.com/ldesfontaine/opencloud/internal/role"
)

func proxmoxRoleReport(version string) role.Report {
	return role.Report{Roles: []role.Role{{Name: role.ProxmoxVE, Version: version}}}
}

func connectAgent(t *testing.T, server *testServer, name, id string) string {
	t.Helper()
	server.enroll(t, name, id)
	return openAgentSession(t, server, id)
}

// openAgentSession ouvre le flux de l'agent d'une machine déjà enrôlée et rend
// le jeton de session qui signe ses signaux.
func openAgentSession(t *testing.T, server *testServer, machineID string) string {
	t.Helper()
	enrolled, err := server.machines.Get(context.Background(), machineID)
	if err != nil {
		t.Fatal(err)
	}
	session, err := server.machines.Connect(context.Background(), enrolled.Machine, "192.0.2.1", "v0.0.1", "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	return session.Token
}

// Le signal de l'agent dit ce que la machine est ; la fiche le montre, la
// liste aussi, et un agent qui ne le voit plus l'efface.
func TestRoles_TheSignalWritesTheRoleAndTheMachineSaysIt(t *testing.T) {
	server := newTestServer(t)
	token := connectAgent(t, server, "pve", pveID)
	server.enroll(t, "docker1", dockerVMID)

	report := proxmoxRoleReport("8.2.4")
	if got := server.agentPost(token, "/agent/signal", agentwire.SignalRequest{Roles: &report}); got.Code != http.StatusNoContent {
		t.Fatalf("signal: %d %s", got.Code, got.Body.String())
	}

	one := callAPI(server.Server, http.MethodGet, "/api/machines/"+pveID, nil)
	compareGolden(t, filepath.Join("testdata", "machine_role.golden.json"), normalize(one.Body.String()))
	var listed machinesResponse
	decodeAPI(t, callAPI(server.Server, http.MethodGet, "/api/machines", nil), &listed)
	for _, listedMachine := range listed.Machines {
		wantRoles := 0
		if listedMachine.ID == pveID {
			wantRoles = 1
		}
		if len(listedMachine.Roles) != wantRoles {
			t.Errorf("%s : %d rôles, attendu %d", listedMachine.Name, len(listedMachine.Roles), wantRoles)
		}
	}

	lost := role.Report{Roles: []role.Role{}}
	if got := server.agentPost(token, "/agent/signal", agentwire.SignalRequest{Roles: &lost}); got.Code != http.StatusNoContent {
		t.Fatalf("signal: %d %s", got.Code, got.Body.String())
	}
	var after machineJSON
	decodeAPI(t, callAPI(server.Server, http.MethodGet, "/api/machines/"+pveID, nil), &after)
	if len(after.Roles) != 0 {
		t.Fatalf("un rôle perdu reste : %+v", after.Roles)
	}
}

// Un rôle ne donne aucun droit : détecter Proxmox ne déclare rien et ne lit
// rien de l'hôte.
func TestRoles_ADetectedRoleDeclaresNothing(t *testing.T) {
	server := newTestServer(t)
	token := connectAgent(t, server, "pve", pveID)
	report := proxmoxRoleReport("8.2.4")
	if got := server.agentPost(token, "/agent/signal", agentwire.SignalRequest{Roles: &report}); got.Code != http.StatusNoContent {
		t.Fatalf("signal: %d %s", got.Code, got.Body.String())
	}

	var carrier machineJSON
	decodeAPI(t, callAPI(server.Server, http.MethodGet, "/api/machines/"+pveID, nil), &carrier)
	if carrier.Proxmox.Declared || carrier.Proxmox.Guests != 0 {
		t.Fatalf("la détection a déclaré l'hôte : %+v", carrier.Proxmox)
	}
	if got := callAPI(server.Server, http.MethodGet, "/api/machines/"+pveID+"/proxmox", nil); got.Code != http.StatusNotFound {
		t.Fatalf("l'hôte n'est pas déclaré : %d", got.Code)
	}
}

// Un rapport hors de la liste fermée est refusé seul, et l'ancien reste.
func TestRoles_AnInvalidReportIsRefusedAloneAndTheOldOneStays(t *testing.T) {
	server := newTestServer(t)
	token := connectAgent(t, server, "pve", pveID)
	good := proxmoxRoleReport("8.2.4")
	if got := server.agentPost(token, "/agent/signal", agentwire.SignalRequest{Roles: &good}); got.Code != http.StatusNoContent {
		t.Fatalf("signal: %d %s", got.Code, got.Body.String())
	}

	hostile := map[string]role.Report{
		"rôle inconnu":    {Roles: []role.Role{{Name: "admin"}}},
		"version hostile": {Roles: []role.Role{{Name: role.ProxmoxVE, Version: `8"><script>`}}},
	}
	for name, report := range hostile {
		t.Run(name, func(t *testing.T) {
			got := server.agentPost(token, "/agent/signal", agentwire.SignalRequest{Roles: &report})
			if got.Code != http.StatusBadRequest || !strings.Contains(got.Body.String(), "bad_roles") {
				t.Fatalf("rapport hostile accepté : %d %s", got.Code, got.Body.String())
			}
		})
	}

	var carrier machineJSON
	decodeAPI(t, callAPI(server.Server, http.MethodGet, "/api/machines/"+pveID, nil), &carrier)
	if len(carrier.Roles) != 1 || carrier.Roles[0].Version != "8.2.4" {
		t.Fatalf("le rapport a changé : %+v", carrier.Roles)
	}
}

// walkJSON rend toutes les clés et toutes les valeurs texte d'un document.
func walkJSON(node any, visit func(key, text string)) {
	switch typed := node.(type) {
	case map[string]any:
		for key, child := range typed {
			visit(key, "")
			walkJSON(child, visit)
		}
	case []any:
		for _, child := range typed {
			walkJSON(child, visit)
		}
	case string:
		visit("", typed)
	}
}

// Un jeton d'API lit l'hôte Proxmox d'une machine, ses VM et ses stockages, et
// la réponse ne porte aucun secret : ni le secret du jeton Proxmox, ni une clé
// qui le nomme. Il lit aussi les rôles de la fiche. Il n'écrit rien.
func TestProxmox_AnAPITokenReadsTheHostGuestsAndStoragesWithoutAnySecret(t *testing.T) {
	server, api, _ := proxmoxBench(t)
	token := openAgentSession(t, server, pveID)
	report := proxmoxRoleReport("8.2.4")
	if got := server.agentPost(token, "/agent/signal", agentwire.SignalRequest{Roles: &report}); got.Code != http.StatusNoContent {
		t.Fatalf("signal: %d %s", got.Code, got.Body.String())
	}
	if got := putProxmox(server, pveID, api.request(pveTokenSecret)); got.Code != http.StatusOK {
		t.Fatalf("%d %s", got.Code, got.Body.String())
	}
	bearer := server.issueAPIToken(t, "mcp-maison")

	for _, path := range []string{"/api/machines/" + pveID + "/proxmox", "/api/machines/" + pveID, "/api/machines"} {
		got := callBearer(server.Server, path, bearer)
		if got.Code != http.StatusOK {
			t.Fatalf("%s : %d %s", path, got.Code, got.Body.String())
		}
		if strings.Contains(got.Body.String(), pveTokenSecret) {
			t.Errorf("%s porte le secret du jeton Proxmox", path)
		}
		var document any
		if err := json.Unmarshal(got.Body.Bytes(), &document); err != nil {
			t.Fatal(err)
		}
		walkJSON(document, func(key, text string) {
			if strings.Contains(strings.ToLower(key), "secret") || strings.Contains(text, pveTokenSecret) {
				t.Errorf("%s : %q%q ne devrait pas sortir par un jeton d'API", path, key, text)
			}
		})
	}

	var host proxmoxHostJSON
	decodeAPI(t, callBearer(server.Server, "/api/machines/"+pveID+"/proxmox", bearer), &host)
	if len(host.Guests) != 2 || len(host.Storages) != 1 || host.TokenID != pveTokenID {
		t.Fatalf("hôte lu par jeton d'API : %+v", host)
	}
	var carrier machineJSON
	decodeAPI(t, callBearer(server.Server, "/api/machines/"+pveID, bearer), &carrier)
	if len(carrier.Roles) != 1 || carrier.Roles[0].Name != string(role.ProxmoxVE) {
		t.Fatalf("rôles lus par jeton d'API : %+v", carrier.Roles)
	}

	for _, request := range []struct{ method, path string }{
		{http.MethodPut, "/api/machines/" + pveID + "/proxmox"},
		{http.MethodPost, "/api/machines/" + pveID + "/actions/remove-proxmox"},
		{http.MethodPost, "/api/machines/" + pveID + "/proxmox/guests/5012/actions/shutdown"},
	} {
		got := callBearerWith(server.Server, request.method, request.path, bearer, "{}")
		if got.Code != http.StatusForbidden || errorCode(t, got) != codeAPITokenRouteForbidden {
			t.Errorf("%s %s : %d %s", request.method, request.path, got.Code, got.Body.String())
		}
	}
}
