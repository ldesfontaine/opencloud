package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ldesfontaine/opencloud/internal/agentwire"
	"github.com/ldesfontaine/opencloud/internal/apitoken"
	"github.com/ldesfontaine/opencloud/internal/audit"
	"github.com/ldesfontaine/opencloud/internal/machine"
	"github.com/ldesfontaine/opencloud/internal/verb"
)

// issueWriteToken tire un jeton en écriture avec les verbes donnés.
func (ts *testServer) issueWriteToken(t *testing.T, name string, verbs ...string) (string, apitoken.Token) {
	t.Helper()
	cleartext, token, err := ts.apiTokens.Create(context.Background(), apitoken.Request{Name: name, Scope: apitoken.ScopeWrite, Verbs: verbs})
	if err != nil {
		t.Fatal(err)
	}
	return cleartext, token
}

func allVerbIDs() []string {
	ids := []string{}
	for _, v := range verb.Default.All() {
		ids = append(ids, v.ID)
	}
	return ids
}

func verbPath(t *testing.T, id string) (method, path string) {
	t.Helper()
	found, ok := verb.Default.Lookup(id)
	if !ok {
		t.Fatalf("verb %s is not registered", id)
	}
	method, pattern, _ := strings.Cut(found.Route, " ")
	return method, concretePath(pattern)
}

func (ts *testServer) probeStatus(t *testing.T, id string) string {
	t.Helper()
	var found probeResponse
	decodeAPI(t, callAPI(ts.Server, http.MethodGet, "/api/probes/"+id, nil), &found)
	return found.Probe.Status
}

func replyOf(t *testing.T, recorder *httptest.ResponseRecorder) verbReplyJSON {
	t.Helper()
	var reply verbReplyJSON
	decodeAPI(t, recorder, &reply)
	return reply
}

// Les routes des verbes existent, en POST, et sont des routes d'opérateur
// de l'API : un verbe ne nomme pas une route inventée.
func TestVerbs_NameRealRoutes(t *testing.T) {
	server := newTestServer(t)
	real := map[string]bool{}
	for _, route := range server.routes() {
		real[route.method+" "+route.pattern] = true
	}
	for _, v := range verb.Default.All() {
		if !real[v.Route] {
			t.Errorf("%s: %q is not a route of the server", v.ID, v.Route)
		}
	}
}

// Critère : un jeton en écriture sans verbe ne peut rien faire.
func TestVerbs_WriteTokenWithoutVerbCanDoNothing(t *testing.T) {
	server := newTestServer(t)
	bearer, token := server.issueWriteToken(t, "muet")
	created := server.createProbeVia(t, probeRequest{Name: "site", Kind: "http", Target: "https://a.fr/", MachineID: machine.LocalID})
	before := server.latestAuditID(t)
	for _, id := range allVerbIDs() {
		method, path := verbPath(t, id)
		path = strings.ReplaceAll(path, "/x/", "/"+created.ID+"/")
		got := callBearerWith(server.Server, method, path, bearer, "")
		if got.Code != http.StatusForbidden || errorCode(t, got) != codeAPITokenVerbForbidden {
			t.Errorf("%s: %d %s, want 403 %s", id, got.Code, got.Body.String(), codeAPITokenVerbForbidden)
		}
	}
	if status := server.probeStatus(t, created.ID); status == "paused" {
		t.Fatal("a token without verb paused a probe")
	}
	// Chaque refus est un signal, borné pour ne pas inonder l'audit.
	denied := 0
	for _, event := range server.auditSince(t, before) {
		if event.Action != audit.APITokenDeny {
			t.Errorf("unexpected audit event %s", event.Action)
			continue
		}
		denied++
		if event.Actor.Kind != audit.ActorAPI || event.Actor.Name != token.ID || event.ObjectID != token.ID || event.Details["verb"] == "" {
			t.Errorf("deny event %+v", event)
		}
	}
	if denied == 0 || denied > apiVerbDenyBurst {
		t.Fatalf("%d deny events for %d refusals, want 1 to %d", denied, len(allVerbIDs()), apiVerbDenyBurst)
	}
}

