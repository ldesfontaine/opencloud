package server

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ldesfontaine/opencloud/internal/apitoken"
	"github.com/ldesfontaine/opencloud/internal/audit"
	"github.com/ldesfontaine/opencloud/internal/verbrequest"
)

// callBearerBody est callBearerWith avec un corps JSON.
func callBearerBody(server *Server, method, path, bearer, body string, headers ...string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, path, bytes.NewBufferString(body))
	request.Header.Set("Sec-Fetch-Site", "same-origin")
	request.Header.Set("Authorization", "Bearer "+bearer)
	request.Header.Set("Content-Type", "application/json")
	for i := 0; i+1 < len(headers); i += 2 {
		request.Header.Add(headers[i], headers[i+1])
	}
	recorder := httptest.NewRecorder()
	server.ServeHTTP(recorder, request)
	return recorder
}

// askDeletion fait demander par un jeton la suppression d'une machine et rend
// la réponse du serveur.
func (ts *testServer) askDeletion(t *testing.T, bearer, machineID string) verbRequestReplyJSON {
	t.Helper()
	got := callBearerBody(ts.Server, http.MethodDelete, "/api/machines/"+machineID, bearer, "", onBehalfOfHeader, "alice-sub")
	if got.Code != http.StatusAccepted {
		t.Fatalf("ask: %d %s", got.Code, got.Body.String())
	}
	var reply verbRequestReplyJSON
	decodeAPI(t, got, &reply)
	return reply
}

// fileVerbRequest dépose, au nom du premier jeton, une demande de suppression
// pour la machine : de quoi figer la lecture des demandes.
func (ts *testServer) fileVerbRequest(t *testing.T, machineID string) {
	t.Helper()
	tokens, err := ts.apiTokens.List(context.Background())
	if err != nil || len(tokens) == 0 {
		t.Fatalf("tokens: %v %v", tokens, err)
	}
	_, err = ts.requests.Submit(context.Background(), verbrequest.Submission{
		Verb: "machine.delete", ObjectName: "vps-paris-1", Params: map[string]string{"id": machineID},
		TokenID: tokens[0].ID, TokenName: tokens[0].Name, OnBehalfOf: "alice-sub",
	})
	if err != nil {
		t.Fatal(err)
	}
}

func (ts *testServer) machineExists(t *testing.T, id string) bool {
	t.Helper()
	got := callAPI(ts.Server, http.MethodGet, "/api/machines/"+id, nil)
	return got.Code == http.StatusOK
}

func (ts *testServer) pendingRequests(t *testing.T) []verbRequestJSON {
	t.Helper()
	got := callAPI(ts.Server, http.MethodGet, "/api/verb-requests", nil)
	if got.Code != http.StatusOK {
		t.Fatalf("list: %d %s", got.Code, got.Body.String())
	}
	var response verbRequestsResponse
	decodeAPI(t, got, &response)
	return response.Requests
}

// Q1 : un jeton qui porte le verbe n'exécute jamais. Il reçoit 202 avec
// l'identifiant de la demande, la machine reste, et l'audit nomme le jeton et
// pour qui il appelle.
func TestVerbRequests_ATokenAsksAndNothingIsDeleted(t *testing.T) {
	server := newTestServer(t)
	server.enroll(t, "vps-paris-1", remoteID)
	bearer, token := server.issueWriteToken(t, "agent", "machine.delete")
	before := server.latestAuditID(t)

	reply := server.askDeletion(t, bearer, remoteID)

	if reply.Verb != "machine.delete" || reply.State != "awaiting_confirmation" || reply.RequestID == "" || !reply.ExpiresAt.After(testNow) {
		t.Fatalf("reply %+v", reply)
	}
	if !server.machineExists(t, remoteID) {
		t.Fatal("a token deleted a machine without a confirmation")
	}
	events := server.auditSince(t, before)
	if len(events) != 1 || events[0].Action != audit.VerbRequest {
		t.Fatalf("audit %+v, want a single verb.request", events)
	}
	event := events[0]
	if event.Actor.Kind != audit.ActorAPI || event.Actor.Name != token.ID || event.ObjectID != reply.RequestID ||
		event.Details["verb"] != "machine.delete" || event.Details["object_id"] != remoteID || event.Details["on_behalf_of"] != "alice-sub" {
		t.Fatalf("event %+v", event)
	}
	pending := server.pendingRequests(t)
	if len(pending) != 1 || pending[0].ID != reply.RequestID || pending[0].Object.Name != "vps-paris-1" ||
		pending[0].TokenName != "agent" || pending[0].OnBehalfOf != "alice-sub" {
		t.Fatalf("pending %+v", pending)
	}
}

