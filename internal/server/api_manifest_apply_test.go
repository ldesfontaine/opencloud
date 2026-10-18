package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/ldesfontaine/opencloud/internal/directory"
	"github.com/ldesfontaine/opencloud/internal/lang"
	"github.com/ldesfontaine/opencloud/internal/manifest"
)

var (
	// L'empreinte d'un fichier ou d'un plan dépend des identifiants, tirés à
	// chaque création ; celle d'un passage aussi.
	manifestHash = regexp.MustCompile(`sha256:[0-9a-f]{64}`)
	applyID      = regexp.MustCompile(`apl_[0-9a-f]{16}`)
)

func normalizeManifest(body string) string {
	body = manifestHash.ReplaceAllString(normalize(body), "sha256:HASH")
	return applyID.ReplaceAllString(body, "apl_ID")
}

func (ts *testServer) exportedFile(t *testing.T) string {
	t.Helper()
	recorder := callAPI(ts.Server, http.MethodGet, "/api/manifest/export", nil)
	if recorder.Code != http.StatusOK {
		t.Fatalf("export: %d", recorder.Code)
	}
	return recorder.Body.String()
}

func replaceFirst(t *testing.T, text, old, replacement string) string {
	t.Helper()
	if strings.Count(text, old) != 1 {
		t.Fatalf("%q must appear once in:\n%s", old, text)
	}
	return strings.Replace(text, old, replacement, 1)
}

// editedFile est l'export, modifié de quatre façons : une sonde plus
// fréquente, un projet et une tâche créés sans id, un service retiré (donc
// « hors fichier », gardé tant que -prune n'est pas demandé).
func editedFile(t *testing.T, exported string) string {
	t.Helper()
	text := replaceFirst(t, exported, "    target: 127.0.0.1:5432\n    interval: 1m\n", "    target: 127.0.0.1:5432\n    interval: 30s\n")
	text = replaceFirst(t, text, "    title: Client A\nservices:\n", "    title: Client A\n  - name: ops\nservices:\n")
	text = replaceFirst(t, text, "ignored:\n", "  - name: nightly\n    every: 24h\n    grace: 1h\nignored:\n")
	index := strings.Index(text, "    name: whoami\n")
	start, end := strings.LastIndex(text[:index], "  - id:"), strings.Index(text, "probes:\n")
	if index < 0 || start < 0 || end < start {
		t.Fatalf("no whoami block in:\n%s", text)
	}
	return text[:start] + text[end:]
}

func manifestPayload(file string, prune bool, planHash string) map[string]any {
	return map[string]any{"file": file, "file_name": "opencloud.yaml", "prune": prune, "plan_hash": planHash}
}

func TestManifestAPI_PlanMatchesGolden(t *testing.T) {
	server := newTestServer(t)
	server.seedManifest(t)
	file := editedFile(t, server.exportedFile(t))

	recorder := callAPI(server.Server, http.MethodPost, "/api/manifest/plan", manifestPayload(file, false, ""))
	if recorder.Code != http.StatusOK || recorder.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("plan: %d %s", recorder.Code, recorder.Body.String())
	}
	compareGolden(t, filepath.Join("testdata", "manifest_plan.golden.json"), normalizeManifest(recorder.Body.String()))

	var plan planJSON
	decodeAPI(t, recorder, &plan)
	if !plan.Applicable || plan.Language != "fr" || plan.Summary.Create != 2 || plan.Summary.Update != 1 || plan.Summary.Unmanaged != 1 || len(plan.Problems) != 0 {
		t.Fatalf("plan: %+v", plan)
	}
	// Planifier n'écrit rien.
	if applies := callAPI(server.Server, http.MethodGet, "/api/manifest/applies", nil); !strings.Contains(applies.Body.String(), `"applies":[]`) {
		t.Fatalf("journal after a plan: %s", applies.Body.String())
	}
	// Avec -prune, le service retiré se supprime, et dit ce qu'il emporte.
	pruned := callAPI(server.Server, http.MethodPost, "/api/manifest/plan", manifestPayload(file, true, ""))
	var prunedPlan planJSON
	decodeAPI(t, pruned, &prunedPlan)
	if prunedPlan.Summary.Delete != 1 || prunedPlan.Summary.Unmanaged != 0 || !prunedPlan.Prune || prunedPlan.PlanHash == plan.PlanHash {
		t.Fatalf("pruned plan: %+v", prunedPlan)
	}
}

