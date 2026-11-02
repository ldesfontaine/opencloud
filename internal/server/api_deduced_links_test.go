package server

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/ldesfontaine/opencloud/internal/directory"
	"github.com/ldesfontaine/opencloud/internal/machine"
)

// Un proxy qui fournit la capacité proxy (machine openCloud), une application
// avec une adresse publique et une sans (machine distante).
func (ts *testServer) seedPublicServices(t *testing.T) (proxy, blog, internal string) {
	t.Helper()
	ctx := context.Background()
	declare := func(definition directory.ServiceDefinition) string {
		created, err := ts.directory.CreateService(ctx, definition)
		if err != nil {
			t.Fatal(err)
		}
		return created.ID
	}
	app := func(name, publicURL string) directory.ServiceDefinition {
		return directory.ServiceDefinition{
			MachineID: remoteID, Name: name, Nature: directory.NatureApplication, Origin: directory.OriginContainer, PublicURL: publicURL,
			Units: []directory.UnitDefinition{{Name: name, ContainerName: name, Image: "ghost"}},
		}
	}
	proxy = declare(directory.ServiceDefinition{
		MachineID: machine.LocalID, Name: "caddy", Nature: directory.NatureInfra, Origin: directory.OriginBinary,
		Provides: []directory.Capability{directory.CapabilityProxy},
		Units:    []directory.UnitDefinition{{Name: "caddy", SystemdUnit: "caddy.service", Ports: []string{"443"}}},
	})
	blog = declare(app("blog", "https://blog.example.test"))
	internal = declare(app("wiki", ""))
	return proxy, blog, internal
}

func linkByState(links []serviceLinkJSON, wantDismissed bool) []serviceLinkJSON {
	var found []serviceLinkJSON
	for _, item := range links {
		if item.Dismissed == wantDismissed {
			found = append(found, item)
		}
	}
	return found
}

// public_url + capacité proxy : une route déduite, vue des deux côtés, sans
// rien écrire. Un service sans adresse publique n'en a pas.
func TestAPI_DeducedLinks_PublicURLAndProxyGiveARouteWrittenNowhere(t *testing.T) {
	server := newTestServer(t)
	server.enroll(t, "vps-paris-1", remoteID)
	proxy, blog, internal := server.seedPublicServices(t)

	out := serviceLinksOf(t, server, blog)
	if len(out.Links) != 1 {
		t.Fatalf("liens du blog : %+v", out.Links)
	}
	got := out.Links[0]
	if got.Direction != "incoming" || got.Nature != "route" || got.Proof != "deduced" || got.Signal != "public_url" || got.Removable || got.Stale || got.Dismissed || got.Peer.ServiceID != proxy {
		t.Errorf("route déduite = %+v", got)
	}
	if again := serviceLinksOf(t, server, blog); again.Links[0].ID != got.ID {
		t.Errorf("l'identifiant d'une déduction doit être stable : %s puis %s", got.ID, again.Links[0].ID)
	}
	if out := serviceLinksOf(t, server, proxy); len(out.Links) != 1 || out.Links[0].Direction != "outgoing" {
		t.Errorf("liens du proxy : %+v", out.Links)
	}
	if out := serviceLinksOf(t, server, internal); len(out.Links) != 0 {
		t.Errorf("un service sans adresse publique n'a pas de route : %+v", out.Links)
	}
	stored, err := server.db.ListLinks(context.Background(), "")
	if err != nil || len(stored) != 0 {
		t.Errorf("une déduction ne s'écrit pas : %+v %v", stored, err)
	}
	if links := networkMapOf(t, server).Links; len(links) != 1 || links[0].Proof != "deduced" || !links[0].Proposed || links[0].Nature != "route" {
		t.Errorf("la carte trace la route déduite, à confirmer : %+v", links)
	}
}

