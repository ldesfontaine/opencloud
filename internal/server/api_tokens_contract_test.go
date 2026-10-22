package server

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/ldesfontaine/opencloud/internal/agentwire"
	"github.com/ldesfontaine/opencloud/internal/audit"
	"github.com/ldesfontaine/opencloud/internal/container"
)

// Le contrat avec mcp-maison (main @ 3f24e6a, src/tools/opencloud) : ses
// appels, rejoués tels qu'il les fait (client.ts : Authorization: Bearer,
// Accept: application/json, ses deux en-têtes d'identité, le Host de
// OPENCLOUD_URL), et les champs que tools.ts lit dans chaque réponse.
// Un champ renommé ici casserait un outil de mcp-maison sans que rien ne le
// dise de ce côté-ci.

// mcpMaisonUser est ce que mcp-maison pose (downstream.ts, toHeaderValue) :
// de l'ASCII imprimable, 200 caractères au plus.
const mcpMaisonUser = "f3b2a1c0-sub-oidc"

// mcpMaisonCall rejoue une requête de mcp-maison : rien d'autre que ce que
// client.ts envoie, ni Sec-Fetch-Site ni identifiants de l'opérateur.
func mcpMaisonCall(server *Server, bearer, path string, query url.Values) *httptest.ResponseRecorder {
	target := path
	if len(query) > 0 {
		target += "?" + query.Encode()
	}
	request := httptest.NewRequest(http.MethodGet, target, nil)
	request.Host = "127.0.0.1:8080"
	request.Header.Set("X-MCP-Maison-User", mcpMaisonUser)
	request.Header.Set("X-MCP-Maison-User-Name", "Alice Martin")
	request.Header.Set("Authorization", "Bearer "+bearer)
	request.Header.Set("Accept", "application/json")
	recorder := httptest.NewRecorder()
	server.ServeHTTP(recorder, request)
	return recorder
}

// requireFields vérifie qu'un JSON porte un chemin que mcp-maison lit.
// « a[] » : un tableau, jamais null (l'outil y appelle map, slice ou at) ;
// « a? » : présent, null admis (l'outil teste avant de lire dessous) ;
// « a » : présent.
func requireFields(t *testing.T, where string, value any, path string) {
	t.Helper()
	head, rest, _ := strings.Cut(path, ".")
	object, ok := value.(map[string]any)
	if !ok {
		t.Errorf("%s: %q: un objet attendu, %T reçu", where, path, value)
		return
	}
	key := strings.TrimSuffix(strings.TrimSuffix(head, "[]"), "?")
	field, present := object[key]
	if !present {
		t.Errorf("%s: champ %q absent", where, key)
		return
	}
	switch {
	case strings.HasSuffix(head, "[]"):
		items, isArray := field.([]any)
		if !isArray {
			t.Errorf("%s: %q est %T, un tableau attendu", where, key, field)
			return
		}
		for _, item := range items {
			if rest != "" {
				requireFields(t, where, item, rest)
			}
		}
	case strings.HasSuffix(head, "?"):
		if field != nil && rest != "" {
			requireFields(t, where, field, rest)
		}
	default:
		if rest != "" {
			requireFields(t, where, field, rest)
		}
	}
}

// requireNonEmpty dit qu'un tableau du premier niveau a des éléments : sans
// eux, requireFields ne vérifierait rien en dessous.
func requireNonEmpty(t *testing.T, where string, body map[string]any, keys ...string) {
	t.Helper()
	for _, key := range keys {
		if items, _ := body[key].([]any); len(items) == 0 {
			t.Errorf("%s: %q vide, le banc ne vérifie rien dessous", where, key)
		}
	}
}

