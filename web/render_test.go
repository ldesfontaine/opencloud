package web

import (
	"flag"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

var updateGolden = flag.Bool("update", false, "réécrit les rendus de référence dans testdata/")

// Le rendu de chaque page est figé dans testdata/*.golden.html ;
// `go test ./web -update` les réécrit après un changement voulu.
func TestRender_MatchesGoldenFiles(t *testing.T) {
	server := newTestServer(t)
	cases := []struct {
		name   string
		path   string
		status int
	}{
		{"overview", "/", http.StatusOK},
		{"soon", "/machines", http.StatusOK},
		{"visual-system", "/systeme-visuel", http.StatusOK},
		{"not-found", "/rien-ici", http.StatusNotFound},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			server.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, tc.path, nil))
			if recorder.Code != tc.status {
				t.Fatalf("status %d, want %d", recorder.Code, tc.status)
			}
			got := normalizeStaticBase(server, recorder.Body.String())
			golden := filepath.Join("testdata", tc.name+".golden.html")
			if *updateGolden {
				if err := os.WriteFile(golden, []byte(got), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			want, err := os.ReadFile(golden)
			if err != nil {
				t.Fatalf("%v (lancer go test ./web -update)", err)
			}
			if got != string(want) {
				t.Fatalf("rendu différent de %s (lancer go test ./web -update après vérification)", golden)
			}
		})
	}
}

func normalizeStaticBase(server *Server, body string) string {
	return strings.ReplaceAll(body, server.staticBase, "/static/BUILD/")
}

func TestRender_EveryPageTemplateHasARoute(t *testing.T) {
	server := newTestServer(t)
	rendered := map[string]bool{}
	for _, page := range []string{"overview", "soon", "visual-system", "not-found"} {
		rendered[page] = true
	}
	for page := range server.pages {
		if !rendered[page] {
			t.Errorf("page %q has no route under test", page)
		}
	}
}

var (
	inlineStyle  = regexp.MustCompile(`\sstyle=`)
	inlineScript = regexp.MustCompile(`<script(?:\s[^>]*)?>\s*[^<\s]`)
	inlineEvent  = regexp.MustCompile(`\son[a-z]+=`)
)

// La CSP interdit tout style et script en ligne : un gabarit qui en
// ajoute un rendrait une page cassée en silence.
func TestTemplates_ContainNoInlineStyleOrScript(t *testing.T) {
	server := newTestServer(t)
	for _, path := range []string{"/", "/machines", "/systeme-visuel", "/rien-ici"} {
		recorder := httptest.NewRecorder()
		server.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
		body := recorder.Body.String()
		if inlineStyle.MatchString(body) {
			t.Errorf("%s: attribut style= en ligne", path)
		}
		if inlineScript.MatchString(body) {
			t.Errorf("%s: script en ligne", path)
		}
		if inlineEvent.MatchString(body) {
			t.Errorf("%s: gestionnaire on*= en ligne", path)
		}
	}
}

func TestHTMX_IsPinnedByVersionInTheFileName(t *testing.T) {
	content, err := staticFiles.ReadFile("static/vendor/htmx-2.0.10.min.js")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(content), `version:"2.0.10"`) {
		t.Fatal("le fichier htmx ne porte pas la version de son nom")
	}
}