// La trace des refus a sa propre limite : mille refus n'écrivent pas mille
// lignes, mais chacun reste refusé.
func TestVerbs_DenialsAreAuditedWithTheirOwnLimit(t *testing.T) {
	server := newTestServer(t)
	bearer, _ := server.issueWriteToken(t, "curieux")
	before := server.latestAuditID(t)
	for range apiVerbDenyBurst + 20 {
		got := callBearerWith(server.Server, http.MethodPost, "/api/probes/x/actions/pause", bearer, "")
		if got.Code != http.StatusForbidden {
			t.Fatalf("status %d: a refusal must stay a refusal past the audit limit", got.Code)
		}
	}
	if written := len(server.auditSince(t, before)); written != apiVerbDenyBurst {
		t.Fatalf("%d deny events written, want %d", written, apiVerbDenyBurst)
	}
}

// Critère : avec probe.pause un jeton met une sonde en pause, l'audit dit
// « acteur api, pour qui X », et probe.resume la relance.
func TestVerbs_PauseAndResumeAProbeAreAuditedForWhom(t *testing.T) {
	server := newTestServer(t)
	bearer, token := server.issueWriteToken(t, "agent", "probe.pause", "probe.resume")
	created := server.createProbeVia(t, probeRequest{Name: "site", Kind: "http", Target: "https://a.fr/", MachineID: machine.LocalID})
	before := server.latestAuditID(t)

	paused := callBearerWith(server.Server, http.MethodPost, "/api/probes/"+created.ID+"/actions/pause", bearer, "", onBehalfOfHeader, "alice-sub")
	if paused.Code != http.StatusOK {
		t.Fatalf("pause: %d %s", paused.Code, paused.Body.String())
	}
	reply := replyOf(t, paused)
	if reply.Verb != "probe.pause" || reply.RunID != nil || reply.State != "done" || reply.ReverseWith != "probe.resume" {
		t.Fatalf("reply %+v", reply)
	}
	if status := server.probeStatus(t, created.ID); status != "paused" {
		t.Fatalf("status %q, want paused", status)
	}
	events := server.auditSince(t, before)
	if len(events) != 1 || events[0].Action != audit.ProbePause || events[0].ObjectID != created.ID ||
		events[0].Actor.Kind != audit.ActorAPI || events[0].Actor.Name != token.ID || events[0].Details["on_behalf_of"] != "alice-sub" {
		t.Fatalf("audit %+v", events)
	}

	resumed := callBearerWith(server.Server, http.MethodPost, "/api/probes/"+created.ID+"/actions/resume", bearer, "", onBehalfOfHeader, "alice-sub")
	if resumed.Code != http.StatusOK || replyOf(t, resumed).ReverseWith != "probe.pause" {
		t.Fatalf("resume: %d %s", resumed.Code, resumed.Body.String())
	}
	if status := server.probeStatus(t, created.ID); status == "paused" {
		t.Fatal("the probe is still paused")
	}
}

func TestVerbs_JobPauseAndResume(t *testing.T) {
	server := newTestServer(t)
	server.enroll(t, "vps-paris-1", remoteID)
	bearer, _ := server.issueWriteToken(t, "agent", "job.pause", "job.resume")
	jobID := server.seedJobs(t)
	paused := callBearerWith(server.Server, http.MethodPost, "/api/jobs/"+jobID+"/actions/pause", bearer, "")
	if paused.Code != http.StatusOK || replyOf(t, paused).ReverseWith != "job.resume" {
		t.Fatalf("pause: %d %s", paused.Code, paused.Body.String())
	}
	var found jobResponse
	decodeAPI(t, callAPI(server.Server, http.MethodGet, "/api/jobs/"+jobID, nil), &found)
	if found.Job.Status != "paused" {
		t.Fatalf("job status %q", found.Job.Status)
	}
	if resumed := callBearerWith(server.Server, http.MethodPost, "/api/jobs/"+jobID+"/actions/resume", bearer, ""); resumed.Code != http.StatusOK {
		t.Fatalf("resume: %d %s", resumed.Code, resumed.Body.String())
	}
}

