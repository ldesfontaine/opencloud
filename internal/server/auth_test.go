package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ldesfontaine/opencloud/internal/auth"
)

const (
	testUsername = "lucas"
	testPassword = "correct horse battery"
)

// enableBasic passe le serveur en mode basic, par le composant.
func (ts *testServer) enableBasic(t *testing.T) {
	t.Helper()
	if _, err := ts.operator.EnableBasic(context.Background(), testUsername, testPassword); err != nil {
		t.Fatal(err)
	}
}

// callAs joue une requête comme le front, avec ou sans identifiants Basic.
func callAs(server *Server, method, path, username, password string) *httptest.ResponseRecorder {
	return callAsWithin(context.Background(), server, method, path, username, password)
}

// callAsWithin borne la requête : un flux ouvert (direct de la page de
// statut) ne rend la main qu'à la fin de son contexte.
func callAsWithin(ctx context.Context, server *Server, method, path, username, password string) *httptest.ResponseRecorder {
	request := httptest.NewRequestWithContext(ctx, method, path, nil)
	request.Header.Set("Sec-Fetch-Site", "same-origin")
	if username != "" || password != "" {
		request.SetBasicAuth(username, password)
	}
	recorder := httptest.NewRecorder()
	server.ServeHTTP(recorder, request)
	return recorder
}

// Un chemin concret pour chaque motif : les jokers deviennent « x ».
func concretePath(pattern string) string {
	pattern = strings.ReplaceAll(pattern, "{$}", "")
	parts := strings.Split(pattern, "/")
	for i, part := range parts {
		if strings.HasPrefix(part, "{") {
			parts[i] = "x"
		}
	}
	return strings.Join(parts, "/")
}

func concreteMethod(method string) string {
	if method == anyMethod {
		return http.MethodGet
	}
	return method
}

// Chaque route de l'arbre est jugée : celles de l'opérateur répondent 401
// avec le défi Basic, les autres ne le demandent jamais.
func TestAuthBasic_GuardsEveryOperatorRouteAndLeavesTheOthersOpen(t *testing.T) {
	server := newTestServer(t)
	server.enableBasic(t)
	open := map[string]bool{}
	for _, route := range server.routes() {
		path := concretePath(route.pattern)
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		recorder := callAsWithin(ctx, server.Server, concreteMethod(route.method), path, "", "")
		cancel()
		if isOperatorRoute(route.pattern) {
			if recorder.Code != http.StatusUnauthorized {
				t.Errorf("%s %s: status %d, want 401", route.method, route.pattern, recorder.Code)
			}
			if got := recorder.Header().Get("WWW-Authenticate"); got != `Basic realm="openCloud", charset="UTF-8"` {
				t.Errorf("%s %s: WWW-Authenticate %q", route.method, route.pattern, got)
			}
			continue
		}
		open[route.pattern] = true
		if recorder.Code == http.StatusUnauthorized && recorder.Header().Get("WWW-Authenticate") != "" &&
			strings.HasPrefix(recorder.Header().Get("WWW-Authenticate"), "Basic") {
			t.Errorf("%s %s: an open route asked for the operator's password", route.method, route.pattern)
		}
	}
	// La liste des ouvertes est une décision : la voici, entière.
	want := []string{
		"/agent/enroll", "/agent/challenge", "/agent/stream", "/agent/signal", "/agent/logs/{request}",
		"/ping/{token}", "/ping/{token}/start", "/ping/{token}/{code}",
		"/statut", "/statut/{$}", "/statut/api/status", "/statut/api/i18n", "/statut/api/events", "/statut/",
		"/.well-known/oauth-authorization-server", "/.well-known/oauth-protected-resource", "/.well-known/oauth-protected-resource/mcp",
		"/oauth/token", "/mcp",
	}
	if len(open) != len(want) {
		t.Errorf("open routes %v, want %v", open, want)
	}
	for _, pattern := range want {
		if !open[pattern] {
			t.Errorf("%s should stay open", pattern)
		}
	}
	if open["/oauth/authorize"] || open["/"] || open["/api/events"] || open["/api/services/{id}/logs/stream"] {
		t.Error("the consent step, the front, the live stream and the log stream must be guarded")
	}
}

