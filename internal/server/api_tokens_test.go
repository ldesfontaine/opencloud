package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ldesfontaine/opencloud/internal/apitoken"
	"github.com/ldesfontaine/opencloud/internal/audit"
	"github.com/ldesfontaine/opencloud/internal/brand"
	"github.com/ldesfontaine/opencloud/internal/mcpcheck"
	"github.com/ldesfontaine/opencloud/internal/probe"
	"github.com/ldesfontaine/opencloud/internal/store/storetest"
)

// issueAPIToken tire un jeton d'API sans passer par l'interface : le test
// regarde ce que le jeton ouvre, pas comment on le crée.
func (ts *testServer) issueAPIToken(t *testing.T, name string) string {
	t.Helper()
	cleartext, _, err := ts.apiTokens.Create(context.Background(), apitoken.Request{Name: name})
	if err != nil {
		t.Fatal(err)
	}
	return cleartext
}

// callBearer joue une lecture de l'API sous un jeton, sans les identifiants
// de l'opérateur : le banc ne pose rien sur une requête qui porte déjà
// Authorization.
func callBearer(server *Server, path, bearer string) *httptest.ResponseRecorder {
	return callBearerWith(server, http.MethodGet, path, bearer, "")
}

func callBearerWith(server *Server, method, path, bearer, peer string, headers ...string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, path, nil)
	request.Header.Set("Sec-Fetch-Site", "same-origin")
	request.Header.Set("Authorization", "Bearer "+bearer)
	if method != http.MethodGet {
		request.Header.Set("Content-Type", "application/json")
	}
	if peer != "" {
		request.RemoteAddr = peer + ":4000"
	}
	for i := 0; i+1 < len(headers); i += 2 {
		request.Header.Add(headers[i], headers[i+1])
	}
	recorder := httptest.NewRecorder()
	server.ServeHTTP(recorder, request)
	return recorder
}

// Chaque lecture de la liste fermée répond à un jeton : 200, ou 404 pour un
// identifiant que personne n'a posé, jamais un refus d'accès.
func TestAPIToken_ReadsEveryAllowlistedRoute(t *testing.T) {
	server := newTestServer(t)
	bearer := server.issueAPIToken(t, "mcp-maison")
	for _, entry := range apiTokenRoutes {
		method, pattern, _ := strings.Cut(entry, " ")
		path := concretePath(pattern)
		got := callBearerWith(server.Server, method, path, bearer, "")
		if got.Code == http.StatusUnauthorized || got.Code == http.StatusForbidden {
			t.Errorf("%s: status %d %s", entry, got.Code, got.Body.String())
		}
	}
}

// Toute autre route de l'API, écriture, réglage ou flux, répond 403 : la
// liste est fermée, et un jeton ne passe pas par une route ajoutée plus tard.
func TestAPIToken_RefusesEveryOtherRoute(t *testing.T) {
	server := newTestServer(t)
	bearer := server.issueAPIToken(t, "mcp-maison")
	checked := 0
	for _, route := range server.routes() {
		if route.method == anyMethod || !strings.HasPrefix(route.pattern, "/api/") || isAPITokenRoute(route.method, route.pattern) {
			continue
		}
		checked++
		got := callBearerWith(server.Server, route.method, concretePath(route.pattern), bearer, "")
		if got.Code != http.StatusForbidden || errorCode(t, got) != codeAPITokenRouteForbidden {
			t.Errorf("%s %s: status %d %s, want 403 %s", route.method, route.pattern, got.Code, got.Body.String(), codeAPITokenRouteForbidden)
		}
	}
	if checked < 60 {
		t.Fatalf("only %d routes checked: the walk sees too little", checked)
	}
	// Le cas qui compte : un jeton ne s'en crée pas un autre.
	created := callBearerWith(server.Server, http.MethodPost, "/api/settings/api-tokens", bearer, "")
	if created.Code != http.StatusForbidden {
		t.Fatalf("a token created a token: %d", created.Code)
	}
}

func TestAPIToken_AllowlistNamesRealRoutes(t *testing.T) {
	server := newTestServer(t)
	real := map[string]bool{}
	for _, route := range server.routes() {
		real[route.method+" "+route.pattern] = true
	}
	for _, entry := range apiTokenRoutes {
		if !real[entry] {
			t.Errorf("%q is not a route of the server", entry)
		}
		if !strings.HasPrefix(entry, "GET /api/") {
			t.Errorf("%q: a token opens reads of the API only", entry)
		}
	}
}

