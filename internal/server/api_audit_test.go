package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/auth"
	gomcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/ldesfontaine/opencloud/internal/alert"
	"github.com/ldesfontaine/opencloud/internal/audit"
	"github.com/ldesfontaine/opencloud/internal/directory"
	"github.com/ldesfontaine/opencloud/internal/mcp"
)

// callAsOperator joue une requête JSON comme le front, sous les
// identifiants Basic de l'opérateur.
func callAsOperator(server *Server, method, path, password string, payload any) *httptest.ResponseRecorder {
	var body bytes.Buffer
	if payload != nil {
		_ = json.NewEncoder(&body).Encode(payload)
	}
	request := httptest.NewRequest(method, path, &body)
	request.Header.Set("Sec-Fetch-Site", "same-origin")
	request.Header.Set("Content-Type", "application/json")
	request.SetBasicAuth(testUsername, password)
	recorder := httptest.NewRecorder()
	server.ServeHTTP(recorder, request)
	return recorder
}

// latestAuditID est le dernier événement écrit : ce qui suit est du test.
func (ts *testServer) latestAuditID(t *testing.T) int64 {
	t.Helper()
	events, err := ts.db.ListAuditEvents(context.Background(), audit.Query{Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(events) == 0 {
		return 0
	}
	return events[0].ID
}

// auditSince rend les événements écrits après after, du plus ancien au plus
// récent.
func (ts *testServer) auditSince(t *testing.T, after int64) []audit.Event {
	t.Helper()
	events, err := ts.db.ListAuditEvents(context.Background(), audit.Query{Limit: 1000})
	if err != nil {
		t.Fatal(err)
	}
	since := []audit.Event{}
	for _, event := range events {
		if event.ID > after {
			since = append(since, event)
		}
	}
	slices.Reverse(since)
	return since
}

// isSecretDetailName reprend la règle du journal : un nom qui dit un secret,
// tel quel ou en suffixe.
func isSecretDetailName(name string) bool {
	for _, forbidden := range []string{"password", "token", "secret", "url"} {
		if name == forbidden || strings.HasSuffix(name, "_"+forbidden) {
			return true
		}
	}
	return false
}

// Chaque geste de l'opérateur, par l'API, va au journal sous son identité
// Basic ; aucun secret saisi ou tiré pendant ces gestes n'y paraît, ni en nom
// de détail ni en valeur.
func TestAudit_OperatorGesturesAreJournaledUnderTheirIdentityWithoutSecrets(t *testing.T) {
	server := newTestServer(t)
	server.enroll(t, "vps-paris-1", remoteID)
	mark := server.latestAuditID(t)
	const (
		channelURL     = "https://discord.com/api/webhooks/1/x"
		channelSecret  = "s3cret du canal"                     // gitleaks:allow -- faux secret de test
		channelSecret2 = "nouveau secret du canal"             // gitleaks:allow -- faux secret de test
		mailPassword   = "mot de passe du relais"              // gitleaks:allow -- faux secret de test
		newPassword    = "nouveau mot de passe de l interface" // gitleaks:allow -- faux secret de test
	)
	secrets := []string{channelURL, "webhooks/1", channelSecret, channelSecret2, mailPassword, newPassword, testPassword}
	expect := func(recorder *httptest.ResponseRecorder, code int) {
		t.Helper()
		if recorder.Code != code {
			t.Fatalf("status %d, want %d: %s", recorder.Code, code, recorder.Body.String())
		}
	}

	channel := channelRequest{Name: "salon ops", URL: channelURL, Format: "discord", Secret: channelSecret, MinSeverity: "attention", Enabled: true}
	created := callAsOperator(server.Server, http.MethodPost, "/api/alerts/channels", testPassword, channel)
	expect(created, http.StatusCreated)
	var createdChannel channelJSON
	decodeAPI(t, created, &createdChannel)
	channel.Secret = channelSecret2
	expect(callAsOperator(server.Server, http.MethodPut, fmt.Sprintf("/api/alerts/channels/%d", createdChannel.ID), testPassword, channel), http.StatusOK)

	job := callAsOperator(server.Server, http.MethodPost, "/api/jobs", testPassword, jobRequest{Name: "sauvegarde", MachineID: remoteID, IntervalMinutes: 60, GraceMinutes: 10})
	expect(job, http.StatusCreated)
	var createdJob jobJSON
	decodeAPI(t, job, &createdJob)
	first, err := server.db.GetHeartbeat(context.Background(), createdJob.ID)
	if err != nil {
		t.Fatal(err)
	}
	expect(callAsOperator(server.Server, http.MethodPost, "/api/jobs/"+createdJob.ID+"/actions/regenerate-token", testPassword, emptyRequest{}), http.StatusNoContent)
	regenerated, err := server.db.GetHeartbeat(context.Background(), createdJob.ID)
	if err != nil {
		t.Fatal(err)
	}
	secrets = append(secrets, first.Token, regenerated.Token)

	expect(callAsOperator(server.Server, http.MethodPost, "/api/probes", testPassword, probeRequest{Name: "api", Kind: "http", Target: "https://example.com/health", MachineID: remoteID}), http.StatusCreated)
	expect(callAsOperator(server.Server, http.MethodPost, "/api/directory/projects", testPassword, projectRequest{Name: "client-a", Title: "Client A"}), http.StatusCreated)
	expect(callAsOperator(server.Server, http.MethodPut, "/api/settings/mail", testPassword, mailSettingsRequest{
		Host: "smtp.exemple.fr", Port: 587, TLS: "starttls", Username: "ops", Password: mailPassword, From: "ops@exemple.fr",
	}), http.StatusOK)

	enabled := callAsOperator(server.Server, http.MethodPost, "/api/mcp/actions/enable", testPassword, emptyRequest{})
	expect(enabled, http.StatusCreated)
	var clientSecret mcpSecretResponse
	decodeAPI(t, enabled, &clientSecret)
	token := callAsOperator(server.Server, http.MethodPost, "/api/mcp/tokens", testPassword, mcpTokenRequest{Name: "inspecteur"})
	expect(token, http.StatusCreated)
	var apiToken mcpTokenResponse
	decodeAPI(t, token, &apiToken)
	enrollment := callAsOperator(server.Server, http.MethodPost, "/api/machines/tokens", testPassword, tokenRequest{Name: "vps-lyon-2"})
	expect(enrollment, http.StatusCreated)
	var enrollmentToken tokenResponse
	decodeAPI(t, enrollment, &enrollmentToken)
	secrets = append(secrets, clientSecret.Secret, apiToken.Token, enrollmentToken.Token)

	expect(callAsOperator(server.Server, http.MethodPut, "/api/auth/password", testPassword, authPasswordRequest{Password: newPassword}), http.StatusOK)

	journal := callAsOperator(server.Server, http.MethodGet, "/api/audit?limit=200", newPassword, nil)
	expect(journal, http.StatusOK)
	for _, secret := range secrets {
		if secret != "" && strings.Contains(journal.Body.String(), secret) {
			t.Errorf("the journal leaks %q", secret)
		}
	}

	var actions []audit.Action
	for _, event := range server.auditSince(t, mark) {
		actions = append(actions, event.Action)
		if event.Actor != audit.Operator(testUsername) {
			t.Errorf("%s: actor %+v, want the operator", event.Action, event.Actor)
		}
		for name := range event.Details {
			if isSecretDetailName(name) {
				t.Errorf("%s: detail %q names a secret", event.Action, name)
			}
		}
	}
	want := []audit.Action{
		audit.ChannelCreate, audit.ChannelUpdate, audit.HeartbeatCreate, audit.HeartbeatRegenerateToken,
		audit.ProbeCreate, audit.ProjectCreate, audit.SettingsUpdate, audit.MCPEnable, audit.MCPTokenCreate,
		audit.EnrollmentTokenCreate, audit.AuthChangePassword,
	}
	if !slices.Equal(actions, want) {
		t.Fatalf("actions %v\nwant %v", actions, want)
	}
}

// Le journal est une route de l'opérateur : sans identifiants, 401.
func TestAudit_ReadRequiresTheOperator(t *testing.T) {
	server := newTestServer(t)
	if got := callAs(server.Server, http.MethodGet, "/api/audit", "", ""); got.Code != http.StatusUnauthorized {
		t.Fatalf("without credentials: %d", got.Code)
	}
	if got := callAs(server.Server, http.MethodGet, "/api/audit", testUsername, testPassword); got.Code != http.StatusOK {
		t.Fatalf("with credentials: %d %s", got.Code, got.Body.String())
	}
}

// Une page s'arrête à la limite et dit où reprendre ; un filtre par objet
// ne rend que cet objet ; un paramètre illisible est refusé.
func TestAudit_PagesFiltersAndRefusesABadQuery(t *testing.T) {
	server := newTestServer(t)
	mark := server.latestAuditID(t)
	var ids []string
	for _, name := range []string{"client-a", "client-b", "client-c"} {
		var project projectJSON
		decodeAPI(t, callAPI(server.Server, http.MethodPost, "/api/directory/projects", projectRequest{Name: name}), &project)
		ids = append(ids, project.ID)
	}
	var first auditResponse
	decodeAPI(t, callAPI(server.Server, http.MethodGet, "/api/audit?limit=2", nil), &first)
	if len(first.Events) != 2 || first.NextBefore == nil || first.Events[0].ObjectID != ids[2] || first.Events[0].ActorKind != "operator" {
		t.Fatalf("first page: %+v", first)
	}
	var second auditResponse
	decodeAPI(t, callAPI(server.Server, http.MethodGet, fmt.Sprintf("/api/audit?limit=2&before=%d", *first.NextBefore), nil), &second)
	if len(second.Events) == 0 || second.Events[0].ObjectID != ids[0] {
		t.Fatalf("second page: %+v", second)
	}
	var filtered auditResponse
	decodeAPI(t, callAPI(server.Server, http.MethodGet, "/api/audit?object_kind=project&object_id="+ids[1], nil), &filtered)
	if len(filtered.Events) != 1 || filtered.Events[0].ObjectID != ids[1] || filtered.Events[0].Action != string(audit.ProjectCreate) || filtered.NextBefore != nil {
		t.Fatalf("filtered: %+v", filtered)
	}
	if filtered.Events[0].ID <= mark || filtered.Events[0].ObjectName != "client-b" || filtered.Events[0].Details == nil {
		t.Fatalf("event: %+v", filtered.Events[0])
	}
	for _, query := range []string{"limit=abc", "limit=0", "before=-1", "object_kind=nope"} {
		got := callAPI(server.Server, http.MethodGet, "/api/audit?"+query, nil)
		if got.Code != http.StatusBadRequest || errorCode(t, got) != codeBadQuery {
			t.Errorf("%s: %d %s", query, got.Code, got.Body.String())
		}
	}
}

// Un geste appelé sans acteur dans son contexte n'est pas tu : il est écrit
// sous « unknown », et c'est ce que le journal montre.
func TestAudit_GestureWithoutActorIsJournaledAsUnknown(t *testing.T) {
	server := newTestServer(t)
	mark := server.latestAuditID(t)
	if _, err := server.directory.CreateProject(context.Background(), directory.ProjectDefinition{Name: "orphelin"}); err != nil {
		t.Fatal(err)
	}
	events := server.auditSince(t, mark)
	if len(events) != 1 || events[0].Actor.Kind != audit.ActorUnknown {
		t.Fatalf("events: %+v", events)
	}
}

// Une écriture MCP est journalisée sous le jeton qui l'a faite.
func TestAudit_MCPWriteIsJournaledUnderItsToken(t *testing.T) {
	server := newTestServer(t)
	server.enroll(t, "vps-paris-1", remoteID)
	probeID := server.seedProbes(t, server.seedGaps(t))
	mark := server.latestAuditID(t)
	tools := server.newMCPServer(false)
	tools.AddReceivingMiddleware(func(next gomcp.MethodHandler) gomcp.MethodHandler {
		return func(ctx context.Context, method string, request gomcp.Request) (gomcp.Result, error) {
			if call, ok := request.(*gomcp.CallToolRequest); ok {
				call.Extra = &gomcp.RequestExtra{TokenInfo: &auth.TokenInfo{
					Scopes: []string{string(mcp.ScopeWrite)},
					Extra:  map[string]any{"token_id": "tok0000000000001", "kind": string(mcp.KindAPI)},
				}}
			}
			return next(ctx, method, request)
		}
	})
	session := connectMCP(t, tools)
	if text, isError := callTool(t, session, "pause_probe", map[string]any{"probe_id": probeID}); isError {
		t.Fatalf("pause_probe: %s", text)
	}
	events := server.auditSince(t, mark)
	if len(events) != 1 || events[0].Action != audit.ProbePause || events[0].Actor != audit.MCP("tok0000000000001") {
		t.Fatalf("events: %+v", events)
	}
}

// Une autorisation OAuth est nommée par sa famille, pas par son jeton
// d'accès, qui change toutes les heures.
func TestMCPActorName_NamesTheTokenOrTheFamily(t *testing.T) {
	api := &auth.TokenInfo{Extra: map[string]any{"token_id": "tok", "family_id": "", "kind": string(mcp.KindAPI)}}
	oauth := &auth.TokenInfo{Extra: map[string]any{"token_id": "tok", "family_id": "fam", "kind": string(mcp.KindAccess)}}
	if got := mcpActorName(api); got != "tok" {
		t.Errorf("api token: %q", got)
	}
	if got := mcpActorName(oauth); got != "fam" {
		t.Errorf("oauth: %q", got)
	}
	if got := mcpActorName(nil); got != "" {
		t.Errorf("no token: %q", got)
	}
}

// À la première configuration, un en-tête Basic qu'aucun mode ne vérifie ne
// nomme pas l'opérateur : l'activation est journalisée sous un opérateur sans
// nom, le compte créé se lit dans les détails.
func TestAudit_UnverifiedBasicHeaderNamesNobody(t *testing.T) {
	server := newFirstSetupServer(t)
	mark := server.latestAuditID(t)
	body := `{"mode":"basic","username":"lucas","password":"une phrase assez longue"}`
	request := httptest.NewRequest(http.MethodPost, "/api/auth/actions/enable", strings.NewReader(body))
	request.Header.Set("Sec-Fetch-Site", "same-origin")
	request.Header.Set("Content-Type", "application/json")
	request.SetBasicAuth("quelqu-un", "rien")
	recorder := httptest.NewRecorder()
	server.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("enable: %d %s", recorder.Code, recorder.Body.String())
	}
	// L'activation révoque aussi les jetons MCP : deux gestes, le même acteur.
	events := server.auditSince(t, mark)
	if len(events) != 2 || events[0].Action != audit.AuthEnable || events[0].Details["username"] != "lucas" || events[1].Action != audit.MCPTokenRevoke {
		t.Fatalf("events: %+v", events)
	}
	for _, event := range events {
		if event.Actor != audit.Operator("") || strings.Contains(fmt.Sprint(event.Details), "une phrase") {
			t.Fatalf("event: %+v", event)
		}
	}
}

// La page du journal est figée dans testdata/audit.golden.json : le front en
// relit les clés (api/contract.test.ts).
func TestAudit_ReadMatchesGolden(t *testing.T) {
	server := newTestServer(t)
	server.enroll(t, "vps-paris-1", remoteID)
	server.seedAudit(t)
	got := callAPI(server.Server, http.MethodGet, "/api/audit?limit=2", nil)
	if got.Code != http.StatusOK {
		t.Fatalf("status %d: %s", got.Code, got.Body.String())
	}
	compareGolden(t, filepath.Join("testdata", "audit.golden.json"), normalize(got.Body.String()))
}

// La page du journal sait combien de canaux le reçoivent et combien de ses
// envois échouent. Le serveur de test ne branche pas l'envoi : les
// livraisons réservées restent en attente, rien ne part ni n'échoue seul.
func TestAPI_AuditForwarding_CountsChannelsAndFailures(t *testing.T) {
	server := newTestServer(t)
	var none auditForwardingJSON
	decodeAPI(t, callAPI(server.Server, http.MethodGet, "/api/audit/forwarding", nil), &none)
	if none.Channels != 0 || none.Failing != 0 {
		t.Fatalf("before any channel: %+v", none)
	}

	var channel channelJSON
	decodeAPI(t, callAPI(server.Server, http.MethodPost, "/api/alerts/channels", map[string]any{
		"name": "journal", "url": "http://127.0.0.1:9/hook", "format": "json", "min_severity": "attention", "receives_audit": true, "enabled": true,
	}), &channel)
	if !channel.ReceivesAudit {
		t.Fatalf("the channel must read back as receiving the audit log: %+v", channel)
	}
	recorder := callAPI(server.Server, http.MethodGet, "/api/audit/forwarding", nil)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status %d: %s", recorder.Code, recorder.Body.String())
	}
	compareGolden(t, filepath.Join("testdata", "audit_forwarding.golden.json"), normalize(recorder.Body.String()))

	// Un geste de plus réserve une livraison ; la noter en échec la compte.
	callAPI(server.Server, http.MethodPost, "/api/directory/projects", projectRequest{Name: "client-a"})
	pending, err := server.db.ListPendingAuditDeliveries(context.Background())
	if err != nil || len(pending) == 0 {
		t.Fatalf("pending deliveries: %v, %v", pending, err)
	}
	failed := pending[0]
	failed.Status = alert.DeliveryFailed
	failed.UpdatedAt = testNow
	failed.NextAttemptAt = testNow.Add(5 * time.Second)
	if err := server.db.UpdateAuditDelivery(context.Background(), failed); err != nil {
		t.Fatal(err)
	}
	var failing auditForwardingJSON
	decodeAPI(t, callAPI(server.Server, http.MethodGet, "/api/audit/forwarding", nil), &failing)
	if failing.Channels != 1 || failing.Failing != 1 {
		t.Fatalf("after a failure: %+v", failing)
	}
}

func TestAudit_ForwardingRequiresTheOperator(t *testing.T) {
	server := newTestServer(t)
	if got := callAs(server.Server, http.MethodGet, "/api/audit/forwarding", "", ""); got.Code != http.StatusUnauthorized {
		t.Fatalf("without credentials: %d", got.Code)
	}
}