// Un échec de la route d'opérateur passe tel quel : même code, mêmes clés.
func TestVerbs_RouteFailuresPassThroughUnchanged(t *testing.T) {
	server := newTestServer(t)
	bearer, _ := server.issueWriteToken(t, "agent", "probe.pause", "probe.resume")
	created := server.createProbeVia(t, probeRequest{Name: "site", Kind: "http", Target: "https://a.fr/", MachineID: machine.LocalID})
	operator := callAPI(server.Server, http.MethodPost, "/api/probes/"+created.ID+"/actions/resume", map[string]any{})
	byToken := callBearerWith(server.Server, http.MethodPost, "/api/probes/"+created.ID+"/actions/resume", bearer, "")
	if operator.Code == http.StatusNoContent || byToken.Code != operator.Code || byToken.Body.String() != operator.Body.String() {
		t.Fatalf("operator %d %s, token %d %s", operator.Code, operator.Body.String(), byToken.Code, byToken.Body.String())
	}
	missing := callBearerWith(server.Server, http.MethodPost, "/api/probes/absent/actions/pause", bearer, "")
	if missing.Code != http.StatusNotFound {
		t.Fatalf("missing probe: %d", missing.Code)
	}
}

// Un verbe se porte un par un : probe.pause n'ouvre pas probe.resume.
func TestVerbs_ACarriedVerbDoesNotOpenItsReverse(t *testing.T) {
	server := newTestServer(t)
	bearer, _ := server.issueWriteToken(t, "agent", "probe.pause")
	created := server.createProbeVia(t, probeRequest{Name: "site", Kind: "http", Target: "https://a.fr/", MachineID: machine.LocalID})
	got := callBearerWith(server.Server, http.MethodPost, "/api/probes/"+created.ID+"/actions/resume", bearer, "")
	if got.Code != http.StatusForbidden || errorCode(t, got) != codeAPITokenVerbForbidden {
		t.Fatalf("%d %s", got.Code, got.Body.String())
	}
}

// Un jeton en lecture sur un POST reste route_forbidden, verbe ou non.
func TestVerbs_ReadTokenOnAPostIsRouteForbidden(t *testing.T) {
	server := newTestServer(t)
	bearer := server.issueAPIToken(t, "lecteur")
	got := callBearerWith(server.Server, http.MethodPost, "/api/probes/x/actions/pause", bearer, "")
	if got.Code != http.StatusForbidden || errorCode(t, got) != codeAPITokenRouteForbidden {
		t.Fatalf("%d %s", got.Code, got.Body.String())
	}
}

// Un jeton inconnu sur la route d'un verbe répond 401 comme ailleurs.
func TestVerbs_UnknownTokenOnAVerbRouteIs401(t *testing.T) {
	server := newTestServer(t)
	got := callBearerWith(server.Server, http.MethodPost, "/api/probes/x/actions/pause", "ock_inconnu", "")
	if got.Code != http.StatusUnauthorized {
		t.Fatalf("%d %s", got.Code, got.Body.String())
	}
}

// Critère : aucune route destructrice n'est atteignable, même par un jeton
// en écriture qui porte tous les verbes. La liste est blanche : chaque route
// non-GET qui n'est pas un verbe du registre est refusée.
func TestVerbs_EveryOtherWriteRouteIsRefusedToAFullWriteToken(t *testing.T) {
	server := newTestServer(t)
	bearer, _ := server.issueWriteToken(t, "plein", allVerbIDs()...)
	checked := 0
	for _, route := range server.routes() {
		if route.method == anyMethod || route.method == http.MethodGet || !strings.HasPrefix(route.pattern, "/api/") {
			continue
		}
		if _, isVerb := verb.Default.ByRoute(route.method + " " + route.pattern); isVerb {
			continue
		}
		checked++
		got := callBearerWith(server.Server, route.method, concretePath(route.pattern), bearer, "")
		if got.Code != http.StatusForbidden || errorCode(t, got) != codeAPITokenRouteForbidden {
			t.Errorf("%s %s: status %d %s, want 403 %s", route.method, route.pattern, got.Code, got.Body.String(), codeAPITokenRouteForbidden)
		}
	}
	if checked < 50 {
		t.Fatalf("only %d routes checked: the walk sees too little", checked)
	}
	// Les gestes qui comptent le plus, nommés : le test de table ne les nomme pas.
	for _, target := range []struct{ method, path string }{
		{http.MethodPost, "/api/settings/api-tokens"},
		{http.MethodDelete, "/api/machines/x"},
		{http.MethodPost, "/api/machines/x/actions/revoke"},
		{http.MethodPost, "/api/machines/x/actions/reenroll"},
		{http.MethodPut, "/api/machines/x/update-last"},
		{http.MethodGet, "/api/agent-migration"},
		{http.MethodPost, "/api/directory/services/x/actions/deploy"},
		{http.MethodPut, "/api/firewalls/x"},
		{http.MethodDelete, "/api/firewalls/x"},
		{http.MethodPut, "/api/machines/x/proxmox"},
		{http.MethodPost, "/api/auth/actions/disable"},
		{http.MethodPost, "/api/alerts/x/actions/acknowledge"},
	} {
		if got := callBearerWith(server.Server, target.method, target.path, bearer, ""); got.Code != http.StatusForbidden {
			t.Errorf("%s %s: %d", target.method, target.path, got.Code)
		}
	}
}

