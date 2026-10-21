package server

import (
	"context"
	"net/http"
	"testing"

	"github.com/ldesfontaine/opencloud/internal/container"
	"github.com/ldesfontaine/opencloud/internal/directory"
	"github.com/ldesfontaine/opencloud/internal/machine"
	"github.com/ldesfontaine/opencloud/internal/status"
	"github.com/ldesfontaine/opencloud/internal/store/storetest"
)

// Un service que des sondes ou des composants visent ne se supprime pas : 409,
// la clé du catalogue et le plan avec la liste de ce qui le lie, que la fiche
// donne aussi. Détaché, il part.
func TestAPI_DeleteLinkedService_Answers409WithThePlan(t *testing.T) {
	server := newTestServer(t)
	server.enroll(t, "vps-paris-1", remoteID)
	var created declaredServiceJSON
	decodeAPI(t, callAPI(server.Server, http.MethodPost, "/api/directory/services", nextcloudRequest(remoteID)), &created)
	probeCreated := server.createProbeVia(t, probeRequest{Name: "site", Kind: "http", Target: "https://a.fr/", MachineID: machine.LocalID, ServiceID: created.ID})
	component, err := server.status.CreateComponent(context.Background(), status.ComponentDefinition{PageID: storetest.StatusPage(t, server.db, "statut"),
		Name: "Nuage", Members: []status.MemberRef{{Kind: status.KindService, ID: created.ID}},
	})
	if err != nil {
		t.Fatal(err)
	}

	var detail declaredServiceDetailJSON
	decodeAPI(t, callAPI(server.Server, http.MethodGet, "/api/directory/services/"+created.ID, nil), &detail)
	if len(detail.Links.Probes) != 1 || detail.Links.Probes[0].ID != probeCreated.ID || len(detail.Links.Components) != 1 || detail.Links.Components[0].ID != component.ID {
		t.Fatalf("la fiche doit dire ce qui la lie: %+v", detail.Links)
	}

	got := callAPI(server.Server, http.MethodDelete, "/api/directory/services/"+created.ID, nil)
	if got.Code != http.StatusConflict || errorCode(t, got) != "directory.service_linked" {
		t.Fatalf("%d %s", got.Code, got.Body.String())
	}
	var refusal deletionRefusalJSON
	decodeAPI(t, got, &refusal)
	blockers := refusal.Plan.Blockers
	if !refusal.Plan.Blocked || len(blockers) != 2 || blockers[0].Kind != "probes" || blockers[0].Objects[0].Name != "site" ||
		blockers[1].Kind != "components" || blockers[1].Objects[0].Name != "Nuage" {
		t.Fatalf("le refus doit porter le plan: %+v", refusal)
	}
	if got := callAPI(server.Server, http.MethodGet, "/api/directory/services/"+created.ID, nil); got.Code != http.StatusOK {
		t.Fatalf("le service a disparu malgré le refus: %d", got.Code)
	}

	callAPI(server.Server, http.MethodDelete, "/api/status/components/"+component.ID, nil)
	callAPI(server.Server, http.MethodDelete, "/api/probes/"+probeCreated.ID, nil)
	if got := callAPI(server.Server, http.MethodDelete, "/api/directory/services/"+created.ID, nil); got.Code != http.StatusNoContent {
		t.Fatalf("détaché: %d %s", got.Code, got.Body.String())
	}
	if got := callAPI(server.Server, http.MethodDelete, "/api/directory/services/"+created.ID, nil); got.Code != http.StatusNotFound {
		t.Fatalf("supprimé deux fois: %d", got.Code)
	}
}

