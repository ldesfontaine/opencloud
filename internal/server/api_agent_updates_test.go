package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ldesfontaine/opencloud/internal/agentwire"
	"github.com/ldesfontaine/opencloud/internal/audit"
	"github.com/ldesfontaine/opencloud/internal/server/servertest"
)

const proxyID = "22222222-3333-4444-8555-666666666666"

// updatesBench : deux machines en ligne et migrées, dont une à mettre à jour
// en dernier, et la version v0.0.3 importée sur le serveur.
func updatesBench(t *testing.T) *testServer {
	t.Helper()
	server := newTestServer(t)
	for _, machine := range []struct{ name, id string }{{"vps-paris-1", remoteID}, {"a-proxy", proxyID}} {
		enrolled, private := server.enroll(t, machine.name, machine.id)
		resp, _, _, cancel := openStream(t, server, enrolled.ID, private)
		t.Cleanup(func() { resp.Body.Close() })
		t.Cleanup(cancel)
	}
	server.publishRelease(t, "v0.0.3")("v0.0.3")
	return server
}

func TestAgentUpdates_ThePlanIsReadableByAReadTokenAndRecommendsAnOrder(t *testing.T) {
	server := updatesBench(t)
	// a-proxy porte le chemin des ordres : il passe en dernier.
	set := callAPI(server.Server, http.MethodPut, "/api/machines/"+proxyID+"/update-last", map[string]any{"update_last": true})
	if set.Code != http.StatusOK || !strings.Contains(set.Body.String(), `"update_last":true`) {
		t.Fatalf("update-last: %d %s", set.Code, set.Body.String())
	}
	bearer := server.issueAPIToken(t, "mcp-lecteur")
	got := callBearer(server.Server, "/api/agent-updates", bearer)
	if got.Code != http.StatusOK {
		t.Fatalf("plan: %d %s", got.Code, got.Body.String())
	}
	var plan agentUpdatePlanJSON
	if err := json.Unmarshal(got.Body.Bytes(), &plan); err != nil {
		t.Fatal(err)
	}
	if plan.Target != "v0.0.3" || plan.InProgress != nil || len(plan.Machines) != 3 {
		t.Fatalf("plan %+v", plan)
	}
	first, second, third := plan.Machines[0], plan.Machines[1], plan.Machines[2]
	if first.Name != "vps-paris-1" || first.Order == nil || *first.Order != 1 || second.Name != "a-proxy" || second.Order == nil || *second.Order != 2 || !second.UpdateLast {
		t.Fatalf("order %+v", plan.Machines)
	}
	if first.Version != "v0.0.2" || first.Target != "v0.0.3" || first.UpToDate || !first.RollbackCapable || !first.Online || first.BlockedReason != "" {
		t.Fatalf("first %+v", first)
	}
	// La machine openCloud, dont l'agent ne s'est pas connecté, n'a pas de rang : la raison est dite.
	if third.Name != "opencloud" || third.Order != nil || third.BlockedReason != "offline" || third.RollbackCapable {
		t.Fatalf("third %+v", third)
	}
	compareGolden(t, filepath.Join("testdata", "agent_updates.golden.json"), normalize(got.Body.String()))

	// Lire le plan n'a rien envoyé : le serveur ne met jamais plusieurs agents
	// à jour d'un coup.
	runs := callAPI(server.Server, http.MethodGet, "/api/actions", nil)
	if !strings.Contains(runs.Body.String(), `"runs":[]`) {
		t.Fatalf("reading the plan started something: %s", runs.Body.String())
	}
}

func TestAgentUpdates_TheMigrationLinesComeFromTheInterface(t *testing.T) {
	server := newTestServer(t)
	got := callAPI(server.Server, http.MethodGet, "/api/agent-migration", nil)
	if got.Code != http.StatusOK {
		t.Fatalf("%d %s", got.Code, got.Body.String())
	}
	compareGolden(t, filepath.Join("testdata", "agent_migration.golden.json"), normalize(got.Body.String()))
	// Les commandes ne sont pas pour un jeton : elles posent des fichiers à root.
	if refused := callBearer(server.Server, "/api/agent-migration", server.issueAPIToken(t, "mcp-lecteur")); refused.Code != http.StatusForbidden {
		t.Fatalf("a token read the migration: %d", refused.Code)
	}
}