// Un jeton qui ne porte pas le verbe est refusé comme pour tout verbe : pas de
// demande déposée.
func TestVerbRequests_ATokenWithoutTheVerbFilesNothing(t *testing.T) {
	server := newTestServer(t)
	server.enroll(t, "vps-paris-1", remoteID)
	bearer, _ := server.issueWriteToken(t, "muet", "probe.pause")
	got := callBearerBody(server.Server, http.MethodDelete, "/api/machines/"+remoteID, bearer, "")
	if got.Code != http.StatusForbidden || errorCode(t, got) != codeAPITokenVerbForbidden {
		t.Fatalf("%d %s", got.Code, got.Body.String())
	}
	if len(server.pendingRequests(t)) != 0 {
		t.Fatal("a request was filed without the verb")
	}
}

// Confirmer, refuser et lire les demandes sont des gestes d'opérateur : un
// jeton, même plein, les reçoit en 403, sa propre demande comprise.
func TestVerbRequests_ATokenCannotConfirmRejectOrRead(t *testing.T) {
	server := newTestServer(t)
	server.enroll(t, "vps-paris-1", remoteID)
	bearer, _ := server.issueWriteToken(t, "plein", allVerbIDs()...)
	reply := server.askDeletion(t, bearer, remoteID)
	for _, target := range []struct{ method, path string }{
		{http.MethodPost, "/api/verb-requests/" + reply.RequestID + "/actions/confirm"},
		{http.MethodPost, "/api/verb-requests/" + reply.RequestID + "/actions/reject"},
		{http.MethodGet, "/api/verb-requests"},
	} {
		got := callBearerBody(server.Server, target.method, target.path, bearer, "")
		if got.Code != http.StatusForbidden || errorCode(t, got) != codeAPITokenRouteForbidden {
			t.Errorf("%s %s: %d %s", target.method, target.path, got.Code, got.Body.String())
		}
	}
	if !server.machineExists(t, remoteID) || len(server.pendingRequests(t)) != 1 {
		t.Fatal("the request did not stay untouched")
	}
}

// Confirmer rejoue la route d'opérateur avec les paramètres gardés : la
// machine demandée part, pas une autre ; l'audit nomme l'opérateur, la demande
// et pour qui le jeton appelait. Une seconde confirmation est refusée.
func TestVerbRequests_ConfirmReplaysTheKeptParametersUnderTheOperator(t *testing.T) {
	server := newTestServer(t)
	server.enroll(t, "vps-paris-1", remoteID)
	const otherID = "99999999-2222-4333-8444-555555555555"
	server.enroll(t, "vps-lyon-2", otherID)
	bearer, _ := server.issueWriteToken(t, "agent", "machine.delete")
	reply := server.askDeletion(t, bearer, remoteID)
	before := server.latestAuditID(t)

	got := callAPI(server.Server, http.MethodPost, "/api/verb-requests/"+reply.RequestID+"/actions/confirm", nil)

	if got.Code != http.StatusNoContent {
		t.Fatalf("confirm: %d %s", got.Code, got.Body.String())
	}
	if server.machineExists(t, remoteID) || !server.machineExists(t, otherID) {
		t.Fatal("confirming did not delete exactly the requested machine")
	}
	var deletion *audit.Event
	for _, event := range server.auditSince(t, before) {
		if event.Action == audit.MachineDelete {
			deletion = &event
		}
	}
	if deletion == nil {
		t.Fatal("no machine.delete in the audit")
	}
	if deletion.Actor.Kind != audit.ActorOperator || deletion.ObjectID != remoteID ||
		deletion.Details["request_id"] != reply.RequestID || deletion.Details["on_behalf_of"] != "alice-sub" {
		t.Fatalf("event %+v", *deletion)
	}
	if again := callAPI(server.Server, http.MethodPost, "/api/verb-requests/"+reply.RequestID+"/actions/confirm", nil); again.Code != http.StatusConflict || errorCode(t, again) != "verb_request.already_decided" {
		t.Fatalf("second confirm: %d %s", again.Code, again.Body.String())
	}
	if len(server.pendingRequests(t)) != 0 {
		t.Fatal("a confirmed request still waits")
	}
}

