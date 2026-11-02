package server

import (
	"context"
	"net/http"
	"path/filepath"
	"testing"

	"github.com/ldesfontaine/opencloud/internal/directory"
	"github.com/ldesfontaine/opencloud/internal/machine"
)

// Trois services : l'application et sa base (un service Compose chacun, dans
// le projet des conteneurs de la planche), et un proxy en binaire sur la
// machine openCloud. Rend leurs identifiants.
func (ts *testServer) seedLinkedServices(t *testing.T) (app, db, proxy string) {
	t.Helper()
	ctx := context.Background()
	declare := func(definition directory.ServiceDefinition) string {
		created, err := ts.directory.CreateService(ctx, definition)
		if err != nil {
			t.Fatal(err)
		}
		return created.ID
	}
	compose := func(name, unit string) directory.ServiceDefinition {
		return directory.ServiceDefinition{
			MachineID: remoteID, Name: name, Nature: directory.NatureApplication, Origin: directory.OriginCompose, ComposeProject: "nextcloud",
			Units: []directory.UnitDefinition{{Name: unit, Image: "nextcloud"}},
		}
	}
	app, db = declare(compose("cloud", "nextcloud")), declare(compose("cloud-db", "nextcloud-db"))
	proxy = declare(directory.ServiceDefinition{
		MachineID: machine.LocalID, Name: "caddy", Nature: directory.NatureInfra, Origin: directory.OriginBinary,
		Units: []directory.UnitDefinition{{Name: "caddy", SystemdUnit: "caddy.service", Ports: []string{"443"}}},
	})
	return app, db, proxy
}

func serviceLinksOf(t *testing.T, ts *testServer, serviceID string) serviceLinksResponse {
	t.Helper()
	var response serviceLinksResponse
	decodeAPI(t, callAPI(ts.Server, http.MethodGet, "/api/directory/services/"+serviceID+"/links", nil), &response)
	return response
}

// depends_on devient un lien déclaré par l'outil, lisible des deux côtés avec
// sa preuve, son signal et le service déclaré qui porte chaque bout.
func TestAPI_ServiceLinks_ComposeDependsOnIsStoredAsADeclaredOrderLink(t *testing.T) {
	server := newTestServer(t)
	server.enroll(t, "vps-paris-1", remoteID)
	server.seedContainers(t)
	app, db, _ := server.seedLinkedServices(t)

	out := serviceLinksOf(t, server, app)
	if len(out.Links) != 1 {
		t.Fatalf("liens de l'application : %+v", out.Links)
	}
	got := out.Links[0]
	if got.Direction != "outgoing" || got.Nature != "order" || got.Proof != "declared" || got.Signal != "depends_on" || got.Stale || got.Removable {
		t.Errorf("lien = %+v", got)
	}
	if got.Peer.ServiceID != db || got.Peer.ServiceName != "cloud-db" || got.Peer.MachineName != "vps-paris-1" || got.Peer.ContainerID == "" || got.Self.ServiceID != app {
		t.Errorf("extrémités = %+v / %+v", got.Self, got.Peer)
	}
	in := serviceLinksOf(t, server, db)
	if len(in.Links) != 1 || in.Links[0].Direction != "incoming" || in.Links[0].Peer.ServiceID != app || in.Links[0].ID != got.ID {
		t.Errorf("liens de la base : %+v", in.Links)
	}
	compareGolden(t, filepath.Join("testdata", "service_links.golden.json"), normalize(callAPI(server.Server, http.MethodGet, "/api/directory/services/"+app+"/links", nil).Body.String()))

	// Un rapport de plus ne change rien : le lien est revu, pas doublé.
	server.seedContainers(t)
	if again := serviceLinksOf(t, server, app); len(again.Links) != 1 || again.Links[0].ID != got.ID {
		t.Errorf("après un second rapport : %+v", again.Links)
	}
}

