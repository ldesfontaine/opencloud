package web

import (
	"context"
	"flag"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/ldesfontaine/opencloud/internal/heartbeat"
	"github.com/ldesfontaine/opencloud/internal/lang"
)

var updateGolden = flag.Bool("update", false, "réécrit les rendus de référence dans testdata/")

// Le rendu de chaque page est figé dans testdata/*.golden.html, dans chaque
// langue ; `go test ./web -update` les réécrit après un changement voulu.
type renderCase struct {
	name   string
	path   string
	status int
}

func renderCases(jobID string) []renderCase {
	return []renderCase{
		{"overview", "/", http.StatusOK},
		{"machines", "/machines", http.StatusOK},
		{"machine-new", "/machines/nouvelle", http.StatusOK},
		{"machine", "/machines/" + remoteID, http.StatusOK},
		{"machine-tab", "/machines/" + remoteID + "/services", http.StatusOK},
		{"machine-local", "/machines/local", http.StatusOK},
		{"jobs", "/taches", http.StatusOK},
		{"job-new", "/taches/nouvelle", http.StatusOK},
		{"job", "/taches/" + jobID, http.StatusOK},
		{"soon", "/services", http.StatusOK},
		{"visual-system", "/systeme-visuel", http.StatusOK},
		{"not-found", "/rien-ici", http.StatusNotFound},
	}
}

func TestRender_MatchesGoldenFiles(t *testing.T) {
	for _, code := range lang.Codes() {
		server := newTestServer(t)
		server.enroll(t, "vps-paris-1", remoteID)
		if _, _, err := server.machines.CreateToken(context.Background(), "vps-lyon-2"); err != nil {
			t.Fatal(err)
		}
		jobID := server.seedJobs(t)
		if err := server.saveLanguage(code); err != nil {
			t.Fatal(err)
		}
		for _, tc := range renderCases(jobID) {
			t.Run(tc.name+"/"+string(code), func(t *testing.T) {
				recorder := httptest.NewRecorder()
				server.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, tc.path, nil))
				if recorder.Code != tc.status {
					t.Fatalf("status %d, want %d", recorder.Code, tc.status)
				}
				got := normalize(server.Server, recorder.Body.String())
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

const remoteID = "11111111-2222-4333-8444-555555555555"

var (
	csrfValue  = regexp.MustCompile(`name="csrf" value="[0-9a-f]+"`)
	tokenValue = regexp.MustCompile(`oc_[a-z2-7]{20,}`)
	tokenMask  = regexp.MustCompile(`oc_[a-z2-7]{6}…`)
	tokenID    = regexp.MustCompile(`jetons/[0-9a-f]{16}/`)
	pingToken  = regexp.MustCompile(`hb_[a-z2-7]{26}`)
	jobID      = regexp.MustCompile(`taches/[0-9a-f]{16}`)
	clockValue = regexp.MustCompile(`\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2}`)
)

// L'empreinte des statiques, le jeton CSRF, les jetons d'enrôlement et de
// ping, les identifiants de tâche changent à chaque build ou requête, et
// les horodatages suivent le fuseau de la machine ; le golden les remplace
// par une valeur fixe.
func normalize(server *Server, body string) string {
	body = strings.ReplaceAll(body, server.staticBase, "/static/BUILD/")
	body = csrfValue.ReplaceAllString(body, `name="csrf" value="CSRF"`)
	body = tokenValue.ReplaceAllString(body, "oc_TOKEN")
	body = tokenMask.ReplaceAllString(body, "oc_MASKED…")
	body = tokenID.ReplaceAllString(body, "jetons/TOKENID/")
	body = pingToken.ReplaceAllString(body, "hb_TOKEN")
	body = jobID.ReplaceAllString(body, "taches/JOBID")
	return clockValue.ReplaceAllString(body, "DATE TIME")
}

// Deux tâches pour des rendus parlants : l'une rattachée à la machine
// distante, démarrée puis finie ; l'autre sans machine, en échec.
func (ts *testServer) seedJobs(t *testing.T) string {
	t.Helper()
	ctx := context.Background()
	backup, err := ts.heartbeats.Create(ctx, heartbeat.Definition{Name: "sauvegarde nextcloud", MachineID: remoteID, Interval: 24 * time.Hour, Grace: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	zero := 0
	for _, ping := range []heartbeat.Ping{{Kind: heartbeat.KindStart}, {Kind: heartbeat.KindExitCode, ExitCode: &zero}} {
		ping.Source = "51.15.20.114"
		ping.Method = "GET"
		if _, err := ts.heartbeats.Receive(ctx, backup.Token, ping); err != nil {
			t.Fatal(err)
		}
	}
	certbot, err := ts.heartbeats.Create(ctx, heartbeat.Definition{Name: "certbot renew", Interval: 12 * time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	one := 1
	if _, err := ts.heartbeats.Receive(ctx, certbot.Token, heartbeat.Ping{Kind: heartbeat.KindExitCode, ExitCode: &one, Source: "10.8.0.1", Method: "POST"}); err != nil {
		t.Fatal(err)
	}
	return backup.ID
}

func TestRender_EveryPageTemplateHasARoute(t *testing.T) {
	server := newTestServer(t)
	rendered := map[string]bool{}
	for _, page := range []string{"overview", "machines", "machine-new", "machine-token", "machine", "jobs", "job-new", "job", "soon", "visual-system", "not-found"} {
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
		server.enroll(t, "vps-paris-1", remoteID)
		jobID := server.seedJobs(t)
		for _, path := range []string{"/", "/machines", "/machines/nouvelle", "/machines/" + remoteID, "/machines/" + remoteID + "/reseau", "/taches", "/taches/nouvelle", "/taches/" + jobID, "/systeme-visuel", "/rien-ici"} {
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
