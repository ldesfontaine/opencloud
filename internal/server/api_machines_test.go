package server

import (
	"context"
	"net/http"
	"regexp"
	"strings"
	"testing"

	"github.com/ldesfontaine/opencloud/internal/lang"
	"github.com/ldesfontaine/opencloud/internal/machine"
)

// Le jeton d'enrôlement est lisible de tous les comptes de la machine dès
// qu'il est dans les arguments d'un processus : la commande ne le met qu'après
// printf, un builtin qui ne crée pas de processus, et sudo ne reçoit rien.
func TestInstallCommand_KeepsTheTokenOutOfEveryExecutedArgv(t *testing.T) {
	safe := regexp.MustCompile(`^oc_[a-z0-9_]+$`)
	for range 200 {
		token, _, err := machine.NewToken("vps-paris-1", "")
		if err != nil {
			t.Fatal(err)
		}
		// Les guillemets simples ne se cassent que sur une apostrophe ; le
		// jeton n'a que des minuscules, des chiffres et « _ ».
		if !safe.MatchString(token) {
			t.Fatalf("token %q has characters that need escaping", token)
		}
		for _, code := range []lang.Code{lang.French, lang.English} {
			command := installCommand("https://opencloud.example.org", token, code, nil)
			builtin, executed, piped := strings.Cut(command, " | ")
			if !piped || builtin != "printf '%s\\n' '"+token+"'" {
				t.Fatalf("the token must only follow printf: %q", command)
			}
			if strings.Contains(executed, token) || strings.Contains(executed, "-token") || !strings.HasPrefix(executed, "sudo opencloud agent -server https://opencloud.example.org") {
				t.Fatalf("the executed command must carry no token: %q", executed)
			}
		}
	}
	if command := installCommand("https://x", "oc_abc", lang.English, nil); !strings.HasSuffix(command, " -lang en") {
		t.Fatalf("language missing: %q", command)
	}
}

// Les droits cochés passent dans la ligne, lisibles et sans guillemets ;
// observe seul ne s'écrit pas, c'est le défaut de l'agent.
func TestInstallCommand_CarriesTheCheckedRights(t *testing.T) {
	observeOnly := []machine.Capability{{Name: machine.CapabilityObserve}}
	if command := installCommand("https://x", "oc_abc", lang.French, observeOnly); strings.Contains(command, "-rights") {
		t.Fatalf("observe alone must not be written: %q", command)
	}
	checked, err := machine.NormalizeCapabilities([]machine.Capability{
		{Name: machine.CapabilityFilesWrite, Paths: []string{"/srv/workspace", "/opt/site"}},
		{Name: machine.CapabilityAgentUpdate},
	})
	if err != nil {
		t.Fatal(err)
	}
	command := installCommand("https://x", "oc_abc", lang.French, checked)
	if !strings.HasSuffix(command, " -rights observe,agent.update,files.write=/opt/site:/srv/workspace") {
		t.Fatalf("rights missing or misplaced: %q", command)
	}
	// Le droit coché par défaut dans le formulaire s'écrit dans la ligne : seul
	// observe seul s'omet.
	local, err := machine.ParseCapabilities("probe.lan")
	if err != nil {
		t.Fatal(err)
	}
	if command := installCommand("https://x", "oc_abc", lang.French, local); !strings.HasSuffix(command, " -rights observe,probe.lan") {
		t.Fatalf("probe.lan missing: %q", command)
	}
}

func TestAPI_CreateMachineToken_ShowsTheTokenOnceAndListsItAsPending(t *testing.T) {
	server := newTestServer(t)
	recorder := callAPI(server.Server, http.MethodPost, "/api/machines/tokens", tokenRequest{Name: "vps-paris-1"})
	if recorder.Code != http.StatusCreated {
		t.Fatalf("status %d: %s", recorder.Code, recorder.Body.String())
	}
	var response tokenResponse
	decodeAPI(t, recorder, &response)
	if !tokenValue.MatchString(response.Token) || response.Command != "printf '%s\\n' '"+response.Token+"' | sudo opencloud agent -server http://example.com" || response.URLLocal {
		t.Fatalf("token response %+v", response)
	}
	pending, err := server.machines.PendingTokens(context.Background())
	if err != nil || len(pending) != 1 || pending[0].Name != "vps-paris-1" {
		t.Fatalf("pending %+v %v", pending, err)
	}
	list := callAPI(server.Server, http.MethodGet, "/api/machines", nil)
	if !strings.Contains(list.Body.String(), pending[0].Masked()) || tokenValue.MatchString(list.Body.String()) {
		t.Fatal("the list must show the masked token and never the cleartext")
	}

	if got := callAPI(server.Server, http.MethodDelete, "/api/machines/tokens/"+pending[0].ID, nil); got.Code != http.StatusNoContent {
		t.Fatalf("cancel: %d", got.Code)
	}
	if got := callAPI(server.Server, http.MethodDelete, "/api/machines/tokens/"+pending[0].ID, nil); got.Code != http.StatusNoContent {
		t.Fatalf("cancel again: %d", got.Code)
	}
	if pending, _ := server.machines.PendingTokens(context.Background()); len(pending) != 0 {
		t.Fatalf("still pending %+v", pending)
	}
}