// Seuls les POST d'action sont des verbes : ni lecture, ni suppression.
func TestVerbs_RegistryHoldsOnlyActionPosts(t *testing.T) {
	for _, v := range verb.Default.All() {
		method, pattern, _ := strings.Cut(v.Route, " ")
		if method != http.MethodPost || !strings.Contains(pattern, "/actions/") {
			t.Errorf("%s: %q is not an action route", v.ID, v.Route)
		}
	}
}

// Les clés de catalogue d'un verbe existent dans les deux langues.
func TestVerbs_CatalogKeysExistInEveryLanguage(t *testing.T) {
	server := newTestServer(t)
	for _, code := range []string{"fr", "en"} {
		got := callAPI(server.Server, http.MethodGet, "/api/i18n/"+code, nil)
		if got.Code != http.StatusOK {
			t.Fatalf("catalog %s: %d", code, got.Code)
		}
		for _, v := range verb.Default.All() {
			for _, key := range []string{v.Label, v.Summary} {
				if !strings.Contains(got.Body.String(), `"`+key+`"`) {
					t.Errorf("%s: %s missing in %s", v.ID, key, code)
				}
			}
		}
	}
}

// Le débit est par jeton, distinct des échecs d'authentification : le
// jeton qui va trop vite attend, un autre jeton et les lectures passent.
func TestVerbs_RateLimitIsPerTokenAndSeparateFromAuthFailures(t *testing.T) {
	server := newTestServer(t)
	fast, _ := server.issueWriteToken(t, "rapide", "probe.pause")
	other, _ := server.issueWriteToken(t, "calme", "probe.pause")
	for i := range apiVerbBurst {
		// Une sonde absente : le geste échoue, mais il est admis, donc compté.
		if got := callBearerWith(server.Server, http.MethodPost, "/api/probes/absent/actions/pause", fast, ""); got.Code != http.StatusNotFound {
			t.Fatalf("call %d: %d %s", i, got.Code, got.Body.String())
		}
	}
	limited := callBearerWith(server.Server, http.MethodPost, "/api/probes/absent/actions/pause", fast, "")
	if limited.Code != http.StatusTooManyRequests || limited.Header().Get("Retry-After") == "" {
		t.Fatalf("past the burst: %d, Retry-After %q", limited.Code, limited.Header().Get("Retry-After"))
	}
	if got := callBearerWith(server.Server, http.MethodPost, "/api/probes/absent/actions/pause", other, ""); got.Code != http.StatusNotFound {
		t.Fatalf("another token shares the bucket: %d", got.Code)
	}
	if got := callBearerWith(server.Server, http.MethodGet, "/api/counts", fast, ""); got.Code != http.StatusOK {
		t.Fatalf("a read is limited by the verb bucket: %d", got.Code)
	}
	// L'inverse : les échecs d'authentification d'une adresse ne touchent pas
	// le seau d'un jeton.
	for range authAttemptBurst + 1 {
		callBearerWith(server.Server, http.MethodGet, "/api/counts", "ock_faux", "198.51.100.7")
	}
	if got := callBearerWith(server.Server, http.MethodPost, "/api/probes/absent/actions/pause", other, "", ""); got.Code == http.StatusTooManyRequests {
		t.Fatalf("auth failures drained a token bucket")
	}
}

