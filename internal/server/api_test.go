package server

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/ldesfontaine/opencloud/internal/heartbeat"
	"github.com/ldesfontaine/opencloud/internal/lang"
)

var updateGolden = flag.Bool("update", false, "réécrit les réponses de référence dans testdata/")

// callAPI joue une requête JSON comme le front la fait : même origine,
// corps en application/json.
func callAPI(server *Server, method, path string, payload any) *httptest.ResponseRecorder {
	var body bytes.Buffer
	if payload != nil {
		_ = json.NewEncoder(&body).Encode(payload)
	}
	request := httptest.NewRequest(method, path, &body)
	request.Header.Set("Sec-Fetch-Site", "same-origin")
	if payload != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	recorder := httptest.NewRecorder()
	server.ServeHTTP(recorder, request)
	return recorder
}

func decodeAPI(t *testing.T, recorder *httptest.ResponseRecorder, into any) {
	t.Helper()
	if err := json.Unmarshal(recorder.Body.Bytes(), into); err != nil {
		t.Fatalf("decode %s: %v", recorder.Body.String(), err)
	}
}

func errorCode(t *testing.T, recorder *httptest.ResponseRecorder) string {
	t.Helper()
	var response apiError
	decodeAPI(t, recorder, &response)
	return response.Error
}

