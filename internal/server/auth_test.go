package server

import (
	"context"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ldesfontaine/opencloud/internal/auth"
	"github.com/ldesfontaine/opencloud/internal/mcp"
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
	checkOpenRoutes(t, open)
}

// La liste des ouvertes est une décision : la voici, entière, la même pour
// chaque mode.
var wantOpenRoutes = []string{
	"/agent/enroll", "/agent/challenge", "/agent/stream", "/agent/signal", "/agent/logs/{request}",
	"/ping/{token}", "/ping/{token}/start", "/ping/{token}/{code}",
	"/statut", "/statut/{$}", "/statut/api/status", "/statut/api/i18n", "/statut/api/events", "/statut/",
	"/.well-known/oauth-authorization-server", "/.well-known/oauth-protected-resource", "/.well-known/oauth-protected-resource/mcp",
	"/oauth/token", "/mcp",
}

func checkOpenRoutes(t *testing.T, open map[string]bool) {
	t.Helper()
	if len(open) != len(wantOpenRoutes) {
		t.Errorf("open routes %v, want %v", open, wantOpenRoutes)
	}
	for _, pattern := range wantOpenRoutes {
		if !open[pattern] {
			t.Errorf("%s should stay open", pattern)
		}
	}
	if open["/oauth/authorize"] || open["/"] || open["/api/events"] || open["/api/containers/{id}/logs/stream"] {
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

// La page de statut charge ses scripts, styles et icône par le fourre-tout
// de l'interface : ils restent lisibles sans identifiants, la page de
// l'interface elle-même non.
func TestAuthBasic_StatusPageAssetsStayPublic(t *testing.T) {
	server := newTestServer(t)
	server.enableBasic(t)
	assets, err := fs.Glob(server.app, "assets/*")
	if err != nil || len(assets) == 0 {
		t.Fatalf("no compiled assets: %v", err)
	}
	for _, asset := range append(assets, "theme.js", "favicon.svg") {
		if got := callAs(server.Server, http.MethodGet, "/"+asset, "", ""); got.Code != http.StatusOK {
			t.Fatalf("/%s: status %d", asset, got.Code)
		}
	}
	for _, page := range []string{"/", "/index.html", "/machines"} {
		if got := callAs(server.Server, http.MethodGet, page, "", ""); got.Code != http.StatusUnauthorized {
			t.Fatalf("%s: status %d, want 401", page, got.Code)
		}
	}
}

// Le refus d'une page, affiché quand l'opérateur annule la fenêtre du
// navigateur, dit comment rouvrir l'accès, dans la langue du navigateur.
func TestAuthBasic_RefusedPageSaysHowToRecoverALostPassword(t *testing.T) {
	server := newTestServer(t)
	server.enableBasic(t)
	cases := map[string]string{
		"fr-FR,fr;q=0.9,en;q=0.8": "Identifiants requis. Mot de passe perdu : opencloud auth disable -config <chemin de la config>",
		"en-US,en;q=0.9":          "Credentials required. Lost password: opencloud auth disable -config <config path>",
		"":                        "Credentials required. Lost password: opencloud auth disable -config <config path>",
	}
	for acceptLanguage, want := range cases {
		request := httptest.NewRequest(http.MethodGet, "/machines", nil)
		request.Header.Set("Accept-Language", acceptLanguage)
		recorder := httptest.NewRecorder()
		server.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusUnauthorized {
			t.Fatalf("%q: status %d, want 401", acceptLanguage, recorder.Code)
		}
		if got := strings.TrimSpace(recorder.Body.String()); got != want {
			t.Errorf("%q: body %q, want %q", acceptLanguage, got, want)
		}
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

// Activer l'authentification révoque tous les jetons MCP, d'API et OAuth,
// et dit combien ; le client OAuth reste. Changer le mot de passe ou
// désactiver ne révoque rien.
func TestAuthAPI_EnableRevokesEveryMCPTokenAndPasswordChangeDoesNot(t *testing.T) {
	server := newTestServer(t)
	liveServer := httptest.NewServer(server.Server)
	t.Cleanup(liveServer.Close)
	endpoint := liveServer.URL + mcpPath
	secret := server.enableMCP(t)
	reader := server.createAPIToken(t, "lecteur")
	writer := server.createScopedToken(t, mcpTokenRequest{Name: "écrivain", Scope: string(mcp.ScopeWrite)})
	grant := server.oauthGrant(t, secret)

	enabled := callAPI(server.Server, http.MethodPost, "/api/auth/actions/enable", authEnableRequest{Username: testUsername, Password: testPassword})
	if enabled.Code != http.StatusOK {
		t.Fatalf("enable: %d %s", enabled.Code, enabled.Body.String())
	}
	var response authEnableResponse
	decodeAPI(t, enabled, &response)
	if response.Mode != "basic" || response.MCPRevoked != (mcpRevokedJSON{APITokens: 2, Sessions: 1}) {
		t.Fatalf("response %+v", response)
	}
	for _, bearer := range []string{reader, writer, grant.AccessToken} {
		if _, err := listToolsOverHTTP(t, endpoint, bearer); err == nil {
			t.Fatal("a token survived the activation")
		}
	}
	// Le client OAuth reste : une nouvelle autorisation passe, désormais
	// derrière les identifiants.
	if _, enabled, err := server.access.Client(context.Background()); err != nil || !enabled {
		t.Fatalf("client gone: %v %v", enabled, err)
	}

	// Un jeton créé après l'activation survit au changement de mot de
	// passe et à la désactivation.
	created := callBasicJSON(server.Server, http.MethodPost, "/api/mcp/tokens", `{"name":"après"}`, testPassword)
	if created.Code != http.StatusCreated {
		t.Fatalf("create after: %d %s", created.Code, created.Body.String())
	}
	var after mcpTokenResponse
	decodeAPI(t, created, &after)
	const newPassword = "another long passphrase"
	if got := callBasicJSON(server.Server, http.MethodPut, "/api/auth/password", `{"password":"`+newPassword+`"}`, testPassword); got.Code != http.StatusOK {
		t.Fatalf("change: %d %s", got.Code, got.Body.String())
	}
	if _, err := listToolsOverHTTP(t, endpoint, after.Token); err != nil {
		t.Fatalf("password change revoked a token: %v", err)
	}
	if got := callBasicJSON(server.Server, http.MethodPost, "/api/auth/actions/disable", `{}`, newPassword); got.Code != http.StatusOK {
		t.Fatalf("disable: %d %s", got.Code, got.Body.String())
	}
	if _, err := listToolsOverHTTP(t, endpoint, after.Token); err != nil {
		t.Fatalf("disabling revoked a token: %v", err)
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

// Les pairs des requêtes du mode forward : httptest donne 192.0.2.1, et le
// mandataire déclaré est son réseau ; l'autre est un conteneur voisin.
const (
	trustedPeer   = "192.0.2.1"
	untrustedPeer = "203.0.113.9"
)

// enableForward passe le serveur en mode forward derrière un mandataire
// déclaré, l'en-tête par défaut.
func (ts *testServer) enableForward(t *testing.T) {
	t.Helper()
	ts.trustedProxies = parsePrefixes(t, "192.0.2.0/24")
	if _, err := ts.operator.EnableForward(context.Background(), auth.DefaultForwardHeader); err != nil {
		t.Fatal(err)
	}
}

// callForwarded joue une requête depuis peer, avec l'identité dans
// Remote-User quand elle n'est pas vide, comme le poserait Authelia.
func callForwarded(server *Server, method, path, peer, identity string) *httptest.ResponseRecorder {
	return callForwardedWithin(context.Background(), server, method, path, peer, identity)
}

func callForwardedWithin(ctx context.Context, server *Server, method, path, peer, identity string) *httptest.ResponseRecorder {
	request := httptest.NewRequestWithContext(ctx, method, path, nil)
	request.RemoteAddr = peer + ":4000"
	request.Header.Set("Sec-Fetch-Site", "same-origin")
	if identity != "" {
		request.Header.Set(auth.DefaultForwardHeader, identity)
	}
	recorder := httptest.NewRecorder()
	server.ServeHTTP(recorder, request)
	return recorder
}

// callForwardedJSON : une écriture de l'API comme le front derrière le
// mandataire, avec un en-tête choisi.
func callForwardedJSON(server *Server, method, path, body, peer, header, identity string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.RemoteAddr = peer + ":4000"
	request.Header.Set("Sec-Fetch-Site", "same-origin")
	request.Header.Set("Content-Type", "application/json")
	if identity != "" {
		request.Header.Set(header, identity)
	}
	recorder := httptest.NewRecorder()
	server.ServeHTTP(recorder, request)
	return recorder
}

// Le jumeau du test basic : depuis le mandataire mais sans identité, chaque
// route de l'opérateur répond 401 sans défi Basic ; les ouvertes répondent
// exactement ce qu'elles répondent sans authentification.
func TestAuthForward_GuardsEveryOperatorRouteAndLeavesTheOthersOpen(t *testing.T) {
	reference := newTestServer(t)
	server := newTestServer(t)
	server.enableForward(t)
	open := map[string]bool{}
	for _, route := range server.routes() {
		path := concretePath(route.pattern)
		method := concreteMethod(route.method)
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		recorder := callForwardedWithin(ctx, server.Server, method, path, trustedPeer, "")
		cancel()
		if isOperatorRoute(route.pattern) {
			if recorder.Code != http.StatusUnauthorized {
				t.Errorf("%s %s: status %d, want 401", route.method, route.pattern, recorder.Code)
			}
			if got := recorder.Header().Get("WWW-Authenticate"); got != "" {
				t.Errorf("%s %s: forward mode must not challenge, got %q", route.method, route.pattern, got)
			}
			continue
		}
		open[route.pattern] = true
		ctx, cancel = context.WithTimeout(context.Background(), 100*time.Millisecond)
		unguarded := callForwardedWithin(ctx, reference.Server, method, path, trustedPeer, "")
		cancel()
		if recorder.Code != unguarded.Code {
			t.Errorf("%s %s: status %d in forward mode, %d without authentication", route.method, route.pattern, recorder.Code, unguarded.Code)
		}
	}
	checkOpenRoutes(t, open)
}

// Seul un mandataire déclaré qui porte l'identité passe : ni le mandataire
// sans identité, ni un conteneur voisin qui écrit l'en-tête lui-même.
func TestAuthForward_OnlyATrustedProxyWithTheIdentityPasses(t *testing.T) {
	server := newTestServer(t)
	server.enableForward(t)
	if got := callForwarded(server.Server, http.MethodGet, "/api/counts", trustedPeer, testUsername); got.Code != http.StatusOK {
		t.Fatalf("trusted proxy with identity: status %d", got.Code)
	}
	if got := callForwarded(server.Server, http.MethodGet, "/machines", trustedPeer, testUsername); got.Code != http.StatusOK {
		t.Fatalf("front through the proxy: status %d", got.Code)
	}
	missing := callForwarded(server.Server, http.MethodGet, "/api/counts", trustedPeer, "")
	if missing.Code != http.StatusUnauthorized || errorCode(t, missing) != codeUnauthorized {
		t.Fatalf("trusted proxy without identity: status %d %s", missing.Code, missing.Body.String())
	}
	if got := callForwarded(server.Server, http.MethodGet, "/api/counts", trustedPeer, "   "); got.Code != http.StatusUnauthorized {
		t.Fatalf("blank identity: status %d", got.Code)
	}
	forged := callForwarded(server.Server, http.MethodGet, "/api/counts", untrustedPeer, testUsername)
	if forged.Code != http.StatusUnauthorized || errorCode(t, forged) != codeUnauthorized {
		t.Fatalf("untrusted peer with identity: status %d %s", forged.Code, forged.Body.String())
	}
	if got := forged.Header().Get("WWW-Authenticate"); got != "" {
		t.Fatalf("no Basic challenge in forward mode, got %q", got)
	}
	// Basic ne rouvre rien : il n'y a pas de mot de passe en forward.
	if got := callAs(server.Server, http.MethodGet, "/api/counts", testUsername, testPassword); got.Code != http.StatusUnauthorized {
		t.Fatalf("basic credentials in forward mode: status %d", got.Code)
	}
	if got := callForwarded(server.Server, http.MethodGet, "/statut", untrustedPeer, ""); got.Code != http.StatusOK {
		t.Fatalf("/statut stays public: %d", got.Code)
	}
	if got := callForwarded(server.Server, http.MethodGet, "/ping/hb_unknown", untrustedPeer, ""); got.Code != http.StatusNotFound {
		t.Fatalf("/ping: status %d, want the ping's own 404", got.Code)
	}
}

// Un mandataire qui ajoute l'identité au lieu d'écraser laisse passer celle
// du client en premier : deux valeurs, c'est un refus, dans les deux ordres.
func TestAuthForward_IdentityHeaderSentTwiceIsRefused(t *testing.T) {
	server := newTestServer(t)
	server.enableForward(t)
	for _, values := range [][]string{{"admin", testUsername}, {testUsername, testUsername}} {
		request := httptest.NewRequest(http.MethodGet, "/api/counts", nil)
		request.RemoteAddr = trustedPeer + ":4000"
		request.Header.Set("Sec-Fetch-Site", "same-origin")
		for _, value := range values {
			request.Header.Add(auth.DefaultForwardHeader, value)
		}
		recorder := httptest.NewRecorder()
		server.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusUnauthorized || errorCode(t, recorder) != codeUnauthorized {
			t.Fatalf("identity %v: status %d %s", values, recorder.Code, recorder.Body.String())
		}
	}
}

// Le refus d'une page dit de passer par le mandataire, et comment rouvrir
// l'accès, dans la langue du navigateur.
func TestAuthForward_RefusedPageSaysToGoThroughTheProxy(t *testing.T) {
	server := newTestServer(t)
	server.enableForward(t)
	cases := map[string]string{
		"fr-FR,fr;q=0.9": "Accès refusé : passez par le mandataire d'authentification. Pour rouvrir l'accès : opencloud auth disable -config <chemin de la config>",
		"en-US,en;q=0.9": "Access denied: go through the authentication proxy. To reopen access: opencloud auth disable -config <config path>",
	}
	for acceptLanguage, want := range cases {
		request := httptest.NewRequest(http.MethodGet, "/machines", nil)
		request.RemoteAddr = untrustedPeer + ":4000"
		request.Header.Set("Accept-Language", acceptLanguage)
		recorder := httptest.NewRecorder()
		server.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusUnauthorized {
			t.Fatalf("%q: status %d, want 401", acceptLanguage, recorder.Code)
		}
		if got := strings.TrimSpace(recorder.Body.String()); got != want {
			t.Errorf("%q: body %q, want %q", acceptLanguage, got, want)
		}
	}
}

// Le garde-fou : on n'active forward que depuis une requête qui porte déjà
// l'identité d'un mandataire de confiance, sans quoi on s'enfermerait dehors.
func TestAuthAPI_EnableForwardRequiresTheIdentityFromATrustedProxy(t *testing.T) {
	server := newTestServer(t)
	server.trustedProxies = parsePrefixes(t, "192.0.2.0/24")
	const enable = "/api/auth/actions/enable"
	const remoteUser = auth.DefaultForwardHeader

	missing := callForwardedJSON(server.Server, http.MethodPost, enable, `{"mode":"forward"}`, trustedPeer, remoteUser, "")
	if missing.Code != http.StatusUnprocessableEntity || errorCode(t, missing) != "auth.forward_header_missing" {
		t.Fatalf("without the header: %d %s", missing.Code, missing.Body.String())
	}
	forged := callForwardedJSON(server.Server, http.MethodPost, enable, `{"mode":"forward"}`, untrustedPeer, remoteUser, testUsername)
	if forged.Code != http.StatusUnprocessableEntity || errorCode(t, forged) != "auth.forward_header_missing" {
		t.Fatalf("from an untrusted peer: %d %s", forged.Code, forged.Body.String())
	}
	other := callForwardedJSON(server.Server, http.MethodPost, enable, `{"mode":"forward","header":"X-Forwarded-User"}`, trustedPeer, remoteUser, testUsername)
	if other.Code != http.StatusUnprocessableEntity || errorCode(t, other) != "auth.forward_header_missing" {
		t.Fatalf("carrying another header than the one asked: %d %s", other.Code, other.Body.String())
	}
	invalid := callForwardedJSON(server.Server, http.MethodPost, enable, `{"mode":"forward","header":"Remote User"}`, trustedPeer, remoteUser, testUsername)
	if invalid.Code != http.StatusUnprocessableEntity || errorCode(t, invalid) != "auth.header_invalid" {
		t.Fatalf("invalid header name: %d %s", invalid.Code, invalid.Body.String())
	}
	unknown := callForwardedJSON(server.Server, http.MethodPost, enable, `{"mode":"none"}`, trustedPeer, remoteUser, testUsername)
	if unknown.Code != http.StatusUnprocessableEntity || errorCode(t, unknown) != "auth.mode_invalid" {
		t.Fatalf("unknown mode: %d %s", unknown.Code, unknown.Body.String())
	}
	var state authResponse
	decodeAPI(t, callAPI(server.Server, http.MethodGet, "/api/auth", nil), &state)
	if state.Mode != "none" {
		t.Fatalf("a refused activation changed the mode: %+v", state)
	}

	enabled := callForwardedJSON(server.Server, http.MethodPost, enable, `{"mode":"forward"}`, trustedPeer, remoteUser, testUsername)
	if enabled.Code != http.StatusOK {
		t.Fatalf("enable: %d %s", enabled.Code, enabled.Body.String())
	}
	var response authEnableResponse
	decodeAPI(t, enabled, &response)
	if response.Mode != "forward" || response.Header != remoteUser || response.Username != testUsername ||
		response.EnabledAt == nil || !response.EnabledAt.Equal(testNow) {
		t.Fatalf("enabled %+v", response)
	}

	read := callForwarded(server.Server, http.MethodGet, "/api/auth", trustedPeer, "marie")
	if read.Code != http.StatusOK {
		t.Fatalf("read through the proxy: %d", read.Code)
	}
	decodeAPI(t, read, &state)
	if state.Mode != "forward" || state.Header != remoteUser || state.Username != "marie" {
		t.Fatalf("the identity of the current request is shown: %+v", state)
	}
	if got := callAPI(server.Server, http.MethodGet, "/api/auth", nil); got.Code != http.StatusUnauthorized {
		t.Fatalf("after enabling, a request without identity: %d", got.Code)
	}
	change := callForwardedJSON(server.Server, http.MethodPut, "/api/auth/password", `{"password":"another long passphrase"}`, trustedPeer, remoteUser, testUsername)
	if change.Code != http.StatusConflict || errorCode(t, change) != "auth.no_password" {
		t.Fatalf("change password in forward mode: %d %s", change.Code, change.Body.String())
	}
	again := callForwardedJSON(server.Server, http.MethodPost, enable, `{"mode":"forward"}`, trustedPeer, remoteUser, testUsername)
	if again.Code != http.StatusConflict || errorCode(t, again) != "auth.already_enabled" {
		t.Fatalf("enable twice: %d %s", again.Code, again.Body.String())
	}
	disabled := callForwardedJSON(server.Server, http.MethodPost, "/api/auth/actions/disable", `{}`, trustedPeer, remoteUser, testUsername)
	if disabled.Code != http.StatusOK {
		t.Fatalf("disable: %d %s", disabled.Code, disabled.Body.String())
	}
	if got := callAs(server.Server, http.MethodGet, "/api/counts", "", ""); got.Code != http.StatusOK {
		t.Fatalf("after disabling, anonymous: %d", got.Code)
	}
}

// Un en-tête choisi remplace Remote-User, et l'activation révoque les
// jetons MCP comme en basic.
func TestAuthAPI_EnableForwardWithAnotherHeaderRevokesMCPTokens(t *testing.T) {
	server := newTestServer(t)
	server.trustedProxies = parsePrefixes(t, "192.0.2.0/24")
	server.enableMCP(t)
	server.createAPIToken(t, "lecteur")
	const header = "X-Forwarded-User"
	enabled := callForwardedJSON(server.Server, http.MethodPost, "/api/auth/actions/enable", `{"mode":"forward","header":"`+header+`"}`, trustedPeer, header, testUsername)
	if enabled.Code != http.StatusOK {
		t.Fatalf("enable: %d %s", enabled.Code, enabled.Body.String())
	}
	var response authEnableResponse
	decodeAPI(t, enabled, &response)
	if response.Header != header || response.MCPRevoked != (mcpRevokedJSON{APITokens: 1, Sessions: 0}) {
		t.Fatalf("response %+v", response)
	}
	// Remote-User ne vaut plus rien : seul l'en-tête choisi porte l'identité.
	if got := callForwarded(server.Server, http.MethodGet, "/api/counts", trustedPeer, testUsername); got.Code != http.StatusUnauthorized {
		t.Fatalf("Remote-User with another header configured: %d", got.Code)
	}
	ok := callForwardedJSON(server.Server, http.MethodGet, "/api/counts", "", trustedPeer, header, testUsername)
	if ok.Code != http.StatusOK {
		t.Fatalf("the configured header: %d %s", ok.Code, ok.Body.String())
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