// Critère : on_behalf_of vient de l'en-tête posé par mcp-maison, jamais du
// corps de la requête ni d'un détail forgé.
func TestVerbs_OnBehalfOfCannotBeForgedFromTheBody(t *testing.T) {
	server := newTestServer(t)
	bearer, _ := server.issueWriteToken(t, "agent", "probe.pause")
	created := server.createProbeVia(t, probeRequest{Name: "site", Kind: "http", Target: "https://a.fr/", MachineID: machine.LocalID})
	before := server.latestAuditID(t)

	body := strings.NewReader(`{"on_behalf_of":"forged","details":{"on_behalf_of":"forged"},"actor":{"kind":"operator","name":"forged"}}`)
	request := httptest.NewRequest(http.MethodPost, "/api/probes/"+created.ID+"/actions/pause", body)
	request.Header.Set("Authorization", "Bearer "+bearer)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Sec-Fetch-Site", "same-origin")
	request.Header.Set(onBehalfOfHeader, "real-sub")
	recorder := httptest.NewRecorder()
	server.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("%d %s", recorder.Code, recorder.Body.String())
	}
	events := server.auditSince(t, before)
	if len(events) != 1 || events[0].Details["on_behalf_of"] != "real-sub" || events[0].Actor.Kind != audit.ActorAPI {
		t.Fatalf("audit %+v", events)
	}

	// Sans en-tête, le corps ne l'invente pas.
	resume := httptest.NewRequest(http.MethodPost, "/api/probes/"+created.ID+"/actions/pause", strings.NewReader(`{"on_behalf_of":"forged"}`))
	resume.Header.Set("Authorization", "Bearer "+bearer)
	resume.Header.Set("Content-Type", "application/json")
	resume.Header.Set("Sec-Fetch-Site", "same-origin")
	last := server.latestAuditID(t)
	server.ServeHTTP(httptest.NewRecorder(), resume)
	for _, event := range server.auditSince(t, last) {
		if value, present := event.Details["on_behalf_of"]; present {
			t.Fatalf("on_behalf_of %q from the body", value)
		}
	}
}

// Ni le jeton ni son empreinte n'apparaissent dans une réponse de verbe, la
// liste des jetons ou l'audit.
func TestVerbs_NoSecretInRepliesNorAudit(t *testing.T) {
	server := newTestServer(t)
	bearer, token := server.issueWriteToken(t, "agent", "probe.pause")
	created := server.createProbeVia(t, probeRequest{Name: "site", Kind: "http", Target: "https://a.fr/", MachineID: machine.LocalID})
	before := server.latestAuditID(t) - 5
	replies := []string{
		callBearerWith(server.Server, http.MethodPost, "/api/probes/"+created.ID+"/actions/pause", bearer, "").Body.String(),
		callBearerWith(server.Server, http.MethodPost, "/api/probes/"+created.ID+"/actions/resume", bearer, "").Body.String(),
		callAPI(server.Server, http.MethodGet, "/api/settings/api-tokens", nil).Body.String(),
	}
	events, err := json.Marshal(server.auditSince(t, max(before, 0)))
	if err != nil {
		t.Fatal(err)
	}
	replies = append(replies, string(events))
	for _, text := range replies {
		if strings.Contains(text, bearer) || strings.Contains(text, token.Hash) {
			t.Fatalf("secret in %q", text)
		}
	}
}

