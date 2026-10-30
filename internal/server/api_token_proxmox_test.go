package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ldesfontaine/opencloud/internal/action"
	"github.com/ldesfontaine/opencloud/internal/agentwire"
	"github.com/ldesfontaine/opencloud/internal/audit"
	"github.com/ldesfontaine/opencloud/internal/machine"
	"github.com/ldesfontaine/opencloud/internal/verb"
)

var proxmoxVerbIDs = []string{"proxmox.vm.start", "proxmox.vm.shutdown", "proxmox.vm.snapshot", "proxmox.vm.snapshot_delete"}

// Les invités du faux hôte : 200 est arrêté, 5012 en marche.
const (
	stoppedVMID = "200"
	runningVMID = "5012"
)

// proxmoxTokenBench déclare l'hôte du faux Proxmox et ouvre le flux de son
// agent : le relevé donne au serveur l'état des invités, sur lequel il juge,
// et l'agent en ligne reçoit l'ordre écrit.
func proxmoxTokenBench(t *testing.T) *testServer {
	t.Helper()
	server, api, _, hostKey := proxmoxBenchWithKey(t)
	if got := putProxmox(server, pveID, api.request(pveTokenSecret)); got.Code != http.StatusOK {
		t.Fatalf("declare: %d %s", got.Code, got.Body.String())
	}
	resp, _, _, cancel := openStream(t, server, pveID, hostKey)
	t.Cleanup(func() { cancel(); resp.Body.Close() })
	return server
}

// postGuestVerb appelle la route d'un geste avec un Bearer et un corps.
func postGuestVerb(server *testServer, bearer, vmid, gesture, body string, headers ...string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodPost, "/api/machines/"+pveID+"/proxmox/guests/"+vmid+"/actions/"+gesture, strings.NewReader(body))
	request.Header.Set("Authorization", "Bearer "+bearer)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Sec-Fetch-Site", "same-origin")
	for i := 0; i+1 < len(headers); i += 2 {
		request.Header.Add(headers[i], headers[i+1])
	}
	recorder := httptest.NewRecorder()
	server.ServeHTTP(recorder, request)
	return recorder
}

// Critère : un jeton qui porte le verbe démarre une VM de l'hôte, l'audit dit
// l'acteur et pour qui, la réponse suit le contrat des verbes, avec l'ordre
// écrit pour l'agent.
func TestProxmoxVerbs_AStartByATokenIsAuditedAndShapedAsAVerb(t *testing.T) {
	server := proxmoxTokenBench(t)
	bearer, token := server.issueWriteToken(t, "agent", "proxmox.vm.start")
	before := server.latestAuditID(t)

	got := postGuestVerb(server, bearer, stoppedVMID, "start", "{}", onBehalfOfHeader, "alice-sub")
	if got.Code != http.StatusOK {
		t.Fatalf("start: %d %s", got.Code, got.Body.String())
	}
	reply := replyOf(t, got)
	if reply.Verb != "proxmox.vm.start" || reply.RunID == nil || *reply.RunID == "" || reply.State == "" || reply.ReverseWith != "proxmox.vm.shutdown" {
		t.Fatalf("reply %+v", reply)
	}
	runs, err := server.db.ListActionRuns(context.Background(), action.Query{MachineID: pveID, Limit: 10})
	if err != nil || len(runs) != 1 || runs[0].ID != *reply.RunID || runs[0].Kind != agentwire.KindProxmoxStart {
		t.Fatalf("runs %+v %v", runs, err)
	}
	var order, gesture *audit.Event
	events := server.auditSince(t, before)
	for index := range events {
		switch events[index].Action {
		case audit.ActionOrder:
			order = &events[index]
		case audit.MachineProxmoxGuest:
			gesture = &events[index]
		}
	}
	for name, event := range map[string]*audit.Event{"order": order, "guest gesture": gesture} {
		if event == nil {
			t.Fatalf("no %s event in %+v", name, events)
		}
		if event.Actor.Kind != audit.ActorAPI || event.Actor.Name != token.ID || event.Details["on_behalf_of"] != "alice-sub" {
			t.Errorf("%s event %+v", name, event)
		}
	}
}