// Deux tâches pour des réponses parlantes : l'une rattachée à la machine
// distante, démarrée puis finie ; l'autre sans machine, en échec.
func (ts *testServer) seedJobs(t *testing.T) string {
	t.Helper()
	ctx := context.Background()
	backup, err := ts.heartbeats.Create(ctx, heartbeat.Definition{Name: "sauvegarde nextcloud", MachineID: remoteID, Interval: 24 * time.Hour, Grace: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	zero := 0
	for _, ping := range []heartbeat.Ping{{Kind: heartbeat.KindStart}, {Kind: heartbeat.KindExitCode, ExitCode: &zero}} {
		ping.Source = "51.15.20.114"
		ping.Method = "GET"
		if _, err := ts.heartbeats.Receive(ctx, backup.Token, ping); err != nil {
			t.Fatal(err)
		}
	}
	certbot, err := ts.heartbeats.Create(ctx, heartbeat.Definition{Name: "certbot renew", Interval: 12 * time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	one := 1
	if _, err := ts.heartbeats.Receive(ctx, certbot.Token, heartbeat.Ping{Kind: heartbeat.KindExitCode, ExitCode: &one, Source: "10.8.0.1", Method: "POST"}); err != nil {
		t.Fatal(err)
	}
	return backup.ID
}

var (
	pingToken  = regexp.MustCompile(`hb_[a-z2-7]{26}`)
	shortID    = regexp.MustCompile(`"(id|machine_id)":"[0-9a-f]{16}"`)
	tokenMask  = regexp.MustCompile(`oc_[a-z2-7]{6}…`)
	tokenValue = regexp.MustCompile(`oc_[a-z2-7]{20,}`)
)

// Les identifiants de tâche et de jeton, les jetons de ping et
// d'enrôlement changent à chaque requête ; la référence les remplace.
func normalize(body string) string {
	body = pingToken.ReplaceAllString(body, "hb_TOKEN")
	body = shortID.ReplaceAllString(body, `"$1":"ID"`)
	body = tokenMask.ReplaceAllString(body, "oc_MASKED…")
	return tokenValue.ReplaceAllString(body, "oc_TOKEN")
}

func compareGolden(t *testing.T, golden, got string) {
	t.Helper()
	if *updateGolden {
		if err := os.WriteFile(golden, []byte(got), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("%v (lancer go test ./web -update)", err)
	}
	if got != string(want) {
		t.Fatalf("réponse différente de %s :\n%s\n(lancer go test ./web -update après vérification)", golden, got)
	}
}

// Chaque lecture de l'API est figée dans testdata/*.golden.json ;
// `go test ./web -update` les réécrit après un changement voulu.
func TestAPI_ReadsMatchGoldenFiles(t *testing.T) {
	server := newTestServer(t)
	server.enroll(t, "vps-paris-1", remoteID)
	if _, _, err := server.machines.CreateToken(context.Background(), "vps-lyon-2"); err != nil {
		t.Fatal(err)
	}
	jobID := server.seedJobs(t)
	cases := map[string]string{
		"session":  "/api/session",
		"counts":   "/api/counts",
		"machines": "/api/machines",
		"machine":  "/api/machines/" + remoteID,
		"local":    "/api/machines/local",
		"jobs":     "/api/jobs",
		"job":      "/api/jobs/" + jobID,
	}
	for name, path := range cases {
		t.Run(name, func(t *testing.T) {
			recorder := callAPI(server.Server, http.MethodGet, path, nil)
			if recorder.Code != http.StatusOK {
				t.Fatalf("status %d: %s", recorder.Code, recorder.Body.String())
			}
			if got := recorder.Header().Get("Content-Type"); got != "application/json" {
				t.Fatalf("Content-Type %q", got)
			}
			compareGolden(t, filepath.Join("testdata", name+".golden.json"), normalize(recorder.Body.String()))
		})
	}
}

func TestAPI_Catalog_ServesEveryKeyOfALanguage(t *testing.T) {
	server := newTestServer(t)
	for _, code := range lang.Codes() {
		recorder := callAPI(server.Server, http.MethodGet, "/api/i18n/"+string(code), nil)
		if recorder.Code != http.StatusOK {
			t.Fatalf("%s: status %d", code, recorder.Code)
		}
		var strings map[string]string
		decodeAPI(t, recorder, &strings)
		catalog := server.catalogs.For(code)
		if len(strings) != len(catalog.Keys()) || strings["nav.overview"] != catalog.Get("nav.overview") {
			t.Fatalf("%s: %d strings, want %d", code, len(strings), len(catalog.Keys()))
		}
	}
	if got := callAPI(server.Server, http.MethodGet, "/api/i18n/klingon", nil); got.Code != http.StatusNotFound {
		t.Fatalf("unknown language: %d", got.Code)
	}
}

func TestAPI_SetLanguage_SavesAndRefusesUnknown(t *testing.T) {
	server := newTestServer(t)
	if got := callAPI(server.Server, http.MethodPut, "/api/session/language", languageRequest{Language: "en"}); got.Code != http.StatusNoContent {
		t.Fatalf("status %d: %s", got.Code, got.Body.String())
	}
	saved, err := server.settings.Load()
	if err != nil || saved.Language != "en" {
		t.Fatalf("saved %+v %v", saved, err)
	}
	var session sessionResponse
	decodeAPI(t, callAPI(server.Server, http.MethodGet, "/api/session", nil), &session)
	if session.Language != "en" || len(session.Languages) != 2 {
		t.Fatalf("session %+v", session)
	}
	got := callAPI(server.Server, http.MethodPut, "/api/session/language", languageRequest{Language: "klingon"})
	if got.Code != http.StatusBadRequest || errorCode(t, got) != "error.unknown_language" {
		t.Fatalf("unknown language: %d %s", got.Code, got.Body.String())
	}
}

// Un POST venu d'un autre site, ou un formulaire classique, est refusé
// avant d'atteindre le handler ; une lecture passe toujours.
func TestAPI_Writes_RequireTheSameOrigin(t *testing.T) {
	server := newTestServer(t)
	crossSite := httptest.NewRequest(http.MethodPut, "/api/session/language", strings.NewReader(`{"language":"en"}`))
	crossSite.Header.Set("Content-Type", "application/json")
	crossSite.Header.Set("Sec-Fetch-Site", "cross-site")
	recorder := httptest.NewRecorder()
	server.ServeHTTP(recorder, crossSite)
	if recorder.Code != http.StatusForbidden || errorCode(t, recorder) != codeOrigin {
		t.Fatalf("cross-site: %d %s", recorder.Code, recorder.Body.String())
	}

	form := httptest.NewRequest(http.MethodPut, "/api/session/language", strings.NewReader("language=en"))
	form.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	recorder = httptest.NewRecorder()
	server.ServeHTTP(recorder, form)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("form post: %d", recorder.Code)
	}

	read := httptest.NewRequest(http.MethodGet, "/api/session", nil)
	read.Header.Set("Sec-Fetch-Site", "cross-site")
	recorder = httptest.NewRecorder()
	server.ServeHTTP(recorder, read)
	if recorder.Code != http.StatusOK {
		t.Fatalf("cross-site read: %d", recorder.Code)
	}
	if saved, _ := server.settings.Load(); saved.Language == "en" {
		t.Fatal("a refused write must not be applied")
	}
}

func TestAPI_UnknownRoute_IsAJSON404(t *testing.T) {
	server := newTestServer(t)
	for _, path := range []string{"/api/nope", "/api/machines/x/y"} {
		got := callAPI(server.Server, http.MethodGet, path, nil)
		if got.Code != http.StatusNotFound || errorCode(t, got) != codeNotFound {
			t.Errorf("%s: %d %s", path, got.Code, got.Body.String())
		}
	}
	if got := callAPI(server.Server, http.MethodPost, "/api/counts", nil); got.Code != http.StatusNotFound {
		t.Errorf("wrong method: %d", got.Code)
	}
}

var refusalCall = regexp.MustCompile(`apiRefuse\(w, http\.Status[A-Za-z]+, "([^"]+)"\)`)

// Le garde-fou : chaque refus porte une clé qui existe dans les deux
// catalogues, jamais une phrase en dur.
func TestAPI_RefusalKeysExistInEveryCatalog(t *testing.T) {
	server := newTestServer(t)
	sources, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	found := 0
	for _, source := range sources {
		if strings.HasSuffix(source, "_test.go") {
			continue
		}
		content, err := os.ReadFile(source)
		if err != nil {
			t.Fatal(err)
		}
		for _, match := range refusalCall.FindAllStringSubmatch(string(content), -1) {
			found++
			for _, code := range lang.Codes() {
				if strings.HasPrefix(server.catalogs.For(code).Get(match[1]), "[") {
					t.Errorf("%s: la clé %q manque dans %s", source, match[1], code)
				}
			}
		}
	}
	if found == 0 {
		t.Fatal("no refusal found: the guard no longer sees the code")
	}
}