// Création par l'API : la portée, les verbes, et les refus avec leur clé.
func TestVerbs_CreateWriteTokenThroughTheAPI(t *testing.T) {
	server := newTestServer(t)
	created := callAPI(server.Server, http.MethodPost, "/api/settings/api-tokens",
		apiTokenRequest{Name: "agent", Scope: "write", Verbs: []string{"probe.pause", "probe.resume"}})
	if created.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", created.Code, created.Body.String())
	}
	var body apiTokenCreated
	decodeAPI(t, created, &body)
	if body.Item.Scope != "write" || len(body.Item.Verbs) != 2 {
		t.Fatalf("created %+v", body.Item)
	}
	var list apiTokensResponse
	decodeAPI(t, callAPI(server.Server, http.MethodGet, "/api/settings/api-tokens", nil), &list)
	if len(list.Tokens) != 1 || len(list.Tokens[0].Verbs) != 2 || len(list.Verbs) != len(verb.Default.All()) {
		t.Fatalf("list %+v", list)
	}
	// Le jeton créé par l'API agit, avec ses seuls verbes.
	probe := server.createProbeVia(t, probeRequest{Name: "site", Kind: "http", Target: "https://a.fr/", MachineID: machine.LocalID})
	if got := callBearerWith(server.Server, http.MethodPost, "/api/probes/"+probe.ID+"/actions/pause", body.Token, ""); got.Code != http.StatusOK {
		t.Fatalf("pause: %d %s", got.Code, got.Body.String())
	}
	// Le créé journalise ses verbes.
	creation := server.auditSince(t, 0)
	found := false
	for _, event := range creation {
		if event.Action == audit.APITokenCreate && event.Details["verbs"] == "probe.pause,probe.resume" && event.Details["scope"] == "write" {
			found = true
		}
	}
	if !found {
		t.Fatal("the creation audit does not say the verbs")
	}

	cases := map[string]struct {
		request apiTokenRequest
		key     string
	}{
		"unknown verb":  {apiTokenRequest{Name: "x", Scope: "write", Verbs: []string{"machine.delete"}}, "api_token.verb_unknown"},
		"alert ack":     {apiTokenRequest{Name: "x", Scope: "write", Verbs: []string{"alert.acknowledge"}}, "api_token.verb_unknown"},
		"verbs on read": {apiTokenRequest{Name: "x", Verbs: []string{"probe.pause"}}, "api_token.verbs_need_write"},
		"unknown scope": {apiTokenRequest{Name: "x", Scope: "admin"}, "api_token.scope_invalid"},
	}
	for name, c := range cases {
		got := callAPI(server.Server, http.MethodPost, "/api/settings/api-tokens", c.request)
		if got.Code != http.StatusUnprocessableEntity || errorCode(t, got) != c.key {
			t.Errorf("%s: %d %s, want 422 %s", name, got.Code, got.Body.String(), c.key)
		}
	}
	// Un jeton ne se crée pas un autre jeton, même avec tous les verbes.
	full, _ := server.issueWriteToken(t, "plein", allVerbIDs()...)
	if got := callBearerWith(server.Server, http.MethodPost, "/api/settings/api-tokens", full, ""); got.Code != http.StatusForbidden {
		t.Fatalf("a token created a token: %d", got.Code)
	}
}

// agent.ping de bout en bout : le même code que l'opérateur, l'ordre signé
// est écrit, et la réponse porte son identifiant, son état et l'inverse.
func TestVerbs_PingAnAgentReturnsTheRun(t *testing.T) {
	server := newTestServer(t)
	enrolled, private := server.enroll(t, "vps-paris-1", remoteID)
	resp, _, _, cancel := openStream(t, server, enrolled.ID, private)
	defer resp.Body.Close()
	defer cancel()
	bearer, token := server.issueWriteToken(t, "agent", "agent.ping")
	before := server.latestAuditID(t)

	got := callBearerWith(server.Server, http.MethodPost, "/api/machines/"+remoteID+"/actions/ping", bearer, "", onBehalfOfHeader, "bob-sub")
	if got.Code != http.StatusOK {
		t.Fatalf("ping: %d %s", got.Code, got.Body.String())
	}
	reply := replyOf(t, got)
	if reply.Verb != "agent.ping" || reply.RunID == nil || *reply.RunID == "" || reply.State != "sent" || reply.ReverseWith != "agent.ping" {
		t.Fatalf("reply %+v", reply)
	}
	// Le contrat n'ouvre pas le run : ni sortie, ni paramètres, ni signature.
	if strings.Contains(got.Body.String(), "params") || strings.Contains(got.Body.String(), "output") {
		t.Fatalf("the reply leaks the run: %s", got.Body.String())
	}
	events := server.auditSince(t, before)
	if len(events) != 1 || events[0].Action != audit.ActionOrder || events[0].ObjectID != *reply.RunID ||
		events[0].Actor.Kind != audit.ActorAPI || events[0].Actor.Name != token.ID || events[0].Details["on_behalf_of"] != "bob-sub" ||
		events[0].Details["kind"] != string(agentwire.KindAgentPing) {
		t.Fatalf("audit %+v", events)
	}
}

// Un jeton qui a servi un verbe pour l'opérateur de la route Basic ne change
// rien à la route de l'opérateur : 204, pas le contrat des verbes.
func TestVerbs_OperatorKeepsItsOwnReply(t *testing.T) {
	server := newTestServer(t)
	created := server.createProbeVia(t, probeRequest{Name: "site", Kind: "http", Target: "https://a.fr/", MachineID: machine.LocalID})
	if got := callAPI(server.Server, http.MethodPost, "/api/probes/"+created.ID+"/actions/pause", map[string]any{}); got.Code != http.StatusNoContent {
		t.Fatalf("operator pause: %d %s", got.Code, got.Body.String())
	}
}
