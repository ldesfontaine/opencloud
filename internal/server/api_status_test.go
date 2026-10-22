package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ldesfontaine/opencloud/internal/live"
	"github.com/ldesfontaine/opencloud/internal/machine"
	"github.com/ldesfontaine/opencloud/internal/server/servertest"
	"github.com/ldesfontaine/opencloud/internal/status"
	"github.com/ldesfontaine/opencloud/internal/store/storetest"
)

// Un composant « Nuage » fait de tout ce que les autres seeds ont créé :
// la machine distante, le service déclaré nextcloud (dont une unité n'a pas
// de conteneur), la tâche en échec, la sonde dégradée. Un incident ouvert dessus, un autre résolu hier. Le composant
// « Silence » n'a qu'une sonde neuve : rien à en dire, caché du public.
func (ts *testServer) seedStatus(t *testing.T, declaredServiceID, jobID, probeID string) (string, string) {
	t.Helper()
	ctx := context.Background()
	page, err := ts.status.CreatePage(ctx, status.PageDefinition{
		Slug: seededSlug, Title: "Statut de la maison", Announcement: "Coupure électrique samedi, de 8 h à 10 h.",
	})
	if err != nil {
		t.Fatal(err)
	}
	cloud, err := ts.status.CreateComponent(ctx, status.ComponentDefinition{PageID: page.ID, Name: "Nuage", Members: []status.MemberRef{
		{Kind: status.KindMachine, ID: remoteID},
		{Kind: status.KindService, ID: declaredServiceID},
		{Kind: status.KindHeartbeat, ID: jobID},
		{Kind: status.KindProbe, ID: probeID},
	}})
	if err != nil {
		t.Fatal(err)
	}
	probes, err := ts.db.ListProbes(ctx, machine.LocalID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ts.status.CreateComponent(ctx, status.ComponentDefinition{PageID: page.ID, Name: "Silence", Position: 1, Members: []status.MemberRef{{Kind: status.KindProbe, ID: probes[0].ID}}}); err != nil {
		t.Fatal(err)
	}
	opened, err := ts.status.OpenIncident(ctx, status.IncidentDefinition{
		Title: "Le nuage ne répond plus", Impact: status.ImpactDown, Status: status.StatusInvestigating,
		Message: "Nous regardons.", Targets: []status.MemberRef{{Kind: status.KindService, ID: declaredServiceID}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ts.status.AddUpdate(ctx, opened.ID, status.StatusIdentified, "Le disque est plein."); err != nil {
		t.Fatal(err)
	}
	yesterday, err := ts.status.OpenIncident(ctx, status.IncidentDefinition{
		Title: "Certificat renouvelé en retard", Impact: status.ImpactDegraded, Status: status.StatusMonitoring,
		Targets: []status.MemberRef{{Kind: status.KindService, ID: declaredServiceID}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ts.status.AddUpdate(ctx, yesterday.ID, status.StatusResolved, "Renouvelé."); err != nil {
		t.Fatal(err)
	}
	return cloud.ID, opened.ID
}

// seededSlug est l'adresse de la page que seedStatus crée : /statut/maison.
const seededSlug = "maison"

func (ts *testServer) seededPageID(t *testing.T) string {
	t.Helper()
	page, err := ts.status.PageBySlug(context.Background(), seededSlug)
	if err != nil {
		t.Fatal(err)
	}
	return page.ID
}

func (ts *testServer) seedAll(t *testing.T) (componentID, incidentID string) {
	t.Helper()
	ts.enroll(t, "vps-paris-1", remoteID)
	jobID := ts.seedJobs(t)
	ts.seedContainers(t)
	declaredID := ts.seedGaps(t)
	probeID := ts.seedProbes(t, declaredID)
	return ts.seedStatus(t, declaredID, jobID, probeID)
}

func TestStatusAPI_ReadsMatchGoldenFiles(t *testing.T) {
	server := newTestServer(t)
	componentID, incidentID := server.seedAll(t)
	cases := map[string]string{
		"status_public":     "/statut/" + seededSlug + "/api/status",
		"status_components": "/api/status/components?page=" + server.seededPageID(t),
		"status_component":  "/api/status/components/" + componentID,
		"status_incidents":  "/api/status/incidents",
		"status_incident":   "/api/status/incidents/" + incidentID,
		"status_pages":      "/api/status/pages",
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
	servertest.RequireFront(t)
	server := newTestServer(t)
	server.seedAll(t)
	secrets := []string{
		"vps-paris-1", "opencloud-host", "51.15.20.114", "10.8.0.1", remoteID, `"local"`,
		"nextcloud", "postgres", "cloud.exemple.fr", "5432", "sauvegarde", "certbot", "hb_",
		strings.Repeat("a", 12), strings.Repeat("b", 12), "openCloud dev", "Silence",
	}
	for _, path := range []string{"/statut/maison/api/status", "/statut/maison/api/i18n", "/statut/maison"} {
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
	body := get(server.Server, "/statut/maison/api/status").Body.String()
	for _, expected := range []string{`"Nuage"`, `"Le nuage ne répond plus"`, `"Statut de la maison"`, `"global":"down"`} {
		if !strings.Contains(body, expected) {
			t.Errorf("public status lacks %s", expected)
		}
	}
}

// Les routes publiques comptent par adresse résolue ; un en-tête forgé
// n'ouvre pas un seau neuf tant qu'aucun mandataire n'est déclaré. Les
// chiffres des seaux se testent dans le paquet public ; ici, le montage et
// l'adresse que la racine lui donne.
func TestStatusPublic_IsRateLimitedBySourceBehindTheRoot(t *testing.T) {
	servertest.RequireFront(t)
	server := newTestServer(t)
	storetest.StatusPage(t, server.db, "statut")
	passed, refused := 0, 0
	for i := range 100 {
		request := httptest.NewRequest(http.MethodGet, "/statut/statut/api/status", nil)
		request.Header.Set("X-Forwarded-For", "203.0.113."+strconv.Itoa(i))
		recorder := httptest.NewRecorder()
		server.ServeHTTP(recorder, request)
		switch {
		case recorder.Code == http.StatusOK:
			passed++
		case recorder.Code == http.StatusTooManyRequests && recorder.Header().Get("Retry-After") != "":
			refused++
		}
	}
	if passed == 0 || refused == 0 || passed+refused != 100 {
		t.Fatalf("%d passed, %d refused of 100: a burst passes, then a forged X-Forwarded-For must not open a fresh bucket", passed, refused)
	}
	if recorder := get(server.Server, "/"); recorder.Code != http.StatusOK {
		t.Fatalf("the administration shares no bucket with the public: %d", recorder.Code)
	}
}

// Seules les pages HTML publiques se laissent mettre dans un cadre : leur
// API et tout le reste gardent la politique stricte.
func TestStatusPublic_OnlyThePageIsEmbeddable(t *testing.T) {
	server := newTestServer(t)
	for _, path := range []string{"/statut", "/statut/", "/statut/maison", "/statut/maison/"} {
		recorder := get(server.Server, path)
		if got := recorder.Header().Get("Content-Security-Policy"); got != embeddablePolicy || !strings.Contains(got, "frame-ancestors *") {
			t.Errorf("%s: CSP %q", path, got)
		}
		if got := recorder.Header().Get("X-Frame-Options"); got != "" {
			t.Errorf("%s: X-Frame-Options %q", path, got)
		}
	}
	for _, path := range []string{"/statut/maison/api/status", "/statut/maison/api/i18n", "/statut/maison/rien", "/statutx", "/", "/api/status/components"} {
		recorder := get(server.Server, path)
		if got := recorder.Header().Get("Content-Security-Policy"); got != contentSecurityPolicy {
			t.Errorf("%s: CSP %q", path, got)
		}
		if got := recorder.Header().Get("X-Frame-Options"); got != "DENY" {
			t.Errorf("%s: X-Frame-Options %q", path, got)
		}
	}
	if recorder := get(server.Server, "/statut/maison/rien"); recorder.Code != http.StatusNotFound {
		t.Errorf("unknown public path: %d", recorder.Code)
	}
}

func TestStatusPublic_PageIsServedWithoutTheAdministration(t *testing.T) {
	servertest.RequireFront(t)
	server := newTestServer(t)
	storetest.StatusPage(t, server.db, "statut")
	recorder := get(server.Server, "/statut/statut")
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
	pageID := storetest.StatusPage(t, server.db, "statut")
	public, cancelPublic := openLive(t, server, "/statut/statut/api/events", "")
	defer cancelPublic()
	admin, cancelAdmin := openLive(t, server, "/api/events", "")
	defer cancelAdmin()
	if got := public(); got != "connected" {
		t.Fatalf("first public event %q", got)
	}
	if got := admin(); got != "connected" {
		t.Fatalf("first admin event %q", got)
	}
	if _, err := server.status.CreateComponent(context.Background(), status.ComponentDefinition{PageID: pageID, Name: "Site web"}); err != nil {
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
	// L'incident vise des objets, pas des composants : sans objet, le composant
	// n'est plus touché et ne dit rien.
	if component.Name != "Nuage bleu" || component.Derived != "" || component.Effective != "" {
		t.Fatalf("component %+v", component)
	}
	recorder = callAPI(server.Server, http.MethodPost, "/api/status/components", map[string]any{"page_id": server.seededPageID(t), "name": "", "members": []any{}})
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
		"title": "Migration", "impact": "maintenance", "status": "scheduled", "targets": remoteTargets(),
		"starts_at": starts, "ends_at": starts.Add(30 * time.Minute),
	})
	if recorder.Code != http.StatusCreated {
		t.Fatalf("schedule: %d %s", recorder.Code, recorder.Body.String())
	}
	recorder = callAPI(server.Server, http.MethodPut, "/api/status/pages/"+server.seededPageID(t), map[string]any{"slug": seededSlug, "title": "", "announcement": "", "language": "en"})
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

// publicStatusView lit de la page publique ce que les tests des incidents
// regardent : l'état de chaque composant et les incidents ouverts.
type publicStatusView struct {
	Global     string `json:"global"`
	Components []struct {
		Name  string `json:"name"`
		State string `json:"state"`
	} `json:"components"`
	Open []struct {
		Title      string   `json:"title"`
		Impact     string   `json:"impact"`
		Components []string `json:"components"`
	} `json:"open"`
}

func (v publicStatusView) stateOf(name string) string {
	for _, component := range v.Components {
		if component.Name == name {
			return component.State
		}
	}
	return ""
}

func readPublicStatus(t *testing.T, server *testServer) publicStatusView {
	t.Helper()
	recorder := callAPI(server.Server, http.MethodGet, "/statut/"+seededSlug+"/api/status", nil)
	if recorder.Code != http.StatusOK {
		t.Fatalf("public status: %d %s", recorder.Code, recorder.Body.String())
	}
	var view publicStatusView
	decodeAPI(t, recorder, &view)
	return view
}

// componentIDByName retrouve un composant de l'administration par son nom.
func componentIDByName(t *testing.T, server *testServer, name string) (id, derived string) {
	t.Helper()
	var response componentsResponse
	decodeAPI(t, callAPI(server.Server, http.MethodGet, "/api/status/components", nil), &response)
	for _, component := range response.Components {
		if component.Name == name {
			return component.ID, component.Derived
		}
	}
	t.Fatalf("no component named %q in %+v", name, response.Components)
	return "", ""
}

// remoteTargets vise la machine distante, que le composant « Nuage » montre.
func remoteTargets() []map[string]string {
	return []map[string]string{{"kind": "machine", "id": remoteID}}
}

func TestStatusAPI_ChangeIncident_RewritesItAndThePublicPageFollows(t *testing.T) {
	server := newTestServer(t)
	_, incidentID := server.seedAll(t)
	silenceID, _ := componentIDByName(t, server, "Silence")
	_, cloudDerived := componentIDByName(t, server, "Nuage")
	// La sonde que montre « Silence », et elle seule.
	probes, err := server.db.ListProbes(context.Background(), machine.LocalID)
	if err != nil {
		t.Fatal(err)
	}
	before := readPublicStatus(t, server)
	if before.stateOf("Nuage") != "down" || before.stateOf("Silence") != "" || len(before.Open) != 1 {
		t.Fatalf("before the change: %+v", before)
	}

	recorder := callAPI(server.Server, http.MethodPut, "/api/status/incidents/"+incidentID, map[string]any{
		"title": "  Disque remplacé  ", "targets": []map[string]string{{"kind": "probe", "id": probes[0].ID}},
	})
	if recorder.Code != http.StatusOK {
		t.Fatalf("change: %d %s", recorder.Code, recorder.Body.String())
	}
	var changed incidentJSON
	decodeAPI(t, recorder, &changed)
	if changed.ID != incidentID || changed.Title != "Disque remplacé" || changed.Impact != "down" || changed.Status != "identified" {
		t.Fatalf("incident %+v", changed)
	}
	if len(changed.Targets) != 1 || changed.Targets[0].Kind != "probe" || changed.Targets[0].ID != probes[0].ID || changed.Targets[0].Name != probes[0].Name {
		t.Fatalf("targets %+v", changed.Targets)
	}
	if len(changed.Components) != 1 || changed.Components[0].ID != silenceID || len(changed.Updates) != 2 {
		t.Fatalf("components %+v, %d updates: the timeline must stay", changed.Components, len(changed.Updates))
	}
	stored := callAPI(server.Server, http.MethodGet, "/api/status/incidents/"+incidentID, nil)
	var reread incidentJSON
	decodeAPI(t, stored, &reread)
	if reread.Title != "Disque remplacé" || len(reread.Components) != 1 || reread.Components[0].ID != silenceID {
		t.Fatalf("stored incident %+v", reread)
	}

	// L'incident a quitté le Nuage, qui ne montre plus que son état propre,
	// et pèse sur Silence, que rien ne montrait.
	after := readPublicStatus(t, server)
	if len(after.Open) != 1 || after.Open[0].Title != "Disque remplacé" || len(after.Open[0].Components) != 1 || after.Open[0].Components[0] != "Silence" {
		t.Fatalf("open incidents %+v", after.Open)
	}
	if after.stateOf("Silence") != "down" || after.stateOf("Nuage") != cloudDerived {
		t.Fatalf("Silence %q Nuage %q (derived %q)", after.stateOf("Silence"), after.stateOf("Nuage"), cloudDerived)
	}
	if after.Global != "down" {
		t.Fatalf("global %q", after.Global)
	}
}

// Un incident sur un service paraît sur chaque page dont un composant montre
// ce service ou l'une de ses sondes, et l'administration lit les composants
// touchés, toutes pages confondues.
func TestAPI_Incident_ShowsOnThePagesWhoseComponentsItTouches(t *testing.T) {
	server := newTestServer(t)
	ctx := context.Background()
	server.enroll(t, "vps-paris-1", remoteID)
	serviceID := server.seedGaps(t)
	probeID := server.seedProbes(t, serviceID)
	shop, err := server.status.CreatePage(ctx, status.PageDefinition{Slug: "boutique"})
	if err != nil {
		t.Fatal(err)
	}
	blog, err := server.status.CreatePage(ctx, status.PageDefinition{Slug: "blog"})
	if err != nil {
		t.Fatal(err)
	}
	onShop, err := server.status.CreateComponent(ctx, status.ComponentDefinition{PageID: shop.ID, Name: "Cloud", Members: []status.MemberRef{{Kind: status.KindService, ID: serviceID}}})
	if err != nil {
		t.Fatal(err)
	}
	onBlog, err := server.status.CreateComponent(ctx, status.ComponentDefinition{PageID: blog.ID, Name: "Site", Members: []status.MemberRef{{Kind: status.KindProbe, ID: probeID}}})
	if err != nil {
		t.Fatal(err)
	}

	recorder := callAPI(server.Server, http.MethodPost, "/api/status/incidents", map[string]any{
		"title": "Nextcloud est tombé", "impact": "down", "status": "investigating",
		"targets": []map[string]string{{"kind": "service", "id": serviceID}},
	})
	if recorder.Code != http.StatusCreated {
		t.Fatalf("open: %d %s", recorder.Code, recorder.Body.String())
	}
	var opened incidentJSON
	decodeAPI(t, recorder, &opened)
	if len(opened.Targets) != 1 || opened.Targets[0].Name != "Nextcloud" || len(opened.Components) != 2 ||
		opened.Components[0].ID != onShop.ID || opened.Components[1].ID != onBlog.ID {
		t.Fatalf("targets %+v components %+v", opened.Targets, opened.Components)
	}
	for _, page := range []status.Page{shop, blog} {
		var listed incidentsResponse
		decodeAPI(t, callAPI(server.Server, http.MethodGet, "/api/status/incidents?page="+page.ID, nil), &listed)
		if len(listed.Incidents) != 1 || listed.Incidents[0].ID != opened.ID || len(listed.Incidents[0].Components) != 2 {
			t.Errorf("incidents of %s: %+v", page.Slug, listed.Incidents)
		}
		snapshot, err := server.status.Snapshot(ctx, page.ID)
		if err != nil || len(snapshot.Open) != 1 || len(snapshot.Open[0].Components) != 1 {
			t.Errorf("snapshot of %s: %+v %v", page.Slug, snapshot.Open, err)
		}
	}
}

func TestStatusAPI_ChangeIncident_MovesAMaintenanceWindow(t *testing.T) {
	server := newTestServer(t)
	server.seedAll(t)
	starts := testNow.Add(time.Hour)
	recorder := callAPI(server.Server, http.MethodPost, "/api/status/incidents", map[string]any{
		"title": "Migration", "impact": "maintenance", "status": "scheduled", "targets": remoteTargets(),
		"starts_at": starts, "ends_at": starts.Add(30 * time.Minute),
	})
	if recorder.Code != http.StatusCreated {
		t.Fatalf("schedule: %d %s", recorder.Code, recorder.Body.String())
	}
	var planned incidentJSON
	decodeAPI(t, recorder, &planned)

	later := starts.Add(2 * time.Hour)
	recorder = callAPI(server.Server, http.MethodPut, "/api/status/incidents/"+planned.ID, map[string]any{
		"title": planned.Title, "targets": remoteTargets(), "starts_at": later, "ends_at": later.Add(time.Hour),
	})
	if recorder.Code != http.StatusOK {
		t.Fatalf("move: %d %s", recorder.Code, recorder.Body.String())
	}
	var moved incidentJSON
	decodeAPI(t, recorder, &moved)
	if moved.StartsAt == nil || !moved.StartsAt.Equal(later) || moved.EndsAt == nil || !moved.EndsAt.Equal(later.Add(time.Hour)) || moved.Status != "scheduled" {
		t.Fatalf("window %v → %v, status %q", moved.StartsAt, moved.EndsAt, moved.Status)
	}
	// Sans fenêtre, une maintenance n'a pas de sens.
	recorder = callAPI(server.Server, http.MethodPut, "/api/status/incidents/"+planned.ID, map[string]any{"title": planned.Title, "targets": remoteTargets()})
	if recorder.Code != http.StatusUnprocessableEntity || errorCode(t, recorder) != "incident.window_invalid" {
		t.Fatalf("no window: %d %s", recorder.Code, recorder.Body.String())
	}
}

func TestStatusAPI_ChangeIncident_Refuses(t *testing.T) {
	server := newTestServer(t)
	cloudID, incidentID := server.seedAll(t)
	starts := testNow.Add(time.Hour)
	cases := []struct {
		name string
		path string
		body map[string]any
		code int
		key  string
	}{
		{"inconnu", "/api/status/incidents/nope", map[string]any{"title": "x", "targets": remoteTargets()}, http.StatusNotFound, "not_found"},
		{"titre vide", "/api/status/incidents/" + incidentID, map[string]any{"title": " ", "targets": remoteTargets()}, http.StatusUnprocessableEntity, "incident.title_invalid"},
		{"sans cible", "/api/status/incidents/" + incidentID, map[string]any{"title": "x", "targets": []any{}}, http.StatusUnprocessableEntity, "incident.target_invalid"},
		{"cible inconnue", "/api/status/incidents/" + incidentID, map[string]any{"title": "x", "targets": []map[string]string{{"kind": "machine", "id": "nope"}}}, http.StatusUnprocessableEntity, "incident.target_invalid"},
		{"genre inconnu", "/api/status/incidents/" + incidentID, map[string]any{"title": "x", "targets": []map[string]string{{"kind": "component", "id": cloudID}}}, http.StatusUnprocessableEntity, "incident.target_invalid"},
		{"fenêtre sur un incident", "/api/status/incidents/" + incidentID, map[string]any{"title": "x", "targets": remoteTargets(), "starts_at": starts, "ends_at": starts.Add(time.Hour)}, http.StatusUnprocessableEntity, "incident.window_invalid"},
		{"champ inconnu", "/api/status/incidents/" + incidentID, map[string]any{"title": "x", "targets": remoteTargets(), "impact": "maintenance"}, http.StatusBadRequest, "bad_json"},
	}
	for _, tc := range cases {
		recorder := callAPI(server.Server, http.MethodPut, tc.path, tc.body)
		if recorder.Code != tc.code || errorCode(t, recorder) != tc.key {
			t.Errorf("%s: %d %s, want %d %s", tc.name, recorder.Code, recorder.Body.String(), tc.code, tc.key)
		}
	}
	var unchanged incidentJSON
	decodeAPI(t, callAPI(server.Server, http.MethodGet, "/api/status/incidents/"+incidentID, nil), &unchanged)
	if unchanged.Title != "Le nuage ne répond plus" {
		t.Fatalf("a refusal reached the base: %q", unchanged.Title)
	}
}

func TestStatusAPI_DeleteIncident_RemovesItEverywhere(t *testing.T) {
	server := newTestServer(t)
	server.seedAll(t)
	_, cloudDerived := componentIDByName(t, server, "Nuage")
	var open incidentsResponse
	decodeAPI(t, callAPI(server.Server, http.MethodGet, "/api/status/incidents", nil), &open)
	var incidentID string
	for _, incident := range open.Incidents {
		if incident.Status != "resolved" {
			incidentID = incident.ID
		}
	}
	if incidentID == "" || readPublicStatus(t, server).stateOf("Nuage") != "down" {
		t.Fatal("the seed must leave one open incident on the Nuage")
	}

	recorder := callAPI(server.Server, http.MethodDelete, "/api/status/incidents/"+incidentID, nil)
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("delete: %d %s", recorder.Code, recorder.Body.String())
	}
	if recorder := callAPI(server.Server, http.MethodGet, "/api/status/incidents/"+incidentID, nil); recorder.Code != http.StatusNotFound {
		t.Fatalf("deleted incident still answers: %d", recorder.Code)
	}
	var remaining incidentsResponse
	decodeAPI(t, callAPI(server.Server, http.MethodGet, "/api/status/incidents", nil), &remaining)
	if len(remaining.Incidents) != 1 || remaining.Incidents[0].Status != "resolved" {
		t.Fatalf("incidents left %+v, want the resolved one only", remaining.Incidents)
	}
	view := readPublicStatus(t, server)
	if len(view.Open) != 0 || view.stateOf("Nuage") != cloudDerived {
		t.Fatalf("public page: open %+v, Nuage %q (derived %q)", view.Open, view.stateOf("Nuage"), cloudDerived)
	}
	var counts countsResponse
	decodeAPI(t, callAPI(server.Server, http.MethodGet, "/api/counts", nil), &counts)
	if counts.Status.OpenIncidents != 0 {
		t.Fatalf("open incidents %d", counts.Status.OpenIncidents)
	}
	recorder = callAPI(server.Server, http.MethodDelete, "/api/status/incidents/"+incidentID, nil)
	if recorder.Code != http.StatusNotFound || errorCode(t, recorder) != "not_found" {
		t.Fatalf("delete twice: %d %s", recorder.Code, recorder.Body.String())
	}
}
