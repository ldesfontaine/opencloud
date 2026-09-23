package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	gomcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/ldesfontaine/opencloud/internal/alert"
)

// Le catalogue, fermé : seize lectures, neuf écritures. Un outil ajouté
// ou renommé casse ce test, c'est voulu.
var (
	mcpReadTools = []string{
		"get_overview", "list_machines", "get_machine", "get_machine_network",
		"list_services", "get_service", "get_service_logs",
		"list_jobs", "get_job", "list_probes", "get_probe",
		"get_status_page", "list_incidents", "list_alerts", "get_alert", "list_alert_channels",
	}
	mcpWriteTools = []string{
		"acknowledge_alert", "pause_probe", "resume_probe", "pause_job", "resume_job",
		"open_incident", "add_incident_update", "create_silence", "set_update_policy",
	}
)

// connectMCP branche un vrai client MCP en mémoire sur un serveur d'outils.
func connectMCP(t *testing.T, server *gomcp.Server) *gomcp.ClientSession {
	t.Helper()
	ctx := context.Background()
	clientTransport, serverTransport := gomcp.NewInMemoryTransports()
	serverSession, err := server.Connect(ctx, serverTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = serverSession.Close() })
	client := gomcp.NewClient(&gomcp.Implementation{Name: "test", Version: "0"}, nil)
	session, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	return session
}

// callTool appelle un outil et rend son texte et s'il a refusé.
func callTool(t *testing.T, session *gomcp.ClientSession, name string, args map[string]any) (string, bool) {
	t.Helper()
	result, err := session.CallTool(context.Background(), &gomcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	if len(result.Content) != 1 {
		t.Fatalf("%s: %d contents", name, len(result.Content))
	}
	text, ok := result.Content[0].(*gomcp.TextContent)
	if !ok {
		t.Fatalf("%s: content %T", name, result.Content[0])
	}
	return text.Text, result.IsError
}

func toolErrorCode(t *testing.T, text string) string {
	t.Helper()
	var response apiError
	if err := json.Unmarshal([]byte(text), &response); err != nil {
		t.Fatalf("decode %s: %v", text, err)
	}
	return response.Error
}

func TestMCP_ListsSixteenReadsAndNineWritesWithHonestHints(t *testing.T) {
	server := newTestServer(t)
	session := connectMCP(t, server.mcpServer)
	listed, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, len(listed.Tools))
	for _, tool := range listed.Tools {
		names = append(names, tool.Name)
		if tool.Annotations == nil || tool.Description == "" {
			t.Fatalf("%s: no annotations or description", tool.Name)
		}
		isRead := slices.Contains(mcpReadTools, tool.Name)
		if tool.Annotations.ReadOnlyHint != isRead {
			t.Errorf("%s: ReadOnlyHint %v", tool.Name, tool.Annotations.ReadOnlyHint)
		}
		if !isRead && (tool.Annotations.DestructiveHint == nil || *tool.Annotations.DestructiveHint) {
			t.Errorf("%s: a write must say it is not destructive", tool.Name)
		}
	}
	want := slices.Concat(mcpReadTools, mcpWriteTools)
	slices.Sort(names)
	slices.Sort(want)
	if !slices.Equal(names, want) {
		t.Fatalf("tools %v\nwant %v", names, want)
	}
	if len(mcpReadTools) != mcpReadToolCount || len(mcpWriteTools) != mcpWriteToolCount {
		t.Fatalf("counts %d/%d, want %d/%d", len(mcpReadTools), len(mcpWriteTools), mcpReadToolCount, mcpWriteToolCount)
	}
}

