package public

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestStatusPublic_PageIsServedAsGivenByTheRoot(t *testing.T) {
	server := newBench(t)
	for _, path := range []string{"/statut", "/statut/"} {
		recorder := server.get(path)
		if recorder.Code != http.StatusOK || recorder.Body.String() != testPage {
			t.Fatalf("%s: %d %q", path, recorder.Code, recorder.Body.String())
		}
		if got := recorder.Header().Get("Content-Type"); got != "text/html; charset=utf-8" {
			t.Errorf("%s: Content-Type %q", path, got)
		}
		if got := recorder.Header().Get("Cache-Control"); got != "no-store" {
			t.Errorf("%s: Cache-Control %q", path, got)
		}
	}
}

func TestStatusPublic_PageNotBuilt_Is500WithAHint(t *testing.T) {
	server := newBench(t)
	server.handler.page = func() ([]byte, error) { return nil, errNotBuilt }
	recorder := server.get("/statut")
	if recorder.Code != http.StatusInternalServerError || !strings.Contains(recorder.Body.String(), "make front") {
		t.Fatalf("%d %q", recorder.Code, recorder.Body.String())
	}
}

func TestStatusPublic_UnknownPathUnderItIsAShort404(t *testing.T) {
	server := newBench(t)
	recorder := server.get("/statut/rien")
	if recorder.Code != http.StatusNotFound || strings.Contains(recorder.Body.String(), "root") {
		t.Fatalf("%d %q", recorder.Code, recorder.Body.String())
	}
	post := httptest.NewRecorder()
	server.ServeHTTP(post, httptest.NewRequest(http.MethodPost, "/statut/api/status", nil))
	if post.Code != http.StatusNotFound {
		t.Errorf("POST on an API route: %d", post.Code)
	}
}
