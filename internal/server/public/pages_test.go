package public

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ldesfontaine/opencloud/internal/status"
)

// Deux pages ne se voient pas : chacune montre ses composants, et un
// incident qui touche les deux ne cite sur chacune que les siens.
func TestStatusPublic_TwoPagesDoNotSeeEachOther(t *testing.T) {
	server := newBench(t)
	ctx := context.Background()
	other, err := server.status.CreatePage(ctx, status.PageDefinition{Slug: "client-b", Title: "Client B"})
	if err != nil {
		t.Fatal(err)
	}
	mine, err := server.status.CreateComponent(ctx, status.ComponentDefinition{PageID: server.pageID, Name: "Courrier"})
	if err != nil {
		t.Fatal(err)
	}
	theirs, err := server.status.CreateComponent(ctx, status.ComponentDefinition{PageID: other.ID, Name: "Boutique"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := server.status.OpenIncident(ctx, status.IncidentDefinition{
		Title: "Coupure du fournisseur", Impact: status.ImpactDown, Status: status.StatusInvestigating,
		ComponentIDs: []string{mine.ID, theirs.ID},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := server.status.OpenIncident(ctx, status.IncidentDefinition{
		Title: "Panne de la boutique", Impact: status.ImpactDown, Status: status.StatusInvestigating,
		ComponentIDs: []string{theirs.ID},
	}); err != nil {
		t.Fatal(err)
	}

	body := server.get("/statut/statut/api/status").Body.String()
	for _, foreign := range []string{"Boutique", "Client B", "Panne de la boutique", "client-b"} {
		if strings.Contains(body, foreign) {
			t.Errorf("page statut shows %q of the other page: %s", foreign, body)
		}
	}
	for _, own := range []string{`"Courrier"`, `"Coupure du fournisseur"`, `"components":["Courrier"]`} {
		if !strings.Contains(body, own) {
			t.Errorf("page statut lacks %s: %s", own, body)
		}
	}
	otherBody := server.get("/statut/client-b/api/status").Body.String()
	if strings.Contains(otherBody, "Courrier") || !strings.Contains(otherBody, `"Panne de la boutique"`) || !strings.Contains(otherBody, `"title":"Client B"`) {
		t.Errorf("page client-b: %s", otherBody)
	}
}

// Une adresse inconnue répond 404 partout : page, instantané, chaînes, direct.
func TestStatusPublic_UnknownSlugIs404(t *testing.T) {
	server := newBench(t)
	for _, path := range []string{"/statut/nulle-part", "/statut/nulle-part/", "/statut/nulle-part/api/status", "/statut/nulle-part/api/i18n", "/statut/nulle-part/api/events"} {
		if recorder := server.get(path); recorder.Code != http.StatusNotFound {
			t.Errorf("%s: %d", path, recorder.Code)
		}
	}
	if taken := server.handler.tabs.Taken(); taken != 0 {
		t.Fatalf("an unknown page took %d places of the pool", taken)
	}
}

// /statut seul mène à la page s'il n'y en a qu'une ; sinon 404, jamais une
// liste.
func TestStatusPublic_RootRedirectsToTheOnlyPage(t *testing.T) {
	server := newBench(t)
	for _, path := range []string{"/statut", "/statut/"} {
		recorder := server.get(path)
		if recorder.Code != http.StatusFound || recorder.Header().Get("Location") != "/statut/statut" {
			t.Fatalf("%s with one page: %d %q", path, recorder.Code, recorder.Header().Get("Location"))
		}
	}
	if _, err := server.status.CreatePage(context.Background(), status.PageDefinition{Slug: "client-b"}); err != nil {
		t.Fatal(err)
	}
	recorder := server.get("/statut")
	if recorder.Code != http.StatusNotFound || strings.Contains(recorder.Body.String(), "client-b") || strings.Contains(recorder.Body.String(), "statut/statut") {
		t.Fatalf("with two pages: %d %q", recorder.Code, recorder.Body.String())
	}
	if err := server.status.DeletePage(context.Background(), server.pageID); err != nil {
		t.Fatal(err)
	}
	if recorder := server.get("/statut/statut"); recorder.Code != http.StatusNotFound {
		t.Fatalf("deleted page still answers: %d", recorder.Code)
	}
	recorder = httptest.NewRecorder()
	server.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/statut", nil))
	if recorder.Code != http.StatusFound || recorder.Header().Get("Location") != "/statut/client-b" {
		t.Fatalf("one page again: %d %q", recorder.Code, recorder.Header().Get("Location"))
	}
}
