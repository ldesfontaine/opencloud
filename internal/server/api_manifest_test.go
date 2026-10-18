package server

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/ldesfontaine/opencloud/internal/directory"
	"github.com/ldesfontaine/opencloud/internal/heartbeat"
	"github.com/ldesfontaine/opencloud/internal/lang"
	"github.com/ldesfontaine/opencloud/internal/live"
	"github.com/ldesfontaine/opencloud/internal/manifest"
	"github.com/ldesfontaine/opencloud/internal/probe"
	"github.com/ldesfontaine/opencloud/internal/store"
)

var (
	// Les identifiants tirés à chaque création : l'annuaire préfixe les siens,
	// les sondes et les tâches écrivent seize hexadécimaux.
	yamlDirectoryID = regexp.MustCompile(`\b(svc|prj)_[0-9a-f]{16}\b`)
	yamlShortID     = regexp.MustCompile(`(?m)^(\s+- id|\s+id): [0-9a-f]{16}$`)
)

func normalizeYAML(body string) string {
	body = yamlDirectoryID.ReplaceAllString(body, "${1}_ID")
	return yamlShortID.ReplaceAllString(body, "${1}: ID")
}

// seedManifest pose de quoi remplir chaque section du fichier : des
// services de trois origines, un projet, une sonde sur un service, une tâche
// et une clé ignorée.
func (ts *testServer) seedManifest(t *testing.T) (jobToken string) {
	t.Helper()
	ts.enroll(t, "vps-paris-1", remoteID)
	ts.seedDirectory(t)
	jobID := ts.seedJobs(t)
	services, err := ts.directory.ListServices(context.Background(), directory.Filter{})
	if err != nil {
		t.Fatal(err)
	}
	for _, service := range services {
		if service.Name == "nextcloud" {
			ts.seedProbes(t, service.ID)
		}
	}
	if err := ts.directory.Ignore(context.Background(), directory.IgnoredKey{MachineID: remoteID, Kind: directory.IgnoreComposeProject, Value: "scratch"}); err != nil {
		t.Fatal(err)
	}
	job, err := ts.heartbeats.Get(context.Background(), jobID)
	if err != nil {
		t.Fatal(err)
	}
	return job.Token
}

func TestManifestAPI_ExportMatchesGolden(t *testing.T) {
	server := newTestServer(t)
	jobToken := server.seedManifest(t)

	recorder := callAPI(server.Server, http.MethodGet, "/api/manifest/export", nil)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status %d: %s", recorder.Code, recorder.Body.String())
	}
	if got := recorder.Header().Get("Content-Type"); got != "application/yaml; charset=utf-8" {
		t.Fatalf("Content-Type %q", got)
	}
	if got := recorder.Header().Get("Content-Disposition"); got != `attachment; filename="opencloud.yaml"` {
		t.Fatalf("Content-Disposition %q", got)
	}
	body := recorder.Body.String()
	if strings.Contains(body, jobToken) || strings.Contains(body, "hb_") {
		t.Fatal("the export must never carry a job token")
	}
	if file, problems := manifest.Parse(recorder.Body.Bytes()); len(problems) > 0 || len(file.Services) != 3 || len(file.Probes) != 2 || len(file.Heartbeats) != 2 {
		t.Fatalf("the export does not read back: %+v %+v", file, problems)
	}
	compareGolden(t, filepath.Join("testdata", "manifest_export.golden.yaml"), normalizeYAML(body))
}

func TestManifestAPI_ExportCommentsFollowTheInterfaceLanguage(t *testing.T) {
	server := newTestServer(t)
	server.seedManifest(t)
	french := callAPI(server.Server, http.MethodGet, "/api/manifest/export", nil).Body.String()
	if !strings.Contains(french, "# Les identifiants (id) rattachent") {
		t.Fatalf("french header:\n%s", french)
	}
	if got := callAPI(server.Server, http.MethodPut, "/api/session/language", map[string]string{"language": "en"}); got.Code != http.StatusNoContent {
		t.Fatalf("language: %d", got.Code)
	}
	english := callAPI(server.Server, http.MethodGet, "/api/manifest/export", nil).Body.String()
	if !strings.Contains(english, "# The ids tie each entry to its object") || strings.Contains(english, "rattachent") {
		t.Fatalf("english header:\n%s", english)
	}
	if normalizeYAML(french[strings.Index(french, "version: 1"):]) != normalizeYAML(english[strings.Index(english, "version: 1"):]) {
		t.Fatal("the language changes the comments only, never the data")
	}
}

func TestManifestAPI_ExportIsAnOperatorRoute(t *testing.T) {
	server := newTestServer(t)
	server.enableBasic(t)
	if got := callAs(server.Server, http.MethodGet, "/api/manifest/export", "", ""); got.Code != http.StatusUnauthorized {
		t.Fatalf("without credentials: %d", got.Code)
	}
	if got := callAs(server.Server, http.MethodGet, "/api/manifest/export", testUsername, testPassword); got.Code != http.StatusOK {
		t.Fatalf("with credentials: %d", got.Code)
	}
}