// Un jeton inconnu répond 401 et consomme la limite des échecs Basic ; au-delà
// de la rafale, 429. Un jeton valide ne la consomme pas et passe encore.
func TestAPIToken_Invalid_401AndRateLimited(t *testing.T) {
	server := newTestServer(t)
	good := server.issueAPIToken(t, "mcp-maison")
	attempt := func(peer, bearer string) *httptest.ResponseRecorder {
		return callBearerWith(server.Server, http.MethodGet, "/api/counts", bearer, peer)
	}
	for i := range authAttemptBurst {
		got := attempt("203.0.113.9", "ock_"+strings.Repeat("0", 40))
		if got.Code != http.StatusUnauthorized || errorCode(t, got) != codeAPITokenInvalid {
			t.Fatalf("attempt %d: %d %s", i+1, got.Code, got.Body.String())
		}
		if got.Header().Get("WWW-Authenticate") != "Bearer" {
			t.Fatalf("WWW-Authenticate %q", got.Header().Get("WWW-Authenticate"))
		}
	}
	if got := attempt("203.0.113.9", "ock_"+strings.Repeat("1", 40)); got.Code != http.StatusTooManyRequests {
		t.Fatalf("after the burst: %d, want 429", got.Code)
	}
	if got := attempt("203.0.113.9", good); got.Code != http.StatusOK {
		t.Fatalf("a valid token after the burst: %d", got.Code)
	}
	if got := attempt("198.51.100.7", "ock_"+strings.Repeat("2", 40)); got.Code != http.StatusUnauthorized {
		t.Fatalf("another address: %d, want 401", got.Code)
	}
}

func TestAPIToken_RevokedOrExpiredIsRefused(t *testing.T) {
	server := newTestServer(t)
	ctx := context.Background()
	revoked, token, err := server.apiTokens.Create(ctx, apitoken.Request{Name: "à révoquer"})
	if err != nil {
		t.Fatal(err)
	}
	if got := callBearer(server.Server, "/api/counts", revoked); got.Code != http.StatusOK {
		t.Fatalf("before: %d", got.Code)
	}
	if err := server.apiTokens.Revoke(ctx, token.ID); err != nil {
		t.Fatal(err)
	}
	if got := callBearer(server.Server, "/api/counts", revoked); got.Code != http.StatusUnauthorized {
		t.Fatalf("revoked: %d", got.Code)
	}
	thirty := 30
	expiring, _, err := server.apiTokens.Create(ctx, apitoken.Request{Name: "court", Days: &thirty})
	if err != nil {
		t.Fatal(err)
	}
	server.apiTokens.SetClock(func() time.Time { return testNow.Add(31 * 24 * time.Hour) })
	if got := callBearer(server.Server, "/api/counts", expiring); got.Code != http.StatusUnauthorized {
		t.Fatalf("expired: %d", got.Code)
	}
}

// Tant qu'aucun compte n'existe, un Bearer est ignoré : la première
// configuration seule est servie, et un jeton n'ouvre pas une installation.
func TestAPIToken_IgnoredWhileNoAccount(t *testing.T) {
	server := newFirstSetupServer(t)
	bearer := server.issueAPIToken(t, "mcp-maison")
	requireSetupRequired(t, callBearer(server.Server, "/api/counts", bearer))
}

// Derrière un mandataire, le jeton passe par lui-même : ni l'en-tête
// d'identité ni le pair de confiance ne sont demandés à mcp-maison, qui
// joint openCloud en direct.
func TestAPIToken_WorksInForwardModeWithoutTheProxy(t *testing.T) {
	server := newFirstSetupServer(t)
	bearer := server.issueAPIToken(t, "mcp-maison")
	server.enableForward(t)
	if got := callBearerWith(server.Server, http.MethodGet, "/api/counts", bearer, untrustedPeer); got.Code != http.StatusOK {
		t.Fatalf("status %d %s", got.Code, got.Body.String())
	}
	if got := callBearerWith(server.Server, http.MethodGet, "/api/counts", "ock_"+strings.Repeat("3", 40), untrustedPeer); got.Code != http.StatusUnauthorized {
		t.Fatalf("a wrong token: %d", got.Code)
	}
	// Sans jeton, le même pair reste refusé.
	if got := callForwarded(server.Server, http.MethodGet, "/api/counts", untrustedPeer, ""); got.Code != http.StatusUnauthorized {
		t.Fatalf("no token: %d", got.Code)
	}
}