// Refuser n'écrit rien d'autre que le refus : la machine reste, la demande
// quitte la liste, et elle ne se confirme plus.
func TestVerbRequests_RejectWritesNothingButTheRefusal(t *testing.T) {
	server := newTestServer(t)
	server.enroll(t, "vps-paris-1", remoteID)
	bearer, _ := server.issueWriteToken(t, "agent", "machine.delete")
	reply := server.askDeletion(t, bearer, remoteID)
	before := server.latestAuditID(t)

	got := callAPI(server.Server, http.MethodPost, "/api/verb-requests/"+reply.RequestID+"/actions/reject", nil)

	if got.Code != http.StatusNoContent {
		t.Fatalf("reject: %d %s", got.Code, got.Body.String())
	}
	if !server.machineExists(t, remoteID) {
		t.Fatal("rejecting deleted the machine")
	}
	events := server.auditSince(t, before)
	if len(events) != 1 || events[0].Action != audit.VerbReject || events[0].Actor.Kind != audit.ActorOperator || events[0].ObjectID != reply.RequestID {
		t.Fatalf("audit %+v, want a single verb.reject by the operator", events)
	}
	if len(server.pendingRequests(t)) != 0 {
		t.Fatal("a rejected request still waits")
	}
	if again := callAPI(server.Server, http.MethodPost, "/api/verb-requests/"+reply.RequestID+"/actions/confirm", nil); again.Code != http.StatusConflict {
		t.Fatalf("confirm after reject: %d", again.Code)
	}
}

// Passé le délai de quinze minutes, la demande est échue : elle ne se
// confirme plus, la machine reste, et l'échéance s'écrit au journal.
func TestVerbRequests_AnExpiredRequestIsRefusedAndAudited(t *testing.T) {
	server := newTestServer(t)
	server.enroll(t, "vps-paris-1", remoteID)
	bearer, _ := server.issueWriteToken(t, "agent", "machine.delete")
	reply := server.askDeletion(t, bearer, remoteID)
	server.requests.SetClock(func() time.Time { return testNow.Add(verbrequest.TTL + time.Second) })
	before := server.latestAuditID(t)

	got := callAPI(server.Server, http.MethodPost, "/api/verb-requests/"+reply.RequestID+"/actions/confirm", nil)

	if got.Code != http.StatusConflict || errorCode(t, got) != "verb_request.expired" {
		t.Fatalf("confirm: %d %s", got.Code, got.Body.String())
	}
	if !server.machineExists(t, remoteID) {
		t.Fatal("an expired request deleted the machine")
	}
	events := server.auditSince(t, before)
	if len(events) != 1 || events[0].Action != audit.VerbExpire || events[0].Actor.Kind != audit.ActorRule || events[0].ObjectID != reply.RequestID {
		t.Fatalf("audit %+v, want a single verb.expire by a rule", events)
	}
	if len(server.pendingRequests(t)) != 0 {
		t.Fatal("an expired request still waits")
	}
}

// Une demande à confirmer qui échoue au rejeu (la machine porte un lien) rend
// à l'opérateur le refus de la route, avec son plan, et ne se rejoue pas.
func TestVerbRequests_AFailingReplayShowsTheRefusalOfTheRoute(t *testing.T) {
	server := newTestServer(t)
	server.enroll(t, "vps-paris-1", remoteID)
	server.createProbeVia(t, probeRequest{Name: "site", Kind: "http", Target: "https://a.fr/", MachineID: remoteID})
	bearer, _ := server.issueWriteToken(t, "agent", "machine.delete")
	reply := server.askDeletion(t, bearer, remoteID)

	got := callAPI(server.Server, http.MethodPost, "/api/verb-requests/"+reply.RequestID+"/actions/confirm", nil)

	if got.Code != http.StatusConflict || !strings.Contains(got.Body.String(), `"plan"`) {
		t.Fatalf("confirm: %d %s", got.Code, got.Body.String())
	}
	if !server.machineExists(t, remoteID) {
		t.Fatal("a refused replay deleted the machine")
	}
	if again := callAPI(server.Server, http.MethodPost, "/api/verb-requests/"+reply.RequestID+"/actions/confirm", nil); again.Code != http.StatusConflict || errorCode(t, again) != "verb_request.already_decided" {
		t.Fatalf("a confirmed request replayed: %d %s", again.Code, again.Body.String())
	}
}

// Le même jeton qui redemande la même chose retrouve sa demande ; une machine
// inconnue n'en dépose aucune ; un champ que le verbe ne déclare pas est refusé.
func TestVerbRequests_RepeatedUnknownAndMalformedRequests(t *testing.T) {
	server := newTestServer(t)
	server.enroll(t, "vps-paris-1", remoteID)
	bearer, _ := server.issueWriteToken(t, "agent", "machine.delete")
	first := server.askDeletion(t, bearer, remoteID)
	second := server.askDeletion(t, bearer, remoteID)
	if first.RequestID != second.RequestID || len(server.pendingRequests(t)) != 1 {
		t.Fatalf("repeated: %s then %s", first.RequestID, second.RequestID)
	}
	events := server.auditSince(t, 0)
	requested := 0
	for _, event := range events {
		if event.Action == audit.VerbRequest {
			requested++
		}
	}
	if requested != 1 {
		t.Fatalf("%d verb.request events for one request", requested)
	}
	if got := callBearerBody(server.Server, http.MethodDelete, "/api/machines/inconnue", bearer, ""); got.Code != http.StatusNotFound {
		t.Fatalf("unknown machine: %d", got.Code)
	}
	for name, body := range map[string]string{"unknown field": `{"keep_history":true}`, "not an object": `[1]`, "not json": `oups`} {
		if got := callBearerBody(server.Server, http.MethodDelete, "/api/machines/"+remoteID, bearer, body); got.Code != http.StatusBadRequest || errorCode(t, got) != codeBadJSON {
			t.Errorf("%s: %d %s", name, got.Code, got.Body.String())
		}
	}
	if len(server.pendingRequests(t)) != 1 {
		t.Fatal("a refused request was filed")
	}
}