func TestManifestAPI_ApplyThenJournalMatchGolden(t *testing.T) {
	server := newTestServer(t)
	server.seedManifest(t)
	file := editedFile(t, server.exportedFile(t))
	var plan planJSON
	decodeAPI(t, callAPI(server.Server, http.MethodPost, "/api/manifest/plan", manifestPayload(file, false, "")), &plan)

	applied := callAPI(server.Server, http.MethodPost, "/api/manifest/apply", manifestPayload(file, false, plan.PlanHash))
	if applied.Code != http.StatusCreated {
		t.Fatalf("apply: %d %s", applied.Code, applied.Body.String())
	}
	compareGolden(t, filepath.Join("testdata", "manifest_apply.golden.json"), normalizeManifest(applied.Body.String()))
	var result applyJSON
	decodeAPI(t, applied, &result)
	if result.State != "applied" || result.Done != 3 || result.ActorKind != "operator" || result.FinishedAt == nil || len(result.Changes) != 3 {
		t.Fatalf("apply: %+v", result)
	}

	list := callAPI(server.Server, http.MethodGet, "/api/manifest/applies", nil)
	if list.Code != http.StatusOK {
		t.Fatalf("list: %d", list.Code)
	}
	compareGolden(t, filepath.Join("testdata", "manifest_applies.golden.json"), normalizeManifest(list.Body.String()))
	var listed appliesResponse
	decodeAPI(t, list, &listed)
	if len(listed.Applies) != 1 || len(listed.Applies[0].Changes) != 0 || listed.Applies[0].ID != result.ID {
		t.Fatalf("list: %+v", listed)
	}

	detail := callAPI(server.Server, http.MethodGet, "/api/manifest/applies/"+result.ID, nil)
	if detail.Code != http.StatusOK || detail.Body.String() != applied.Body.String() {
		t.Fatalf("detail: %d\n%s\n%s", detail.Code, detail.Body.String(), applied.Body.String())
	}
	if missing := callAPI(server.Server, http.MethodGet, "/api/manifest/applies/apl_0000000000000000", nil); missing.Code != http.StatusNotFound {
		t.Fatalf("unknown id: %d", missing.Code)
	}

	// Le fichier est maintenant appliqué : rien à changer, et aucune ligne de plus.
	var settled planJSON
	decodeAPI(t, callAPI(server.Server, http.MethodPost, "/api/manifest/plan", manifestPayload(file, false, "")), &settled)
	if settled.Summary.Create+settled.Summary.Update+settled.Summary.Delete != 0 {
		t.Fatalf("the plan must be empty now: %+v", settled)
	}
	again := callAPI(server.Server, http.MethodPost, "/api/manifest/apply", manifestPayload(file, false, settled.PlanHash))
	if again.Code != http.StatusUnprocessableEntity || !strings.Contains(again.Body.String(), `"manifest.nothing_to_apply"`) {
		t.Fatalf("second apply: %d %s", again.Code, again.Body.String())
	}
	if relisted := callAPI(server.Server, http.MethodGet, "/api/manifest/applies?limit=5", nil); strings.Count(relisted.Body.String(), `"apl_`) != 1 {
		t.Fatalf("journal: %s", relisted.Body.String())
	}
	// Et la base a bien bougé, par les gestes de l'interface.
	services, err := server.directory.ListServices(context.Background(), directory.Filter{})
	if err != nil || len(services) != 3 {
		t.Fatalf("services: %v %v", len(services), err)
	}
	projects, _ := server.directory.ListProjects(context.Background())
	if len(projects) != 2 {
		t.Fatalf("projects: %+v", projects)
	}
}

// Quelqu'un édite l'annuaire pendant que l'opérateur lit le plan : l'apply ne
// passe pas, et rend le plan d'aujourd'hui pour qu'il le relise.
func TestManifestAPI_ApplyStalePlanAnswers409WithThePlan(t *testing.T) {
	server := newTestServer(t)
	server.seedManifest(t)
	file := editedFile(t, server.exportedFile(t))
	var plan planJSON
	decodeAPI(t, callAPI(server.Server, http.MethodPost, "/api/manifest/plan", manifestPayload(file, false, "")), &plan)
	if _, err := server.directory.CreateProject(context.Background(), directory.ProjectDefinition{Name: "ops"}); err != nil {
		t.Fatal(err)
	}

	recorder := callAPI(server.Server, http.MethodPost, "/api/manifest/apply", manifestPayload(file, false, plan.PlanHash))
	if recorder.Code != http.StatusConflict {
		t.Fatalf("status %d: %s", recorder.Code, recorder.Body.String())
	}
	var refusal manifestRefusalJSON
	decodeAPI(t, recorder, &refusal)
	if refusal.Error != "manifest.plan_changed" || refusal.Plan.PlanHash == plan.PlanHash || refusal.Plan.Summary.Create != 1 || !refusal.Plan.Applicable {
		t.Fatalf("refusal: %+v", refusal)
	}
	if applies := callAPI(server.Server, http.MethodGet, "/api/manifest/applies", nil); !strings.Contains(applies.Body.String(), `"applies":[]`) {
		t.Fatalf("a stale apply leaves no line: %s", applies.Body.String())
	}
	// L'empreinte d'aujourd'hui passe.
	if ok := callAPI(server.Server, http.MethodPost, "/api/manifest/apply", manifestPayload(file, false, refusal.Plan.PlanHash)); ok.Code != http.StatusCreated {
		t.Fatalf("with today's hash: %d %s", ok.Code, ok.Body.String())
	}
}