func TestAgentUpdates_AnAgentThatDoesNotRollBackIsRefusedWithItsReason(t *testing.T) {
	server := newTestServer(t)
	enrolled, private := server.enroll(t, "vps-paris-1", remoteID)
	resp, cancel := servertest.OpenStreamWithoutRollback(t, server.Server, enrolled.ID, private)
	defer resp.Body.Close()
	defer cancel()
	server.publishRelease(t, "v0.0.3")("v0.0.3")

	machine := callAPI(server.Server, http.MethodGet, "/api/machines/"+remoteID, nil)
	if !strings.Contains(machine.Body.String(), `"rollback_capable":false`) {
		t.Fatalf("machine: %s", machine.Body.String())
	}
	offer := callAPI(server.Server, http.MethodGet, "/api/machines/"+remoteID+"/agent-update", nil)
	if !strings.Contains(offer.Body.String(), `"reason":"rollback_unavailable"`) || !strings.Contains(offer.Body.String(), `"available":false`) {
		t.Fatalf("offer: %s", offer.Body.String())
	}
	got := callAPI(server.Server, http.MethodPost, "/api/machines/"+remoteID+"/actions/update-agent", map[string]any{})
	if got.Code != http.StatusConflict || errorCode(t, got) != "action.rollback_unavailable" {
		t.Fatalf("update: %d %s", got.Code, got.Body.String())
	}
	got = callAPI(server.Server, http.MethodPost, "/api/machines/"+remoteID+"/actions/rollback-agent", map[string]any{})
	if got.Code != http.StatusConflict || errorCode(t, got) != "action.rollback_unavailable" {
		t.Fatalf("rollback: %d %s", got.Code, got.Body.String())
	}
	// Aucune option ne contourne : un corps, un paramètre ou un en-tête « force »
	// n'ont pas de sens pour cette route.
	got = callAPI(server.Server, http.MethodPost, "/api/machines/"+remoteID+"/actions/update-agent?force=1", map[string]any{"force": true})
	if got.Code != http.StatusConflict {
		t.Fatalf("forced update: %d %s", got.Code, got.Body.String())
	}
	plan := callAPI(server.Server, http.MethodGet, "/api/agent-updates", nil)
	if !strings.Contains(plan.Body.String(), `"blocked_reason":"rollback_unavailable"`) || !strings.Contains(plan.Body.String(), `"order":null`) {
		t.Fatalf("plan: %s", plan.Body.String())
	}
}