// Les cinq masquages : ce qu'un opérateur lit en clair, un jeton ne le voit
// pas.
func TestAPIToken_ReadsAreRedacted(t *testing.T) {
	server := newTestServer(t)
	ctx := context.Background()
	server.seedManifest(t)
	server.seedAlerts(t)
	jobID := server.seedJobs(t)
	if _, _, err := server.machines.CreateToken(ctx, "vps-lyon", nil); err != nil {
		t.Fatal(err)
	}
	const password = "hunter2"
	// Une base d'avant le refus des identifiants dans une cible : la création
	// les refuse aujourd'hui, la ligne reste lisible.
	secret := storetest.InsertProbe(t, server.db, "5ec0be5ec0be5ec0", probe.Definition{
		Name: "admin protégé", Kind: probe.KindHTTP, Target: "https://admin:" + password + "@cloud.exemple.fr/admin", MachineID: remoteID,
	}, testNow)
	bearer := server.issueAPIToken(t, "mcp-maison")

	read := func(t *testing.T, path string, asToken bool) string {
		t.Helper()
		var got *httptest.ResponseRecorder
		if asToken {
			got = callBearer(server.Server, path, bearer)
		} else {
			got = callAPI(server.Server, http.MethodGet, path, nil)
		}
		if got.Code != http.StatusOK {
			t.Fatalf("%s: %d %s", path, got.Code, got.Body.String())
		}
		return got.Body.String()
	}

	t.Run("pending enrollment tokens", func(t *testing.T) {
		var operatorMachines, tokenMachines machinesResponse
		mustDecode(t, read(t, "/api/machines", false), &operatorMachines)
		mustDecode(t, read(t, "/api/machines", true), &tokenMachines)
		if len(operatorMachines.Pending) != 1 || len(tokenMachines.Pending) != 0 {
			t.Fatalf("pending tokens: operator %d, token %d", len(operatorMachines.Pending), len(tokenMachines.Pending))
		}
		if len(tokenMachines.Machines) != len(operatorMachines.Machines) {
			t.Fatal("redaction removed machines")
		}
	})

	t.Run("job ping url and snippets", func(t *testing.T) {
		var operatorJob, tokenJob jobResponse
		mustDecode(t, read(t, "/api/jobs/"+jobID, false), &operatorJob)
		tokenBody := read(t, "/api/jobs/"+jobID, true)
		mustDecode(t, tokenBody, &tokenJob)
		if operatorJob.PingURL == "" || len(operatorJob.Snippets) == 0 {
			t.Fatalf("operator job: %+v", operatorJob)
		}
		if tokenJob.PingURL != "" || len(tokenJob.Snippets) != 0 || tokenJob.Job.Name != operatorJob.Job.Name {
			t.Fatalf("token job: %+v", tokenJob)
		}
		// Le jeton de la tâche n'est nulle part ailleurs dans la réponse.
		if pingToken := operatorJob.PingURL[strings.LastIndex(operatorJob.PingURL, "/")+1:]; strings.Contains(tokenBody, pingToken) {
			t.Fatalf("the ping token leaks: %s", tokenBody)
		}
	})

	t.Run("probe target password", func(t *testing.T) {
		for _, path := range []string{"/api/probes/" + secret.ID, "/api/probes"} {
			if !strings.Contains(read(t, path, false), password) {
				t.Errorf("%s: the operator should read the target as written", path)
			}
			if body := read(t, path, true); strings.Contains(body, password) || !strings.Contains(body, "cloud.exemple.fr") {
				t.Errorf("%s: a token must read the target without its password:\n%s", path, body)
			}
		}
	})

	t.Run("channel url and destination", func(t *testing.T) {
		operatorChannels, tokenChannels := read(t, "/api/alerts/channels", false), read(t, "/api/alerts/channels", true)
		if !strings.Contains(operatorChannels, "/api/webhooks/1/x") {
			t.Fatalf("operator channels: %s", operatorChannels)
		}
		if strings.Contains(tokenChannels, "/api/webhooks") || strings.Contains(tokenChannels, "s3cret") || !strings.Contains(tokenChannels, "https://discord.com") {
			t.Fatalf("token channels: %s", tokenChannels)
		}
	})

	t.Run("manifest export", func(t *testing.T) {
		if !strings.Contains(read(t, "/api/manifest/export", false), password) {
			t.Fatal("the operator export should carry the target as written: apply compares it")
		}
		if body := read(t, "/api/manifest/export", true); strings.Contains(body, password) || !strings.Contains(body, "cloud.exemple.fr") {
			t.Fatalf("a token must read the export without the password:\n%s", body)
		}
	})
}