func TestManifestAPI_RefusedFileIsAnswerNotAFailure(t *testing.T) {
	server := newTestServer(t)
	server.seedManifest(t)
	file := replaceFirst(t, server.exportedFile(t), "  - name: vps-paris-1\n", "  - name: vps-paris-1\n  - name: app-3\n")
	file = replaceFirst(t, file, "    interval: 1m\n    timeout: 10s\n    failure_threshold: 3\n    recovery_threshold: 2\n    method: GET", "    interval: 0s\n    timeout: 10s\n    failure_threshold: 3\n    recovery_threshold: 2\n    method: GET")

	recorder := callAPI(server.Server, http.MethodPost, "/api/manifest/plan", manifestPayload(file, false, ""))
	if recorder.Code != http.StatusOK {
		t.Fatalf("plan: %d %s", recorder.Code, recorder.Body.String())
	}
	var plan planJSON
	decodeAPI(t, recorder, &plan)
	if plan.Applicable || len(plan.Problems) == 0 || plan.Problems[0].Line == 0 || plan.Problems[0].Key == "" {
		t.Fatalf("plan: %+v", plan)
	}
	if plan.Changes == nil || plan.Unmanaged == nil {
		t.Fatal("lists must be arrays, never null")
	}

	refused := callAPI(server.Server, http.MethodPost, "/api/manifest/apply", manifestPayload(file, false, plan.PlanHash))
	var refusal manifestRefusalJSON
	decodeAPI(t, refused, &refusal)
	if refused.Code != http.StatusUnprocessableEntity || refusal.Error != "manifest.not_applicable" || refusal.Plan.Applicable {
		t.Fatalf("apply: %d %+v", refused.Code, refusal)
	}

	// Un YAML qui ne se lit pas : la même réponse, avec la ligne fautive.
	broken := callAPI(server.Server, http.MethodPost, "/api/manifest/plan", manifestPayload("version: 1\nmachines: [\n", false, ""))
	decodeAPI(t, broken, &plan)
	if broken.Code != http.StatusOK || plan.Applicable || plan.Problems[0].Key != manifest.KeyYAMLInvalid {
		t.Fatalf("broken: %d %+v", broken.Code, plan)
	}
}

func TestManifestAPI_RefusesWhatIsNotAFileToJudge(t *testing.T) {
	server := newTestServer(t)
	post := func(path string, body []byte) *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(body))
		request.Header.Set("Sec-Fetch-Site", "same-origin")
		request.Header.Set("Content-Type", "application/json")
		recorder := httptest.NewRecorder()
		server.ServeHTTP(recorder, request)
		return recorder
	}
	huge, _ := json.Marshal(map[string]any{"file": strings.Repeat("x", 2<<20)})
	for name, body := range map[string][]byte{
		"not json":      []byte("version: 1"),
		"unknown field": []byte(`{"file":"x","extra":1}`),
		"too big":       huge,
		"wrong type":    []byte(`{"file":12}`),
	} {
		for _, path := range []string{"/api/manifest/plan", "/api/manifest/apply"} {
			recorder := post(path, body)
			if recorder.Code != http.StatusUnprocessableEntity || !strings.Contains(recorder.Body.String(), `"manifest.request_invalid"`) {
				t.Errorf("%s %s: %d %s", name, path, recorder.Code, recorder.Body.String())
			}
		}
	}
	for _, query := range []string{"0", "-1", "abc", "201"} {
		if got := callAPI(server.Server, http.MethodGet, "/api/manifest/applies?limit="+query, nil); got.Code != http.StatusUnprocessableEntity {
			t.Errorf("limit=%s: %d", query, got.Code)
		}
	}
}