// Chaque lecture est figée dans testdata/mcp_<outil>.golden.json sur les
// mêmes données que l'API ; `go test ./internal/server -update` les
// réécrit après vérification. Aucun secret n'y passe.
func TestMCP_ReadToolsMatchGoldenFilesAndLeakNoSecret(t *testing.T) {
	server := newTestServer(t)
	server.enroll(t, "vps-paris-1", remoteID)
	if _, _, err := server.machines.CreateToken(context.Background(), "vps-lyon-2"); err != nil {
		t.Fatal(err)
	}
	jobID := server.seedJobs(t)
	server.seedResources(t)
	serviceID := server.seedServices(t)
	probeID := server.seedProbes(t, serviceID)
	server.seedStatus(t, serviceID, jobID, probeID)
	_, alertID := server.seedAlerts(t)
	job, err := server.heartbeats.Get(context.Background(), jobID)
	if err != nil {
		t.Fatal(err)
	}
	session := connectMCP(t, server.mcpServer)
	cases := map[string]map[string]any{
		"get_overview":        {},
		"list_machines":       {},
		"get_machine":         {"machine_id": remoteID},
		"get_machine_network": {"machine_id": remoteID},
		"list_services":       {"limit": 1},
		"get_service":         {"service_id": serviceID},
		"list_jobs":           {},
		"get_job":             {"job_id": jobID},
		"list_probes":         {"machine_id": remoteID},
		"get_probe":           {"probe_id": probeID},
		"get_status_page":     {},
		"list_incidents":      {"status": "resolved"},
		"list_alerts":         {},
		"get_alert":           {"alert_id": alertID},
		"list_alert_channels": {},
	}
	secrets := []string{job.Token, "hb_", "/ping/", "s3cret", "webhooks/1/x", "oc_", "vps-lyon-2"}
	for name, args := range cases {
		t.Run(name, func(t *testing.T) {
			text, isError := callTool(t, session, name, args)
			if isError {
				t.Fatalf("refused: %s", text)
			}
			for _, secret := range secrets {
				if strings.Contains(text, secret) {
					t.Errorf("leaks %q", secret)
				}
			}
			compareGolden(t, filepath.Join("testdata", "mcp_"+name+".golden.json"), normalize(text)+"\n")
		})
	}
}

// Les journaux passent par l'agent : hors ligne, la clé de l'API.
func TestMCP_ServiceLogsRefuseLikeTheAPI(t *testing.T) {
	server := newTestServer(t)
	server.enroll(t, "vps-paris-1", remoteID)
	serviceID := server.seedServices(t)
	session := connectMCP(t, server.mcpServer)
	text, isError := callTool(t, session, "get_service_logs", map[string]any{"service_id": serviceID})
	if !isError || toolErrorCode(t, text) != "service.machine_offline" {
		t.Fatalf("%v %s", isError, text)
	}
	text, isError = callTool(t, session, "get_service_logs", map[string]any{"service_id": "nope"})
	if !isError || toolErrorCode(t, text) != codeNotFound {
		t.Fatalf("%v %s", isError, text)
	}
}