func TestAPIToken_ReplaysMCPMaisonCalls(t *testing.T) {
	server := newTestServer(t)
	enrolled, private := server.enroll(t, "vps-paris-1", remoteID)
	jobID := server.seedJobs(t)
	containerID := server.seedContainers(t)
	declaredID := server.seedGaps(t)
	probeID := server.seedProbes(t, declaredID)
	server.seedStatus(t, declaredID, jobID, probeID)
	pageID := server.seededPageID(t)
	_, alertID := server.seedAlerts(t)
	server.seedActions(t)
	bearer := server.issueAPIToken(t, "mcp-maison")

	read := func(path string, query url.Values) map[string]any {
		t.Helper()
		got := mcpMaisonCall(server.Server, bearer, path, query)
		if got.Code != http.StatusOK {
			t.Fatalf("%s: %d %s", path, got.Code, got.Body.String())
		}
		var body map[string]any
		if err := json.Unmarshal(got.Body.Bytes(), &body); err != nil {
			t.Fatalf("%s: %v %s", path, err, got.Body.String())
		}
		return body
	}
	check := func(where string, body map[string]any, paths ...string) {
		t.Helper()
		for _, path := range paths {
			requireFields(t, where, body, path)
		}
	}
	machineFields := []string{"id", "name", "hostname", "address", "os", "arch", "online", "last_seen_at", "agent?.version"}
	jobFields := []string{"id", "name", "machine_name", "status", "interval_seconds", "grace_seconds", "last_ping_at", "next_deadline_at", "last_exit_code", "last_duration_ms"}
	probeFields := []string{"id", "name", "kind", "target", "machine_name", "machine_online", "service_name", "status", "last_checked_at", "last_code", "last_reason", "last_duration_ms", "certificate?.not_after", "certificate?.chain_valid", "certificate?.hostname_match"}
	containerRow := []string{"id", "machine_name", "name", "group", "image", "state", "health", "exit_code", "restart_count", "image_check?.kind", "exposure[].kind"}
	alertFields := []string{"id", "kind", "severity", "status", "muted", "object.kind", "object.id", "object.name", "machine_name", "details", "opened_at", "acknowledged_at", "resolved_at"}
	prefixed := func(prefix string, fields []string) []string {
		out := make([]string, 0, len(fields))
		for _, field := range fields {
			out = append(out, prefix+field)
		}
		return out
	}

	// opencloud_overview
	read("/api/counts", nil)

	// opencloud_machines : liste, fiche, réseau.
	machines := read("/api/machines", nil)
	requireNonEmpty(t, "machines", machines, "machines")
	check("machines", machines, prefixed("machines[].", machineFields)...)
	check("machine", read("/api/machines/"+url.PathEscape(remoteID), nil),
		append(machineFields, "agent?.revoked_at", "agent?.capabilities[].name", "agent?.capabilities_differ")...)
	network := read("/api/machines/"+url.PathEscape(remoteID)+"/network", nil)
	requireNonEmpty(t, "network", network, "containers", "groups")
	check("network", network, "machine_name", "online", "engine?.present", "engine?.version", "engine?.reason",
		"containers[].id", "containers[].name", "containers[].state", "containers[].ports", "containers[].exposure",
		"groups[].name", "groups[].internal", "groups[].members[]", "edges[].from", "edges[].to", "edges[].kind")

	// opencloud_containers : liste, fiche.
	containers := read("/api/containers", nil)
	requireNonEmpty(t, "containers", containers, "containers")
	check("containers", containers, prefixed("containers[].", containerRow)...)
	detail := read("/api/containers/"+url.PathEscape(containerID), nil)
	check("container", detail, prefixed("container.", append(containerRow,
		"oom_killed", "started_at", "finished_at", "compose_service", "update_policy",
		"image_check?.newer_tag", "image_check?.target", "ports", "networks[].name", "depends_on[].name", "current"))...)
	check("container", detail, "engine?.version", "engine?.present", "directory?.declared", "directory?.service_name", "directory?.unit_name",
		"transitions[].at", "transitions[].action", "transitions[].new_state", "transitions[].new_health", "transitions[].exit_code")

	// opencloud_container_logs : un tirage unique, que l'agent sert.
	t.Run("logs", func(t *testing.T) {
		resp, session, agentStream, cancel := openStream(t, server, enrolled.ID, private)
		defer resp.Body.Close()
		defer cancel()
		answered := make(chan *httptest.ResponseRecorder, 1)
		go func() {
			answered <- mcpMaisonCall(server.Server, bearer, "/api/containers/"+url.PathEscape(containerID)+"/logs", url.Values{"tail": {"100"}})
		}()
		var command container.LogRequest
		if err := json.Unmarshal([]byte(readCommand(t, agentStream, string(agentwire.KindLogs))), &command); err != nil {
			t.Fatal(err)
		}
		if command.Tail != 100 || command.Follow {
			t.Fatalf("command %+v", command)
		}
		batch := container.LogBatch{Lines: []container.LogLine{{At: testNow, Stream: "stdout", Text: "ready"}, {Stream: "stderr", Text: "oops"}}}
		for _, payload := range []container.LogBatch{batch, {Done: true}} {
			if got := server.agentPost(session, "/agent/logs/"+command.ID, payload); got.Code != http.StatusNoContent {
				t.Fatalf("deliver: %d %s", got.Code, got.Body.String())
			}
		}
		got := <-answered
		if got.Code != http.StatusOK {
			t.Fatalf("logs: %d %s", got.Code, got.Body.String())
		}
		var logs map[string]any
		if err := json.Unmarshal(got.Body.Bytes(), &logs); err != nil {
			t.Fatal(err)
		}
		requireNonEmpty(t, "logs", logs, "lines")
		check("logs", logs, "lines[].at", "lines[].stream", "lines[].text")
	})

	// opencloud_directory : la liste, et l'export lu par getText malgré
	// Accept: application/json.
	services := read("/api/directory/services", nil)
	requireNonEmpty(t, "services", services, "services")
	check("services", services, prefixed("services[].", []string{"id", "machine_name", "name", "title", "nature", "compose_project", "public_url",
		"units[].name", "units[].kind", "units[].image", "units[].version", "units[].container_name", "units[].systemd_unit"})...)
	exported := mcpMaisonCall(server.Server, bearer, "/api/manifest/export", nil)
	if exported.Code != http.StatusOK || !strings.HasPrefix(exported.Header().Get("Content-Type"), "application/yaml") {
		t.Fatalf("export: %d %q", exported.Code, exported.Header().Get("Content-Type"))
	}
	var manifest map[string]any
	if err := yaml.Unmarshal(exported.Body.Bytes(), &manifest); err != nil || len(manifest) == 0 || bytes.HasPrefix(bytes.TrimSpace(exported.Body.Bytes()), []byte("{")) {
		t.Fatalf("export is not the YAML file: %v\n%s", err, exported.Body.String())
	}

	// opencloud_jobs : liste, fiche (sans ping_url ni snippets).
	jobs := read("/api/jobs", nil)
	requireNonEmpty(t, "jobs", jobs, "jobs")
	check("jobs", jobs, prefixed("jobs[].", jobFields)...)
	check("job", read("/api/jobs/"+url.PathEscape(jobID), nil), append(prefixed("job.", jobFields),
		"runs[].started_at", "runs[].duration_ms", "runs[].exit_code", "runs[].outcome",
		"pings[].kind", "pings[].received_at", "pings[].exit_code")...)

	// opencloud_probes : liste avec ses jours, fiche avec ses fenêtres.
	probes := read("/api/probes", nil)
	requireNonEmpty(t, "probes", probes, "probes")
	check("probes", probes, append(prefixed("probes[].", probeFields), "days")...)
	check("probe", read("/api/probes/"+url.PathEscape(probeID), nil), append(prefixed("probe.", probeFields),
		"probe.certificate?", "uptime[].window", "uptime[].total", "uptime[].success",
		"results[].checked_at", "results[].outcome", "results[].duration_ms", "results[].code", "results[].reason")...)

	// opencloud_status : incidents d'une page, fiche et composants, liste.
	incidents := read("/api/status/incidents", url.Values{"page": {pageID}})
	requireNonEmpty(t, "incidents", incidents, "incidents")
	check("incidents", incidents, prefixed("incidents[].", []string{"id", "title", "impact", "status", "starts_at", "ends_at", "resolved_at",
		"components[].name", "updates[].status", "updates[].message", "updates[].created_at"})...)
	pageFields := []string{"id", "slug", "title", "announcement", "public_url"}
	check("page", read("/api/status/pages/"+url.PathEscape(pageID), nil), pageFields...)
	components := read("/api/status/components", url.Values{"page": {pageID}})
	requireNonEmpty(t, "components", components, "components")
	check("components", components, "global", "components[].name", "components[].effective",
		"components[].members[].kind", "components[].members[].name", "components[].members[].state")
	pages := read("/api/status/pages", nil)
	requireNonEmpty(t, "pages", pages, "pages")
	check("pages", pages, prefixed("pages[].", pageFields)...)

	// opencloud_alerts : canaux, fiche, liste.
	channels := read("/api/alerts/channels", nil)
	requireNonEmpty(t, "channels", channels, "channels")
	check("channels", channels, prefixed("channels[].", []string{"id", "name", "format", "url", "min_severity", "notify_resolve", "enabled", "plain"})...)
	check("alert", read("/api/alerts/"+strconv.FormatInt(alertID, 10), nil), append(prefixed("alert.", alertFields),
		"deliveries[].channel_id", "deliveries[].event", "deliveries[].status", "deliveries[].attempts", "deliveries[].reason", "deliveries[].code")...)
	alerts := read("/api/alerts", nil)
	requireNonEmpty(t, "alerts", alerts, "alerts")
	check("alerts", alerts, append(prefixed("alerts[].", alertFields), "counts.open", "counts.unacknowledged")...)

	// opencloud_audit : la page par défaut, puis filtrée comme l'outil le fait.
	events := read("/api/audit", url.Values{"limit": {"20"}})
	requireNonEmpty(t, "audit", events, "events")
	check("audit", events, "next_before", "events[].id", "events[].at", "events[].actor_kind", "events[].actor_name",
		"events[].action", "events[].object_id", "events[].object_name", "events[].details")
	read("/api/audit", url.Values{"limit": {"20"}, "before": {"999999"}, "object_kind": {"probe"}, "object_id": {probeID}})

	// opencloud_actions : tous, puis une machine.
	runs := read("/api/actions", url.Values{"limit": {"10"}})
	requireNonEmpty(t, "actions", runs, "runs")
	check("actions", runs, prefixed("runs[].", []string{"id", "kind", "machine_name", "state", "reason", "requested_by", "requested_at", "finished_at", "params", "output[].line"})...)
	read("/api/actions", url.Values{"machine": {remoteID}, "limit": {"10"}})

	// Les refus, tels que client.ts les traite : {"error":"<clé>"} en 401,
	// 403 et 404 ; un identifiant encodé (segment()) ne change pas de route.
	for _, refusal := range []struct {
		bearer, path string
		status       int
		key          string
	}{
		{bearer, "/api/jobs/" + url.PathEscape("a/b"), http.StatusNotFound, codeNotFound},
		{bearer, "/api/machines/" + url.PathEscape("x/network"), http.StatusNotFound, codeNotFound},
		{bearer, "/api/settings/api-tokens", http.StatusForbidden, codeAPITokenRouteForbidden},
		{"ock_" + strings.Repeat("9", 52), "/api/counts", http.StatusUnauthorized, codeAPITokenInvalid},
	} {
		got := mcpMaisonCall(server.Server, refusal.bearer, refusal.path, nil)
		if got.Code != refusal.status || errorCode(t, got) != refusal.key {
			t.Errorf("%s: %d %s, want %d %s", refusal.path, got.Code, got.Body.String(), refusal.status, refusal.key)
		}
	}
}

