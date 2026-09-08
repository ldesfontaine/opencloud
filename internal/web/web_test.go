package web

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/ldesfontaine/opencloud/internal/auth"
	"github.com/ldesfontaine/opencloud/internal/store"
	"github.com/ldesfontaine/opencloud/migrations"
)

// newTestAuth monte auth sur une base temporaire, avec le compte par défaut.
func newTestAuth(t *testing.T) *auth.Service {
	t.Helper()
	root, err := os.OpenRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { root.Close() })

	logger := slog.New(slog.DiscardHandler)
	testStore, err := store.Open(context.Background(), root, migrations.Files, logger)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { testStore.Close() })

	authService := auth.New(testStore, logger, auth.PasswordPolicy{MinLength: 12})
	if err := authService.EnsureDefaultAccount(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	return authService
}

func newTestServer(t *testing.T) *Server {
	t.Helper()
	return newServerWith(t, Dependencies{Auth: newTestAuth(t)})
}

func newServerWith(t *testing.T, deps Dependencies) *Server {
	t.Helper()
	server, err := New(deps, "test", slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	return server
}

// browser garde les cookies d'une réponse à l'autre, comme un navigateur.
type browser struct {
	t       *testing.T
	handler http.Handler
	cookies map[string]*http.Cookie
}

func newBrowser(t *testing.T) *browser {
	return newBrowserOf(t, newTestServer(t))
}

func newBrowserOf(t *testing.T, server *Server) *browser {
	return &browser{t: t, handler: server.Handler(), cookies: map[string]*http.Cookie{}}
}

func (b *browser) do(request *http.Request) *httptest.ResponseRecorder {
	for _, cookie := range b.cookies {
		request.AddCookie(cookie)
	}
	recorder := httptest.NewRecorder()
	b.handler.ServeHTTP(recorder, request)
	for _, cookie := range recorder.Result().Cookies() {
		if cookie.Value == "" {
			delete(b.cookies, cookie.Name)
			continue
		}
		b.cookies[cookie.Name] = cookie
	}
	return recorder
}

func (b *browser) get(path string) *httptest.ResponseRecorder {
	return b.do(httptest.NewRequest(http.MethodGet, path, nil))
}

func (b *browser) post(path string, form url.Values) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return b.do(request)
}

var csrfFieldPattern = regexp.MustCompile(`name="_csrf" value="([^"]+)"`)

// csrfFrom lit le jeton dans le formulaire rendu, comme le ferait le navigateur.
func (b *browser) csrfFrom(response *httptest.ResponseRecorder) string {
	match := csrfFieldPattern.FindStringSubmatch(response.Body.String())
	if match == nil {
		b.t.Fatalf("aucun champ _csrf dans la page :\n%s", response.Body.String())
	}
	return match[1]
}

func (b *browser) login(username, password string) *httptest.ResponseRecorder {
	csrfToken := b.csrfFrom(b.get("/login"))
	return b.post("/login", url.Values{
		csrfFieldName: {csrfToken},
		"username":    {username},
		"password":    {password},
	})
}

func (b *browser) changePassword(current, next, confirm string) *httptest.ResponseRecorder {
	csrfToken := b.csrfFrom(b.get("/password"))
	return b.post("/password", url.Values{
		csrfFieldName:      {csrfToken},
		"current_password": {current},
		"new_password":     {next},
		"confirm_password": {confirm},
	})
}

func expectRedirect(t *testing.T, response *httptest.ResponseRecorder, location string) {
	t.Helper()
	if response.Code != http.StatusSeeOther || response.Header().Get("Location") != location {
		t.Fatalf("attendu 303 vers %s, reçu %d vers %q", location, response.Code, response.Header().Get("Location"))
	}
}

func TestRoutes_AreFrozen(t *testing.T) {
	want := []string{
		"GET /{$}", "GET /actions/{id}", "GET /actions/{id}/stream",
		"GET /healthz", "GET /login", "POST /login", "POST /logout",
		"POST /machines", "GET /machines/new", "GET /machines/{id}",
		"POST /machines/{id}/access",
		"GET /machines/{id}/actions/{kind}", "POST /machines/{id}/actions/{kind}",
		"POST /machines/{id}/confirm", "POST /machines/{id}/probe",
		"GET /password", "POST /password", "GET /static/",
	}
	got := newTestServer(t).Routes()
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("routes = %v\nattendu %v", got, want)
	}
}

func TestGetRoot_Anonymous_RedirectsToLogin(t *testing.T) {
	expectRedirect(t, newBrowser(t).get("/"), "/login")
}

