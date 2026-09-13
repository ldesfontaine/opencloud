package server

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

func TestAPI_CreateMachineToken_ShowsTheTokenOnceAndListsItAsPending(t *testing.T) {
	server := newTestServer(t)
	recorder := callAPI(server.Server, http.MethodPost, "/api/machines/tokens", tokenRequest{Name: "vps-paris-1"})
	if recorder.Code != http.StatusCreated {
		t.Fatalf("status %d: %s", recorder.Code, recorder.Body.String())
	}
	var response tokenResponse
	decodeAPI(t, recorder, &response)
	if !tokenValue.MatchString(response.Token) || !strings.HasPrefix(response.Command, "sudo opencloud agent -server http://example.com -token oc_") || response.URLLocal {
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

func TestAPI_RemoveMachine_ForgetsItAndKeepsTheLocalOne(t *testing.T) {
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
	got := callAPI(server.Server, http.MethodDelete, "/api/machines/local", nil)
	if got.Code != http.StatusForbidden || errorCode(t, got) != "machine.local_kept" {
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
	if got := callAPI(server.Server, http.MethodPost, "/api/machines/local/actions/reenroll", nil); got.Code != http.StatusForbidden {
		t.Fatalf("reenroll local: %d", got.Code)
	}
	if got := callAPI(server.Server, http.MethodPost, "/api/machines/nope/actions/reenroll", nil); got.Code != http.StatusNotFound {
		t.Fatalf("reenroll unknown: %d", got.Code)
	}
}
