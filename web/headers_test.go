package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSecurityHeaders_AreSetOnEveryResponse(t *testing.T) {
	server := newTestServer(t)
	want := map[string]string{
		"Content-Security-Policy": contentSecurityPolicy,
		"X-Content-Type-Options":  "nosniff",
		"X-Frame-Options":         "DENY",
		"Referrer-Policy":         "same-origin",
	}
	for _, path := range []string{"/", "/systeme-visuel", "/inconnue", server.staticBase + "app.css"} {
		recorder := httptest.NewRecorder()
		server.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
		for header, value := range want {
			if got := recorder.Header().Get(header); got != value {
				t.Errorf("%s: %s = %q, want %q", path, header, got, value)
			}
		}
	}
}

func TestCSP_ForbidsInlineStylesAndScripts(t *testing.T) {
	for _, forbidden := range []string{"'unsafe-inline'", "'unsafe-eval'", "http:", "https:"} {
		if strings.Contains(contentSecurityPolicy, forbidden) {
			t.Errorf("CSP contains %s", forbidden)
		}
	}
}

func TestStatic_IsImmutableUnderItsBuildAndGoneElsewhere(t *testing.T) {
	server := newTestServer(t)

	recorder := httptest.NewRecorder()
	server.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, server.staticBase+"app.css", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("app.css: status %d", recorder.Code)
	}
	if got := recorder.Header().Get("Cache-Control"); got != immutableYear {
		t.Fatalf("Cache-Control = %q", got)
	}
	if got := recorder.Header().Get("Content-Type"); !strings.HasPrefix(got, "text/css") {
		t.Fatalf("Content-Type = %q", got)
	}

	recorder = httptest.NewRecorder()
	server.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/static/000000000000/app.css", nil))
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("stale build: status %d, want 404", recorder.Code)
	}
}