// Confirmer écrit un lien déclaré qui est le sien : il se retire, la déduction
// ne le double pas.
func TestAPI_DeducedLinks_ConfirmWritesADeclaredRoute(t *testing.T) {
	server := newTestServer(t)
	server.enroll(t, "vps-paris-1", remoteID)
	_, blog, _ := server.seedPublicServices(t)
	deduced := serviceLinksOf(t, server, blog).Links[0]

	if got := callAPI(server.Server, http.MethodPost, "/api/directory/links/"+deduced.ID+"/actions/confirm", nil); got.Code != http.StatusNoContent {
		t.Fatalf("confirmer : %d %s", got.Code, got.Body.String())
	}
	out := serviceLinksOf(t, server, blog).Links
	if len(out) != 1 {
		t.Fatalf("liens après confirmation : %+v", out)
	}
	if out[0].Proof != "declared" || out[0].Nature != "route" || out[0].Signal != "operator" || !out[0].Removable || out[0].Dismissed {
		t.Errorf("route confirmée = %+v", out[0])
	}
	if links := networkMapOf(t, server).Links; len(links) != 1 || links[0].Proof != "declared" || links[0].Proposed {
		t.Errorf("la carte trace la route confirmée : %+v", links)
	}
	// L'identifiant déduit n'existe plus : une seconde confirmation n'a rien à confirmer.
	if got := callAPI(server.Server, http.MethodPost, "/api/directory/links/"+deduced.ID+"/actions/confirm", nil); got.Code != http.StatusNotFound {
		t.Errorf("confirmer deux fois : %d", got.Code)
	}
}

// Le test de la règle : un lien écarté ne revient pas par surprise. Ni un
// relevé de plus, ni une modification du service, ni la même déduction
// refaite ne le ramènent ; seul « Rétablir » le fait.
func TestAPI_DeducedLinks_ADismissedRouteNeverComesBackBySurprise(t *testing.T) {
	server := newTestServer(t)
	server.enroll(t, "vps-paris-1", remoteID)
	server.seedContainers(t)
	proxy, blog, _ := server.seedPublicServices(t)
	deduced := serviceLinksOf(t, server, blog).Links[0]

	if got := callAPI(server.Server, http.MethodPost, "/api/directory/links/"+deduced.ID+"/actions/dismiss", nil); got.Code != http.StatusNoContent {
		t.Fatalf("écarter : %d %s", got.Code, got.Body.String())
	}
	assertStillDismissed := func(when string) {
		t.Helper()
		for _, id := range []string{blog, proxy} {
			links := serviceLinksOf(t, server, id).Links
			if proposed := linkByState(links, false); len(proposed) != 0 {
				t.Errorf("%s : la route revient côté %s : %+v", when, id, proposed)
			}
			if kept := linkByState(links, true); len(kept) != 1 || kept[0].Removable || kept[0].Nature != "route" || kept[0].Signal != "public_url" {
				t.Errorf("%s : la route écartée reste notée côté %s : %+v", when, id, kept)
			}
		}
		for _, drawn := range networkMapOf(t, server).Links {
			if drawn.Nature == "route" {
				t.Errorf("%s : la carte trace une route écartée : %+v", when, drawn)
			}
		}
	}
	assertStillDismissed("tout de suite")

	server.seedContainers(t)
	assertStillDismissed("après un relevé")

	// L'adresse change, le service est modifié : la paire est la même, le refus tient.
	ctx := context.Background()
	if _, err := server.directory.UpdateService(ctx, blog, directory.ServiceDefinition{
		MachineID: remoteID, Name: "blog", Nature: directory.NatureApplication, Origin: directory.OriginContainer, PublicURL: "https://blog.autre.example.test",
		Units: []directory.UnitDefinition{{Name: "blog", ContainerName: "blog", Image: "ghost"}},
	}); err != nil {
		t.Fatal(err)
	}
	assertStillDismissed("après une modification du service")

	// Une route écartée n'est ni retirable ni confirmable.
	kept := linkByState(serviceLinksOf(t, server, blog).Links, true)[0]
	if got := callAPI(server.Server, http.MethodDelete, "/api/directory/links/"+kept.ID, nil); got.Code != http.StatusNotFound {
		t.Errorf("retirer une route écartée : %d", got.Code)
	}
	if got := callAPI(server.Server, http.MethodPost, "/api/directory/links/"+deduced.ID+"/actions/confirm", nil); got.Code != http.StatusNotFound {
		t.Errorf("confirmer une route écartée : %d", got.Code)
	}

	// Rétablir la rend à la déduction, et rien de plus.
	if got := callAPI(server.Server, http.MethodPost, "/api/directory/links/"+kept.ID+"/actions/restore", nil); got.Code != http.StatusNoContent {
		t.Fatalf("rétablir : %d %s", got.Code, got.Body.String())
	}
	links := serviceLinksOf(t, server, blog).Links
	if len(links) != 1 || links[0].Proof != "deduced" || links[0].Dismissed || links[0].ID != deduced.ID {
		t.Errorf("après rétablissement : %+v", links)
	}
	if got := callAPI(server.Server, http.MethodPost, "/api/directory/links/"+kept.ID+"/actions/restore", nil); got.Code != http.StatusNotFound {
		t.Errorf("rétablir deux fois : %d", got.Code)
	}
}