// Les trois autres gestes passent aussi par le jeton : le corps de
// snapshot-delete (le nom de l'instantané) est lu comme pour l'opérateur.
func TestProxmoxVerbs_ShutdownSnapshotAndDeleteGoThroughAToken(t *testing.T) {
	server := proxmoxTokenBench(t)
	bearer, _ := server.issueWriteToken(t, "agent", proxmoxVerbIDs...)
	cases := []struct {
		vmid, gesture, body, verbID, reverse string
	}{
		{runningVMID, "shutdown", "{}", "proxmox.vm.shutdown", "proxmox.vm.start"},
		{runningVMID, "snapshot", "{}", "proxmox.vm.snapshot", "proxmox.vm.snapshot_delete"},
		{stoppedVMID, "snapshot-delete", `{"snapshot":"oc-20261021-120000"}`, "proxmox.vm.snapshot_delete", "proxmox.vm.snapshot"},
	}
	for _, c := range cases {
		got := postGuestVerb(server, bearer, c.vmid, c.gesture, c.body)
		if got.Code != http.StatusOK {
			t.Errorf("%s: %d %s", c.gesture, got.Code, got.Body.String())
			continue
		}
		if reply := replyOf(t, got); reply.Verb != c.verbID || reply.ReverseWith != c.reverse || reply.RunID == nil {
			t.Errorf("%s: reply %+v", c.gesture, reply)
		}
	}
}

// Un instantané qui n'est pas de ce produit est refusé par le serveur pour un
// jeton comme pour l'opérateur, et le refus passe inchangé.
func TestProxmoxVerbs_ASnapshotNotOursIsRefusedForAToken(t *testing.T) {
	server := proxmoxTokenBench(t)
	bearer, _ := server.issueWriteToken(t, "agent", "proxmox.vm.snapshot_delete")
	got := postGuestVerb(server, bearer, stoppedVMID, "snapshot-delete", `{"snapshot":"avant-migration"}`)
	if got.Code != http.StatusUnprocessableEntity || errorCode(t, got) != "proxmox.snapshot_invalid" {
		t.Fatalf("%d %s", got.Code, got.Body.String())
	}
}

// Sans le verbe, 403 verb_forbidden et aucun ordre n'est écrit ; un verbe
// ne porte pas son inverse.
func TestProxmoxVerbs_ATokenWithoutTheVerbIsRefused(t *testing.T) {
	server := proxmoxTokenBench(t)
	bearer, _ := server.issueWriteToken(t, "agent", "proxmox.vm.shutdown", "probe.pause")
	for _, c := range []struct{ vmid, gesture, body string }{
		{stoppedVMID, "start", "{}"},
		{runningVMID, "snapshot", "{}"},
		{stoppedVMID, "snapshot-delete", `{"snapshot":"oc-20261021-120000"}`},
	} {
		got := postGuestVerb(server, bearer, c.vmid, c.gesture, c.body)
		if got.Code != http.StatusForbidden || errorCode(t, got) != codeAPITokenVerbForbidden {
			t.Errorf("%s: %d %s", c.gesture, got.Code, got.Body.String())
		}
	}
	noVerb, _ := server.issueWriteToken(t, "muet")
	if got := postGuestVerb(server, noVerb, stoppedVMID, "start", "{}"); got.Code != http.StatusForbidden || errorCode(t, got) != codeAPITokenVerbForbidden {
		t.Errorf("no verb: %d %s", got.Code, got.Body.String())
	}
	if runs, err := server.db.ListActionRuns(context.Background(), action.Query{MachineID: pveID, Limit: 10}); err != nil || len(runs) != 0 {
		t.Fatalf("a refusal wrote runs: %v %v", runs, err)
	}
}

// Un jeton en lecture ne touche jamais un geste, quel qu'il soit.
func TestProxmoxVerbs_AReadTokenIsRouteForbidden(t *testing.T) {
	server := proxmoxTokenBench(t)
	bearer := server.issueAPIToken(t, "lecteur")
	got := postGuestVerb(server, bearer, stoppedVMID, "start", "{}")
	if got.Code != http.StatusForbidden || errorCode(t, got) != codeAPITokenRouteForbidden {
		t.Fatalf("%d %s", got.Code, got.Body.String())
	}
}