// L'acteur d'une requête admise par jeton est api:<id>, pour qui mcp-maison
// dit appeler en plus : un en-tête unique, sans caractère de contrôle,
// borné, lu seulement sous un jeton.
func TestAPIToken_ActorIsAPI(t *testing.T) {
	server := newTestServer(t)
	cleartext, token, err := server.apiTokens.Create(context.Background(), apitoken.Request{Name: "acteur"})
	if err != nil {
		t.Fatal(err)
	}
	actorOf := func(headers ...string) audit.Actor {
		t.Helper()
		var seen audit.Actor
		var tokenSeen bool
		probe := func(w http.ResponseWriter, r *http.Request) {
			seen = audit.ActorFrom(r.Context())
			_, tokenSeen = apiTokenFrom(r.Context())
		}
		request := httptest.NewRequest(http.MethodGet, "/api/counts", nil)
		for i := 0; i+1 < len(headers); i += 2 {
			request.Header.Add(headers[i], headers[i+1])
		}
		recorder := httptest.NewRecorder()
		server.admitAPIToken(recorder, request, cleartext, true, probe)
		if recorder.Code != http.StatusOK || !tokenSeen {
			t.Fatalf("not admitted: %d", recorder.Code)
		}
		return seen
	}
	if got := actorOf(); got.Kind != audit.ActorAPI || got.Name != token.ID || got.OnBehalfOf != "" {
		t.Fatalf("actor %+v", got)
	}
	if got := actorOf(onBehalfOfHeader, "alice@example.org"); got.OnBehalfOf != "alice@example.org" {
		t.Fatalf("on behalf of %q", got.OnBehalfOf)
	}
	// Deux en-têtes : on ne devine pas lequel dit vrai.
	if got := actorOf(onBehalfOfHeader, "a", onBehalfOfHeader, "b"); got.OnBehalfOf != "" {
		t.Fatalf("two headers: %q", got.OnBehalfOf)
	}
	// Une borne de 200 caractères, et rien de non imprimable.
	if got := actorOf(onBehalfOfHeader, strings.Repeat("é", 201)); len([]rune(got.OnBehalfOf)) != maxOnBehalfOfRunes {
		t.Fatalf("bound: %d runes", len([]rune(got.OnBehalfOf)))
	}
	if got := actorOf(onBehalfOfHeader, "bob\x07\x1b[31m"); strings.ContainsAny(got.OnBehalfOf, "\x07\x1b") {
		t.Fatalf("control characters kept: %q", got.OnBehalfOf)
	}
}

// Sous Basic, l'en-tête de mcp-maison ne veut rien dire : l'acteur reste
// l'opérateur, sans « pour qui ».
func TestAPIToken_OnBehalfOfIsIgnoredUnderBasic(t *testing.T) {
	server := newTestServer(t)
	request := httptest.NewRequest(http.MethodPost, "/api/settings/disk", strings.NewReader(`{}`))
	request.Header.Set(onBehalfOfHeader, "mallory")
	request.SetBasicAuth(testUsername, testPassword)
	request.Header.Set("Sec-Fetch-Site", "same-origin")
	request.Header.Set("Content-Type", "application/json")
	before := server.latestAuditID(t)
	server.ServeHTTP(httptest.NewRecorder(), request)
	for _, event := range server.auditSince(t, before) {
		if event.Actor.Kind == audit.ActorAPI || event.Details["on_behalf_of"] != "" {
			t.Fatalf("event %+v", event)
		}
	}
}