func TestMCP_WriteToolsGoThroughTheComponents(t *testing.T) {
	server := newTestServer(t)
	server.enroll(t, "vps-paris-1", remoteID)
	jobID := server.seedJobs(t)
	serviceID := server.seedServices(t)
	probeID := server.seedProbes(t, serviceID)
	componentID, incidentID := server.seedStatus(t, serviceID, jobID, probeID)
	_, alertID := server.seedAlerts(t)
	session := connectMCP(t, server.mcpServer)
	ctx := context.Background()

	text, isError := callTool(t, session, "acknowledge_alert", map[string]any{"alert_id": alertID})
	if isError || !strings.Contains(text, `"acknowledged_at":"2026-09-12T12:00:00Z"`) {
		t.Fatalf("acknowledge: %v %s", isError, text)
	}
	if _, isError = callTool(t, session, "acknowledge_alert", map[string]any{"alert_id": alertID}); isError {
		t.Fatal("acknowledging twice must be harmless")
	}
	if text, _ = callTool(t, session, "acknowledge_alert", map[string]any{"alert_id": 9999}); toolErrorCode(t, text) != codeNotFound {
		t.Fatalf("unknown alert: %s", text)
	}

	if text, isError = callTool(t, session, "resume_job", map[string]any{"job_id": jobID}); !isError || toolErrorCode(t, text) != "job.not_paused" {
		t.Fatalf("resume a running job: %s", text)
	}
	if text, isError = callTool(t, session, "pause_job", map[string]any{"job_id": jobID}); isError || !strings.Contains(text, `"status":"paused"`) {
		t.Fatalf("pause job: %s", text)
	}
	if text, isError = callTool(t, session, "resume_job", map[string]any{"job_id": jobID}); isError || strings.Contains(text, `"status":"paused"`) {
		t.Fatalf("resume job: %s", text)
	}

	if text, isError = callTool(t, session, "pause_probe", map[string]any{"probe_id": probeID}); isError || !strings.Contains(text, `"status":"paused"`) {
		t.Fatalf("pause probe: %s", text)
	}
	if text, isError = callTool(t, session, "resume_probe", map[string]any{"probe_id": probeID}); isError || !strings.Contains(text, `"status":"new"`) {
		t.Fatalf("resume probe: %s", text)
	}
	if text, _ = callTool(t, session, "resume_probe", map[string]any{"probe_id": probeID}); toolErrorCode(t, text) != "probe.not_paused" {
		t.Fatalf("resume twice: %s", text)
	}

	text, isError = callTool(t, session, "open_incident", map[string]any{
		"title": "Base lente", "impact": "degraded", "status": "investigating", "message": "On regarde.", "component_ids": []string{componentID},
	})
	if isError || !strings.Contains(text, `"impact":"degraded"`) {
		t.Fatalf("open incident: %s", text)
	}
	var opened incidentJSON
	if err := json.Unmarshal([]byte(text), &opened); err != nil {
		t.Fatal(err)
	}
	if text, _ = callTool(t, session, "open_incident", map[string]any{"title": "x", "impact": "meteor", "status": "investigating", "component_ids": []string{componentID}}); toolErrorCode(t, text) != "incident.impact_invalid" {
		t.Fatalf("bad impact: %s", text)
	}
	if text, _ = callTool(t, session, "open_incident", map[string]any{"title": "x", "impact": "maintenance", "status": "scheduled", "component_ids": []string{componentID}, "starts_at": "hier"}); toolErrorCode(t, text) != "incident.window_invalid" {
		t.Fatalf("bad instant: %s", text)
	}
	if text, isError = callTool(t, session, "add_incident_update", map[string]any{"incident_id": opened.ID, "status": "resolved", "message": "Réparé."}); isError || !strings.Contains(text, `"status":"resolved"`) {
		t.Fatalf("resolve: %s", text)
	}
	if text, _ = callTool(t, session, "add_incident_update", map[string]any{"incident_id": opened.ID, "status": "monitoring", "message": "Encore ?"}); toolErrorCode(t, text) != "incident.already_resolved" {
		t.Fatalf("update a resolved incident: %s", text)
	}
	if text, _ = callTool(t, session, "add_incident_update", map[string]any{"incident_id": incidentID, "status": "scheduled", "message": "x"}); toolErrorCode(t, text) != "incident.status_invalid" {
		t.Fatalf("bad status: %s", text)
	}

	text, isError = callTool(t, session, "create_silence", map[string]any{"kind": "job_late", "duration_minutes": 120, "reason": "migration"})
	if isError || !strings.Contains(text, `"kind":"job_late"`) {
		t.Fatalf("silence: %s", text)
	}
	if text, _ = callTool(t, session, "create_silence", map[string]any{"duration_minutes": 120}); toolErrorCode(t, text) != "silence.invalid" {
		t.Fatalf("silence everything: %s", text)
	}
	silences, err := server.alerts.Silences(ctx)
	if err != nil || len(silences) != 2 {
		t.Fatalf("silences %d %v", len(silences), err)
	}

	if text, isError = callTool(t, session, "set_update_policy", map[string]any{"service_id": serviceID, "policy": "pinned"}); isError || !strings.Contains(text, `"update_policy":"pinned"`) {
		t.Fatalf("policy: %s", text)
	}
	if text, _ = callTool(t, session, "set_update_policy", map[string]any{"service_id": serviceID, "policy": "never"}); toolErrorCode(t, text) != "update.policy_invalid" {
		t.Fatalf("bad policy: %s", text)
	}
}

