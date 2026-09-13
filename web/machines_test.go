package web

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/ldesfontaine/opencloud/internal/lang"
	"github.com/ldesfontaine/opencloud/internal/machine"
)

func postForm(server *Server, cookie *http.Cookie, path string, form url.Values, htmx bool) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if htmx {
		request.Header.Set("HX-Request", "true")
	}
	if cookie != nil {
		request.AddCookie(cookie)
	}
	recorder := httptest.NewRecorder()
	server.ServeHTTP(recorder, request)
	return recorder
}

func TestCreateMachineToken_ShowsTheTokenOnceAndListsItAsPending(t *testing.T) {
	server := newTestServer(t)
	cookie := csrfCookieFrom(t, server.Server)
	recorder := postForm(server.Server, cookie, "/machines/nouvelle", url.Values{"csrf": {cookie.Value}, "name": {"vps-paris-1"}}, false)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status %d: %s", recorder.Code, recorder.Body.String())
	}
	body := recorder.Body.String()
	if !tokenValue.MatchString(body) || !strings.Contains(body, "opencloud agent -server http://example.com -token oc_") {
		t.Fatalf("token page lacks the token or the command:\n%s", body)
	}
	pending, err := server.machines.PendingTokens(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 || pending[0].Name != "vps-paris-1" {
		t.Fatalf("pending %+v", pending)
	}
	list := httptest.NewRecorder()
	server.ServeHTTP(list, httptest.NewRequest(http.MethodGet, "/machines", nil))
	if !strings.Contains(list.Body.String(), pending[0].Masked()) || tokenValue.MatchString(list.Body.String()) {
		t.Fatal("the list must show the masked token and never the cleartext")
	}
}

func TestCreateMachineToken_BadName_ReturnsTheFormWithAnError(t *testing.T) {
	server := newTestServer(t)
	cookie := csrfCookieFrom(t, server.Server)
	recorder := postForm(server.Server, cookie, "/machines/nouvelle", url.Values{"csrf": {cookie.Value}, "name": {"VPS Paris"}}, false)
	if recorder.Code != http.StatusUnprocessableEntity || !strings.Contains(recorder.Body.String(), `value="VPS Paris"`) {
		t.Fatalf("status %d", recorder.Code)
	}
}

func TestMachineActions_RequireCSRF(t *testing.T) {
	server := newTestServer(t)
	server.enroll(t, "vps-paris-1", remoteID)
	for _, path := range []string{"/machines/nouvelle", "/machines/" + remoteID + "/actions/retirer", "/machines/" + remoteID + "/actions/reenroler", "/machines/jetons/x/actions/annuler"} {
		if got := postForm(server.Server, nil, path, url.Values{"name": {"x"}}, false); got.Code != http.StatusForbidden {
			t.Errorf("%s without csrf: %d", path, got.Code)
		}
	}
}

func TestRemoveMachine_RedirectsAndForgetsIt(t *testing.T) {
	server := newTestServer(t)
	server.enroll(t, "vps-paris-1", remoteID)
	cookie := csrfCookieFrom(t, server.Server)
	form := url.Values{"csrf": {cookie.Value}}

	htmx := postForm(server.Server, cookie, "/machines/"+remoteID+"/actions/retirer", form, true)
	if htmx.Code != http.StatusOK || htmx.Header().Get("HX-Redirect") != "/machines" {
		t.Fatalf("htmx removal: %d %v", htmx.Code, htmx.Header())
	}
	if _, err := server.machines.Get(context.Background(), remoteID); !errors.Is(err, machine.ErrNotFound) {
		t.Fatalf("machine still there: %v", err)
	}
	if again := postForm(server.Server, cookie, "/machines/"+remoteID+"/actions/retirer", form, false); again.Code != http.StatusNotFound {
		t.Errorf("second removal: %d", again.Code)
	}
	if local := postForm(server.Server, cookie, "/machines/local/actions/retirer", form, false); local.Code != http.StatusForbidden {
		t.Errorf("local removal: %d", local.Code)
	}
}