// Le 429 que client.ts lit : Retry-After en secondes (parseRetryAfter), un
// corps court sans le jeton.
func TestAPIToken_RateLimitAnswerIsWhatMCPMaisonReads(t *testing.T) {
	server := newTestServer(t)
	wrong := "ock_" + strings.Repeat("7", 52)
	var got *httptest.ResponseRecorder
	for range authAttemptBurst + 1 {
		got = mcpMaisonCall(server.Server, wrong, "/api/counts", nil)
	}
	if got.Code != http.StatusTooManyRequests {
		t.Fatalf("after the burst: %d", got.Code)
	}
	retry := got.Header().Get("Retry-After")
	if retry == "" || strings.Trim(retry, "0123456789") != "" {
		t.Fatalf("Retry-After %q is not a number of seconds", retry)
	}
	if strings.Contains(got.Body.String(), wrong) {
		t.Fatal("the refused token is echoed")
	}
}

// L'en-tête d'identité arrive tel que mcp-maison l'écrit, à travers la garde
// de l'opérateur entière : un sub de 200 caractères ASCII reste entier, le
// nom affiché (X-MCP-Maison-User-Name) n'est pas lu.
func TestAPIToken_OnBehalfOfReadsTheHeaderMCPMaisonSends(t *testing.T) {
	server := newTestServer(t)
	bearer := server.issueAPIToken(t, "mcp-maison")
	var seen audit.Actor
	handler := server.requireOperator(server.withOperatorActor(func(w http.ResponseWriter, r *http.Request) {
		seen = audit.ActorFrom(r.Context())
		w.WriteHeader(http.StatusNoContent)
	}), false, true)
	sub := strings.Repeat("a1? -", 40) // 200 caractères, ce que toHeaderValue peut rendre
	request := httptest.NewRequest(http.MethodGet, "/api/counts", nil)
	request.Header.Set("X-MCP-Maison-User", sub)
	request.Header.Set("X-MCP-Maison-User-Name", "Alice Martin")
	request.Header.Set("Authorization", "Bearer "+bearer)
	recorder := httptest.NewRecorder()
	handler(recorder, request)
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("status %d %s", recorder.Code, recorder.Body.String())
	}
	if seen.Kind != audit.ActorAPI || seen.OnBehalfOf != strings.TrimSpace(sub) {
		t.Fatalf("actor %+v", seen)
	}
}