// La socket de commande ne sert que le fichier de l'annuaire : tout le reste
// de l'API, l'interface, les agents, les pings et MCP y répondent 404, et
// l'authentification de l'interface n'a pas à s'y jouer (qui atteint la
// socket peut déjà lire la base).
func TestControlSocket_ServesOnlyTheManifest(t *testing.T) {
	server := newTestServer(t)
	server.seedManifest(t)
	server.enableBasic(t)

	call := func(method, path string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(method, "http://control"+path, nil)
		recorder := httptest.NewRecorder()
		server.ControlHandler().ServeHTTP(recorder, request)
		return recorder
	}
	exported := call(http.MethodGet, "/api/manifest/export")
	if exported.Code != http.StatusOK || !strings.HasPrefix(exported.Body.String(), "# ") {
		t.Fatalf("export: %d %s", exported.Code, exported.Body.String())
	}
	if main := callAs(server.Server, http.MethodGet, "/api/manifest/export", "", ""); main.Code != http.StatusUnauthorized {
		t.Fatalf("the TCP side stays guarded: %d", main.Code)
	}
	for _, path := range []string{
		"/api/machines", "/api/session", "/api/directory/services", "/api/probes", "/api/jobs", "/api/mcp",
		"/api/manifest", "/api/manifest/", "/api/manifest/unknown", "/api/events",
		"/", "/machines", "/statut", "/mcp", "/agent/stream", "/ping/x", "/oauth/token", "/.well-known/oauth-authorization-server",
	} {
		if got := call(http.MethodGet, path); got.Code != http.StatusNotFound {
			t.Errorf("GET %s: %d, want 404", path, got.Code)
		}
	}
	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodDelete} {
		if got := call(method, "/api/manifest/export"); got.Code == http.StatusOK {
			t.Errorf("%s /api/manifest/export must not export", method)
		}
		if got := call(method, "/api/directory/services"); got.Code != http.StatusNotFound {
			t.Errorf("%s /api/directory/services: %d, want 404", method, got.Code)
		}
	}
}

func TestControlSocket_AnswersWithTheChainOfTheServer(t *testing.T) {
	server := newTestServer(t)
	got := httptest.NewRecorder()
	server.ControlHandler().ServeHTTP(got, httptest.NewRequest(http.MethodGet, "http://control/api/manifest/export", nil))
	if got.Header().Get("X-Request-ID") == "" {
		t.Fatal("a request on the socket gets its id like any other")
	}
}

// Chaque refus de lecture a son texte dans les deux langues, qui prend
// exactement les arguments que le refus donne : un « %d » de trop afficherait
// « %!d(MISSING) » à l'opérateur.
func TestManifest_ProblemKeysExistInEveryCatalogWithTheirArguments(t *testing.T) {
	server := newTestServer(t)
	for _, key := range manifest.ProblemKeys() {
		problem := manifest.Problem{Key: key, Line: 12, Value: "valeur"}
		for _, code := range lang.Codes() {
			catalog := server.catalogs.For(code)
			text := catalog.Get(key)
			if strings.HasPrefix(text, "[") {
				t.Errorf("la clé %q manque dans %s", key, code)
				continue
			}
			rendered := catalog.Format(key, problem.Arguments()...)
			if strings.Contains(rendered, "%!") {
				t.Errorf("%s dans %s : %q", key, code, rendered)
			}
			if strings.Contains(text, "%d") && !strings.Contains(rendered, "12") {
				t.Errorf("%s dans %s ne dit pas la ligne : %q", key, code, rendered)
			}
		}
	}
	for _, key := range []string{"manifest.export", "manifest.export_header", "manifest.export_needs_review"} {
		for _, code := range lang.Codes() {
			if strings.HasPrefix(server.catalogs.For(code).Get(key), "[") {
				t.Errorf("la clé %q manque dans %s", key, code)
			}
		}
	}
}

// newTestManifest câble le fichier comme serve : les mêmes composants que
// l'interface, la même traduction des refus, le direct prévenu.
func newTestManifest(declared *directory.Directory, probes *probe.Service, heartbeats *heartbeat.Service, db *store.DB, bus *live.Bus, logger *slog.Logger) *manifest.Manifest {
	component := manifest.New(manifest.Dependencies{
		Directory: declared, Probes: probes, Heartbeats: heartbeats, Machines: db, History: db, Journal: db,
	}, logger)
	component.SetErrorKeys(RefusalKey)
	component.SetWatcher(bus)
	component.SetClock(func() time.Time { return testNow })
	return component
}

// tooLargeManifest simule un annuaire qui ne tiendrait pas dans un fichier.
type tooLargeManifest struct{ ManifestService }

func (tooLargeManifest) Export(context.Context, manifest.Annotations, manifest.ExportOptions) ([]byte, error) {
	return nil, fmt.Errorf("export: %w", manifest.ErrExportTooLarge)
}

func TestManifestAPI_ExportTooLargeIsRefusedWithItsKey(t *testing.T) {
	server := newTestServer(t)
	server.manifest = tooLargeManifest{}

	recorder := callAPI(server.Server, http.MethodGet, "/api/manifest/export", nil)
	if recorder.Code != http.StatusUnprocessableEntity || !strings.Contains(recorder.Body.String(), `"manifest.export_too_large"`) {
		t.Fatalf("status %d: %s", recorder.Code, recorder.Body.String())
	}
	if recorder.Header().Get("Content-Disposition") != "" {
		t.Fatal("a refusal must not be a download")
	}
	catalog := server.catalogs.For("fr")
	if text := catalog.Get(manifest.KeyExportTooLarge); text == manifest.KeyExportTooLarge || text == "" {
		t.Fatalf("the key has no text: %q", text)
	}

	session := connectMCP(t, server.mcpServer)
	text, isError := callTool(t, session, "export_directory", map[string]any{})
	if !isError || toolErrorCode(t, text) != manifest.KeyExportTooLarge {
		t.Fatalf("%v %s", isError, text)
	}
}