func TestPostLogin_WithoutCSRF_IsForbidden(t *testing.T) {
	visitor := newBrowser(t)
	visitor.get("/login")

	response := visitor.post("/login", url.Values{"username": {"admin"}, "password": {"opencloud"}})

	if response.Code != http.StatusForbidden {
		t.Fatalf("code = %d, attendu 403", response.Code)
	}
	if !strings.Contains(response.Body.String(), messageFormExpired.Cause) {
		t.Fatal("le refus doit expliquer quoi faire")
	}
}

// Sous une double soumission naïve, un cookie posé par l'attaquant et recopié
// dans le champ passerait. Ici le champ doit porter la signature du serveur.
func TestPostLogin_ForgedCookieAndField_IsForbidden(t *testing.T) {
	visitor := newBrowser(t)
	visitor.cookies[csrfCookieName] = &http.Cookie{Name: csrfCookieName, Value: "attacker"}

	response := visitor.post("/login", url.Values{
		csrfFieldName: {"attacker"},
		"username":    {"admin"},
		"password":    {"opencloud"},
	})

	if response.Code != http.StatusForbidden {
		t.Fatalf("code = %d, attendu 403", response.Code)
	}
}

func TestPostLogin_CrossSite_IsForbiddenByFetchMetadata(t *testing.T) {
	visitor := newBrowser(t)
	csrfToken := visitor.csrfFrom(visitor.get("/login"))
	request := httptest.NewRequest(http.MethodPost, "/login",
		strings.NewReader(url.Values{csrfFieldName: {csrfToken}}.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("Sec-Fetch-Site", "cross-site")

	response := visitor.do(request)

	if response.Code != http.StatusForbidden {
		t.Fatalf("code = %d, attendu 403", response.Code)
	}
}

func TestPostLogin_TooManyFailures_Is429WithMessage(t *testing.T) {
	visitor := newBrowser(t)
	for attempt := 0; attempt < 5; attempt++ {
		visitor.login(auth.DefaultUsername, "wrong")
	}

	response := visitor.login(auth.DefaultUsername, auth.DefaultPassword)

	if response.Code != http.StatusTooManyRequests {
		t.Fatalf("code = %d, attendu 429", response.Code)
	}
	if !strings.Contains(response.Body.String(), messageTooManyAttempts.Cause) {
		t.Fatal("le message doit dire d'attendre")
	}
}

func TestRedirect_HtmxRequest_AsksForFullNavigation(t *testing.T) {
	visitor := newBrowser(t)
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.Header.Set("HX-Request", "true")

	response := visitor.do(request)

	if response.Code != http.StatusOK || response.Header().Get("HX-Redirect") != "/login" {
		t.Fatalf("attendu 200 + HX-Redirect: /login, reçu %d %v", response.Code, response.Header())
	}
}

func TestPostLogin_WrongPassword_ShowsMessage(t *testing.T) {
	response := newBrowser(t).login("admin", "wrong")

	if response.Code != http.StatusUnauthorized {
		t.Fatalf("code = %d, attendu 401", response.Code)
	}
	if !strings.Contains(response.Body.String(), messageInvalidLogin.Cause) {
		t.Fatal("le message d'erreur doit s'afficher")
	}
}

func TestLogin_DefaultPassword_ForcesChangeBeforeAnythingElse(t *testing.T) {
	visitor := newBrowser(t)

	expectRedirect(t, visitor.login(auth.DefaultUsername, auth.DefaultPassword), "/")
	expectRedirect(t, visitor.get("/"), "/password")

	response := visitor.get("/password")
	if response.Code != http.StatusOK {
		t.Fatalf("code = %d", response.Code)
	}
	if !strings.Contains(response.Body.String(), messageDefaultPassword.Cause) {
		t.Fatal("la page doit dire pourquoi on est là")
	}
}

func TestChangePassword_ThenInfrastructureIsVisible(t *testing.T) {
	visitor := newBrowser(t)
	visitor.login(auth.DefaultUsername, auth.DefaultPassword)

	expectRedirect(t, visitor.changePassword(auth.DefaultPassword, "brand-new-password", "brand-new-password"), "/")

	response := visitor.get("/")
	if response.Code != http.StatusOK {
		t.Fatalf("code = %d", response.Code)
	}
	if !strings.Contains(response.Body.String(), "Aucune machine pour l'instant") {
		t.Fatal("la page Infrastructure doit être vide et le dire")
	}
}

func TestChangePassword_Mismatch_IsRefused(t *testing.T) {
	visitor := newBrowser(t)
	visitor.login(auth.DefaultUsername, auth.DefaultPassword)

	response := visitor.changePassword(auth.DefaultPassword, "one", "two")

	if response.Code != http.StatusUnprocessableEntity {
		t.Fatalf("code = %d", response.Code)
	}
	if !strings.Contains(response.Body.String(), messageConfirmationDiffers.Cause) {
		t.Fatal("le refus doit être affiché")
	}
}

func TestChangePassword_TooShort_IsRefusedAndNamesTheKey(t *testing.T) {
	visitor := newBrowser(t)
	visitor.login(auth.DefaultUsername, auth.DefaultPassword)

	response := visitor.changePassword(auth.DefaultPassword, "abc", "abc")

	if response.Code != http.StatusUnprocessableEntity {
		t.Fatalf("code = %d", response.Code)
	}
	body := response.Body.String()
	if !strings.Contains(body, "au moins 12 caractères") || !strings.Contains(body, "min_password_length") {
		t.Fatalf("le refus doit dire la règle et la clé qui la lève :\n%s", body)
	}
}

func TestPasswordPage_SaysTheMinimumLength(t *testing.T) {
	visitor := newBrowser(t)
	visitor.login(auth.DefaultUsername, auth.DefaultPassword)

	response := visitor.get("/password")

	if !strings.Contains(response.Body.String(), "Au moins 12 caractères") {
		t.Fatal("la page doit dire la règle avant qu'on la casse")
	}
}

func TestPostLogin_HugeBody_Is413(t *testing.T) {
	visitor := newBrowser(t)
	csrfToken := visitor.csrfFrom(visitor.get("/login"))
	form := url.Values{csrfFieldName: {csrfToken}, "username": {"admin"}, "password": {strings.Repeat("p", maxFormBytes)}}

	response := visitor.post("/login", form)

	if response.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("code = %d, attendu 413", response.Code)
	}
	if !strings.Contains(response.Body.String(), messageFormTooLarge) {
		t.Fatal("le refus doit dire pourquoi")
	}
}

func TestLogout_ClosesSession(t *testing.T) {
	visitor := newBrowser(t)
	visitor.login(auth.DefaultUsername, auth.DefaultPassword)
	visitor.changePassword(auth.DefaultPassword, "brand-new-password", "brand-new-password")
	csrfToken := visitor.csrfFrom(visitor.get("/"))

	expectRedirect(t, visitor.post("/logout", url.Values{csrfFieldName: {csrfToken}}), "/login")

	expectRedirect(t, visitor.get("/"), "/login")
}

func TestSessionCookie_IsSecureHttpOnlyStrict(t *testing.T) {
	visitor := newBrowser(t)

	visitor.login(auth.DefaultUsername, auth.DefaultPassword)

	for _, name := range []string{sessionCookieName, csrfCookieName} {
		cookie, found := visitor.cookies[name]
		if !found {
			t.Fatalf("cookie %s absent", name)
		}
		if !cookie.Secure || !cookie.HttpOnly || cookie.SameSite != http.SameSiteStrictMode || cookie.Path != "/" {
			t.Fatalf("cookie %s = %s", name, cookie.String())
		}
	}
}

func TestHealthz_AnswersWithoutSession(t *testing.T) {
	response := newBrowser(t).get("/healthz")

	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"version":"test"`) {
		t.Fatalf("code = %d, corps = %s", response.Code, response.Body.String())
	}
}

func TestHtmx_IsEmbeddedAndLoadedByEveryPage(t *testing.T) {
	visitor := newBrowser(t)

	script := visitor.get("/static/htmx.min.js")
	if script.Code != http.StatusOK || !strings.HasPrefix(script.Body.String(), "var htmx=") {
		t.Fatalf("htmx.min.js : code %d, début %q", script.Code, firstBytes(script.Body.String(), 20))
	}

	page := visitor.get("/login").Body.String()
	if !strings.Contains(page, `<script src="/static/htmx.min.js" defer>`) {
		t.Fatal("la mise en page doit charger htmx")
	}
	if !strings.Contains(page, `<body hx-boost:inherited="true">`) {
		t.Fatal("liens et formulaires passent par htmx (héritage explicite en htmx 4)")
	}
}

func firstBytes(text string, count int) string {
	if len(text) < count {
		return text
	}
	return text[:count]
}

func TestPages_CarrySecurityHeaders(t *testing.T) {
	response := newBrowser(t).get("/login")

	if response.Header().Get("Content-Security-Policy") == "" || response.Header().Get("X-Frame-Options") != "DENY" {
		t.Fatalf("en-têtes = %v", response.Header())
	}
}

// La barre latérale porte un onglet, l'interrupteur de thème et le menu du
// compte ; « Mot de passe » et « Se déconnecter » vivent dans le menu.
func TestSidebar_SignedIn_CarriesTheNavigationAndTheAccountMenu(t *testing.T) {
	visitor := newBrowser(t)
	visitor.login(auth.DefaultUsername, auth.DefaultPassword)
	visitor.changePassword(auth.DefaultPassword, "brand-new-password", "brand-new-password")

	body := visitor.get("/").Body.String()

	for _, expected := range []string{`<aside class="sidebar">`, `id="sidebar-toggle"`,
		`<button type="button" class="theme-switch" id="theme-switch"`,
		`<details class="account" id="account-menu">`,
		`<span class="avatar">A</span>`, "Compte", `href="/password"`,
		`action="/logout"`, "Se déconnecter", `<script src="/static/sidebar.js" defer>`} {
		if !strings.Contains(body, expected) {
			t.Fatalf("la barre latérale ne porte pas %q :\n%s", expected, body)
		}
	}
	if strings.Contains(body, "topbar") {
		t.Fatal("la barre du haut a disparu")
	}
}

// Le thème et l'état de la barre sont posés avant le premier rendu : le
// script se charge dans l'en-tête, sans defer, sur toutes les pages.
func TestAppearance_IsAppliedBeforeTheFirstPaint(t *testing.T) {
	visitor := newBrowser(t)

	for _, path := range []string{"/login", "/password"} {
		if path == "/password" {
			visitor.login(auth.DefaultUsername, auth.DefaultPassword)
		}
		body := visitor.get(path).Body.String()
		if !strings.Contains(body, `<script src="/static/appearance.js"></script>`) {
			t.Fatalf("%s doit poser le thème avant le rendu :\n%s", path, body)
		}
	}
}

func TestSidebar_TheOpenTab_IsMarked(t *testing.T) {
	visitor := newBrowser(t)
	visitor.login(auth.DefaultUsername, auth.DefaultPassword)
	visitor.changePassword(auth.DefaultPassword, "brand-new-password", "brand-new-password")

	if !strings.Contains(visitor.get("/").Body.String(), `class="nav-item current" href="/"`) {
		t.Fatal("l'onglet ouvert se voit sur l'Infrastructure")
	}
	if strings.Contains(visitor.get("/password").Body.String(), `class="nav-item current"`) {
		t.Fatal("hors de l'Infrastructure, aucun onglet n'est marqué")
	}
}

// Les fiches de machines et les actions restent sous Infrastructure :
// l'opérateur y est arrivé par là.
func TestSectionForPath_MachinesAndActions_StayUnderInfrastructure(t *testing.T) {
	underInfrastructure := []string{"/", "/machines/new", "/machines/local",
		"/machines/local/actions/diagnostiquer", "/actions/42"}
	for _, path := range underInfrastructure {
		if got := sectionForPath(path); got != sectionInfrastructure {
			t.Fatalf("sectionForPath(%q) = %q, attendu %q", path, got, sectionInfrastructure)
		}
	}

	for _, path := range []string{"/password", "/login", "/healthz"} {
		if got := sectionForPath(path); got != "" {
			t.Fatalf("sectionForPath(%q) = %q, attendu aucune section", path, got)
		}
	}
}

// Avant connexion, ni onglet ni menu : la page de connexion ne montre que
// son formulaire.
func TestSidebar_Anonymous_HasNoSidebar(t *testing.T) {
	body := newBrowser(t).get("/login").Body.String()

	if strings.Contains(body, "sidebar") || strings.Contains(body, "Se déconnecter") {
		t.Fatalf("aucune barre avant connexion :\n%s", body)
	}
}

// Les icônes que Go nomme et celles que le gabarit dessine sont le même jeu :
// un nom sans dessin ne se verrait qu'à l'écran.
func TestIcons_EveryNameFromGo_IsDrawnByTheTemplate(t *testing.T) {
	server := newTestServer(t)
	names := []string{iconActivity, iconAlert, iconCheck, iconClock, iconInfo, iconKey,
		iconRefresh, iconServer, iconShield, iconTerminal, iconX}

	for _, name := range names {
		var drawn strings.Builder
		if err := server.templates["login"].ExecuteTemplate(&drawn, "icon", name); err != nil {
			t.Fatalf("icône %q : %v", name, err)
		}
		if !strings.Contains(drawn.String(), `<svg class="icon"`) {
			t.Fatalf("icône %q : aucun dessin", name)
		}
	}
}

// Un refus se lit toujours pareil : la cause, puis le geste qui la lève.
func TestRefusal_LooksTheSameEverywhere(t *testing.T) {
	body := newBrowser(t).login("admin", "wrong").Body.String()

	for _, expected := range []string{`<section class="notice error">`, `<p class="cause">`, `<p class="remedy">`} {
		if !strings.Contains(body, expected) {
			t.Fatalf("le refus ne prend pas la forme commune (%s) :\n%s", expected, body)
		}
	}
}
