package public

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ldesfontaine/opencloud/internal/alert"
	"github.com/ldesfontaine/opencloud/internal/heartbeat"
	"github.com/ldesfontaine/opencloud/internal/server/servertest"
	"github.com/ldesfontaine/opencloud/internal/status"
	"github.com/ldesfontaine/opencloud/internal/store/storetest"
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
	// La machine est sur les deux pages, la tâche sur celle de la boutique
	// seulement : un incident sur la machine paraît sur les deux pages, un
	// incident sur la tâche sur une seule.
	storetest.EnrollMachine(t, server.machines, servertest.RemoteID, "vps-paris-1")
	job, err := server.heartbeats.Create(ctx, heartbeat.Definition{Name: "sauvegarde", Interval: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := server.status.CreateComponent(ctx, status.ComponentDefinition{PageID: server.pageID, Name: "Courrier", Members: []status.MemberRef{{Kind: status.KindMachine, ID: servertest.RemoteID}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := server.status.CreateComponent(ctx, status.ComponentDefinition{PageID: other.ID, Name: "Boutique", Members: []status.MemberRef{
		{Kind: status.KindMachine, ID: servertest.RemoteID}, {Kind: status.KindHeartbeat, ID: job.ID},
	}}); err != nil {
		t.Fatal(err)
	}
	if _, err := server.status.OpenIncident(ctx, status.IncidentDefinition{
		Title: "Coupure du fournisseur", Impact: status.ImpactDown, Status: status.StatusInvestigating,
		Targets: []status.MemberRef{{Kind: status.KindMachine, ID: servertest.RemoteID}},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := server.status.OpenIncident(ctx, status.IncidentDefinition{
		Title: "Panne de la boutique", Impact: status.ImpactDown, Status: status.StatusInvestigating,
		Targets: []status.MemberRef{{Kind: status.KindHeartbeat, ID: job.ID}},
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

// Un silence annoncé sur une machine paraît comme une maintenance sur la page
// dont un composant la montre, sous son titre public, et sur celle-là seule ;
// ni la machine ni le motif du silence n'y sont nommés, et un silence non
// annoncé sur la même machine n'y laisse aucune trace.
func TestStatusPublic_AnnouncedMuteShowsAsMaintenanceOnItsPageOnly(t *testing.T) {
	server := newBench(t)
	ctx := context.Background()
	other, err := server.status.CreatePage(ctx, status.PageDefinition{Slug: "client-b", Title: "Client B"})
	if err != nil {
		t.Fatal(err)
	}
	storetest.EnrollMachine(t, server.machines, servertest.RemoteID, "vps-paris-1")
	elsewhere := storetest.EnrollMachine(t, server.machines, "33333333-2222-4333-8444-555555555555", "vps-lyon-1")
	if _, err := server.status.CreateComponent(ctx, status.ComponentDefinition{PageID: server.pageID, Name: "Courrier", Members: []status.MemberRef{{Kind: status.KindMachine, ID: servertest.RemoteID}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := server.status.CreateComponent(ctx, status.ComponentDefinition{PageID: other.ID, Name: "Boutique", Members: []status.MemberRef{{Kind: status.KindMachine, ID: elsewhere.ID}}}); err != nil {
		t.Fatal(err)
	}
	_, err = server.db.InsertMute(ctx, alert.Mute{
		Target: alert.Target{Kind: alert.TargetMachine, ID: servertest.RemoteID}, StartsAt: testNow.Add(-time.Minute), EndsAt: testNow.Add(time.Hour),
		Reason: "SECRET-motif interne", Announce: true, PublicTitle: "Remplacement du disque", CreatedAt: testNow,
	})
	if err != nil {
		t.Fatal(err)
	}
	plainID, err := server.db.InsertMute(ctx, alert.Mute{
		Target: alert.Target{Kind: alert.TargetMachine, ID: servertest.RemoteID}, StartsAt: testNow.Add(-time.Minute), EndsAt: testNow.Add(time.Hour),
		Reason: "SECRET-silence non annoncé", CreatedAt: testNow,
	})
	if err != nil {
		t.Fatal(err)
	}

	var mine publicStatusJSON
	if err := json.Unmarshal(server.get("/statut/statut/api/status").Body.Bytes(), &mine); err != nil {
		t.Fatal(err)
	}
	if len(mine.Open) != 1 || mine.Open[0].Title != "Remplacement du disque" || mine.Open[0].Impact != "maintenance" ||
		mine.Open[0].Status != "in_progress" || len(mine.Open[0].Components) != 1 || mine.Open[0].Components[0] != "Courrier" {
		t.Fatalf("page statut: %+v", mine.Open)
	}
	otherBody := server.get("/statut/client-b/api/status").Body.String()
	if strings.Contains(otherBody, "Remplacement du disque") || strings.Contains(otherBody, `"impact":"maintenance"`) {
		t.Errorf("page client-b shows the maintenance of another page: %s", otherBody)
	}
	if body := server.get("/statut/statut/api/status").Body.String(); strings.Contains(body, "vps-paris-1") || strings.Contains(body, servertest.RemoteID) {
		t.Errorf("the announced mute leaks its target: %s", body)
	}
	for _, body := range []string{server.get("/statut/statut/api/status").Body.String(), otherBody} {
		if strings.Contains(body, "SECRET") || strings.Contains(body, fmt.Sprintf(`"mute-%d"`, plainID)) {
			t.Errorf("a mute leaks its reason or an unannounced mute shows: %s", body)
		}
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