func TestManifestAPI_PlanAndApplyAreOperatorRoutes(t *testing.T) {
	server := newTestServer(t)
	server.seedManifest(t)
	file := editedFile(t, server.exportedFile(t))
	server.enableBasic(t)
	call := func(method, path, user, password string) int {
		body, _ := json.Marshal(manifestPayload(file, false, ""))
		request := httptest.NewRequest(method, path, bytes.NewReader(body))
		request.Header.Set("Sec-Fetch-Site", "same-origin")
		request.Header.Set("Content-Type", "application/json")
		if user != "" {
			request.SetBasicAuth(user, password)
		}
		recorder := httptest.NewRecorder()
		server.ServeHTTP(recorder, request)
		return recorder.Code
	}
	for _, route := range []struct{ method, path string }{
		{http.MethodPost, "/api/manifest/plan"}, {http.MethodPost, "/api/manifest/apply"},
		{http.MethodGet, "/api/manifest/applies"}, {http.MethodGet, "/api/manifest/applies/apl_x"},
	} {
		if got := call(route.method, route.path, "", ""); got != http.StatusUnauthorized {
			t.Errorf("%s %s without credentials: %d", route.method, route.path, got)
		}
	}
	if got := call(http.MethodPost, "/api/manifest/plan", testUsername, testPassword); got != http.StatusOK {
		t.Fatalf("plan with credentials: %d", got)
	}
	// Une écriture venue d'un autre site est refusée par la garde même-origine.
	body, _ := json.Marshal(manifestPayload(file, false, ""))
	cross := httptest.NewRequest(http.MethodPost, "/api/manifest/plan", bytes.NewReader(body))
	cross.Header.Set("Sec-Fetch-Site", "cross-site")
	cross.Header.Set("Content-Type", "application/json")
	cross.SetBasicAuth(testUsername, testPassword)
	recorder := httptest.NewRecorder()
	server.ServeHTTP(recorder, cross)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("cross-site: %d", recorder.Code)
	}
}

// Sur TCP l'acteur est l'opérateur, nommé par son identifiant Basic ; sur la
// socket, c'est la commande locale sous le compte qu'elle déclare, nettoyé.
func TestControlSocket_ActorIsCLI(t *testing.T) {
	server := newTestServer(t)
	server.seedManifest(t)
	server.enableBasic(t)
	file := editedFile(t, server.exportedFile2(t))

	onSocket := func(method, path string, payload any) *httptest.ResponseRecorder {
		var body bytes.Buffer
		if payload != nil {
			_ = json.NewEncoder(&body).Encode(payload)
		}
		request := httptest.NewRequest(method, "http://control"+path, &body)
		recorder := httptest.NewRecorder()
		server.ControlHandler().ServeHTTP(recorder, request)
		return recorder
	}
	plan := onSocket(http.MethodPost, "/api/manifest/plan", manifestPayload(file, false, ""))
	if plan.Code != http.StatusOK {
		t.Fatalf("plan on the socket: %d %s", plan.Code, plan.Body.String())
	}
	var judged planJSON
	decodeAPI(t, plan, &judged)
	payload := manifestPayload(file, false, judged.PlanHash)
	payload["actor_name"] = "ro\x00ot" + strings.Repeat("é", 100)
	applied := onSocket(http.MethodPost, "/api/manifest/apply", payload)
	if applied.Code != http.StatusCreated {
		t.Fatalf("apply on the socket: %d %s", applied.Code, applied.Body.String())
	}
	var result applyJSON
	decodeAPI(t, applied, &result)
	if result.ActorKind != "cli" || !strings.HasPrefix(result.ActorName, "root") || strings.ContainsRune(result.ActorName, 0) || len([]rune(result.ActorName)) != maxActorNameRunes {
		t.Fatalf("actor: %q %q", result.ActorKind, result.ActorName)
	}
	// Le journal se lit aussi par la socket.
	if list := onSocket(http.MethodGet, "/api/manifest/applies", nil); list.Code != http.StatusOK || !strings.Contains(list.Body.String(), result.ID) {
		t.Fatalf("journal on the socket: %d %s", list.Code, list.Body.String())
	}

	// Sur TCP, le nom déclaré dans le corps n'est pas lu : celui de
	// l'authentification l'est.
	next := replaceFirst(t, file, "    grace: 1h\nignored:\n", "    grace: 2h\nignored:\n")
	body, _ := json.Marshal(map[string]any{"file": next, "file_name": "../etc/opencloud.yaml", "plan_hash": "", "actor_name": "root"})
	request := httptest.NewRequest(http.MethodPost, "/api/manifest/plan", bytes.NewReader(body))
	request.Header.Set("Sec-Fetch-Site", "same-origin")
	request.Header.Set("Content-Type", "application/json")
	request.SetBasicAuth(testUsername, testPassword)
	recorder := httptest.NewRecorder()
	server.ServeHTTP(recorder, request)
	decodeAPI(t, recorder, &judged)
	body, _ = json.Marshal(map[string]any{"file": next, "file_name": "../etc/opencloud.yaml", "plan_hash": judged.PlanHash, "actor_name": "root"})
	request = httptest.NewRequest(http.MethodPost, "/api/manifest/apply", bytes.NewReader(body))
	request.Header.Set("Sec-Fetch-Site", "same-origin")
	request.Header.Set("Content-Type", "application/json")
	request.SetBasicAuth(testUsername, testPassword)
	recorder = httptest.NewRecorder()
	server.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusCreated {
		t.Fatalf("apply on TCP: %d %s", recorder.Code, recorder.Body.String())
	}
	decodeAPI(t, recorder, &result)
	if result.ActorKind != "operator" || result.ActorName != testUsername || result.FileName != "opencloud.yaml" {
		t.Fatalf("actor on TCP: %+v", result)
	}
}

