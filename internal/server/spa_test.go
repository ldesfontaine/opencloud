package server

import (
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func get(server *Server, path string) *httptest.ResponseRecorder {
	recorder := httptest.NewRecorder()
	server.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
	return recorder
}

// Le front est une seule page : toute adresse sans extension la reçoit,
// jamais en cache, et son routeur décide. Un fichier absent reste un 404.
func TestSPA_ServesIndexForEveryClientRoute(t *testing.T) {
	server := newTestServer(t)
	for _, path := range []string{"/", "/machines", "/taches/abc", "/rien-ici"} {
		recorder := get(server.Server, path)
		if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `<div id="root">`) {
			t.Errorf("%s: %d", path, recorder.Code)
		}
		if got := recorder.Header().Get("Cache-Control"); got != "no-store" {
			t.Errorf("%s: Cache-Control %q", path, got)
		}
	}
	if got := get(server.Server, "/rien.png"); got.Code != http.StatusNotFound {
		t.Errorf("missing file: %d", got.Code)
	}
	if got := get(server.Server, "/index.html"); got.Code != http.StatusOK || got.Header().Get("Cache-Control") != "no-store" {
		t.Errorf("index.html by name: %d %q", got.Code, got.Header().Get("Cache-Control"))
	}
	recorder := httptest.NewRecorder()
	server.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/machines", nil))
	if recorder.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST on a page: %d", recorder.Code)
	}
}

// Les fichiers de dist portent leur empreinte dans leur nom : cache
// immuable ; ce qui vient de public/ (favicon, theme.js) se revalide.
func TestSPA_AssetsAreImmutableAndPublicFilesRevalidated(t *testing.T) {
	server := newTestServer(t)
	assets := listAssets(t, server.Server)
	if len(assets) == 0 {
		t.Fatal("no asset in dist: run make front")
	}
	for _, name := range assets {
		recorder := get(server.Server, "/"+name)
		if recorder.Code != http.StatusOK || recorder.Header().Get("Cache-Control") != immutableYear {
			t.Errorf("%s: %d %q", name, recorder.Code, recorder.Header().Get("Cache-Control"))
		}
	}
	for _, name := range []string{"/favicon.svg", "/theme.js"} {
		recorder := get(server.Server, name)
		if recorder.Code != http.StatusOK || recorder.Header().Get("Cache-Control") != "no-cache" {
			t.Errorf("%s: %d %q", name, recorder.Code, recorder.Header().Get("Cache-Control"))
		}
	}
}

// Les deux polices de la direction artistique sont embarquées et servies
// avec leur type MIME, sans quoi certains navigateurs refusent le fichier.
func TestSPA_FontsAreEmbeddedAndServedAsWoff2(t *testing.T) {
	server := newTestServer(t)
	fonts := 0
	for _, name := range listAssets(t, server.Server) {
		if !strings.HasSuffix(name, ".woff2") {
			continue
		}
		fonts++
		recorder := get(server.Server, "/"+name)
		if got := recorder.Header().Get("Content-Type"); got != "font/woff2" {
			t.Errorf("%s: Content-Type %q", name, got)
		}
		if !strings.HasPrefix(recorder.Body.String(), "wOF2") {
			t.Errorf("%s: not a woff2 file", name)
		}
	}
	if fonts != 2 {
		t.Fatalf("%d fonts embedded, want Geist and Geist Mono", fonts)
	}
}

func listAssets(t *testing.T, server *Server) []string {
	t.Helper()
	var assets []string
	err := fs.WalkDir(server.app, "assets", func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() {
			assets = append(assets, path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return assets
}
