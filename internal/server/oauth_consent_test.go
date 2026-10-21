package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ldesfontaine/opencloud/internal/audit"
	"github.com/ldesfontaine/opencloud/internal/mcp"
)

// --- La page de consentement, comme l'opérateur la voit ---

// L'autorisation rend la page, pas un code ; la page lit la demande telle que
// le serveur la garde : le client, l'URI exacte, ce qu'elle donne.
func TestOAuthConsent_AuthorizeShowsThePageAndNoCode(t *testing.T) {
	server := newTestServer(t)
	server.enableMCP(t)
	got := get(server.Server, authorizeQuery("read write"))
	if strings.Contains(got.Header().Get("Location"), localCallback) {
		t.Fatalf("the client got an answer before the operator: %q", got.Header().Get("Location"))
	}
	id := consentIDOf(t, got)
	var shown mcpConsentJSON
	decodeAPI(t, callAPI(server.Server, http.MethodGet, "/api/mcp/consents/"+id, nil), &shown)
	want := mcpConsentJSON{
		ID: id, ClientID: mcp.ClientID, RedirectURI: localCallback, WriteRequested: true, WriteAvailable: false,
		ReadTools: mcpReadToolCount, AccessSeconds: 3600, RefreshSeconds: 30 * 24 * 3600, ExpiresAt: testNow.Add(mcp.ConsentTTL),
	}
	if shown != want {
		t.Fatalf("page %+v\nwant %+v", shown, want)
	}
}

// Consentir pose le code, que le client échange contre une session en
// lecture ; Paramètres la liste avec sa portée.
func TestOAuthConsent_AllowThenExchangeGivesAReadSession(t *testing.T) {
	server := newTestServer(t)
	secret := server.enableMCP(t)
	code := server.consentedCode(t, "", false)
	got := exchangeCode(server, secret, code)
	if got.Code != http.StatusOK {
		t.Fatalf("exchange: %d %s", got.Code, got.Body.String())
	}
	var grant grantResponse
	decodeAPI(t, got, &grant)
	token, err := server.access.Verify(context.Background(), grant.AccessToken)
	if err != nil || token.Scope != mcp.ScopeRead {
		t.Fatalf("token %+v %v", token, err)
	}
	var state mcpResponse
	decodeAPI(t, callAPI(server.Server, http.MethodGet, "/api/mcp", nil), &state)
	if len(state.Sessions) != 1 || state.Sessions[0].Scope != "read" || !state.Sessions[0].StartedAt.Equal(testNow) {
		t.Fatalf("sessions %+v", state.Sessions)
	}
}

// Refuser renvoie le client avec access_denied et son state (RFC 6749
// §4.1.2.1), sans code ; la demande ne reçoit pas de seconde réponse.
func TestOAuthConsent_DenyRedirectsWithAccessDenied(t *testing.T) {
	server := newTestServer(t)
	server.enableMCP(t)
	id := consentIDOf(t, get(server.Server, authorizeQuery("")))
	location := consent(t, server.Server, id, "deny", false)
	query := location.Query()
	if location.Scheme+"://"+location.Host+location.Path != localCallback || query.Get("error") != "access_denied" || query.Get("state") != "xyz" || query.Has("code") {
		t.Fatalf("location %q", location)
	}
	for _, decision := range []string{"allow", "deny"} {
		got := callAPI(server.Server, http.MethodPost, "/api/mcp/consents/"+id+"/actions/"+decision, mcpConsentDecision{})
		if got.Code != http.StatusConflict || errorCode(t, got) != "mcp.consent_unknown" {
			t.Fatalf("%s after the denial: %d %s", decision, got.Code, got.Body.String())
		}
	}
}

// L'écriture n'est accordée que demandée et cochée, et pas du tout tant que
// /mcp est en lecture seule : la case n'est pas proposée, une décision forgée
// qui la porte reçoit la lecture.
func TestOAuthConsent_WriteIsGrantedOnlyWhenOffered(t *testing.T) {
	server := newTestServer(t)
	secret := server.enableMCP(t)
	for _, c := range []struct {
		scope string
		write bool
	}{{"write", false}, {"", true}, {"write", true}} {
		var grant grantResponse
		decodeAPI(t, exchangeCode(server, secret, server.consentedCode(t, c.scope, c.write)), &grant)
		token, err := server.access.Verify(context.Background(), grant.AccessToken)
		want := mcp.ScopeRead
		if !mcpHTTPReadOnly && c.scope == "write" && c.write {
			want = mcp.ScopeWrite
		}
		if err != nil || token.Scope != want {
			t.Errorf("scope %q checked %v: %+v %v, want %s", c.scope, c.write, token, err, want)
		}
	}
}