func TestAuthBasic_RightCredentialsPassAndWrongPasswordIs401(t *testing.T) {
	server := newTestServer(t)
	server.enableBasic(t)
	if got := callAs(server.Server, http.MethodGet, "/api/counts", testUsername, testPassword); got.Code != http.StatusOK {
		t.Fatalf("right credentials: status %d", got.Code)
	}
	if got := callAs(server.Server, http.MethodGet, "/machines", testUsername, testPassword); got.Code != http.StatusOK {
		t.Fatalf("front with credentials: status %d", got.Code)
	}
	wrong := callAs(server.Server, http.MethodGet, "/api/counts", testUsername, "not the password")
	if wrong.Code != http.StatusUnauthorized || errorCode(t, wrong) != codeUnauthorized {
		t.Fatalf("wrong password: status %d %s", wrong.Code, wrong.Body.String())
	}
	if got := callAs(server.Server, http.MethodGet, "/api/counts", "someone", testPassword); got.Code != http.StatusUnauthorized {
		t.Fatalf("wrong username: status %d", got.Code)
	}
}

func TestAuthBasic_StatusPageAndPingStayPublic(t *testing.T) {
	server := newTestServer(t)
	server.enableBasic(t)
	if got := callAs(server.Server, http.MethodGet, "/statut", "", ""); got.Code != http.StatusOK {
		t.Fatalf("/statut: status %d", got.Code)
	}
	if got := callAs(server.Server, http.MethodGet, "/statut/api/status", "", ""); got.Code != http.StatusOK {
		t.Fatalf("/statut/api/status: status %d", got.Code)
	}
	if got := callAs(server.Server, http.MethodGet, "/ping/hb_unknown", "", ""); got.Code != http.StatusNotFound {
		t.Fatalf("/ping: status %d, want the ping's own 404", got.Code)
	}
}

// Les échecs comptent par adresse : au-delà de la rafale, 429, même avec
// le bon mot de passe ; une autre adresse n'est pas touchée.
func TestAuthBasic_FailuresAreRateLimitedPerAddress(t *testing.T) {
	server := newTestServer(t)
	server.enableBasic(t)
	attempt := func(address, password string) int {
		request := httptest.NewRequest(http.MethodGet, "/api/counts", nil)
		request.RemoteAddr = address + ":4000"
		request.SetBasicAuth(testUsername, password)
		recorder := httptest.NewRecorder()
		server.ServeHTTP(recorder, request)
		return recorder.Code
	}
	for i := range authAttemptBurst {
		if got := attempt("203.0.113.9", "guess"); got != http.StatusUnauthorized {
			t.Fatalf("attempt %d: status %d", i+1, got)
		}
	}
	if got := attempt("203.0.113.9", testPassword); got != http.StatusTooManyRequests {
		t.Fatalf("after the burst: status %d, want 429", got)
	}
	if got := attempt("198.51.100.7", testPassword); got != http.StatusOK {
		t.Fatalf("another address: status %d", got)
	}
}

// Les identifiants vérifiés sont retenus : les requêtes suivantes ne
// consomment ni hachage lent ni jeton de la limite.
func TestAuthBasic_RememberedCredentialsDoNotCountAgainstTheLimit(t *testing.T) {
	server := newTestServer(t)
	server.enableBasic(t)
	for i := range authAttemptBurst * 3 {
		if got := callAs(server.Server, http.MethodGet, "/api/counts", testUsername, testPassword); got.Code != http.StatusOK {
			t.Fatalf("request %d: status %d", i+1, got.Code)
		}
	}
}

