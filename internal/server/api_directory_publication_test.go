package server

import (
	"net/http"
	"testing"
)

// Un projet se publie, se modifie, se dépublie et se supprime par l'API des
// projets ; sa suppression passe par le plan, qui dit la page et les
// composants emportés.
func TestDirectoryPublicationAPI_PublishUpdateUnpublishDelete(t *testing.T) {
	server := newTestServer(t)
	recorder := callAPI(server.Server, http.MethodPost, "/api/directory/projects", map[string]any{
		"name": "client-a", "title": "Client A",
		"publication": map[string]any{"slug": "client-a", "announcement": "", "language": "en"},
	})
	if recorder.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", recorder.Code, recorder.Body.String())
	}
	var created projectJSON
	decodeAPI(t, recorder, &created)
	if created.Publication == nil || created.Publication.Slug != "client-a" ||
		created.Publication.PublicURL != "http://example.com/statut/client-a" || created.Publication.Language != "en" {
		t.Fatalf("created %+v", created)
	}

	recorder = callAPI(server.Server, http.MethodPost, "/api/status/components", map[string]any{"project_id": created.ID, "name": "Site", "members": []any{}})
	if recorder.Code != http.StatusCreated {
		t.Fatalf("component: %d %s", recorder.Code, recorder.Body.String())
	}
	var components componentsResponse
	decodeAPI(t, callAPI(server.Server, http.MethodGet, "/api/status/components?project="+created.ID, nil), &components)
	if len(components.Components) != 1 || components.PublicURL != created.Publication.PublicURL {
		t.Fatalf("components of the project %+v", components)
	}
	if got := callAPI(server.Server, http.MethodGet, "/api/status/components?project=nope", nil); got.Code != http.StatusNotFound {
		t.Fatalf("components of an unknown project: %d", got.Code)
	}

	recorder = callAPI(server.Server, http.MethodPut, "/api/directory/projects/"+created.ID, map[string]any{
		"name": "client-a", "title": "Client B",
		"publication": map[string]any{"slug": "client-b", "announcement": "Coupure.", "language": "fr"},
	})
	if recorder.Code != http.StatusOK {
		t.Fatalf("update: %d %s", recorder.Code, recorder.Body.String())
	}
	var listed projectsResponse
	decodeAPI(t, callAPI(server.Server, http.MethodGet, "/api/directory/projects", nil), &listed)
	if len(listed.Projects) != 1 || listed.Projects[0].Publication == nil ||
		listed.Projects[0].Publication.Slug != "client-b" || listed.Projects[0].Publication.Announcement != "Coupure." {
		t.Fatalf("projects %+v", listed)
	}

	// Dépublier : le projet reste, sa page s'en va avec ses composants saisis.
	recorder = callAPI(server.Server, http.MethodPut, "/api/directory/projects/"+created.ID, map[string]any{
		"name": "client-a", "title": "Client B", "publication": nil,
	})
	if recorder.Code != http.StatusOK {
		t.Fatalf("unpublish: %d %s", recorder.Code, recorder.Body.String())
	}
	var unpublished projectJSON
	decodeAPI(t, recorder, &unpublished)
	if unpublished.Publication != nil {
		t.Fatalf("still published: %+v", unpublished.Publication)
	}
	if got := callAPI(server.Server, http.MethodGet, "/statut/client-b/api/status", nil); got.Code != http.StatusNotFound {
		t.Fatalf("unpublished page answers: %d", got.Code)
	}

	// Republier, puis supprimer le projet publié.
	recorder = callAPI(server.Server, http.MethodPut, "/api/directory/projects/"+created.ID, map[string]any{
		"name": "client-a", "title": "Client B", "publication": map[string]any{"slug": "client-b", "language": "fr"},
	})
	if recorder.Code != http.StatusOK {
		t.Fatalf("republish: %d %s", recorder.Code, recorder.Body.String())
	}
	var plan deletionPlanJSON
	decodeAPI(t, callAPI(server.Server, http.MethodGet, "/api/directory/projects/"+created.ID+"/delete-plan", nil), &plan)
	removed := map[string]int{}
	for _, entry := range plan.Removes {
		removed[entry.Kind] = entry.Count
	}
	if plan.Blocked || removed["publications"] != 1 {
		t.Fatalf("plan %+v", plan)
	}
	if _, found := removed["components"]; !found {
		t.Fatalf("plan without components: %+v", plan)
	}
	if got := callAPI(server.Server, http.MethodDelete, "/api/directory/projects/"+created.ID, nil); got.Code != http.StatusNoContent {
		t.Fatalf("delete: %d %s", got.Code, got.Body.String())
	}
	if got := callAPI(server.Server, http.MethodGet, "/api/directory/projects/"+created.ID, nil); got.Code != http.StatusNotFound {
		t.Fatalf("deleted project: %d", got.Code)
	}
	if got := callAPI(server.Server, http.MethodGet, "/statut/client-b/api/status", nil); got.Code != http.StatusNotFound {
		t.Fatalf("deleted page answers: %d", got.Code)
	}
	decodeAPI(t, callAPI(server.Server, http.MethodGet, "/api/status/components", nil), &components)
	if len(components.Components) != 0 {
		t.Fatalf("the components left with their project: %+v", components.Components)
	}
}

func TestDirectoryPublicationAPI_Refuses(t *testing.T) {
	server := newTestServer(t)
	first := callAPI(server.Server, http.MethodPost, "/api/directory/projects", map[string]any{
		"name": "client-a", "publication": map[string]any{"slug": "a"},
	})
	if first.Code != http.StatusCreated {
		t.Fatalf("first published project: %d %s", first.Code, first.Body.String())
	}
	cases := []struct {
		name        string
		publication map[string]any
		key         string
	}{
		{"adresse invalide", map[string]any{"slug": "A b"}, "project.publish_slug_invalid"},
		{"adresse prise", map[string]any{"slug": "a"}, "project.publish_slug_taken"},
		{"langue inconnue", map[string]any{"slug": "b", "language": "de"}, "project.publish_language_invalid"},
		{"annonce trop longue", map[string]any{"slug": "b", "announcement": string(make([]byte, 501))}, "project.publish_announcement_invalid"},
	}
	for _, tc := range cases {
		recorder := callAPI(server.Server, http.MethodPost, "/api/directory/projects", map[string]any{"name": "autre", "publication": tc.publication})
		if recorder.Code != http.StatusUnprocessableEntity && recorder.Code != http.StatusConflict || errorCode(t, recorder) != tc.key {
			t.Errorf("%s: %d %s, want %s", tc.name, recorder.Code, recorder.Body.String(), tc.key)
		}
	}
	recorder := callAPI(server.Server, http.MethodPost, "/api/status/components", map[string]any{"name": "Site", "members": []any{}})
	if recorder.Code != http.StatusUnprocessableEntity || errorCode(t, recorder) != "component.project_invalid" {
		t.Fatalf("component without a project: %d %s", recorder.Code, recorder.Body.String())
	}
}