// Sur stdio, les écritures restent listées et refusent avec leur clé.
func TestMCP_StdioIsReadOnly(t *testing.T) {
	server := newTestServer(t)
	server.enroll(t, "vps-paris-1", remoteID)
	jobID := server.seedJobs(t)
	session := connectMCP(t, server.newMCPServer(true))
	listed, err := session.ListTools(context.Background(), nil)
	if err != nil || len(listed.Tools) != mcpReadToolCount+mcpWriteToolCount {
		t.Fatalf("%d tools %v", len(listed.Tools), err)
	}
	if text, isError := callTool(t, session, "pause_job", map[string]any{"job_id": jobID}); !isError || toolErrorCode(t, text) != codeReadOnly {
		t.Fatalf("write on stdio: %v %s", isError, text)
	}
	if text, isError := callTool(t, session, "list_jobs", nil); isError || !strings.Contains(text, `"total":2`) {
		t.Fatalf("read on stdio: %s", text)
	}
	if job, _ := server.heartbeats.Get(context.Background(), jobID); job.IsPaused() {
		t.Fatal("the refused write was applied")
	}
}

// --- Le transport HTTP et son Bearer ---

// enableMCP active MCP par l'API et rend le secret montré une fois.
func (ts *testServer) enableMCP(t *testing.T) string {
	t.Helper()
	recorder := callAPI(ts.Server, http.MethodPost, "/api/mcp/actions/enable", nil)
	if recorder.Code != http.StatusCreated {
		t.Fatalf("enable: %d %s", recorder.Code, recorder.Body.String())
	}
	var response mcpSecretResponse
	decodeAPI(t, recorder, &response)
	return response.Secret
}

func (ts *testServer) createAPIToken(t *testing.T, name string) string {
	t.Helper()
	recorder := callAPI(ts.Server, http.MethodPost, "/api/mcp/tokens", mcpTokenRequest{Name: name})
	if recorder.Code != http.StatusCreated {
		t.Fatalf("create token: %d %s", recorder.Code, recorder.Body.String())
	}
	var response mcpTokenResponse
	decodeAPI(t, recorder, &response)
	return response.Token
}

// bearerClient est un client HTTP qui porte un Bearer sur chaque requête.
type bearerTransport struct {
	token string
	next  http.RoundTripper
}

func (b bearerTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if b.token != "" {
		r.Header.Set("Authorization", "Bearer "+b.token)
	}
	return b.next.RoundTrip(r)
}

// listToolsOverHTTP branche le client streamable du SDK sur le serveur
// réel et rend le nombre d'outils, ou l'erreur de connexion.
func listToolsOverHTTP(t *testing.T, endpoint, token string) (int, error) {
	t.Helper()
	ctx := context.Background()
	client := gomcp.NewClient(&gomcp.Implementation{Name: "test", Version: "0"}, nil)
	transport := &gomcp.StreamableClientTransport{Endpoint: endpoint, HTTPClient: &http.Client{Transport: bearerTransport{token: token, next: http.DefaultTransport}}}
	session, err := client.Connect(ctx, transport, nil)
	if err != nil {
		return 0, err
	}
	defer session.Close()
	listed, err := session.ListTools(ctx, nil)
	if err != nil {
		return 0, err
	}
	return len(listed.Tools), nil
}

func TestMCP_HTTPIsClosedWhenDisabledAndNeedsABearerWhenEnabled(t *testing.T) {
	server := newTestServer(t)
	liveServer := httptest.NewServer(server.Server)
	defer liveServer.Close()
	endpoint := liveServer.URL + mcpPath

	if got := get(server.Server, mcpPath); got.Code != http.StatusNotFound || errorCode(t, got) != codeNotFound {
		t.Fatalf("disabled: %d %s", got.Code, got.Body.String())
	}
	if got := get(server.Server, "/.well-known/oauth-authorization-server"); got.Code != http.StatusNotFound {
		t.Fatalf("disabled metadata: %d", got.Code)
	}
	server.enableMCP(t)
	got := get(server.Server, mcpPath)
	if got.Code != http.StatusUnauthorized || !strings.Contains(got.Header().Get("WWW-Authenticate"), `resource_metadata="http://example.com/.well-known/oauth-protected-resource"`) {
		t.Fatalf("no bearer: %d %q", got.Code, got.Header().Get("WWW-Authenticate"))
	}
	if _, err := listToolsOverHTTP(t, endpoint, ""); err == nil {
		t.Fatal("the SDK client connected without a bearer")
	}
	if _, err := listToolsOverHTTP(t, endpoint, "ock_"+strings.Repeat("a", 52)); err == nil {
		t.Fatal("an unknown token connected")
	}
	token := server.createAPIToken(t, "inspecteur")
	count, err := listToolsOverHTTP(t, endpoint, token)
	if err != nil || count != mcpReadToolCount+mcpWriteToolCount {
		t.Fatalf("with a token: %d %v", count, err)
	}
	// Révoqué, le même jeton ne passe plus.
	var state mcpResponse
	decodeAPI(t, callAPI(server.Server, http.MethodGet, "/api/mcp", nil), &state)
	if len(state.Tokens) != 1 || state.Tokens[0].LastUsedAt == nil {
		t.Fatalf("tokens %+v", state.Tokens)
	}
	if got := callAPI(server.Server, http.MethodDelete, "/api/mcp/tokens/"+state.Tokens[0].ID, nil); got.Code != http.StatusNoContent {
		t.Fatalf("revoke: %d", got.Code)
	}
	if _, err := listToolsOverHTTP(t, endpoint, token); err == nil {
		t.Fatal("a revoked token connected")
	}
}