func TestAuthAPI_EnableChangeDisable(t *testing.T) {
	server := newTestServer(t)
	var state authResponse
	decodeAPI(t, callAPI(server.Server, http.MethodGet, "/api/auth", nil), &state)
	if state.Mode != "none" || state.Username != "" || state.PasswordMinLength != auth.MinPasswordLength {
		t.Fatalf("initial state %+v", state)
	}

	short := callAPI(server.Server, http.MethodPost, "/api/auth/actions/enable", authEnableRequest{Username: testUsername, Password: "short"})
	if short.Code != http.StatusUnprocessableEntity || errorCode(t, short) != "auth.password_too_short" {
		t.Fatalf("short password: %d %s", short.Code, short.Body.String())
	}
	badName := callAPI(server.Server, http.MethodPost, "/api/auth/actions/enable", authEnableRequest{Username: "a:b", Password: testPassword})
	if badName.Code != http.StatusUnprocessableEntity || errorCode(t, badName) != "auth.username_invalid" {
		t.Fatalf("bad username: %d %s", badName.Code, badName.Body.String())
	}

	enabled := callAPI(server.Server, http.MethodPost, "/api/auth/actions/enable", authEnableRequest{Username: testUsername, Password: testPassword})
	if enabled.Code != http.StatusOK {
		t.Fatalf("enable: %d %s", enabled.Code, enabled.Body.String())
	}
	if strings.Contains(enabled.Body.String(), "$2") || strings.Contains(enabled.Body.String(), testPassword) {
		t.Fatal("neither the hash nor the password leaves the process")
	}
	decodeAPI(t, enabled, &state)
	if state.Mode != "basic" || state.Username != testUsername || state.EnabledAt == nil || !state.EnabledAt.Equal(testNow) {
		t.Fatalf("enabled state %+v", state)
	}
	// Désormais l'API elle-même demande les identifiants.
	if got := callAPI(server.Server, http.MethodGet, "/api/auth", nil); got.Code != http.StatusUnauthorized {
		t.Fatalf("after enabling, anonymous read: %d", got.Code)
	}

	const newPassword = "another long passphrase"
	changed := callBasicJSON(server.Server, http.MethodPut, "/api/auth/password", `{"password":"`+newPassword+`"}`, testPassword)
	if changed.Code != http.StatusOK {
		t.Fatalf("change: %d %s", changed.Code, changed.Body.String())
	}
	// L'ancien mot de passe, pourtant retenu en mémoire, ne vaut plus rien.
	if got := callAs(server.Server, http.MethodGet, "/api/auth", testUsername, testPassword); got.Code != http.StatusUnauthorized {
		t.Fatalf("old password after change: %d", got.Code)
	}
	read := callAs(server.Server, http.MethodGet, "/api/auth", testUsername, newPassword)
	if read.Code != http.StatusOK {
		t.Fatalf("new password: %d", read.Code)
	}
	compareGolden(t, filepath.Join("testdata", "auth.golden.json"), read.Body.String())

	again := callBasicJSON(server.Server, http.MethodPost, "/api/auth/actions/enable", `{"username":"x","password":"`+newPassword+`"}`, newPassword)
	if again.Code != http.StatusConflict || errorCode(t, again) != "auth.already_enabled" {
		t.Fatalf("enable twice: %d %s", again.Code, again.Body.String())
	}

	disabled := callBasicJSON(server.Server, http.MethodPost, "/api/auth/actions/disable", `{}`, newPassword)
	if disabled.Code != http.StatusOK {
		t.Fatalf("disable: %d %s", disabled.Code, disabled.Body.String())
	}
	if got := callAs(server.Server, http.MethodGet, "/api/counts", "", ""); got.Code != http.StatusOK {
		t.Fatalf("after disabling, anonymous: %d", got.Code)
	}
	twice := callAPI(server.Server, http.MethodPost, "/api/auth/actions/disable", struct{}{})
	if twice.Code != http.StatusConflict || errorCode(t, twice) != "auth.disabled" {
		t.Fatalf("disable twice: %d %s", twice.Code, twice.Body.String())
	}
	changeOff := callAPI(server.Server, http.MethodPut, "/api/auth/password", authPasswordRequest{Password: newPassword})
	if changeOff.Code != http.StatusConflict || errorCode(t, changeOff) != "auth.disabled" {
		t.Fatalf("change while disabled: %d %s", changeOff.Code, changeOff.Body.String())
	}
}