// La liste de Paramètres : le jeton créé s'y lit masqué, avec les durées
// offertes et le bloc .env tiré de l'adresse d'écoute.
func TestAPITokensAPI_ListCreateRevoke(t *testing.T) {
	server := newTestServer(t)
	created := callAPI(server.Server, http.MethodPost, "/api/settings/api-tokens", apiTokenRequest{Name: "mcp-maison"})
	if created.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", created.Code, created.Body.String())
	}
	var body apiTokenCreated
	decodeAPI(t, created, &body)
	if !strings.HasPrefix(body.Token, "ock_") || body.Item.Scope != "read" || body.Item.ExpiresAt == nil {
		t.Fatalf("created %+v", body)
	}
	listed := callAPI(server.Server, http.MethodGet, "/api/settings/api-tokens", nil)
	if strings.Contains(listed.Body.String(), body.Token) {
		t.Fatal("the list must never carry the token")
	}
	var list apiTokensResponse
	decodeAPI(t, listed, &list)
	if len(list.Tokens) != 1 || list.Limit != apitoken.MaxTokens || list.Env.OpenCloudURL != "http://example.com:80" {
		t.Fatalf("list %+v", list)
	}
	for _, request := range []apiTokenRequest{{Name: ""}, {Name: "ok", Scope: "write"}, {Name: "ok", ExpiresInDays: ptr(7)}} {
		if got := callAPI(server.Server, http.MethodPost, "/api/settings/api-tokens", request); got.Code != http.StatusUnprocessableEntity {
			t.Errorf("%+v: %d %s", request, got.Code, got.Body.String())
		}
	}
	if got := callAPI(server.Server, http.MethodDelete, "/api/settings/api-tokens/"+body.Item.ID, nil); got.Code != http.StatusNoContent {
		t.Fatalf("revoke: %d %s", got.Code, got.Body.String())
	}
	if got := callBearer(server.Server, "/api/counts", body.Token); got.Code != http.StatusUnauthorized {
		t.Fatalf("revoked token: %d", got.Code)
	}
	if got := callAPI(server.Server, http.MethodDelete, "/api/settings/api-tokens/"+body.Item.ID, nil); got.Code != http.StatusNotFound {
		t.Fatalf("revoke twice: %d", got.Code)
	}
}

func TestMCPEnvOf(t *testing.T) {
	cases := map[string][2]string{
		"127.0.0.1:8080":  {"127.0.0.1", "http://127.0.0.1:8080"},
		"0.0.0.0:9000":    {"127.0.0.1", "http://127.0.0.1:9000"},
		":8080":           {"127.0.0.1", "http://127.0.0.1:8080"},
		"192.168.1.10:80": {"192.168.1.10", "http://192.168.1.10:80"},
		"[::]:8080":       {"127.0.0.1", "http://127.0.0.1:8080"},
		"nonsense":        {"127.0.0.1", "http://127.0.0.1:8080"},
	}
	for listen, want := range cases {
		host, base := mcpEnvOf(listen)
		if host != want[0] || base != want[1] {
			t.Errorf("%q: %q %q, want %q %q", listen, host, base, want[0], want[1])
		}
	}
}

func ptr[T any](value T) *T { return &value }

func mustDecode(t *testing.T, body string, into any) {
	t.Helper()
	if err := json.Unmarshal([]byte(body), into); err != nil {
		t.Fatalf("decode %s: %v", body, err)
	}
}

// La carte de Paramètres lit le /health de mcp-maison à chaque appel : sans
// adresse réglée elle suggère celle à écrire, avec une adresse elle rend ce
// que l'autre côté répond, version et outils comptés.
func TestSettingsMCP_ReadsTheHealthOfMCPMaison(t *testing.T) {
	server := newTestServer(t)
	read := func() mcpCheckResponse {
		t.Helper()
		got := callAPI(server.Server, http.MethodGet, "/api/settings/mcp", nil)
		if got.Code != http.StatusOK {
			t.Fatalf("status %d %s", got.Code, got.Body.String())
		}
		var response mcpCheckResponse
		decodeAPI(t, got, &response)
		return response
	}
	if got := read(); got.State != string(mcpcheck.StateNotConfigured) || got.URL != "" || got.SuggestedURL != "http://example.com:3000" {
		t.Fatalf("not configured: %+v", got)
	}

	health := `{"status":"ok","version":"0.1.0","tools":["` + brand.ID + `_get_job","` + brand.ID + `_list_alerts","gmail_search"]}`
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/health" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(health))
	}))
	server.mcpURL = fake.URL
	server.mcpCheck = mcpcheck.New(fake.URL, server.now)
	got := read()
	if got.State != string(mcpcheck.StateReachable) || got.Version != "0.1.0" || got.OpenCloudTools != 2 || got.URL != fake.URL {
		t.Fatalf("reachable: %+v", got)
	}
	// Le jeton d'API n'ouvre pas la carte : elle est de l'opérateur.
	if refused := callBearer(server.Server, "/api/settings/mcp", server.issueAPIToken(t, "mcp-maison")); refused.Code != http.StatusForbidden {
		t.Fatalf("a token read the settings: %d", refused.Code)
	}
	fake.Close()
	if got := read(); got.State != string(mcpcheck.StateUnreachable) {
		t.Fatalf("closed: %+v", got)
	}
}