func TestMCP_HTTPToolCallReturnsTheSameFactsAsTheAPI(t *testing.T) {
	server := newTestServer(t)
	server.enroll(t, "vps-paris-1", remoteID)
	liveServer := httptest.NewServer(server.Server)
	defer liveServer.Close()
	server.enableMCP(t)
	token := server.createAPIToken(t, "inspecteur")
	ctx := context.Background()
	client := gomcp.NewClient(&gomcp.Implementation{Name: "test", Version: "0"}, nil)
	transport := &gomcp.StreamableClientTransport{Endpoint: liveServer.URL + mcpPath, HTTPClient: &http.Client{Transport: bearerTransport{token: token, next: http.DefaultTransport}}}
	session, err := client.Connect(ctx, transport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	text, isError := callTool(t, session, "list_machines", nil)
	if isError {
		t.Fatalf("refused: %s", text)
	}
	var listed machinesToolResponse
	if err := json.Unmarshal([]byte(text), &listed); err != nil {
		t.Fatal(err)
	}
	var fromAPI machinesResponse
	decodeAPI(t, callAPI(server.Server, http.MethodGet, "/api/machines", nil), &fromAPI)
	if len(listed.Machines) != 2 || listed.Machines[1] != fromAPI.Machines[1] || listed.Machines[1].ID != remoteID {
		t.Fatalf("mcp %+v\napi %+v", listed.Machines, fromAPI.Machines)
	}
}

// --- L'API des réglages ---

func TestMCPAPI_EnableCreateTokensRedirectsDisable(t *testing.T) {
	server := newTestServer(t)
	var state mcpResponse
	decodeAPI(t, callAPI(server.Server, http.MethodGet, "/api/mcp", nil), &state)
	if state.Enabled || state.Endpoint != "http://example.com/mcp" || state.StdioCommand != "opencloud mcp -config /etc/opencloud/config.toml" || state.Tools.Reads != mcpReadToolCount {
		t.Fatalf("disabled state %+v", state)
	}
	if got := callAPI(server.Server, http.MethodPost, "/api/mcp/tokens", mcpTokenRequest{Name: "x"}); got.Code != http.StatusConflict || errorCode(t, got) != "mcp.disabled" {
		t.Fatalf("token while disabled: %d %s", got.Code, got.Body.String())
	}
	secret := server.enableMCP(t)
	if !strings.HasPrefix(secret, "ocs_") {
		t.Fatalf("secret %q", secret)
	}
	if got := callAPI(server.Server, http.MethodPost, "/api/mcp/actions/enable", nil); got.Code != http.StatusConflict || errorCode(t, got) != "mcp.already_enabled" {
		t.Fatalf("enable twice: %d", got.Code)
	}
	if got := callAPI(server.Server, http.MethodPost, "/api/mcp/tokens", mcpTokenRequest{Name: " "}); got.Code != http.StatusUnprocessableEntity || errorCode(t, got) != "mcp.name_invalid" {
		t.Fatalf("bad name: %d", got.Code)
	}
	server.createAPIToken(t, "inspecteur")
	if got := callAPI(server.Server, http.MethodPut, "/api/mcp/redirect-uris", mcpRedirectURIsRequest{RedirectURIs: []string{"claude.ai/cb"}}); got.Code != http.StatusUnprocessableEntity || errorCode(t, got) != "mcp.redirect_uri_invalid" {
		t.Fatalf("bad redirect: %d %s", got.Code, got.Body.String())
	}
	got := callAPI(server.Server, http.MethodPut, "/api/mcp/redirect-uris", mcpRedirectURIsRequest{RedirectURIs: []string{"https://claude.ai/api/mcp/auth_callback", ""}})
	if got.Code != http.StatusOK {
		t.Fatalf("redirects: %d %s", got.Code, got.Body.String())
	}
	decodeAPI(t, got, &state)
	if !state.Enabled || !strings.HasPrefix(state.SecretMasked, secret[:10]) || len(state.RedirectURIs) != 1 || len(state.Tokens) != 1 || state.Tokens[0].Name != "inspecteur" {
		t.Fatalf("state %+v", state)
	}
	if strings.Contains(got.Body.String(), secret) {
		t.Fatal("the secret must never be read back")
	}
	compareGolden(t, filepath.Join("testdata", "mcp.golden.json"), normalize(got.Body.String()))

	var regenerated mcpSecretResponse
	decodeAPI(t, callAPI(server.Server, http.MethodPost, "/api/mcp/actions/regenerate-secret", nil), &regenerated)
	if regenerated.Secret == "" || regenerated.Secret == secret {
		t.Fatalf("regenerated %q", regenerated.Secret)
	}
	if got := callAPI(server.Server, http.MethodPost, "/api/mcp/actions/disable", nil); got.Code != http.StatusNoContent {
		t.Fatalf("disable: %d", got.Code)
	}
	decodeAPI(t, callAPI(server.Server, http.MethodGet, "/api/mcp", nil), &state)
	if state.Enabled || len(state.Tokens) != 0 || state.SecretMasked != "" {
		t.Fatalf("after disable %+v", state)
	}
	if got := callAPI(server.Server, http.MethodPost, "/api/mcp/actions/regenerate-secret", nil); got.Code != http.StatusConflict {
		t.Fatalf("regenerate while disabled: %d", got.Code)
	}
}

// --- OAuth de bout en bout, comme un client MCP le joue ---

const (
	pkceVerifier  = "dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk"
	pkceChallenge = "E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM"
	localCallback = "http://localhost:33418/oauth/callback"
)

func postForm(server *Server, path string, form map[string]string, basic [2]string) *httptest.ResponseRecorder {
	values := make([]string, 0, len(form))
	for key, value := range form {
		values = append(values, key+"="+value)
	}
	request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(strings.Join(values, "&")))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if basic[0] != "" {
		request.SetBasicAuth(basic[0], basic[1])
	}
	recorder := httptest.NewRecorder()
	server.ServeHTTP(recorder, request)
	return recorder
}