// Un identifiant inconnu ou périmé ne mène nulle part : ni page, ni code,
// ni redirect.
func TestOAuthConsent_UnknownOrExpiredRequestIsRefused(t *testing.T) {
	server := newTestServer(t)
	server.enableMCP(t)
	stale := consentIDOf(t, get(server.Server, authorizeQuery("")))
	server.access.SetClock(func() time.Time { return testNow.Add(mcp.ConsentTTL) })
	for _, id := range []string{"0123456789abcdef0123456789abcdef", stale} {
		for _, call := range []struct{ method, path string }{
			{http.MethodGet, "/api/mcp/consents/" + id},
			{http.MethodPost, "/api/mcp/consents/" + id + "/actions/allow"},
			{http.MethodPost, "/api/mcp/consents/" + id + "/actions/deny"},
		} {
			got := callAPI(server.Server, call.method, call.path, mcpConsentDecision{})
			if got.Code != http.StatusConflict || errorCode(t, got) != "mcp.consent_unknown" || strings.Contains(got.Body.String(), "redirect") {
				t.Errorf("%s %s: %d %s", call.method, call.path, got.Code, got.Body.String())
			}
		}
	}
}

// La réponse est une écriture de l'opérateur : ses identifiants, même
// origine, un Host connu, et rien d'autre que la décision dans le corps.
// Chaque refus laisse la demande en attente.
func TestOAuthConsent_AnswerIsAnOperatorWriteFromTheSameOrigin(t *testing.T) {
	server := newTestServer(t)
	server.enableMCP(t)
	id := consentIDOf(t, get(server.Server, authorizeQuery("")))
	allow := "/api/mcp/consents/" + id + "/actions/allow"
	send := func(change func(*http.Request), body string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodPost, allow, strings.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Sec-Fetch-Site", "same-origin")
		change(request)
		recorder := httptest.NewRecorder()
		server.ServeHTTP(recorder, request)
		return recorder
	}
	cases := map[string]struct {
		change func(*http.Request)
		body   string
		status int
		code   string
	}{
		"anonymous":    {func(r *http.Request) { r.Header.Set(anonymousHeader, "1") }, `{}`, http.StatusUnauthorized, codeUnauthorized},
		"cross site":   {func(r *http.Request) { r.Header.Set("Sec-Fetch-Site", "cross-site") }, `{}`, http.StatusForbidden, codeOrigin},
		"form post":    {func(r *http.Request) { r.Header.Set("Content-Type", "application/x-www-form-urlencoded") }, `write=true`, http.StatusForbidden, codeOrigin},
		"unknown host": {func(r *http.Request) { r.Host = "autre.exemple" }, `{}`, http.StatusForbidden, codeHost},
		"extra field":  {func(*http.Request) {}, `{"write":false,"redirect_uri":"https://evil.example/cb"}`, http.StatusBadRequest, codeBadJSON},
	}
	for name, c := range cases {
		got := send(c.change, c.body)
		if got.Code != c.status || errorCode(t, got) != c.code {
			t.Errorf("%s: %d %s", name, got.Code, got.Body.String())
		}
	}
	if location := consent(t, server.Server, id, "allow", false); !location.Query().Has("code") {
		t.Fatalf("the request did not survive the refusals: %q", location)
	}
}

// Le oui et le non vont au journal sous l'opérateur : le client, l'URI de
// retour et la portée, jamais le code ni le state.
func TestOAuthConsent_AnswersAreJournaledUnderTheOperator(t *testing.T) {
	server := newTestServer(t)
	server.enableMCP(t)
	mark := server.latestAuditID(t)
	code := server.consentedCode(t, "", false)
	consent(t, server.Server, consentIDOf(t, get(server.Server, authorizeQuery("write"))), "deny", false)
	events := server.auditSince(t, mark)
	if len(events) != 2 {
		t.Fatalf("events %+v", events)
	}
	for i, want := range []struct {
		action audit.Action
		scope  string
	}{{audit.MCPSessionAuthorize, "read"}, {audit.MCPSessionDeny, "write"}} {
		event := events[i]
		details := audit.Details{"client_id": mcp.ClientID, "redirect_uri": localCallback, "scope": want.scope}
		if event.Action != want.action || event.Actor != audit.Operator(testUsername) || len(event.Details) != len(details) {
			t.Fatalf("event %d: %+v", i, event)
		}
		for name, value := range details {
			if event.Details[name] != value {
				t.Errorf("event %d: %s = %q, want %q", i, name, event.Details[name], value)
			}
		}
		for _, value := range event.Details {
			if strings.Contains(value, code) || strings.Contains(value, "xyz") {
				t.Errorf("event %d leaks %q", i, value)
			}
		}
	}
}