// La commande de secours écrit dans la base depuis un autre processus :
// serve la suit à la requête suivante, sans redémarrer, et les
// identifiants qu'il avait retenus ne rouvrent rien à la réactivation.
func TestAuthBasic_DisableFromAnotherProcessTakesEffectWithoutRestart(t *testing.T) {
	server := newTestServer(t)
	server.enableBasic(t)
	if got := callAs(server.Server, http.MethodGet, "/api/counts", testUsername, testPassword); got.Code != http.StatusOK {
		t.Fatalf("before: %d", got.Code)
	}
	rescue, err := auth.New(server.db, server.logger)
	if err != nil {
		t.Fatal(err)
	}
	if err := rescue.Disable(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := callAs(server.Server, http.MethodGet, "/api/counts", "", ""); got.Code != http.StatusOK {
		t.Fatalf("after the rescue command: %d", got.Code)
	}
	if _, err := rescue.EnableBasic(context.Background(), testUsername, "a brand new passphrase"); err != nil {
		t.Fatal(err)
	}
	if got := callAs(server.Server, http.MethodGet, "/api/counts", testUsername, testPassword); got.Code != http.StatusUnauthorized {
		t.Fatalf("the remembered old password reopened the door: %d", got.Code)
	}
}

// Le direct et les flux SSE passent avec les identifiants Basic, comme le
// navigateur les renvoie de lui-même à la même origine.
func TestAuthBasic_LiveStreamOpensWithCredentials(t *testing.T) {
	server := newTestServer(t)
	server.enableBasic(t)
	liveServer := httptest.NewServer(server.Server)
	t.Cleanup(liveServer.Close)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	request, _ := http.NewRequestWithContext(ctx, http.MethodGet, liveServer.URL+"/api/events", nil)
	anonymous, err := liveServer.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	anonymous.Body.Close()
	if anonymous.StatusCode != http.StatusUnauthorized {
		t.Fatalf("anonymous stream: %d", anonymous.StatusCode)
	}
	request, _ = http.NewRequestWithContext(ctx, http.MethodGet, liveServer.URL+"/api/events", nil)
	request.SetBasicAuth(testUsername, testPassword)
	response, err := liveServer.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK || response.Header.Get("Content-Type") != "text/event-stream" {
		t.Fatalf("stream with credentials: %d %q", response.StatusCode, response.Header.Get("Content-Type"))
	}
}

func TestAuth_ForwardModeIsReservedAndClosesTheInterface(t *testing.T) {
	server := newTestServer(t)
	if err := server.db.SaveAuthState(context.Background(), auth.State{Mode: auth.ModeForward}, testNow); err != nil {
		t.Fatal(err)
	}
	if got := callAs(server.Server, http.MethodGet, "/api/counts", "", ""); got.Code != http.StatusServiceUnavailable {
		t.Fatalf("forward mode not written yet must close, got %d", got.Code)
	}
	if got := callAs(server.Server, http.MethodGet, "/statut", "", ""); got.Code != http.StatusOK {
		t.Fatalf("/statut stays public: %d", got.Code)
	}
}

func callBasicJSON(server *Server, method, path, body, password string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.Header.Set("Sec-Fetch-Site", "same-origin")
	request.Header.Set("Content-Type", "application/json")
	request.SetBasicAuth(testUsername, password)
	recorder := httptest.NewRecorder()
	server.ServeHTTP(recorder, request)
	return recorder
}