func TestAPI_CreateMachineToken_RefusesABadNameWithItsKey(t *testing.T) {
	server := newTestServer(t)
	got := callAPI(server.Server, http.MethodPost, "/api/machines/tokens", tokenRequest{Name: "VPS Paris"})
	if got.Code != http.StatusUnprocessableEntity || errorCode(t, got) != "machine.name_invalid" {
		t.Fatalf("%d %s", got.Code, got.Body.String())
	}
	if got := callAPI(server.Server, http.MethodPost, "/api/machines/tokens", map[string]any{"name": "x", "extra": 1}); got.Code != http.StatusBadRequest || errorCode(t, got) != codeBadJSON {
		t.Fatalf("unknown field: %d %s", got.Code, got.Body.String())
	}
}

func TestAPI_RemoveMachine_ForgetsItTheOpenCloudOneToo(t *testing.T) {
	server := newTestServer(t)
	server.enroll(t, "vps-paris-1", remoteID)
	if got := callAPI(server.Server, http.MethodDelete, "/api/machines/"+remoteID, nil); got.Code != http.StatusNoContent {
		t.Fatalf("remove: %d %s", got.Code, got.Body.String())
	}
	if got := callAPI(server.Server, http.MethodGet, "/api/machines/"+remoteID, nil); got.Code != http.StatusNotFound {
		t.Fatalf("after remove: %d", got.Code)
	}
	if got := callAPI(server.Server, http.MethodDelete, "/api/machines/"+remoteID, nil); got.Code != http.StatusNotFound {
		t.Fatalf("remove again: %d", got.Code)
	}
	if got := callAPI(server.Server, http.MethodDelete, "/api/machines/local", nil); got.Code != http.StatusNoContent {
		t.Fatalf("remove local: %d %s", got.Code, got.Body.String())
	}
}

func TestAPI_Reenroll_IssuesATokenForTheSameMachine(t *testing.T) {
	server := newTestServer(t)
	server.enroll(t, "vps-paris-1", remoteID)
	recorder := callAPI(server.Server, http.MethodPost, "/api/machines/"+remoteID+"/actions/reenroll", nil)
	if recorder.Code != http.StatusCreated {
		t.Fatalf("status %d: %s", recorder.Code, recorder.Body.String())
	}
	var response tokenResponse
	decodeAPI(t, recorder, &response)
	if response.Name != "vps-paris-1" || !tokenValue.MatchString(response.Token) {
		t.Fatalf("token response %+v", response)
	}
	pending, _ := server.machines.PendingTokens(context.Background())
	if len(pending) != 1 || pending[0].MachineID != remoteID {
		t.Fatalf("pending %+v", pending)
	}
	if got := callAPI(server.Server, http.MethodPost, "/api/machines/local/actions/reenroll", nil); got.Code != http.StatusCreated {
		t.Fatalf("reenroll local: %d", got.Code)
	}
	if got := callAPI(server.Server, http.MethodPost, "/api/machines/nope/actions/reenroll", nil); got.Code != http.StatusNotFound {
		t.Fatalf("reenroll unknown: %d", got.Code)
	}
}

// Changer les droits d'un agent, c'est le ré-enrôler avec d'autres droits
// (décision 10) : la ligne les porte, le jeton les garde ; sans droits dans
// la demande, ceux d'avant. Un droit faux se refuse avant tout jeton.
func TestAPI_Reenroll_ChangesTheRightsOfTheAgent(t *testing.T) {
	server := newTestServer(t)
	server.enroll(t, "vps-paris-1", remoteID)
	path := "/api/machines/" + remoteID + "/actions/reenroll"
	recorder := callAPI(server.Server, http.MethodPost, path, map[string]any{"capabilities": []map[string]any{{"name": "agent.update"}}})
	var response tokenResponse
	decodeAPI(t, recorder, &response)
	if recorder.Code != http.StatusCreated || !strings.HasSuffix(response.Command, " -rights observe,agent.update") {
		t.Fatalf("%d %+v", recorder.Code, response)
	}
	pending, _ := server.machines.PendingTokens(context.Background())
	if len(pending) != 1 || machine.FormatCapabilities(pending[0].Capabilities) != "observe,agent.update" {
		t.Fatalf("pending %+v", pending)
	}
	kept := callAPI(server.Server, http.MethodPost, path, map[string]any{})
	decodeAPI(t, kept, &response)
	if kept.Code != http.StatusCreated || strings.Contains(response.Command, "-rights") {
		t.Fatalf("without rights: %d %+v", kept.Code, response)
	}
	refused := callAPI(server.Server, http.MethodPost, path, map[string]any{"capabilities": []map[string]any{{"name": "shell.exec"}}})
	if refused.Code != http.StatusUnprocessableEntity || errorCode(t, refused) != "machine.capability_unknown" {
		t.Fatalf("unknown right: %d %s", refused.Code, refused.Body.String())
	}
}