func TestOAuth_DiscoveryAuthorizeExchangeRefreshAndReplay(t *testing.T) {
	server := newTestServer(t)
	secret := server.enableMCP(t)

	var metadata serverMetadata
	decodeAPI(t, get(server.Server, "/.well-known/oauth-authorization-server"), &metadata)
	if metadata.Issuer != "http://example.com" || metadata.AuthorizationEndpoint != "http://example.com/oauth/authorize" || metadata.TokenEndpoint != "http://example.com/oauth/token" || metadata.CodeChallengeMethodsSupported[0] != "S256" {
		t.Fatalf("metadata %+v", metadata)
	}
	var resource resourceMetadata
	decodeAPI(t, get(server.Server, "/.well-known/oauth-protected-resource/mcp"), &resource)
	if resource.Resource != "http://example.com/mcp" || resource.AuthorizationServers[0] != "http://example.com" {
		t.Fatalf("resource %+v", resource)
	}

	// Un redirect_uri non déclaré : 400, jamais de redirect.
	got := get(server.Server, "/oauth/authorize?response_type=code&client_id=opencloud&redirect_uri=https://evil.example/cb&code_challenge="+pkceChallenge+"&code_challenge_method=S256")
	if got.Code != http.StatusBadRequest || got.Header().Get("Location") != "" {
		t.Fatalf("evil redirect: %d %q", got.Code, got.Header().Get("Location"))
	}
	// Une demande fausse sur une URI acceptée : l'erreur part dans le redirect.
	got = get(server.Server, "/oauth/authorize?response_type=code&client_id=other&redirect_uri="+localCallback+"&state=xyz&code_challenge="+pkceChallenge+"&code_challenge_method=S256")
	if got.Code != http.StatusFound || !strings.Contains(got.Header().Get("Location"), "error=unauthorized_client") || !strings.Contains(got.Header().Get("Location"), "state=xyz") {
		t.Fatalf("bad client: %d %q", got.Code, got.Header().Get("Location"))
	}
	got = get(server.Server, "/oauth/authorize?response_type=code&client_id=opencloud&redirect_uri="+localCallback+"&state=xyz&code_challenge="+pkceChallenge+"&code_challenge_method=S256")
	if got.Code != http.StatusFound {
		t.Fatalf("authorize: %d %s", got.Code, got.Body.String())
	}
	location := got.Header().Get("Location")
	if !strings.HasPrefix(location, localCallback+"?") || !strings.Contains(location, "state=xyz") || !strings.Contains(location, "code=occ_") {
		t.Fatalf("location %q", location)
	}
	code := location[strings.Index(location, "code=")+5:]
	code, _, _ = strings.Cut(code, "&")

	// L'échange : mauvais secret, puis le bon.
	got = postForm(server.Server, "/oauth/token", map[string]string{"grant_type": "authorization_code", "client_id": "opencloud", "client_secret": "ocs_wrong", "code": code, "redirect_uri": localCallback, "code_verifier": pkceVerifier}, [2]string{})
	if got.Code != http.StatusUnauthorized || !strings.Contains(got.Body.String(), "invalid_client") {
		t.Fatalf("wrong secret: %d %s", got.Code, got.Body.String())
	}
	got = postForm(server.Server, "/oauth/token", map[string]string{"grant_type": "authorization_code", "client_id": "opencloud", "client_secret": secret, "code": code, "redirect_uri": localCallback, "code_verifier": pkceVerifier}, [2]string{})
	if got.Code != http.StatusOK || got.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("exchange: %d %s", got.Code, got.Body.String())
	}
	var first grantResponse
	decodeAPI(t, got, &first)
	if first.TokenType != "Bearer" || first.ExpiresIn != 3600 || !strings.HasPrefix(first.AccessToken, "oca_") || !strings.HasPrefix(first.RefreshToken, "ocr_") {
		t.Fatalf("grant %+v", first)
	}
	// Le code ne sert qu'une fois.
	got = postForm(server.Server, "/oauth/token", map[string]string{"grant_type": "authorization_code", "client_id": "opencloud", "client_secret": secret, "code": code, "redirect_uri": localCallback, "code_verifier": pkceVerifier}, [2]string{})
	if got.Code != http.StatusBadRequest || !strings.Contains(got.Body.String(), "invalid_grant") {
		t.Fatalf("code reuse: %d %s", got.Code, got.Body.String())
	}
	// L'accès ouvre /mcp ; le rafraîchissement, en Basic, le remplace.
	request := httptest.NewRequest(http.MethodGet, mcpPath, nil)
	request.Header.Set("Authorization", "Bearer "+first.AccessToken)
	recorder := httptest.NewRecorder()
	server.ServeHTTP(recorder, request)
	if recorder.Code == http.StatusUnauthorized {
		t.Fatalf("access token refused: %d %s", recorder.Code, recorder.Body.String())
	}
	got = postForm(server.Server, "/oauth/token", map[string]string{"grant_type": "refresh_token", "refresh_token": first.RefreshToken}, [2]string{"opencloud", secret})
	if got.Code != http.StatusOK {
		t.Fatalf("refresh: %d %s", got.Code, got.Body.String())
	}
	var second grantResponse
	decodeAPI(t, got, &second)
	var state mcpResponse
	decodeAPI(t, callAPI(server.Server, http.MethodGet, "/api/mcp", nil), &state)
	if len(state.Sessions) != 1 {
		t.Fatalf("sessions %+v", state.Sessions)
	}
	// Rejoué : la famille tombe, le second accès avec elle.
	got = postForm(server.Server, "/oauth/token", map[string]string{"grant_type": "refresh_token", "refresh_token": first.RefreshToken}, [2]string{"opencloud", secret})
	if got.Code != http.StatusBadRequest || !strings.Contains(got.Body.String(), "invalid_grant") {
		t.Fatalf("replay: %d %s", got.Code, got.Body.String())
	}
	request = httptest.NewRequest(http.MethodGet, mcpPath, nil)
	request.Header.Set("Authorization", "Bearer "+second.AccessToken)
	recorder = httptest.NewRecorder()
	server.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("access of a revoked family: %d", recorder.Code)
	}
	decodeAPI(t, callAPI(server.Server, http.MethodGet, "/api/mcp", nil), &state)
	if len(state.Sessions) != 0 {
		t.Fatalf("sessions after replay %+v", state.Sessions)
	}
	if got := postForm(server.Server, "/oauth/token", map[string]string{"grant_type": "password"}, [2]string{}); got.Code != http.StatusBadRequest || !strings.Contains(got.Body.String(), "unsupported_grant_type") {
		t.Fatalf("grant type: %d %s", got.Code, got.Body.String())
	}
}