// exportedFile2 lit l'export par la socket, que le mode basic ne garde pas.
func (ts *testServer) exportedFile2(t *testing.T) string {
	t.Helper()
	recorder := httptest.NewRecorder()
	ts.ControlHandler().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "http://control/api/manifest/export", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("export: %d", recorder.Code)
	}
	return recorder.Body.String()
}

func TestControlSocket_EveryRouteHasAMethod(t *testing.T) {
	server := newTestServer(t)
	for _, route := range server.routes() {
		if strings.HasPrefix(route.pattern, controlPrefix) && route.method == anyMethod {
			t.Errorf("%s: a route without method opens every method on a door with no guard", route.pattern)
		}
	}
	call := func(method, path string) int {
		recorder := httptest.NewRecorder()
		server.ControlHandler().ServeHTTP(recorder, httptest.NewRequest(method, "http://control"+path, strings.NewReader("{}")))
		return recorder.Code
	}
	for _, route := range []struct{ method, path string }{
		{http.MethodGet, "/api/manifest/plan"}, {http.MethodPut, "/api/manifest/apply"},
		{http.MethodDelete, "/api/manifest/applies/apl_x"}, {http.MethodPost, "/api/manifest/applies"},
	} {
		if got := call(route.method, route.path); got < 400 {
			t.Errorf("%s %s answers %d", route.method, route.path, got)
		}
	}
}

// busyManifest répond « occupé » : un apply en cours ailleurs.
type busyManifest struct{ ManifestService }

func (busyManifest) Apply(context.Context, manifest.Request, manifest.Actor) (manifest.Apply, manifest.Plan, error) {
	return manifest.Apply{}, manifest.Plan{}, manifest.ErrBusy
}

func TestManifestAPI_BusyAnswers409(t *testing.T) {
	server := newTestServer(t)
	server.manifest = busyManifest{server.manifest}
	recorder := callAPI(server.Server, http.MethodPost, "/api/manifest/apply", manifestPayload("version: 1\n", false, "x"))
	if recorder.Code != http.StatusConflict || !strings.Contains(recorder.Body.String(), `"manifest.busy"`) {
		t.Fatalf("%d %s", recorder.Code, recorder.Body.String())
	}
}

// Chaque clé que le journal ou une réponse peut porter a son texte, et chaque
// refus d'un domaine que le fichier écrit se traduit en une clé qui existe.
func TestManifest_RefusalKeysExistInEveryCatalog(t *testing.T) {
	server := newTestServer(t)
	keys := manifest.ApplyKeys()
	for _, err := range []error{directory.ErrServiceLinked, directory.ErrNameTaken, directory.ErrProjectNameTaken} {
		key := RefusalKey(err)
		if key == "" {
			t.Fatalf("%v has no key", err)
		}
		keys = append(keys, key)
	}
	if RefusalKey(errors.New("boom")) != "" {
		t.Fatal("an unknown error must have no key: its text may carry the file")
	}
	for _, key := range keys {
		for _, code := range lang.Codes() {
			if strings.HasPrefix(server.catalogs.For(code).Get(key), "[") {
				t.Errorf("la clé %q manque dans %s", key, code)
			}
		}
	}
}
