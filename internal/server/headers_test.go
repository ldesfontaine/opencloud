package server

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
	for _, path := range []string{"/", "/machines", "/api/session", "/api/nope", "/favicon.svg"} {
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

// Vite doit sortir une page sans script ni style en ligne, sinon la CSP
// la casse en silence.
func TestCSP_IndexHasNoInlineScriptOrStyle(t *testing.T) {
	server := newTestServer(t)
	recorder := httptest.NewRecorder()
	server.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/", nil))
	body := recorder.Body.String()
	for _, forbidden := range []string{"<script>", "<style", "onload=", "javascript:"} {
		if strings.Contains(body, forbidden) {
			t.Errorf("index.html contains %s", forbidden)
		}
	}
}