func TestOAuth_TokenEndpointIsRateLimitedBySource(t *testing.T) {
	server := newTestServer(t)
	server.enableMCP(t)
	for range mcpTokenBurst {
		if got := postForm(server.Server, "/oauth/token", map[string]string{"grant_type": "refresh_token", "refresh_token": "x", "client_id": "opencloud", "client_secret": "y"}, [2]string{}); got.Code != http.StatusUnauthorized {
			t.Fatalf("within the burst: %d", got.Code)
		}
	}
	if got := postForm(server.Server, "/oauth/token", map[string]string{"grant_type": "refresh_token", "refresh_token": "x"}, [2]string{}); got.Code != http.StatusTooManyRequests || got.Header().Get("Retry-After") == "" {
		t.Fatalf("beyond the burst: %d", got.Code)
	}
}

// Les sessions se coupent depuis Paramètres.
func TestMCPAPI_RevokeSessionCutsTheClient(t *testing.T) {
	server := newTestServer(t)
	secret := server.enableMCP(t)
	got := get(server.Server, "/oauth/authorize?response_type=code&client_id=opencloud&redirect_uri="+localCallback+"&code_challenge="+pkceChallenge+"&code_challenge_method=S256")
	location := got.Header().Get("Location")
	code := location[strings.Index(location, "code=")+5:]
	var grant grantResponse
	decodeAPI(t, postForm(server.Server, "/oauth/token", map[string]string{"grant_type": "authorization_code", "client_id": "opencloud", "client_secret": secret, "code": code, "redirect_uri": localCallback, "code_verifier": pkceVerifier}, [2]string{}), &grant)
	var state mcpResponse
	decodeAPI(t, callAPI(server.Server, http.MethodGet, "/api/mcp", nil), &state)
	if len(state.Sessions) != 1 {
		t.Fatalf("sessions %+v", state.Sessions)
	}
	if got := callAPI(server.Server, http.MethodDelete, "/api/mcp/sessions/"+state.Sessions[0].ID, nil); got.Code != http.StatusNoContent {
		t.Fatalf("revoke session: %d", got.Code)
	}
	if got := callAPI(server.Server, http.MethodDelete, "/api/mcp/sessions/"+state.Sessions[0].ID, nil); got.Code != http.StatusNotFound {
		t.Fatalf("revoke twice: %d", got.Code)
	}
	request := httptest.NewRequest(http.MethodGet, mcpPath, nil)
	request.Header.Set("Authorization", "Bearer "+grant.AccessToken)
	recorder := httptest.NewRecorder()
	server.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("cut session still passes: %d", recorder.Code)
	}
}

// Un silence créé par MCP se lit ensuite par l'API, comme tout le reste.
func TestMCP_ChangesAreVisibleToTheAPI(t *testing.T) {
	server := newTestServer(t)
	session := connectMCP(t, server.mcpServer)
	if _, isError := callTool(t, session, "create_silence", map[string]any{"kind": string(alert.KindJobLate), "duration_minutes": 30}); isError {
		t.Fatal("silence refused")
	}
	var silences silencesResponse
	decodeAPI(t, callAPI(server.Server, http.MethodGet, "/api/alerts/silences", nil), &silences)
	if len(silences.Silences) != 1 || silences.Silences[0].Kind != string(alert.KindJobLate) {
		t.Fatalf("silences %+v", silences.Silences)
	}
}
