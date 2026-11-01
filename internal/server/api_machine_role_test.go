package server

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"net/http"
	"slices"
	"testing"

	"github.com/ldesfontaine/opencloud/internal/audit"
	"github.com/ldesfontaine/opencloud/internal/machine"
)

// Le rôle se choisit à l'ajout : la route le porte jusqu'au jeton, l'enrôlement
// l'inscrit sur la machine, et la machine dit les modules de son rôle. Un rôle
// hors liste se refuse avant tout jeton ; absent, c'est le rôle générique.
func TestAPI_MachineRole_ChosenAtTheTokenAndSaidByTheMachine(t *testing.T) {
	server := newTestServer(t)
	for _, chosen := range []struct {
		body map[string]any
		role machine.Role
	}{
		{map[string]any{"name": "pve-1", "role": "proxmox"}, machine.RoleProxmox},
		{map[string]any{"name": "plain-1"}, machine.RoleGeneric},
	} {
		if got := callAPI(server.Server, http.MethodPost, "/api/machines/tokens", chosen.body); got.Code != http.StatusCreated {
			t.Fatalf("%v : %d %s", chosen.body, got.Code, got.Body.String())
		}
		pending, err := server.machines.PendingTokens(context.Background())
		if err != nil || !slices.ContainsFunc(pending, func(token machine.Token) bool { return token.Name == chosen.body["name"] && token.Role == chosen.role }) {
			t.Fatalf("le jeton de %v ne porte pas %s : %+v %v", chosen.body, chosen.role, pending, err)
		}
	}
	refused := callAPI(server.Server, http.MethodPost, "/api/machines/tokens", map[string]any{"name": "x-1", "role": "kubernetes"})
	if refused.Code != http.StatusUnprocessableEntity || errorCode(t, refused) != "machine.role_unknown" {
		t.Fatalf("rôle inconnu : %d %s", refused.Code, refused.Body.String())
	}

	cleartext, _, err := server.machines.CreateToken(context.Background(), "pve-2", machine.RoleProxmox, nil)
	if err != nil {
		t.Fatal(err)
	}
	public, _, _ := ed25519.GenerateKey(rand.Reader)
	enrolled, err := server.machines.Enroll(context.Background(), machine.Enrollment{
		MachineID: remoteID, PublicKey: public, Token: cleartext, Hostname: "pve-2", Address: "192.0.2.9", OS: "Debian 12", Arch: "amd64", AgentVersion: "v0.0.1",
	})
	if err != nil || enrolled.Role != machine.RoleProxmox {
		t.Fatalf("enrôlement : %+v %v", enrolled, err)
	}
	var response machineJSON
	decodeAPI(t, callAPI(server.Server, http.MethodGet, "/api/machines/"+remoteID, nil), &response)
	if response.Role != "proxmox" || !slices.Equal(response.Modules, []string{"hypervisor"}) {
		t.Fatalf("la fiche dit %q %v", response.Role, response.Modules)
	}
}

// Changer le rôle change les modules, pas les droits de l'agent ; l'opérateur
// seul le fait, le journal le garde, et redire le même rôle n'écrit rien.
func TestAPI_MachineRole_OnlyTheOperatorChangesItAndTheAuditSaysSo(t *testing.T) {
	server := newTestServer(t)
	server.enroll(t, "vps-paris-1", remoteID)
	full, _ := server.issueWriteToken(t, "plein", allVerbIDs()...)
	path := "/api/machines/" + remoteID + "/role"
	if got := callBearerWith(server.Server, http.MethodPut, path, full, ""); got.Code != http.StatusForbidden || errorCode(t, got) != codeAPITokenRouteForbidden {
		t.Fatalf("un jeton a changé le rôle : %d %s", got.Code, got.Body.String())
	}
	if got := callAPI(server.Server, http.MethodPut, "/api/machines/nope/role", map[string]any{"role": "docker"}); got.Code != http.StatusNotFound {
		t.Fatalf("machine inconnue : %d", got.Code)
	}
	if got := callAPI(server.Server, http.MethodPut, path, map[string]any{"role": "kubernetes"}); got.Code != http.StatusUnprocessableEntity || errorCode(t, got) != "machine.role_unknown" {
		t.Fatalf("rôle inconnu : %d %s", got.Code, got.Body.String())
	}
	got := callAPI(server.Server, http.MethodPut, path, map[string]any{"role": "docker"})
	var response machineJSON
	decodeAPI(t, got, &response)
	if got.Code != http.StatusOK || response.Role != "docker" || !slices.Contains(response.Modules, "containers") {
		t.Fatalf("%d %+v", got.Code, response)
	}
	if len(response.Agent.Requested) != 1 {
		t.Fatalf("les droits de l'agent ont changé : %+v", response.Agent.Requested)
	}
	callAPI(server.Server, http.MethodPut, path, map[string]any{"role": "docker"})

	events, err := server.db.ListAuditEvents(context.Background(), audit.Query{Limit: 10, ObjectKind: audit.ObjectMachine})
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, event := range events {
		if event.Action == audit.MachineRole {
			count++
			if event.ObjectID != remoteID || event.Details["role"] != "docker" || event.Details["previous"] != "generic" || event.Actor.Kind != audit.ActorOperator {
				t.Fatalf("événement %+v", event)
			}
		}
	}
	if count != 1 {
		t.Fatalf("%d événements de rôle", count)
	}
}

// La session dit les rôles et leur préréglage de droits : l'ajout d'une
// machine les propose sans les redéfinir.
func TestAPI_Session_SaysTheRolesAndTheirRights(t *testing.T) {
	server := newTestServer(t)
	var session sessionResponse
	decodeAPI(t, callAPI(server.Server, http.MethodGet, "/api/session", nil), &session)
	if len(session.Roles) != len(machine.Roles()) {
		t.Fatalf("%d rôles", len(session.Roles))
	}
	for _, offered := range session.Roles {
		if offered.ID == "proxmox" && !slices.Contains(offered.Capabilities, machine.CapabilityProxmoxPower) {
			t.Fatalf("le rôle proxmox ne coche pas proxmox.power : %v", offered.Capabilities)
		}
	}
}