// Une adresse publique retirée ou un proxy disparu : la déduction s'éteint, et
// ce qu'on avait écarté ne compte plus pour rien.
func TestAPI_DeducedLinks_NoPublicURLOrNoProxyMeansNoRoute(t *testing.T) {
	server := newTestServer(t)
	server.enroll(t, "vps-paris-1", remoteID)
	proxy, blog, _ := server.seedPublicServices(t)
	id := serviceLinksOf(t, server, blog).Links[0].ID

	if got := callAPI(server.Server, http.MethodDelete, "/api/directory/services/"+proxy, nil); got.Code != http.StatusNoContent {
		t.Fatalf("retrait du proxy : %d %s", got.Code, got.Body.String())
	}
	if links := serviceLinksOf(t, server, blog).Links; len(links) != 0 {
		t.Errorf("sans proxy : %+v", links)
	}
	if got := callAPI(server.Server, http.MethodPost, "/api/directory/links/"+id+"/actions/dismiss", nil); got.Code != http.StatusNotFound {
		t.Errorf("écarter une déduction éteinte : %d", got.Code)
	}
}

// La déduction lit l'adresse publique mais n'en recopie rien : un secret semé
// dans l'adresse (identifiant, jeton en paramètre) n'arrive dans aucun lien,
// stocké, déduit, écarté ou confirmé, ni dans une réponse qui les lit.
func TestAPI_DeducedLinks_SeededSecretInThePublicURL_NeverInAnyOutput(t *testing.T) {
	const secret = "hunter2-SECRET-VALUE"
	server := newTestServer(t)
	server.enroll(t, "vps-paris-1", remoteID)
	ctx := context.Background()
	proxy, err := server.directory.CreateService(ctx, directory.ServiceDefinition{
		MachineID: machine.LocalID, Name: "caddy", Nature: directory.NatureInfra, Origin: directory.OriginBinary,
		Provides: []directory.Capability{directory.CapabilityProxy},
		Units:    []directory.UnitDefinition{{Name: "caddy", SystemdUnit: "caddy.service"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	blog, err := server.directory.CreateService(ctx, directory.ServiceDefinition{
		MachineID: remoteID, Name: "blog", Nature: directory.NatureApplication, Origin: directory.OriginContainer,
		PublicURL: "https://admin:" + secret + "@blog.example.test/?token=" + secret,
		Units:     []directory.UnitDefinition{{Name: "blog", ContainerName: "blog", Image: "ghost"}},
	})
	if err != nil {
		t.Fatal(err)
	}

	read := func() []string {
		var outputs []string
		for _, path := range []string{"/api/directory/services/" + blog.ID + "/links", "/api/directory/services/" + proxy.ID + "/links", "/api/network", "/api/machines/" + remoteID + "/network"} {
			got := callAPI(server.Server, http.MethodGet, path, nil)
			if got.Code != http.StatusOK {
				t.Fatalf("%s : %d", path, got.Code)
			}
			outputs = append(outputs, got.Body.String())
		}
		stored, err := server.db.ListLinks(ctx, "")
		if err != nil {
			t.Fatal(err)
		}
		dump, _ := json.Marshal(stored)
		return append(outputs, string(dump))
	}
	check := func(when string) {
		t.Helper()
		for _, output := range read() {
			if strings.Contains(output, secret) {
				t.Errorf("%s : le secret semé est dans une sortie : %.200s", when, output)
			}
		}
	}
	check("déduit")
	id := serviceLinksOf(t, server, blog.ID).Links[0].ID
	if got := callAPI(server.Server, http.MethodPost, "/api/directory/links/"+id+"/actions/confirm", nil); got.Code != http.StatusNoContent {
		t.Fatalf("confirmer : %d", got.Code)
	}
	check("confirmé")
}
