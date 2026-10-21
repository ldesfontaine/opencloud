package server

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ldesfontaine/opencloud/internal/server/servertest"
	"github.com/ldesfontaine/opencloud/internal/store/storetest"
)

// Sans compte, chaque route est jugée : celles de la première configuration
// passent, les autres routes de l'opérateur répondent 401
// auth.setup_required sans défi Basic, et les routes ouvertes le restent,
// les mêmes qu'en basic et en forward.
func TestFirstSetup_ServesOnlyTheSetupRoutesAndLeavesTheOthersOpen(t *testing.T) {
	server := newFirstSetupServer(t)
	open := map[string]bool{}
	for _, route := range server.routes() {
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		recorder := callAsWithin(ctx, server.Server, concreteMethod(route.method), concretePath(route.pattern), "", "")
		cancel()
		refused := recorder.Code == http.StatusUnauthorized
		if got := recorder.Header().Get("WWW-Authenticate"); strings.HasPrefix(got, "Basic") {
			t.Errorf("%s %s: a Basic challenge without any account to give", route.method, route.pattern)
		}
		switch {
		case !isOperatorRoute(route.pattern):
			open[route.pattern] = true
			if refused && strings.Contains(recorder.Body.String(), codeSetupRequired) {
				t.Errorf("%s %s: an open route waits for the first setup", route.method, route.pattern)
			}
		case isFirstSetupRoute(route.method, route.pattern):
			if refused {
				t.Errorf("%s %s: status 401, the first setup needs it", route.method, route.pattern)
			}
		case !refused:
			t.Errorf("%s %s: status %d, want 401", route.method, route.pattern, recorder.Code)
		case strings.HasPrefix(route.pattern, "/api/"):
			if code := errorCode(t, recorder); code != codeSetupRequired {
				t.Errorf("%s %s: refusal %q, want %q", route.method, route.pattern, code, codeSetupRequired)
			}
		}
	}
	checkOpenRoutes(t, open)
}

// Le parcours de l'écran : lire l'état, créer le compte, puis l'interface
// s'ouvre avec ces identifiants, et seulement avec eux.
func TestFirstSetup_EnableOpensTheRoutesToTheNewAccount(t *testing.T) {
	server := newFirstSetupServer(t)
	requireSetupRequired(t, callAs(server.Server, http.MethodGet, "/api/machines", "", ""))

	read := callAs(server.Server, http.MethodGet, "/api/auth", "", "")
	var state authResponse
	decodeAPI(t, read, &state)
	if read.Code != http.StatusOK || state.Mode != "none" || state.Username != "" {
		t.Fatalf("read before the setup: %d %+v", read.Code, state)
	}

	enabled := callAPI(server.Server, http.MethodPost, "/api/auth/actions/enable", authEnableRequest{Username: testUsername, Password: testPassword})
	if enabled.Code != http.StatusOK {
		t.Fatalf("enable: %d %s", enabled.Code, enabled.Body.String())
	}
	if got := callAs(server.Server, http.MethodGet, "/api/machines", testUsername, testPassword); got.Code != http.StatusOK {
		t.Fatalf("with the new account: %d %s", got.Code, got.Body.String())
	}
	anonymous := callAs(server.Server, http.MethodGet, "/api/machines", "", "")
	if anonymous.Code != http.StatusUnauthorized || errorCode(t, anonymous) != codeUnauthorized || anonymous.Header().Get("WWW-Authenticate") == "" {
		t.Fatalf("without credentials after the setup: %d %s", anonymous.Code, anonymous.Body.String())
	}
	// La création du compte n'est servie qu'une fois : ensuite, elle exige le
	// compte, et le refuse.
	if got := callAs(server.Server, http.MethodPost, "/api/auth/actions/enable", "", ""); got.Code != http.StatusUnauthorized {
		t.Fatalf("a second anonymous setup: %d", got.Code)
	}
}

// L'écran de première configuration se charge sans compte : la page, ses
// fichiers, la session et le catalogue de sa langue.
func TestFirstSetup_TheFrontLoadsWithoutAnAccount(t *testing.T) {
	servertest.RequireFront(t)
	server := newFirstSetupServer(t)
	for _, path := range []string{"/", "/machines", "/favicon.svg", "/api/session", "/api/i18n/fr"} {
		if got := callAs(server.Server, http.MethodGet, path, "", ""); got.Code != http.StatusOK {
			t.Errorf("%s: status %d", path, got.Code)
		}
	}
}

// Les routes publiques ne dépendent pas de la première configuration : la
// page de statut et les pings répondent comme d'habitude.
func TestFirstSetup_PublicRoutesStayPublic(t *testing.T) {
	server := newFirstSetupServer(t)
	storetest.StatusPage(t, server.db, "statut")
	if got := callAs(server.Server, http.MethodGet, "/statut/statut/api/status", "", ""); got.Code != http.StatusOK {
		t.Fatalf("/statut/statut/api/status: %d", got.Code)
	}
	if got := callAs(server.Server, http.MethodGet, "/ping/hb_unknown", "", ""); got.Code != http.StatusNotFound {
		t.Fatalf("/ping: %d, want the ping's own 404", got.Code)
	}
	if got := callAs(server.Server, http.MethodGet, "/.well-known/oauth-authorization-server", "", ""); got.Code == http.StatusUnauthorized {
		t.Fatalf("well-known: %d", got.Code)
	}
}

// Hors /api, le refus dit d'ouvrir l'interface, dans la langue du navigateur.
func TestFirstSetup_RefusedPageSaysToOpenTheInterface(t *testing.T) {
	server := newFirstSetupServer(t)
	request := httptest.NewRequest(http.MethodGet, mcpAuthorizePath, nil)
	request.Header.Set("Accept-Language", "fr-FR,fr;q=0.9")
	recorder := httptest.NewRecorder()
	server.ServeHTTP(recorder, request)
	want := "Première configuration en attente : ouvrez openCloud dans le navigateur et créez le compte de l'opérateur."
	if recorder.Code != http.StatusUnauthorized || strings.TrimSpace(recorder.Body.String()) != want {
		t.Fatalf("status %d, body %q", recorder.Code, recorder.Body.String())
	}
}

// Une écriture refusée faute de compte l'est sur ses en-têtes : son corps
// n'est pas lu.
func TestFirstSetup_RefusesAWriteWithoutReadingItsBody(t *testing.T) {
	server := newFirstSetupServer(t)
	reading := make(chan struct{})
	body := &signalingBody{ReadCloser: io.NopCloser(strings.NewReader(`{"name":"web"}`)), reading: reading}
	request := httptest.NewRequest(http.MethodPost, "/api/machines/tokens", body)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Sec-Fetch-Site", "same-origin")
	recorder := httptest.NewRecorder()
	server.ServeHTTP(recorder, request)
	requireSetupRequired(t, recorder)
	select {
	case <-reading:
		t.Fatal("the body of a write refused for want of an account was read")
	default:
	}
}