// Les droits cochés passent du formulaire à la ligne d'installation ; un
// droit inconnu ou un chemin faux se refuse avec sa clé.
func TestAPI_CreateMachineToken_PutsTheCheckedRightsInTheCommand(t *testing.T) {
	server := newTestServer(t)
	request := tokenRequest{Name: "vps-paris-1", Capabilities: []capabilityJSON{
		{Name: machine.CapabilityBackupReceive, Paths: []string{"/srv/backups"}}, {Name: machine.CapabilityDockerUpdate},
	}}
	recorder := callAPI(server.Server, http.MethodPost, "/api/machines/tokens", request)
	if recorder.Code != http.StatusCreated {
		t.Fatalf("status %d: %s", recorder.Code, recorder.Body.String())
	}
	var response tokenResponse
	decodeAPI(t, recorder, &response)
	if !strings.HasSuffix(response.Command, " -rights observe,docker.update,backup.receive=/srv/backups") {
		t.Fatalf("command %q", response.Command)
	}
	for _, refused := range []struct {
		capability capabilityJSON
		key        string
	}{
		{capabilityJSON{Name: "shell.exec"}, "machine.capability_unknown"},
		{capabilityJSON{Name: machine.CapabilityFilesWrite, Paths: []string{"relative"}}, "machine.capability_path"},
		{capabilityJSON{Name: machine.CapabilityBackupReceive}, "machine.capability_scope"},
	} {
		got := callAPI(server.Server, http.MethodPost, "/api/machines/tokens", tokenRequest{Name: "vps-lyon-2", Capabilities: []capabilityJSON{refused.capability}})
		if got.Code != http.StatusUnprocessableEntity || errorCode(t, got) != refused.key {
			t.Errorf("%+v: %d %s", refused.capability, got.Code, got.Body.String())
		}
	}
}

// Révoquer garde la machine : 204, la fiche dit l'agent révoqué ; une
// seconde fois, 409 machine.no_agent ; la machine openCloud ne se révoque
// pas.
func TestAPI_RevokeAgent_KeepsTheMachineAndSaysTheAgentIsRevoked(t *testing.T) {
	server := newTestServer(t)
	server.enroll(t, "vps-paris-1", remoteID)
	if got := callAPI(server.Server, http.MethodPost, "/api/machines/"+remoteID+"/actions/revoke", nil); got.Code != http.StatusNoContent {
		t.Fatalf("revoke: %d %s", got.Code, got.Body.String())
	}
	var revoked machineJSON
	decodeAPI(t, callAPI(server.Server, http.MethodGet, "/api/machines/"+remoteID, nil), &revoked)
	if revoked.Agent == nil || revoked.Agent.RevokedAt == nil {
		t.Fatalf("machine after revoke %+v", revoked)
	}
	if got := callAPI(server.Server, http.MethodPost, "/api/machines/"+remoteID+"/actions/revoke", nil); got.Code != http.StatusConflict || errorCode(t, got) != "machine.no_agent" {
		t.Fatalf("revoke twice: %d %s", got.Code, got.Body.String())
	}
	if got := callAPI(server.Server, http.MethodPost, "/api/machines/nobody/actions/revoke", nil); got.Code != http.StatusNotFound {
		t.Fatalf("unknown machine: %d", got.Code)
	}
	if got := callAPI(server.Server, http.MethodPost, "/api/machines/local/actions/revoke", nil); got.Code != http.StatusNoContent {
		t.Fatalf("revoke local: %d %s", got.Code, got.Body.String())
	}
}

// La ligne de l'agent de la machine openCloud s'offre tant qu'elle n'a
// jamais eu d'agent : la même tant qu'elle attend, avec observe et probe.lan ; 409 une
// fois qu'un agent l'a eue.
func TestAPI_OfferLocalEnrollment_UntilTheOpenCloudMachineHasAnAgent(t *testing.T) {
	server := newTestServer(t)
	if got := callAPI(server.Server, http.MethodPost, "/api/machines/local-enrollment", nil); got.Code != http.StatusConflict || errorCode(t, got) != "machine.local_enrolled" {
		t.Fatalf("with its agent: %d %s", got.Code, got.Body.String())
	}
	// Retirée, la machine openCloud n'a plus d'agent : la ligne s'offre de
	// nouveau, au nom du nom d'hôte.
	if got := callAPI(server.Server, http.MethodDelete, "/api/machines/"+machine.LocalID, nil); got.Code != http.StatusNoContent {
		t.Fatalf("remove: %d %s", got.Code, got.Body.String())
	}
	var first, second tokenResponse
	decodeAPI(t, callAPI(server.Server, http.MethodPost, "/api/machines/local-enrollment", nil), &first)
	decodeAPI(t, callAPI(server.Server, http.MethodPost, "/api/machines/local-enrollment", nil), &second)
	if first.Name != "opencloud-host" || !tokenValue.MatchString(first.Token) || !strings.HasSuffix(first.Command, " -rights observe,probe.lan") || first.Command != second.Command {
		t.Fatalf("offers %+v %+v", first, second)
	}
}
