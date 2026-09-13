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

	"github.com/ldesfontaine/opencloud/internal/lang"
)

var updateGolden = flag.Bool("update", false, "réécrit les rendus de référence dans testdata/")

// Le rendu de chaque page est figé dans testdata/*.golden.html, dans chaque
// langue ; `go test ./web -update` les réécrit après un changement voulu.
func TestRender_MatchesGoldenFiles(t *testing.T) {
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
	for _, code := range lang.Codes() {
		server := newTestServer(t)
		if err := server.saveLanguage(code); err != nil {
			t.Fatal(err)
		}
		for _, tc := range cases {
			t.Run(tc.name+"/"+string(code), func(t *testing.T) {
				recorder := httptest.NewRecorder()
				server.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, tc.path, nil))
				if recorder.Code != tc.status {
					t.Fatalf("status %d, want %d", recorder.Code, tc.status)
				}
				got := normalize(server, recorder.Body.String())
				compareGolden(t, filepath.Join("testdata", tc.name+"."+string(code)+".golden.html"), got)
			})
		}
	}
}

func compareGolden(t *testing.T, golden, got string) {
	t.Helper()
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
}

var csrfValue = regexp.MustCompile(`name="csrf" value="[0-9a-f]+"`)

// L'empreinte des statiques et le jeton CSRF changent à chaque build ou
// requête ; le golden les remplace par une valeur fixe.
func normalize(server *Server, body string) string {
	body = strings.ReplaceAll(body, server.staticBase, "/static/BUILD/")
	return csrfValue.ReplaceAllString(body, `name="csrf" value="CSRF"`)
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
	missingKey   = regexp.MustCompile(`\[[a-z0-9_]+(?:\.[a-z0-9_]+)+\]`)
)

// La CSP interdit tout style et script en ligne : un gabarit qui en
// ajoute un rendrait une page cassée en silence. Une clé de traduction
// manquante s'afficherait entre crochets : même test.
func TestTemplates_ContainNoInlineStyleScriptOrMissingKey(t *testing.T) {
	for _, code := range lang.Codes() {
		server := newTestServer(t)
		if err := server.saveLanguage(code); err != nil {
			t.Fatal(err)
		}
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
			if match := missingKey.FindString(body); match != "" {
				t.Errorf("%s (%s): clé de traduction manquante %s", path, code, match)
			}
		}
	}
}

var templateKey = regexp.MustCompile(`\.T\.(?:Get|Format) "([^"]+)"`)

// Chaque clé nommée dans un gabarit existe dans chaque langue, même sur une
// branche de gabarit que les pages de test n'atteignent pas.
func TestTemplates_EveryKeyExistsInEveryLanguage(t *testing.T) {
	catalogs, err := lang.Load()
	if err != nil {
		t.Fatal(err)
	}
	err = filepath.WalkDir("templates", func(path string, entry os.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, match := range templateKey.FindAllStringSubmatch(string(content), -1) {
			for _, code := range lang.Codes() {
				if got := catalogs[code].Get(match[1]); strings.HasPrefix(got, "[") {
					t.Errorf("%s: clé %s absente de %s", path, match[1], code)
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
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