func TestReenrollMachine_IssuesATokenBoundToTheMachine(t *testing.T) {
	server := newTestServer(t)
	server.enroll(t, "vps-paris-1", remoteID)
	cookie := csrfCookieFrom(t, server.Server)
	recorder := postForm(server.Server, cookie, "/machines/"+remoteID+"/actions/reenroler", url.Values{"csrf": {cookie.Value}}, false)
	if recorder.Code != http.StatusOK || !tokenValue.MatchString(recorder.Body.String()) {
		t.Fatalf("status %d", recorder.Code)
	}
	pending, err := server.machines.PendingTokens(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 || pending[0].MachineID != remoteID {
		t.Fatalf("pending %+v", pending)
	}
}

func TestCancelToken_RemovesItFromThePendingList(t *testing.T) {
	server := newTestServer(t)
	if _, _, err := server.machines.CreateToken(context.Background(), "vps-lyon-2"); err != nil {
		t.Fatal(err)
	}
	pending, _ := server.machines.PendingTokens(context.Background())
	cookie := csrfCookieFrom(t, server.Server)
	recorder := postForm(server.Server, cookie, "/machines/jetons/"+pending[0].ID+"/actions/annuler", url.Values{"csrf": {cookie.Value}}, false)
	if recorder.Code != http.StatusSeeOther {
		t.Fatalf("status %d", recorder.Code)
	}
	if left, _ := server.machines.PendingTokens(context.Background()); len(left) != 0 {
		t.Fatalf("still pending: %+v", left)
	}
}

func TestMachinePage_UnknownMachineOrTab_Is404(t *testing.T) {
	server := newTestServer(t)
	server.enroll(t, "vps-paris-1", remoteID)
	for _, path := range []string{"/machines/nobody", "/machines/" + remoteID + "/inconnu"} {
		recorder := httptest.NewRecorder()
		server.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
		if recorder.Code != http.StatusNotFound {
			t.Errorf("%s: %d", path, recorder.Code)
		}
	}
}

func TestFragments_RenderWithoutTheShell(t *testing.T) {
	server := newTestServer(t)
	server.enroll(t, "vps-paris-1", remoteID)
	for _, path := range []string{"/machines/tableau", "/machines/" + remoteID + "/en-tete"} {
		recorder := httptest.NewRecorder()
		server.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
		body := recorder.Body.String()
		if recorder.Code != http.StatusOK || strings.Contains(body, "<html") || !strings.Contains(body, "vps-paris-1") {
			t.Errorf("%s: %d\n%s", path, recorder.Code, body)
		}
	}
}

func TestNavigation_CountsMachines(t *testing.T) {
	server := newTestServer(t)
	server.enroll(t, "vps-paris-1", remoteID)
	recorder := httptest.NewRecorder()
	server.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/", nil))
	// La machine distante n'a pas de flux : le compteur passe au rouge.
	if !strings.Contains(recorder.Body.String(), `<span class="count hot">2</span>`) {
		t.Fatal("sidebar does not count the two machines, one of them offline")
	}
}

func TestPublicURL_PrefersConfigThenTrustedProxyThenHost(t *testing.T) {
	server := newTestServer(t)
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.Host = "oc.example.fr"
	request.Header.Set("X-Forwarded-Host", "proxy.example.fr")
	request.Header.Set("X-Forwarded-Proto", "https")
	if got, local := server.resolvePublicURL(request); got != "http://oc.example.fr" || local {
		t.Errorf("untrusted proxy: %q %v", got, local)
	}
	server.trustedProxies = parsePrefixes(t, "192.0.2.0/24")
	if got, _ := server.resolvePublicURL(request); got != "https://proxy.example.fr" {
		t.Errorf("trusted proxy: %q", got)
	}
	server.publicURL = "https://public.example.fr/"
	if got, _ := server.resolvePublicURL(request); got != "https://public.example.fr" {
		t.Errorf("config: %q", got)
	}
	server.publicURL = "http://127.0.0.1:8080"
	if _, local := server.resolvePublicURL(request); !local {
		t.Error("loopback not flagged as local")
	}
}

func TestInstallCommand_CarriesTheLanguageOnlyWhenNotDefault(t *testing.T) {
	if got := installCommand("https://oc.example.fr", "oc_x", lang.French); got != "sudo opencloud agent -server https://oc.example.fr -token oc_x" {
		t.Errorf("fr: %q", got)
	}
	if got := installCommand("https://oc.example.fr", "oc_x", lang.English); !strings.HasSuffix(got, " -lang en") {
		t.Errorf("en: %q", got)
	}
}