// Valider lève « à vérifier » si la définition passe la validation, et la dit
// par sa clé sinon ; deux fois, c'est la même réponse ; un service inconnu est
// un 404.
func TestAPI_Review_ClearsAndRefusesInvalid(t *testing.T) {
	server := newTestServer(t)
	server.enroll(t, "vps-paris-1", remoteID)
	taken := directory.Service{
		ID: "svc_0123456789abcdef", MachineID: remoteID, Name: "whoami", Nature: directory.NatureApplication,
		Origin: directory.OriginContainer, DeclaredBy: directory.ByMigration, NeedsReview: true,
		CreatedAt: testNow, UpdatedAt: testNow,
		Units: []directory.Unit{{
			ID: "unit_0123456789abcdef", ServiceID: "svc_0123456789abcdef", Name: "whoami", Kind: directory.UnitContainer,
			Image: "Not A Valid Image", ContainerName: "whoami", UpdatePolicy: container.PolicyWatch,
		}},
	}
	if err := server.db.InsertDeclaredService(context.Background(), taken); err != nil {
		t.Fatal(err)
	}
	path := "/api/directory/services/" + taken.ID + "/actions/review"

	got := callAPI(server.Server, http.MethodPost, path, nil)
	if got.Code != http.StatusUnprocessableEntity || errorCode(t, got) != "directory.image_invalid" {
		t.Fatalf("définition invalide: %d %s", got.Code, got.Body.String())
	}
	var still declaredServiceDetailJSON
	decodeAPI(t, callAPI(server.Server, http.MethodGet, "/api/directory/services/"+taken.ID, nil), &still)
	if !still.NeedsReview {
		t.Fatal("un refus ne valide pas")
	}

	fixed := declaredServiceRequest{
		MachineID: remoteID, Name: "whoami", Nature: "application", Origin: "container",
		Units: []unitRequest{{ID: "unit_0123456789abcdef", Name: "whoami", Image: "traefik/whoami", ContainerName: "whoami"}},
	}
	if got := callAPI(server.Server, http.MethodPut, "/api/directory/services/"+taken.ID, fixed); got.Code != http.StatusOK {
		t.Fatalf("correction: %d %s", got.Code, got.Body.String())
	}
	var edited declaredServiceDetailJSON
	decodeAPI(t, callAPI(server.Server, http.MethodGet, "/api/directory/services/"+taken.ID, nil), &edited)
	if !edited.NeedsReview {
		t.Fatal("modifier un service repris ne le valide pas")
	}

	var reviewed declaredServiceJSON
	got = callAPI(server.Server, http.MethodPost, path, nil)
	if got.Code != http.StatusOK {
		t.Fatalf("validation: %d %s", got.Code, got.Body.String())
	}
	decodeAPI(t, got, &reviewed)
	if reviewed.NeedsReview || reviewed.ID != taken.ID || reviewed.DeclaredBy != "migration" {
		t.Fatalf("service validé = %+v", reviewed)
	}
	if got := callAPI(server.Server, http.MethodPost, path, nil); got.Code != http.StatusOK {
		t.Fatalf("valider deux fois: %d", got.Code)
	}
	if got := callAPI(server.Server, http.MethodPost, "/api/directory/services/svc_inconnu/actions/review", nil); got.Code != http.StatusNotFound {
		t.Fatalf("service inconnu: %d", got.Code)
	}
}

// La politique d'une unité : une valeur hors liste, ou autre que watch sur un
// binaire, est refusée avec la clé du catalogue ; un corps illisible est une
// mauvaise requête ; rien n'est écrit.
func TestAPI_UnitUpdatePolicy_RefusesWithTheKey(t *testing.T) {
	server := newTestServer(t)
	server.enroll(t, "vps-paris-1", remoteID)
	server.seedDirectory(t)
	proxy, err := server.db.ListDeclaredServices(context.Background(), directory.Filter{MachineID: machine.LocalID})
	if err != nil || len(proxy) != 1 {
		t.Fatalf("proxy = %+v, %v", proxy, err)
	}
	binaryUnit := proxy[0].Units[0].ID

	for name, policy := range map[string]string{"hors liste": "pinned", "vide": "", "majuscules": "HOLD"} {
		got := callAPI(server.Server, http.MethodPut, "/api/directory/units/"+binaryUnit+"/update-policy", unitPolicyRequest{Policy: policy})
		if got.Code != http.StatusUnprocessableEntity || errorCode(t, got) != "directory.update_policy_invalid" {
			t.Errorf("%s: %d %s", name, got.Code, got.Body.String())
		}
	}
	// Un binaire n'a pas d'image à interroger : hold n'a pas de sens.
	got := callAPI(server.Server, http.MethodPut, "/api/directory/units/"+binaryUnit+"/update-policy", unitPolicyRequest{Policy: "hold"})
	if got.Code != http.StatusUnprocessableEntity || errorCode(t, got) != "directory.update_policy_invalid" {
		t.Fatalf("binaire: %d %s", got.Code, got.Body.String())
	}
	if got := callAPI(server.Server, http.MethodPut, "/api/directory/units/"+binaryUnit+"/update-policy", map[string]any{"policy": "watch", "autre": 1}); got.Code != http.StatusBadRequest {
		t.Fatalf("champ inconnu: %d", got.Code)
	}
	got = callAPI(server.Server, http.MethodPut, "/api/directory/units/unit_inconnue/update-policy", unitPolicyRequest{Policy: "hold"})
	if got.Code != http.StatusNotFound || errorCode(t, got) != "directory.unit_not_found" {
		t.Fatalf("unité inconnue: %d %s", got.Code, got.Body.String())
	}
	if got := callAPI(server.Server, http.MethodPut, "/api/directory/units/"+binaryUnit+"/update-policy", unitPolicyRequest{Policy: "watch"}); got.Code != http.StatusNoContent {
		t.Fatalf("watch sur un binaire: %d %s", got.Code, got.Body.String())
	}
}
