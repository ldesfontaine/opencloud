package web

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// Rejoue le premier passage : le cookie CSRF posé par le serveur.
func csrfCookieFrom(t *testing.T, server *Server) *http.Cookie {
	t.Helper()
	recorder := httptest.NewRecorder()
	server.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/", nil))
	for _, cookie := range recorder.Result().Cookies() {
		if cookie.Name == csrfCookieName {
			return cookie
		}
	}
	t.Fatal("no csrf cookie set")
	return nil
}

func postLanguage(server *Server, cookie *http.Cookie, form url.Values) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodPost, "/langue", strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if cookie != nil {
		request.AddCookie(cookie)
	}
	recorder := httptest.NewRecorder()
	server.ServeHTTP(recorder, request)
	return recorder
}

func TestSetLanguage_SavesAndRedirectsBack(t *testing.T) {
	server := newTestServer(t)
	cookie := csrfCookieFrom(t, server)
	recorder := postLanguage(server, cookie, url.Values{
		"csrf": {cookie.Value}, "language": {"en"}, "return": {"/machines"},
	})
	if recorder.Code != http.StatusSeeOther || recorder.Header().Get("Location") != "/machines" {
		t.Fatalf("status %d, location %q", recorder.Code, recorder.Header().Get("Location"))
	}
	saved, err := server.settings.Load()
	if err != nil {
		t.Fatal(err)
	}
	if saved.Language != "en" {
		t.Fatalf("saved %+v", saved)
	}
	page := httptest.NewRecorder()
	server.ServeHTTP(page, httptest.NewRequest(http.MethodGet, "/", nil))
	if !strings.Contains(page.Body.String(), `<html lang="en">`) {
		t.Fatal("page still rendered in the previous language")
	}
}

func TestSetLanguage_WithoutValidCSRF_IsRefused(t *testing.T) {
	server := newTestServer(t)
	cookie := csrfCookieFrom(t, server)
	if got := postLanguage(server, cookie, url.Values{"csrf": {"wrong"}, "language": {"en"}}); got.Code != http.StatusForbidden {
		t.Errorf("bad token: status %d", got.Code)
	}
	if got := postLanguage(server, nil, url.Values{"csrf": {cookie.Value}, "language": {"en"}}); got.Code != http.StatusForbidden {
		t.Errorf("no cookie: status %d", got.Code)
	}
}

func TestSetLanguage_UnknownLanguage_IsRefused(t *testing.T) {
	server := newTestServer(t)
	cookie := csrfCookieFrom(t, server)
	got := postLanguage(server, cookie, url.Values{"csrf": {cookie.Value}, "language": {"klingon"}})
	if got.Code != http.StatusBadRequest {
		t.Fatalf("status %d", got.Code)
	}
}

func TestReturnPath_OnlyFollowsLocalPaths(t *testing.T) {
	cases := map[string]string{
		"/machines":           "/machines",
		"/systeme-visuel?x=1": "/systeme-visuel?x=1",
		"//evil.example":      "/",
		"https://evil":        "/",
		"":                    "/",
	}
	for candidate, want := range cases {
		if got := returnPath(candidate); got != want {
			t.Errorf("%q: got %q, want %q", candidate, got, want)
		}
	}
}

func TestCSRFCookie_IsSecureHttpOnlyAndStrict(t *testing.T) {
	cookie := csrfCookieFrom(t, newTestServer(t))
	if !cookie.HttpOnly || !cookie.Secure || cookie.SameSite != http.SameSiteStrictMode || cookie.Path != "/" {
		t.Fatalf("cookie %+v", cookie)
	}
}
