package directory

import (
	"testing"

	"github.com/ldesfontaine/opencloud/internal/link"
)

func routeService(id, machineID, name, publicURL string, provides ...Capability) Service {
	return Service{
		ID: id, MachineID: machineID, Name: name, Origin: OriginContainer, PublicURL: publicURL, Provides: provides,
		Units: []Unit{{ID: "u_" + id, Name: name, ContainerName: name}},
	}
}

func TestDeduceRoutes_AProxyReachesEveryServiceWithAPublicAddress(t *testing.T) {
	services := []Service{
		routeService("p", "m1", "caddy", "", CapabilityProxy),
		routeService("a", "m2", "blog", "https://blog.example.test"),
		routeService("b", "m2", "wiki", ""),
		routeService("c", "m1", "caddy-admin", "https://admin.example.test", CapabilityProxy),
	}
	deduced := DeduceRoutes(services, nil)
	// Deux proxys, trois paires : caddy → blog, caddy-admin → blog, caddy → caddy-admin ; jamais vers soi.
	if len(deduced) != 3 {
		t.Fatalf("routes = %+v", deduced)
	}
	for _, found := range deduced {
		if found.From == found.To || found.Nature != link.NatureRoute || found.Proof != link.ProofDeduced || found.Signal != link.SignalPublicURL {
			t.Errorf("route mal formée : %+v", found)
		}
		if found.To.Key == link.ContainerKey("wiki") {
			t.Errorf("un service sans adresse publique n'est pas joint : %+v", found)
		}
	}
	if again := DeduceRoutes(services, nil); again[0].ID != deduced[0].ID || again[2].ID != deduced[2].ID {
		t.Error("l'identifiant et l'ordre d'une déduction sont stables")
	}
}

func TestDeduceRoutes_ADecidedPairIsNeverProposedAgain(t *testing.T) {
	services := []Service{routeService("p", "m1", "caddy", "", CapabilityProxy), routeService("a", "m2", "blog", "https://blog.example.test")}
	pair := DeduceRoutes(services, nil)[0]
	decided := knownRoutes([]link.Link{}, []link.Link{{From: pair.From, To: pair.To, Nature: link.NatureRoute, Proof: link.ProofDeduced}})
	if got := DeduceRoutes(services, decided); len(got) != 0 {
		t.Errorf("une paire écartée revient : %+v", got)
	}
	// Un lien d'une autre nature entre les mêmes instances ne décide pas de la route.
	other := knownRoutes([]link.Link{{From: pair.From, To: pair.To, Nature: link.NatureConfig, Proof: link.ProofDeclared}}, nil)
	if got := DeduceRoutes(services, other); len(got) != 1 {
		t.Errorf("un lien de configuration n'écarte pas la route : %+v", got)
	}
}

func TestDeduceRoutes_NeedsAProxyAndAKeyOnBothSides(t *testing.T) {
	noKey := Service{ID: "k", MachineID: "m1", Name: "ghost", Origin: OriginContainer, PublicURL: "https://x.example.test", Units: []Unit{{ID: "u", Name: "ghost"}}}
	if got := DeduceRoutes([]Service{routeService("a", "m2", "blog", "https://blog.example.test")}, nil); len(got) != 0 {
		t.Errorf("sans proxy : %+v", got)
	}
	if got := DeduceRoutes([]Service{routeService("p", "m1", "caddy", "", CapabilityProxy), noKey}, nil); len(got) != 0 {
		t.Errorf("une unité sans clé ne se lie pas : %+v", got)
	}
}