// Les garde-fous du serveur valent pour un jeton comme pour l'opérateur : un
// état incohérent, un invité inconnu, un geste qui n'existe pas (404 comme
// avant pour l'opérateur, y compris l'arrêt forcé ; un jeton n'ouvre pas une
// route qui n'est ni une lecture ni un verbe).
func TestProxmoxVerbs_ServerGuardsHoldForATokenToo(t *testing.T) {
	server := proxmoxTokenBench(t)
	bearer, _ := server.issueWriteToken(t, "agent", proxmoxVerbIDs...)
	cases := map[string]struct {
		vmid, gesture string
		status        int
		key           string
	}{
		"start a running":    {runningVMID, "start", http.StatusConflict, "proxmox.state_mismatch"},
		"shutdown a stopped": {stoppedVMID, "shutdown", http.StatusConflict, "proxmox.state_mismatch"},
		"unknown guest":      {"9999", "start", http.StatusConflict, "proxmox.guest_unknown"},
	}
	for name, c := range cases {
		got := postGuestVerb(server, bearer, c.vmid, c.gesture, "{}")
		if got.Code != c.status || errorCode(t, got) != c.key {
			t.Errorf("%s: %d %s", name, got.Code, got.Body.String())
		}
	}
	for _, gesture := range []string{"reboot", "stop", "{verb}"} {
		if got := postGuestVerb(server, bearer, runningVMID, gesture, "{}"); got.Code != http.StatusForbidden || errorCode(t, got) != codeAPITokenRouteForbidden {
			t.Errorf("token, %s: %d %s", gesture, got.Code, got.Body.String())
		}
		if got := postGuestAction(server, pveID, runningVMID, gesture, "{}"); got.Code != http.StatusNotFound || errorCode(t, got) != "not_found" {
			t.Errorf("operator, %s: %d %s", gesture, got.Code, got.Body.String())
		}
	}
}

// proxmox.self_target : arrêter la machine qui héberge ce serveur reste
// refusé par un jeton. L'invité du faux hôte porte le nom d'hôte de la
// machine du serveur, d'où le rapprochement.
func TestProxmoxVerbs_TheServersOwnGuestIsNeverShutDownByAToken(t *testing.T) {
	server, api, _, hostKey := proxmoxBenchWithKey(t)
	local, err := server.machines.Get(context.Background(), machine.LocalID)
	if err != nil || local.Hostname == "" {
		t.Skipf("the test server has no named local machine: %+v %v", local, err)
	}
	api.setGuestName(local.Hostname)
	if got := putProxmox(server, pveID, api.request(pveTokenSecret)); got.Code != http.StatusOK {
		t.Fatalf("declare: %d %s", got.Code, got.Body.String())
	}
	resp, _, _, cancel := openStream(t, server, pveID, hostKey)
	defer resp.Body.Close()
	defer cancel()
	bearer, _ := server.issueWriteToken(t, "agent", proxmoxVerbIDs...)
	got := postGuestVerb(server, bearer, runningVMID, "shutdown", "{}")
	if got.Code != http.StatusConflict || errorCode(t, got) != "proxmox.self_target" {
		t.Fatalf("%d %s", got.Code, got.Body.String())
	}
}

// Les quatre verbes sont au registre, dans l'ordre, par paires d'inverses, et
// chaque route est exacte : un joker ou un autre mot reste un 404.
func TestProxmoxVerbs_AreRegisteredAsPairs(t *testing.T) {
	want := map[string]string{
		"proxmox.vm.start": "proxmox.vm.shutdown", "proxmox.vm.shutdown": "proxmox.vm.start",
		"proxmox.vm.snapshot": "proxmox.vm.snapshot_delete", "proxmox.vm.snapshot_delete": "proxmox.vm.snapshot",
	}
	for id, reverse := range want {
		found, ok := verb.Default.Lookup(id)
		if !ok || found.Reverse != reverse {
			t.Errorf("%s: %+v %v", id, found, ok)
		}
	}
	if body := mustVerb(t, "proxmox.vm.snapshot_delete").Body; len(body) != 1 || body[0] != "snapshot" {
		t.Errorf("snapshot_delete body %v", body)
	}
}

func mustVerb(t *testing.T, id string) verb.Verb {
	t.Helper()
	found, ok := verb.Default.Lookup(id)
	if !ok {
		t.Fatalf("verb %s is not registered", id)
	}
	return found
}

// --- GET /api/token/verbs ---

type tokenVerbsReply struct {
	Scope string `json:"scope"`
	Verbs []struct {
		ID      string   `json:"id"`
		Method  string   `json:"method"`
		Path    string   `json:"path"`
		Params  []string `json:"params"`
		Body    []string `json:"body"`
		Reverse string   `json:"reverse"`
		Label   string   `json:"label"`
		Summary string   `json:"summary"`
	} `json:"verbs"`
}

func (ts *testServer) tokenVerbsOf(t *testing.T, bearer string) (tokenVerbsReply, string) {
	t.Helper()
	got := callBearerWith(ts.Server, http.MethodGet, "/api/token/verbs", bearer, "")
	if got.Code != http.StatusOK {
		t.Fatalf("token verbs: %d %s", got.Code, got.Body.String())
	}
	var reply tokenVerbsReply
	decodeAPI(t, got, &reply)
	return reply, got.Body.String()
}