func TestAgentUpdates_AMigratedAgentHasAnOrderThatLeavesAndBlocksTheNextOne(t *testing.T) {
	server := updatesBench(t)
	first := callAPI(server.Server, http.MethodPost, "/api/machines/"+remoteID+"/actions/update-agent", map[string]any{})
	if first.Code != http.StatusCreated {
		t.Fatalf("update: %d %s", first.Code, first.Body.String())
	}
	second := callAPI(server.Server, http.MethodPost, "/api/machines/"+proxyID+"/actions/update-agent", map[string]any{})
	if second.Code != http.StatusConflict || errorCode(t, second) != "action.update_in_progress" {
		t.Fatalf("second update: %d %s", second.Code, second.Body.String())
	}
	back := callAPI(server.Server, http.MethodPost, "/api/machines/"+proxyID+"/actions/rollback-agent", map[string]any{})
	if back.Code != http.StatusConflict || errorCode(t, back) != "action.update_in_progress" {
		t.Fatalf("rollback while updating: %d %s", back.Code, back.Body.String())
	}
	plan := callAPI(server.Server, http.MethodGet, "/api/agent-updates", nil)
	var decoded agentUpdatePlanJSON
	if err := json.Unmarshal(plan.Body.Bytes(), &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.InProgress == nil || decoded.InProgress.Kind != "agent.update" || decoded.InProgress.MachineName != "vps-paris-1" {
		t.Fatalf("in progress %+v", decoded.InProgress)
	}
}

// Critère : un jeton qui porte agent.update met à jour un agent, une machine
// à la fois ; il ne porte pas ce qu'il n'a pas coché, et il ne peut ni signer
// ni importer une version (aucune route ne le fait).
func TestVerbs_AgentUpdateAndRollbackByAToken(t *testing.T) {
	server := updatesBench(t)
	carried, _ := server.issueWriteToken(t, "agent-ia", "agent.update")
	only, _ := server.issueWriteToken(t, "autre", "probe.pause")

	// Non porté.
	if got := callBearerWith(server.Server, http.MethodPost, "/api/machines/"+remoteID+"/actions/update-agent", only, ""); got.Code != http.StatusForbidden || errorCode(t, got) != codeAPITokenVerbForbidden {
		t.Fatalf("not carried: %d %s", got.Code, got.Body.String())
	}
	// Mauvaise route : agent.update ne porte pas le retour arrière.
	if got := callBearerWith(server.Server, http.MethodPost, "/api/machines/"+remoteID+"/actions/rollback-agent", carried, ""); got.Code != http.StatusForbidden || errorCode(t, got) != codeAPITokenVerbForbidden {
		t.Fatalf("wrong route: %d %s", got.Code, got.Body.String())
	}
	// Porté : la réponse est le contrat des verbes, avec l'ordre écrit.
	got := callBearerWith(server.Server, http.MethodPost, "/api/machines/"+remoteID+"/actions/update-agent", carried, "")
	if got.Code != http.StatusOK {
		t.Fatalf("carried: %d %s", got.Code, got.Body.String())
	}
	var reply verbReplyJSON
	if err := json.Unmarshal(got.Body.Bytes(), &reply); err != nil {
		t.Fatal(err)
	}
	if reply.Verb != "agent.update" || reply.RunID == nil || reply.State != "sent" || reply.ReverseWith != "agent.rollback" {
		t.Fatalf("reply %+v", reply)
	}
	// Une seule à la fois, aussi pour un jeton.
	again := callBearerWith(server.Server, http.MethodPost, "/api/machines/"+proxyID+"/actions/update-agent", carried, "")
	if again.Code != http.StatusConflict || errorCode(t, again) != "action.update_in_progress" {
		t.Fatalf("second by token: %d %s", again.Code, again.Body.String())
	}
	// L'audit dit qui : le jeton, pour qui, et de quelle version à quelle version.
	events, err := server.db.ListAuditEvents(context.Background(), audit.Query{Limit: 10, ObjectKind: audit.ObjectAction})
	if err != nil || len(events) != 1 {
		t.Fatalf("audit %+v %v", events, err)
	}
	event := events[0]
	// Le jeton qui a agi ne figure nulle part dans ce qui est écrit, ni dans la
	// réponse qu'il reçoit.
	for _, written := range []string{fmt.Sprintf("%+v", event), got.Body.String()} {
		if strings.Contains(written, carried) {
			t.Fatalf("the token itself was written: %s", written)
		}
	}
	if event.Actor.Kind != audit.ActorAPI || event.Actor.Label != "agent-ia" || event.Details["from"] != "v0.0.2" || event.Details["to"] != "v0.0.3" {
		t.Fatalf("audit %+v", event)
	}
}

func TestVerbs_AgentUpdateAndRollbackAreEachOthersReverse(t *testing.T) {
	server := newTestServer(t)
	bearer, _ := server.issueWriteToken(t, "agent-ia", "agent.update", "agent.rollback")
	got := callBearer(server.Server, "/api/token/verbs", bearer)
	if got.Code != http.StatusOK {
		t.Fatalf("%d %s", got.Code, got.Body.String())
	}
	var verbs tokenVerbsResponse
	if err := json.Unmarshal(got.Body.Bytes(), &verbs); err != nil {
		t.Fatal(err)
	}
	seen := map[string]tokenVerbJSON{}
	for _, v := range verbs.Verbs {
		seen[v.ID] = v
	}
	update, rollback := seen["agent.update"], seen["agent.rollback"]
	if update.Reverse != "agent.rollback" || rollback.Reverse != "agent.update" || update.Path != "/api/machines/{id}/actions/update-agent" ||
		rollback.Path != "/api/machines/{id}/actions/rollback-agent" || len(update.Params) != 1 || update.Params[0] != "id" || len(update.Body) != 0 {
		t.Fatalf("verbs %+v", seen)
	}
	// Les phrases disent ce qu'ils ne font jamais.
	for _, v := range []tokenVerbJSON{update, rollback} {
		if !strings.Contains(v.Summary, "Ne signe ni n'importe jamais une version") {
			t.Errorf("%s: %q", v.ID, v.Summary)
		}
	}
}

// Ni signer ni importer une version : aucune route de l'API ne le fait, et le
// registre n'en offre pas.
func TestVerbs_NoVerbSignsOrImportsARelease(t *testing.T) {
	for _, route := range newTestServer(t).routes() {
		lowered := strings.ToLower(route.pattern)
		if strings.HasPrefix(route.pattern, "/api/") && (strings.Contains(lowered, "release") || strings.Contains(lowered, "sign")) {
			t.Errorf("an API route touches releases: %s %s", route.method, route.pattern)
		}
	}
}

func TestUpdateLast_OnlyTheOperatorSetsItAndTheAuditSaysSo(t *testing.T) {
	server := updatesBench(t)
	full, _ := server.issueWriteToken(t, "plein", allVerbIDs()...)
	if got := callBearerWith(server.Server, http.MethodPut, "/api/machines/"+proxyID+"/update-last", full, ""); got.Code != http.StatusForbidden || errorCode(t, got) != codeAPITokenRouteForbidden {
		t.Fatalf("a token set the flag: %d %s", got.Code, got.Body.String())
	}
	if got := callAPI(server.Server, http.MethodPut, "/api/machines/nope/update-last", map[string]any{"update_last": true}); got.Code != http.StatusNotFound {
		t.Fatalf("unknown machine: %d %s", got.Code, got.Body.String())
	}
	got := callAPI(server.Server, http.MethodPut, "/api/machines/"+proxyID+"/update-last", map[string]any{"update_last": true})
	if got.Code != http.StatusOK {
		t.Fatalf("%d %s", got.Code, got.Body.String())
	}
	// Redire la même valeur ne journalise rien de plus.
	callAPI(server.Server, http.MethodPut, "/api/machines/"+proxyID+"/update-last", map[string]any{"update_last": true})
	events, err := server.db.ListAuditEvents(context.Background(), audit.Query{Limit: 10, ObjectKind: audit.ObjectMachine})
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, event := range events {
		if event.Action == audit.MachineUpdateLast {
			count++
			if event.ObjectID != proxyID || event.Details["update_last"] != "true" || event.Actor.Kind != audit.ActorOperator {
				t.Fatalf("event %+v", event)
			}
		}
	}
	if count != 1 {
		t.Fatalf("%d update_last events", count)
	}
	got = callAPI(server.Server, http.MethodPut, "/api/machines/"+proxyID+"/update-last", map[string]any{"update_last": false})
	if !strings.Contains(got.Body.String(), `"update_last":false`) {
		t.Fatalf("%s", got.Body.String())
	}
}

func TestRollbackRoute_ASuccessfulManualRollbackIsConfirmedByTheAgentReport(t *testing.T) {
	server := newTestServer(t)
	enrolled, private := server.enroll(t, "vps-paris-1", remoteID)
	resp, _, agentStream, cancel := openStream(t, server, enrolled.ID, private)
	defer resp.Body.Close()
	defer cancel()
	got := callAPI(server.Server, http.MethodPost, "/api/machines/"+remoteID+"/actions/rollback-agent", map[string]any{})
	if got.Code != http.StatusCreated {
		t.Fatalf("rollback: %d %s", got.Code, got.Body.String())
	}
	var sent actionRunJSON
	if err := json.Unmarshal(got.Body.Bytes(), &sent); err != nil {
		t.Fatal(err)
	}
	if sent.Kind != "agent.rollback" || sent.State != "sent" {
		t.Fatalf("run %+v", sent)
	}
	signed, err := agentwire.DecodeSignedOrder([]byte(readCommand(t, agentStream, agentwire.EventOrder)))
	if err != nil {
		t.Fatal(err)
	}
	var body agentwire.OrderBody
	if err := json.Unmarshal(signed.Body, &body); err != nil || body.Kind != agentwire.KindAgentRollback || body.RunID != sent.ID {
		t.Fatalf("order %+v %v", body, err)
	}
}
