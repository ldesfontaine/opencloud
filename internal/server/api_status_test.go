package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ldesfontaine/opencloud/internal/live"
	"github.com/ldesfontaine/opencloud/internal/machine"
	"github.com/ldesfontaine/opencloud/internal/status"
)

// Un composant « Nuage » fait de tout ce que les autres seeds ont créé :
// la machine distante, sa base arrêtée, la tâche en échec, la sonde
// dégradée. Un incident ouvert dessus, un autre résolu hier. Le composant
// « Silence » n'a qu'une sonde neuve : rien à en dire, caché du public.
func (ts *testServer) seedStatus(t *testing.T, serviceID, jobID, probeID string) (string, string) {
	t.Helper()
	ctx := context.Background()
	cloud, err := ts.status.CreateComponent(ctx, status.ComponentDefinition{Name: "Nuage", Members: []status.MemberRef{
		{Kind: status.KindMachine, ID: remoteID},
		{Kind: status.KindService, ID: serviceID},
		{Kind: status.KindHeartbeat, ID: jobID},
		{Kind: status.KindProbe, ID: probeID},
	}})
	if err != nil {
		t.Fatal(err)
	}
	probes, err := ts.probes.List(ctx, machine.LocalID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ts.status.CreateComponent(ctx, status.ComponentDefinition{Name: "Silence", Position: 1, Members: []status.MemberRef{{Kind: status.KindProbe, ID: probes[0].ID}}}); err != nil {
		t.Fatal(err)
	}
	opened, err := ts.status.OpenIncident(ctx, status.IncidentDefinition{
		Title: "Le nuage ne répond plus", Impact: status.ImpactDown, Status: status.StatusInvestigating,
		Message: "Nous regardons.", ComponentIDs: []string{cloud.ID},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ts.status.AddUpdate(ctx, opened.ID, status.StatusIdentified, "Le disque est plein."); err != nil {
		t.Fatal(err)
	}
	yesterday, err := ts.status.OpenIncident(ctx, status.IncidentDefinition{
		Title: "Certificat renouvelé en retard", Impact: status.ImpactDegraded, Status: status.StatusMonitoring,
		ComponentIDs: []string{cloud.ID},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ts.status.AddUpdate(ctx, yesterday.ID, status.StatusResolved, "Renouvelé."); err != nil {
		t.Fatal(err)
	}
	if _, err := ts.status.SetPage(ctx, status.Page{Title: "Statut de la maison", Announcement: "Coupure électrique samedi, de 8 h à 10 h."}); err != nil {
		t.Fatal(err)
	}
	return cloud.ID, opened.ID
}

func (ts *testServer) seedAll(t *testing.T) (componentID, incidentID string) {
	t.Helper()
	ts.enroll(t, "vps-paris-1", remoteID)
	jobID := ts.seedJobs(t)
	serviceID := ts.seedServices(t)
	probeID := ts.seedProbes(t, serviceID)
	return ts.seedStatus(t, serviceID, jobID, probeID)
}

func TestStatusAPI_ReadsMatchGoldenFiles(t *testing.T) {
	server := newTestServer(t)
	componentID, incidentID := server.seedAll(t)
	cases := map[string]string{
		"status_public":     "/statut/api/status",
		"status_components": "/api/status/components",
		"status_component":  "/api/status/components/" + componentID,
		"status_incidents":  "/api/status/incidents",
		"status_incident":   "/api/status/incidents/" + incidentID,
		"status_page":       "/api/status/page",
	}
	for name, path := range cases {
		t.Run(name, func(t *testing.T) {
			recorder := callAPI(server.Server, http.MethodGet, path, nil)
			if recorder.Code != http.StatusOK {
				t.Fatalf("status %d: %s", recorder.Code, recorder.Body.String())
			}
			compareGolden(t, filepath.Join("testdata", name+".golden.json"), normalize(recorder.Body.String()))
		})
	}
}

// Le public ne voit que le nom des composants et ce que l'opérateur a
// écrit : ni un nom de machine, de conteneur, d'image ou de sonde, ni une
// cible, une adresse, un identifiant d'objet, un jeton.
func TestStatusPublic_LeaksNothingOfTheObjects(t *testing.T) {
	server := newTestServer(t)
	server.seedAll(t)
	secrets := []string{
		"vps-paris-1", "opencloud-host", "51.15.20.114", "10.8.0.1", remoteID, `"local"`,
		"nextcloud", "postgres", "cloud.exemple.fr", "5432", "sauvegarde", "certbot", "hb_",
		strings.Repeat("a", 12), strings.Repeat("b", 12), "openCloud dev", "Silence",
	}
	for _, path := range []string{"/statut/api/status", "/statut/api/i18n", "/statut"} {
		recorder := get(server.Server, path)
		if recorder.Code != http.StatusOK {
			t.Fatalf("%s: status %d", path, recorder.Code)
		}
		body := recorder.Body.String()
		for _, secret := range secrets {
			if strings.Contains(body, secret) {
				t.Errorf("%s leaks %q", path, secret)
			}
		}
	}
	body := get(server.Server, "/statut/api/status").Body.String()
	for _, expected := range []string{`"Nuage"`, `"Le nuage ne répond plus"`, `"Statut de la maison"`, `"global":"down"`} {
		if !strings.Contains(body, expected) {
			t.Errorf("public status lacks %s", expected)
		}
	}
}

func TestStatusPublic_CatalogServesOnlyThePageKeysInItsLanguage(t *testing.T) {
	server := newTestServer(t)
	if _, err := server.status.SetPage(context.Background(), status.Page{Language: "en"}); err != nil {
		t.Fatal(err)
	}
	recorder := get(server.Server, "/statut/api/i18n")
	var strings map[string]string
	decodeAPI(t, recorder, &strings)
	if len(strings) == 0 || strings["status.state_down"] != "Outage" {
		t.Fatalf("catalog %v", strings)
	}
	for key := range strings {
		if !hasStatusPrefix(key) {
			t.Fatalf("key %q is not a public one", key)
		}
	}
}

// Les routes publiques comptent par adresse résolue ; un en-tête forgé
// n'ouvre pas un seau neuf tant qu'aucun mandataire n'est déclaré.
func TestStatusPublic_IsRateLimitedBySource(t *testing.T) {
	server := newTestServer(t)
	for i := range statusPerSourceBurst {
		request := httptest.NewRequest(http.MethodGet, "/statut/api/status", nil)
		request.Header.Set("X-Forwarded-For", "203.0.113."+string(rune('0'+i%10)))
		recorder := httptest.NewRecorder()
		server.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusOK {
			t.Fatalf("request %d: %d", i+1, recorder.Code)
		}
	}
	recorder := get(server.Server, "/statut")
	if recorder.Code != http.StatusTooManyRequests || recorder.Header().Get("Retry-After") == "" {
		t.Fatalf("beyond the burst: %d, Retry-After %q", recorder.Code, recorder.Header().Get("Retry-After"))
	}
	if recorder := get(server.Server, "/"); recorder.Code != http.StatusOK {
		t.Fatalf("the administration shares no bucket with the public: %d", recorder.Code)
	}
}

// Seule la page HTML publique se laisse mettre dans un cadre : son API et
// tout le reste gardent la politique stricte.
func TestStatusPublic_OnlyThePageIsEmbeddable(t *testing.T) {
	server := newTestServer(t)
	for _, path := range []string{"/statut", "/statut/"} {
		recorder := get(server.Server, path)
		if got := recorder.Header().Get("Content-Security-Policy"); got != embeddablePolicy || !strings.Contains(got, "frame-ancestors *") {
			t.Errorf("%s: CSP %q", path, got)
		}
		if got := recorder.Header().Get("X-Frame-Options"); got != "" {
			t.Errorf("%s: X-Frame-Options %q", path, got)
		}
	}
	for _, path := range []string{"/statut/api/status", "/statut/api/i18n", "/statut/rien", "/", "/api/status/components"} {
		recorder := get(server.Server, path)
		if got := recorder.Header().Get("Content-Security-Policy"); got != contentSecurityPolicy {
			t.Errorf("%s: CSP %q", path, got)
		}
		if got := recorder.Header().Get("X-Frame-Options"); got != "DENY" {
			t.Errorf("%s: X-Frame-Options %q", path, got)
		}
	}
	if recorder := get(server.Server, "/statut/rien"); recorder.Code != http.StatusNotFound {
		t.Errorf("unknown public path: %d", recorder.Code)
	}
}

func TestStatusPublic_PageIsServedWithoutTheAdministration(t *testing.T) {
	server := newTestServer(t)
	recorder := get(server.Server, "/statut")
	body := recorder.Body.String()
	if recorder.Code != http.StatusOK || !strings.Contains(body, `<div id="root">`) {
		t.Fatalf("status %d: %s", recorder.Code, body)
	}
	if got := recorder.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control %q", got)
	}
	for _, forbidden := range []string{"<script>", "<style", "onload=", "javascript:"} {
		if strings.Contains(body, forbidden) {
			t.Errorf("statut.html contains %s", forbidden)
		}
	}
	index := get(server.Server, "/").Body.String()
	if body == index {
		t.Error("the public page must be its own entry, not the administration's index")
	}
}

// Le direct public ne porte que « status », sur son propre bus ; le même
// sujet part sur le bus interne pour l'administration.
func TestStatusPublic_StreamsStatusOnBothBuses(t *testing.T) {
	server := newTestServer(t)
	public, cancelPublic := openLive(t, server, "/statut/api/events", "")
	defer cancelPublic()
	admin, cancelAdmin := openLive(t, server, "/api/events", "")
	defer cancelAdmin()
	if got := public(); got != "connected" {
		t.Fatalf("first public event %q", got)
	}
	if got := admin(); got != "connected" {
		t.Fatalf("first admin event %q", got)
	}
	if _, err := server.status.CreateComponent(context.Background(), status.ComponentDefinition{Name: "Site web"}); err != nil {
		t.Fatal(err)
	}
	if got := public(); got != "status" {
		t.Fatalf("public event %q, want status", got)
	}
	if got := admin(); got != "status" {
		t.Fatalf("admin event %q, want status", got)
	}
	server.bus.Publish(live.TopicJobs)
	if got := admin(); got != "jobs" {
		t.Fatalf("admin event %q, want jobs", got)
	}
	if server.publicBus.Count() != 1 || server.bus.Count() != 1 {
		t.Fatalf("subscribers public %d admin %d", server.publicBus.Count(), server.bus.Count())
	}
}

func TestStatusAPI_WritesGoThroughTheComponent(t *testing.T) {
	server := newTestServer(t)
	componentID, incidentID := server.seedAll(t)
	// Un composant se renomme et perd ses objets ; caché du public dès lors.
	recorder := callAPI(server.Server, http.MethodPut, "/api/status/components/"+componentID, map[string]any{"name": "Nuage bleu", "members": []any{}})
	if recorder.Code != http.StatusOK {
		t.Fatalf("update: %d %s", recorder.Code, recorder.Body.String())
	}
	var component componentJSON
	decodeAPI(t, recorder, &component)
	// L'incident ouvert impose encore son état.
	if component.Name != "Nuage bleu" || component.Derived != "" || component.Effective != "down" {
		t.Fatalf("component %+v", component)
	}
	recorder = callAPI(server.Server, http.MethodPost, "/api/status/components", map[string]any{"name": "", "members": []any{}})
	if recorder.Code != http.StatusUnprocessableEntity || errorCode(t, recorder) != "component.name_invalid" {
		t.Fatalf("empty name: %d %s", recorder.Code, recorder.Body.String())
	}
	recorder = callAPI(server.Server, http.MethodPost, "/api/status/incidents/"+incidentID+"/updates", map[string]any{"status": "resolved", "message": "Réparé."})
	if recorder.Code != http.StatusOK {
		t.Fatalf("resolve: %d %s", recorder.Code, recorder.Body.String())
	}
	recorder = callAPI(server.Server, http.MethodPost, "/api/status/incidents/"+incidentID+"/updates", map[string]any{"status": "monitoring", "message": ""})
	if recorder.Code != http.StatusConflict || errorCode(t, recorder) != "incident.already_resolved" {
		t.Fatalf("update after resolution: %d %s", recorder.Code, recorder.Body.String())
	}
	starts := testNow.Add(time.Hour)
	recorder = callAPI(server.Server, http.MethodPost, "/api/status/incidents", map[string]any{
		"title": "Migration", "impact": "maintenance", "status": "scheduled", "component_ids": []string{componentID},
		"starts_at": starts, "ends_at": starts.Add(30 * time.Minute),
	})
	if recorder.Code != http.StatusCreated {
		t.Fatalf("schedule: %d %s", recorder.Code, recorder.Body.String())
	}
	recorder = callAPI(server.Server, http.MethodPut, "/api/status/page", map[string]any{"title": "", "announcement": "", "language": "en"})
	if recorder.Code != http.StatusOK {
		t.Fatalf("page: %d %s", recorder.Code, recorder.Body.String())
	}
	var counts countsResponse
	decodeAPI(t, callAPI(server.Server, http.MethodGet, "/api/counts", nil), &counts)
	if counts.Status.OpenIncidents != 1 {
		t.Fatalf("open incidents %d, want the scheduled maintenance only", counts.Status.OpenIncidents)
	}
	recorder = callAPI(server.Server, http.MethodDelete, "/api/status/components/"+componentID, nil)
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("delete: %d", recorder.Code)
	}
	if recorder := callAPI(server.Server, http.MethodGet, "/api/status/components/"+componentID, nil); recorder.Code != http.StatusNotFound {
		t.Fatalf("deleted component still answers: %d", recorder.Code)
	}
}