// Révoquer le jeton emporte ses demandes : un jeton volé puis révoqué ne
// laisse rien à confirmer.
func TestVerbRequests_RevokingTheTokenDropsItsRequests(t *testing.T) {
	server := newTestServer(t)
	server.enroll(t, "vps-paris-1", remoteID)
	bearer, token := server.issueWriteToken(t, "agent", "machine.delete")
	reply := server.askDeletion(t, bearer, remoteID)
	if got := callAPI(server.Server, http.MethodDelete, "/api/settings/api-tokens/"+token.ID, nil); got.Code != http.StatusNoContent {
		t.Fatalf("revoke: %d %s", got.Code, got.Body.String())
	}
	if len(server.pendingRequests(t)) != 0 {
		t.Fatal("a revoked token left a request")
	}
	got := callAPI(server.Server, http.MethodPost, "/api/verb-requests/"+reply.RequestID+"/actions/confirm", nil)
	if got.Code != http.StatusNotFound || !server.machineExists(t, remoteID) {
		t.Fatalf("confirm: %d, machine kept: %v", got.Code, server.machineExists(t, remoteID))
	}
}

// Le bouton de l'écran reste direct : l'opérateur qui supprime une machine
// n'attend la confirmation de personne.
func TestVerbRequests_TheOperatorDeletesDirectly(t *testing.T) {
	server := newTestServer(t)
	server.enroll(t, "vps-paris-1", remoteID)
	if got := callAPI(server.Server, http.MethodDelete, "/api/machines/"+remoteID, nil); got.Code != http.StatusNoContent {
		t.Fatalf("delete: %d %s", got.Code, got.Body.String())
	}
	if len(server.pendingRequests(t)) != 0 {
		t.Fatal("an operator deletion filed a request")
	}
}

// Le registre le dit : le verbe est offert au jeton comme verbe à
// confirmation, sans inverse.
func TestVerbRequests_TheTokenReadsThatTheVerbNeedsAConfirmation(t *testing.T) {
	server := newTestServer(t)
	bearer, _ := server.issueWriteToken(t, "agent", "machine.delete", "probe.pause")
	got := callBearerWith(server.Server, http.MethodGet, "/api/token/verbs", bearer, "")
	var reply tokenVerbsResponse
	decodeAPI(t, got, &reply)
	found := map[string]tokenVerbJSON{}
	for _, offered := range reply.Verbs {
		found[offered.ID] = offered
	}
	deletion := found["machine.delete"]
	if !deletion.Confirm || deletion.Method != http.MethodDelete || deletion.Path != "/api/machines/{id}" || deletion.Reverse != "" || !strings.Contains(deletion.Summary, "15") {
		t.Fatalf("machine.delete: %+v", deletion)
	}
	if found["probe.pause"].Confirm {
		t.Fatal("probe.pause asks for a confirmation")
	}
}

// La portée d'un jeton vaut aussi pour la demande : un jeton limité à une
// autre machine n'en dépose aucune sur celle-ci.
func TestVerbRequests_AReachLimitedTokenCannotAskForAnotherMachine(t *testing.T) {
	server := newTestServer(t)
	server.enroll(t, "vps-paris-1", remoteID)
	cleartext, _, err := server.apiTokens.Create(context.Background(), apitoken.Request{
		Name: "limité", Scope: apitoken.ScopeWrite, Verbs: []string{"machine.delete"},
		Reach: apitoken.Reach{OnlyMachines: true, Machines: []string{"local"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	got := callBearerBody(server.Server, http.MethodDelete, "/api/machines/"+remoteID, cleartext, "")
	if got.Code != http.StatusForbidden || errorCode(t, got) != codeAPITokenOutOfScope {
		t.Fatalf("%d %s", got.Code, got.Body.String())
	}
	if len(server.pendingRequests(t)) != 0 {
		t.Fatal("a request was filed out of scope")
	}
}
