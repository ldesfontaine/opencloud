package server

import (
	"context"
	"net/http"
	"testing"

	"github.com/ldesfontaine/opencloud/internal/directory"
)

// Une page se crée, se lit, se modifie et se supprime par l'API ; sa
// suppression passe par le plan, qui dit les composants emportés.
func TestStatusPagesAPI_CreateUpdateDelete(t *testing.T) {
	server := newTestServer(t)
	recorder := callAPI(server.Server, http.MethodPost, "/api/status/pages", map[string]any{
		"slug": "client-a", "title": "Client A", "announcement": "", "language": "en", "project_id": "",
	})
	if recorder.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", recorder.Code, recorder.Body.String())
	}
	var created pageJSON
	decodeAPI(t, recorder, &created)
	if created.Slug != "client-a" || created.PublicURL != "http://example.com/statut/client-a" || created.Language != "en" {
		t.Fatalf("created %+v", created)
	}
	recorder = callAPI(server.Server, http.MethodPost, "/api/status/components", map[string]any{"page_id": created.ID, "name": "Site", "members": []any{}})
	if recorder.Code != http.StatusCreated {
		t.Fatalf("component: %d %s", recorder.Code, recorder.Body.String())
	}
	var components componentsResponse
	decodeAPI(t, callAPI(server.Server, http.MethodGet, "/api/status/components?page="+created.ID, nil), &components)
	if len(components.Components) != 1 || components.PublicURL != created.PublicURL {
		t.Fatalf("components of the page %+v", components)
	}
	if got := callAPI(server.Server, http.MethodGet, "/api/status/components?page=nope", nil); got.Code != http.StatusNotFound {
		t.Fatalf("components of an unknown page: %d", got.Code)
	}

	recorder = callAPI(server.Server, http.MethodPut, "/api/status/pages/"+created.ID, map[string]any{
		"slug": "client-b", "title": "Client B", "announcement": "Coupure.", "language": "fr", "project_id": "",
	})
	if recorder.Code != http.StatusOK {
		t.Fatalf("update: %d %s", recorder.Code, recorder.Body.String())
	}
	var pages pagesResponse
	decodeAPI(t, callAPI(server.Server, http.MethodGet, "/api/status/pages", nil), &pages)
	if len(pages.Pages) != 1 || pages.Pages[0].Slug != "client-b" || pages.Pages[0].Announcement != "Coupure." {
		t.Fatalf("pages %+v", pages)
	}

	var plan deletionPlanJSON
	decodeAPI(t, callAPI(server.Server, http.MethodGet, "/api/status/pages/"+created.ID+"/delete-plan", nil), &plan)
	if plan.Blocked || len(plan.Removes) != 1 || plan.Removes[0].Kind != "components" || plan.Removes[0].Count != 1 {
		t.Fatalf("plan %+v", plan)
	}
	if got := callAPI(server.Server, http.MethodDelete, "/api/status/pages/"+created.ID, nil); got.Code != http.StatusNoContent {
		t.Fatalf("delete: %d", got.Code)
	}
	if got := callAPI(server.Server, http.MethodGet, "/api/status/pages/"+created.ID, nil); got.Code != http.StatusNotFound {
		t.Fatalf("deleted page: %d", got.Code)
	}
	decodeAPI(t, callAPI(server.Server, http.MethodGet, "/api/status/components", nil), &components)
	if len(components.Components) != 0 {
		t.Fatalf("the components left with their page: %+v", components.Components)
	}
}

func TestStatusPagesAPI_Refuses(t *testing.T) {
	server := newTestServer(t)
	project, err := server.directory.CreateProject(context.Background(), directory.ProjectDefinition{Name: "client-a"})
	if err != nil {
		t.Fatal(err)
	}
	if got := callAPI(server.Server, http.MethodPost, "/api/status/pages", map[string]any{"slug": "a", "project_id": project.ID}); got.Code != http.StatusCreated {
		t.Fatalf("first page of the project: %d %s", got.Code, got.Body.String())
	}
	cases := []struct {
		name string
		body map[string]any
		key  string
	}{
		{"adresse invalide", map[string]any{"slug": "A b"}, "statuspage.slug_invalid"},
		{"adresse prise", map[string]any{"slug": "a"}, "statuspage.slug_taken"},
		{"projet inconnu", map[string]any{"slug": "b", "project_id": "prj_nope"}, "statuspage.project_invalid"},
		{"projet déjà servi", map[string]any{"slug": "b", "project_id": project.ID}, "statuspage.project_taken"},
		{"langue inconnue", map[string]any{"slug": "b", "language": "de"}, "statuspage.language_invalid"},
	}
	for _, tc := range cases {
		recorder := callAPI(server.Server, http.MethodPost, "/api/status/pages", tc.body)
		if recorder.Code != http.StatusUnprocessableEntity || errorCode(t, recorder) != tc.key {
			t.Errorf("%s: %d %s, want %s", tc.name, recorder.Code, recorder.Body.String(), tc.key)
		}
	}
	// Le projet qui a sa page se refuse, avec le plan qui la nomme.
	recorder := callAPI(server.Server, http.MethodDelete, "/api/directory/projects/"+project.ID, nil)
	if recorder.Code != http.StatusConflict || errorCode(t, recorder) != "project.has_status_page" {
		t.Fatalf("delete the project: %d %s", recorder.Code, recorder.Body.String())
	}
	recorder = callAPI(server.Server, http.MethodPost, "/api/status/components", map[string]any{"name": "Site", "members": []any{}})
	if recorder.Code != http.StatusUnprocessableEntity || errorCode(t, recorder) != "component.page_invalid" {
		t.Fatalf("component without a page: %d %s", recorder.Code, recorder.Body.String())
	}
}