// Un jeton n'ouvre que les motifs de la liste : ni la casse, ni une barre
// finale, ni un chemin encodé, ni HEAD, ni un flux, ni une page, ni les
// routes de l'agent ne l'élargissent. HEAD sur une lecture admise reste une
// lecture.
func TestAPIToken_PathVariantsDoNotWidenTheList(t *testing.T) {
	server := newTestServer(t)
	bearer := server.issueAPIToken(t, "mcp-maison")
	for _, attempt := range []struct{ method, path string }{
		{http.MethodHead, "/api/settings/mcp"},
		{http.MethodHead, "/api/settings/api-tokens"},
		{http.MethodGet, "/API/settings/mcp"},
		{http.MethodGet, "/api/Settings/mcp"},
		{http.MethodGet, "/api/settings/mcp/"},
		{http.MethodGet, "/api/settings%2Fmcp"},
		{http.MethodGet, "/api/settings/api-tokens"},
		{http.MethodGet, "/api/auth"},
		{http.MethodPost, "/api/auth/actions/enable"},
		{http.MethodPost, "/api/counts"},
		{http.MethodGet, "/api/counts/"},
		{http.MethodGet, "/api/events"},
		{http.MethodGet, "/api/containers/x/logs/stream"},
		{http.MethodGet, "/api/agents/server-key"},
		{http.MethodGet, "/api/machines/x/delete-plan"},
		{http.MethodGet, "/api/nope"},
		{http.MethodGet, "/"},
		{http.MethodGet, "/machines"},
		{http.MethodGet, "/parametres"},
	} {
		got := callBearerWith(server.Server, attempt.method, attempt.path, bearer, "")
		if got.Code != http.StatusForbidden {
			t.Errorf("%s %s: %d %s, want 403", attempt.method, attempt.path, got.Code, got.Body.String())
		}
	}
	// Un chemin à nettoyer est redirigé avant toute garde, sans réponse de
	// handler ; la cible redirigée reste refusée ci-dessus.
	for _, path := range []string{"/api/./settings/mcp", "//api/settings/mcp", "/api/counts/../settings/mcp"} {
		if got := callBearerWith(server.Server, http.MethodGet, path, bearer, ""); got.Code < 300 || got.Code >= 400 {
			t.Errorf("GET %s: %d %s, want a redirect", path, got.Code, got.Body.String())
		}
	}
	// Les routes de l'agent ont leur propre preuve : un jeton d'API n'en est pas une.
	for _, path := range []string{"/agent/stream", "/agent/parcels/x/y"} {
		if got := callBearerWith(server.Server, http.MethodGet, path, bearer, ""); got.Code < 400 {
			t.Errorf("GET %s: %d, want a refusal", path, got.Code)
		}
	}
	if got := callBearerWith(server.Server, http.MethodHead, "/api/counts", bearer, ""); got.Code != http.StatusOK {
		t.Errorf("HEAD /api/counts: %d", got.Code)
	}
}

// Le jeton présenté n'apparaît dans aucun journal du processus, refusé,
// admis ou hors de sa liste, même au niveau debug.
func TestAPIToken_NeverLogged(t *testing.T) {
	server := newTestServer(t)
	var journal bytes.Buffer
	server.logger = slog.New(slog.NewTextHandler(&journal, &slog.HandlerOptions{Level: slog.LevelDebug}))
	good := server.issueAPIToken(t, "mcp-maison")
	wrong := "ock_" + strings.Repeat("5", 52)
	for _, call := range []struct{ bearer, path string }{
		{good, "/api/counts"}, {good, "/api/settings/mcp"}, {wrong, "/api/counts"}, {wrong, "/api/settings/mcp"},
	} {
		callBearer(server.Server, call.path, call.bearer)
	}
	if !strings.Contains(journal.String(), "api token refused") {
		t.Fatalf("the refusal is not journaled:\n%s", journal.String())
	}
	for _, secret := range []string{good, wrong, good[len(good)-20:], wrong[len(wrong)-20:]} {
		if strings.Contains(journal.String(), secret) {
			t.Fatalf("a token reached the journal:\n%s", journal.String())
		}
	}
}