func TestTokenVerbs_AReadTokenAndAnEmptyWriteTokenGetNoVerb(t *testing.T) {
	server := newTestServer(t)
	read, raw := server.tokenVerbsOf(t, server.issueAPIToken(t, "lecteur"))
	if read.Scope != "read" || read.Verbs == nil || len(read.Verbs) != 0 || !strings.Contains(raw, `"verbs":[]`) {
		t.Fatalf("read: %s", raw)
	}
	bearer, _ := server.issueWriteToken(t, "muet")
	write, raw := server.tokenVerbsOf(t, bearer)
	if write.Scope != "write" || len(write.Verbs) != 0 || !strings.Contains(raw, `"verbs":[]`) {
		t.Fatalf("write without verb: %s", raw)
	}
}

// Le jeton ne voit que ses verbes, avec le contrat exact.
func TestTokenVerbs_ATokenSeesOnlyItsVerbsWithTheContract(t *testing.T) {
	server := newTestServer(t)
	bearer, _ := server.issueWriteToken(t, "agent", "proxmox.vm.snapshot_delete", "proxmox.vm.start", "probe.pause")
	reply, raw := server.tokenVerbsOf(t, bearer)
	if reply.Scope != "write" || len(reply.Verbs) != 3 {
		t.Fatalf("reply %s", raw)
	}
	// L'ordre est celui du registre, pas celui de la création du jeton.
	order := []string{reply.Verbs[0].ID, reply.Verbs[1].ID, reply.Verbs[2].ID}
	if strings.Join(order, ",") != "probe.pause,proxmox.vm.start,proxmox.vm.snapshot_delete" {
		t.Fatalf("order %v", order)
	}
	start := reply.Verbs[1]
	if start.Method != "POST" || start.Path != "/api/machines/{id}/proxmox/guests/{vmid}/actions/start" ||
		strings.Join(start.Params, ",") != "id,vmid" || start.Body == nil || len(start.Body) != 0 || start.Reverse != "proxmox.vm.shutdown" {
		t.Fatalf("start %+v", start)
	}
	deletion := reply.Verbs[2]
	if deletion.Path != "/api/machines/{id}/proxmox/guests/{vmid}/actions/snapshot-delete" ||
		strings.Join(deletion.Body, ",") != "snapshot" || deletion.Reverse != "proxmox.vm.snapshot" {
		t.Fatalf("snapshot_delete %+v", deletion)
	}
	// Les textes sont ceux du catalogue du serveur, pas leurs clés.
	for _, v := range reply.Verbs {
		if v.Label == "" || v.Summary == "" || strings.HasPrefix(v.Label, "api_token.") || strings.HasPrefix(v.Summary, "api_token.") {
			t.Errorf("%s: label %q summary %q", v.ID, v.Label, v.Summary)
		}
	}
	if strings.Contains(raw, "proxmox.vm.shutdown\",\"method") {
		t.Fatal("the token sees a verb it does not carry")
	}
}

// La déduction de chemin et de corps vient du registre : chaque verbe rendu
// à un jeton plein est une vraie route, et ses params sont ceux du chemin.
func TestTokenVerbs_AFullTokenSeesEveryVerbOnARealRoute(t *testing.T) {
	server := newTestServer(t)
	bearer, _ := server.issueWriteToken(t, "plein", allVerbIDs()...)
	reply, _ := server.tokenVerbsOf(t, bearer)
	if len(reply.Verbs) != len(verb.Default.All()) {
		t.Fatalf("%d verbs, want %d", len(reply.Verbs), len(verb.Default.All()))
	}
	real := map[string]bool{}
	for _, route := range server.routes() {
		real[route.method+" "+route.pattern] = true
	}
	for _, v := range reply.Verbs {
		if !real[v.Method+" "+v.Path] {
			t.Errorf("%s: %s %s is not a route", v.ID, v.Method, v.Path)
		}
		if strings.Join(v.Params, ",") != strings.Join(verb.PathParams(v.Path), ",") {
			t.Errorf("%s: params %v do not match %s", v.ID, v.Params, v.Path)
		}
	}
}

func TestTokenVerbs_TheOperatorAndAnUnknownTokenAreRefused(t *testing.T) {
	server := newTestServer(t)
	if got := callAPI(server.Server, http.MethodGet, "/api/token/verbs", nil); got.Code != http.StatusForbidden || errorCode(t, got) != codeAPITokenOnly {
		t.Fatalf("operator: %d %s", got.Code, got.Body.String())
	}
	if got := callBearerWith(server.Server, http.MethodGet, "/api/token/verbs", "ock_inconnu", ""); got.Code != http.StatusUnauthorized {
		t.Fatalf("unknown token: %d", got.Code)
	}
}