func TestAPI_ServiceLinks_TheOperatorAddsAndRemovesHisOwnAndNeverAToolOne(t *testing.T) {
	server := newTestServer(t)
	server.enroll(t, "vps-paris-1", remoteID)
	server.seedContainers(t)
	app, db, proxy := server.seedLinkedServices(t)

	// Le proxy (sur la machine openCloud) utilise l'application (sur l'autre) : un lien entre machines.
	created := callAPI(server.Server, http.MethodPost, "/api/directory/links", linkRequest{FromServiceID: proxy, ToServiceID: app, Port: 8080})
	if created.Code != http.StatusNoContent {
		t.Fatalf("création : %d %s", created.Code, created.Body.String())
	}
	proxyLinks := serviceLinksOf(t, server, proxy)
	if len(proxyLinks.Links) != 1 {
		t.Fatalf("liens du proxy : %+v", proxyLinks.Links)
	}
	mine := proxyLinks.Links[0]
	if mine.Direction != "outgoing" || mine.Nature != "config" || mine.Proof != "declared" || mine.Signal != "operator" || mine.Port != 8080 || !mine.Removable || mine.Peer.ServiceID != app {
		t.Errorf("lien de l'opérateur = %+v", mine)
	}
	if appLinks := serviceLinksOf(t, server, app); len(appLinks.Links) != 2 {
		t.Errorf("l'application voit son lien d'ordre et celui du proxy : %+v", appLinks.Links)
	}

	var overview overviewNetworkResponse
	decodeAPI(t, callAPI(server.Server, http.MethodGet, "/api/network", nil), &overview)
	if len(overview.Links) != 1 || overview.Links[0].ID != mine.ID || overview.Links[0].To.ContainerID == "" || overview.Links[0].From.MachineName == "" {
		t.Errorf("la carte lit les liens entre machines : %+v", overview.Links)
	}

	refusals := map[string]struct {
		request linkRequest
		status  int
		key     string
	}{
		"doublon":         {linkRequest{FromServiceID: proxy, ToServiceID: app, Port: 9}, http.StatusConflict, "link.duplicate"},
		"soi-même":        {linkRequest{FromServiceID: app, ToServiceID: app}, http.StatusUnprocessableEntity, "link.self"},
		"service inconnu": {linkRequest{FromServiceID: app, ToServiceID: "svc_0000000000000000"}, http.StatusUnprocessableEntity, "link.service_invalid"},
		"port faux":       {linkRequest{FromServiceID: app, ToServiceID: db, Port: 70000}, http.StatusUnprocessableEntity, "link.invalid_port"},
	}
	for name, tc := range refusals {
		got := callAPI(server.Server, http.MethodPost, "/api/directory/links", tc.request)
		if got.Code != tc.status || errorCode(t, got) != tc.key {
			t.Errorf("%s : %d %s", name, got.Code, got.Body.String())
		}
	}

	// Le lien d'ordre vient de Compose : il ne se retire pas ici.
	toolLink := serviceLinksOf(t, server, db).Links[0]
	if got := callAPI(server.Server, http.MethodDelete, "/api/directory/links/"+toolLink.ID, nil); got.Code != http.StatusConflict || errorCode(t, got) != "link.not_removable" {
		t.Errorf("retrait d'un lien d'outil : %d %s", got.Code, got.Body.String())
	}
	if got := callAPI(server.Server, http.MethodDelete, "/api/directory/links/"+mine.ID, nil); got.Code != http.StatusNoContent {
		t.Fatalf("retrait : %d %s", got.Code, got.Body.String())
	}
	if got := callAPI(server.Server, http.MethodDelete, "/api/directory/links/"+mine.ID, nil); got.Code != http.StatusNotFound {
		t.Errorf("retrait deux fois : %d", got.Code)
	}
	if left := serviceLinksOf(t, server, proxy); len(left.Links) != 0 {
		t.Errorf("liens restants du proxy : %+v", left.Links)
	}
	if got := callAPI(server.Server, http.MethodGet, "/api/directory/services/svc_0000000000000000/links", nil); got.Code != http.StatusNotFound {
		t.Errorf("service inconnu : %d", got.Code)
	}
}

// Un service à plusieurs unités ne se lie pas sans dire laquelle.
func TestAPI_ServiceLinks_AServiceWithSeveralUnitsNeedsTheUnit(t *testing.T) {
	server := newTestServer(t)
	server.enroll(t, "vps-paris-1", remoteID)
	_, _, proxy := server.seedLinkedServices(t)
	var cloud declaredServiceJSON
	decodeAPI(t, callAPI(server.Server, http.MethodPost, "/api/directory/services", nextcloudRequest(remoteID)), &cloud)
	if len(cloud.Units) != 2 {
		t.Fatalf("unités : %+v", cloud.Units)
	}

	got := callAPI(server.Server, http.MethodPost, "/api/directory/links", linkRequest{FromServiceID: proxy, ToServiceID: cloud.ID})
	if got.Code != http.StatusUnprocessableEntity || errorCode(t, got) != "link.unit_required" {
		t.Fatalf("sans unité : %d %s", got.Code, got.Body.String())
	}
	got = callAPI(server.Server, http.MethodPost, "/api/directory/links", linkRequest{FromServiceID: proxy, ToServiceID: cloud.ID, ToUnitID: cloud.Units[0].ID})
	if got.Code != http.StatusNoContent {
		t.Fatalf("avec l'unité : %d %s", got.Code, got.Body.String())
	}
	got = callAPI(server.Server, http.MethodPost, "/api/directory/links", linkRequest{FromServiceID: proxy, ToServiceID: cloud.ID, ToUnitID: "unit_0000000000000000"})
	if got.Code != http.StatusUnprocessableEntity || errorCode(t, got) != "directory.unit_invalid" {
		t.Errorf("unité d'un autre service : %d %s", got.Code, got.Body.String())
	}
}
