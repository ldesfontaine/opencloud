package directory

import (
	"testing"
	"time"

	"github.com/ldesfontaine/opencloud/internal/service"
)

// Un service Compose déclaré sur une machine, une unité par nom donné.
func declaredCompose(id, machineID, project string, unitNames ...string) Service {
	declared := Service{ID: id, MachineID: machineID, Name: project, Origin: OriginCompose, ComposeProject: project, Nature: NatureApplication}
	for _, name := range unitNames {
		declared.Units = append(declared.Units, Unit{ID: "unit_" + project + "_" + name, ServiceID: id, Name: name, Kind: UnitContainer, Image: name, Version: "1"})
	}
	return declared
}

// Un conteneur seul déclaré, sous le nom que Docker lui donne.
func declaredContainer(id, machineID, containerName string) Service {
	return Service{
		ID: id, MachineID: machineID, Name: containerName, Origin: OriginContainer, Nature: NatureApplication,
		Units: []Unit{{ID: "unit_" + containerName, ServiceID: id, Name: containerName, Kind: UnitContainer, Image: "traefik/whoami", Version: "v1", ContainerName: containerName}},
	}
}

func declaredBinary(id, machineID, systemdUnit string) Service {
	return Service{
		ID: id, MachineID: machineID, Name: systemdUnit[:len(systemdUnit)-len(".service")], Origin: OriginBinary, Nature: NatureInfra,
		Units: []Unit{{ID: "unit_" + systemdUnit, ServiceID: id, Name: "main", Kind: UnitBinary, Version: "1", SystemdUnit: systemdUnit}},
	}
}

// Un conteneur Compose vivant : fiche, projet, service dans le projet.
func composeContainer(machineID, name, project, composeService string) service.Service {
	return service.Service{
		ID: service.ID(machineID, name), MachineID: machineID, Kind: service.KindContainer, Name: name, Group: project,
		ComposeService: composeService, Image: composeService + ":1", State: service.StateRunning,
	}
}

func aloneContainer(machineID, name string) service.Service {
	return service.Service{
		ID: service.ID(machineID, name), MachineID: machineID, Kind: service.KindContainer, Name: name,
		Image: "traefik/whoami:v1", State: service.StateRunning,
	}
}

func TestMatch_ComposeKeyTakesTheProjectUnit(t *testing.T) {
	declared := []Service{declaredCompose("svc_1", "m1", "nextcloud", "app", "db")}
	app := composeContainer("m1", "nextcloud-app-1", "nextcloud", "app")
	// Un autre projet qui a le même service, et un service du projet qui n'est pas déclaré.
	other := composeContainer("m1", "blog-app-1", "blog", "app")
	cache := composeContainer("m1", "nextcloud-redis-1", "nextcloud", "redis")

	matches := Match(declared, []service.Service{app, other, cache})

	if got := matches[app.ID]; got.UnitID != "unit_nextcloud_app" || got.Level != service.MatchCompose {
		t.Fatalf("app = %+v", got)
	}
	if _, matched := matches[other.ID]; matched {
		t.Fatalf("un autre projet ne se rattache pas: %+v", matches[other.ID])
	}
	if _, matched := matches[cache.ID]; matched {
		t.Fatalf("un service du projet qui n'est pas déclaré reste non déclaré: %+v", matches[cache.ID])
	}
}

func TestMatch_ContainerNameIsWeak(t *testing.T) {
	declared := []Service{declaredContainer("svc_1", "m1", "whoami")}
	whoami := aloneContainer("m1", "whoami")

	got := Match(declared, []service.Service{whoami})[whoami.ID]

	if got.UnitID != "unit_whoami" || got.Level != service.MatchName || !got.Level.IsWeak() {
		t.Fatalf("whoami = %+v", got)
	}
	if service.MatchCompose.IsWeak() {
		t.Fatal("le niveau compose n'est pas faible")
	}
}

// Un conteneur Compose dont le nom est aussi déclaré par un service
// « conteneur seul » : le projet gagne, l'unité « conteneur » n'a plus rien.
func TestMatch_ComposeBeatsName(t *testing.T) {
	declared := []Service{
		declaredCompose("svc_1", "m1", "nextcloud", "app"),
		declaredContainer("svc_2", "m1", "nextcloud-app-1"),
	}
	app := composeContainer("m1", "nextcloud-app-1", "nextcloud", "app")

	got := Match(declared, []service.Service{app})[app.ID]

	if got.UnitID != "unit_nextcloud_app" || got.Level != service.MatchCompose {
		t.Fatalf("app = %+v", got)
	}
}

func TestMatch_ReplicasShareTheUnit(t *testing.T) {
	declared := []Service{declaredCompose("svc_1", "m1", "web", "app")}
	first := composeContainer("m1", "web-app-1", "web", "app")
	second := composeContainer("m1", "web-app-2", "web", "app")

	matches := Match(declared, []service.Service{first, second})

	if matches[first.ID].UnitID != "unit_web_app" || matches[second.ID].UnitID != "unit_web_app" {
		t.Fatalf("matches = %+v", matches)
	}
}

// La clé est le projet : renommé, il ne retrouve plus rien, sans rattrapage.
func TestMatch_RenamedProjectLosesTheUnit(t *testing.T) {
	declared := []Service{declaredCompose("svc_1", "m1", "nextcloud", "app")}
	before := composeContainer("m1", "nextcloud-app-1", "nextcloud", "app")
	renamed := composeContainer("m1", "cloud-app-1", "cloud", "app")

	if _, matched := Match(declared, []service.Service{before})[before.ID]; !matched {
		t.Fatal("le projet déclaré doit se rattacher")
	}
	if got, matched := Match(declared, []service.Service{renamed})[renamed.ID]; matched {
		t.Fatalf("le projet renommé ne se rattache pas: %+v", got)
	}
}

func TestMatch_OtherMachineArchivedAndBinaryNeverMatch(t *testing.T) {
	declared := []Service{
		declaredCompose("svc_1", "m1", "nextcloud", "app"),
		declaredContainer("svc_2", "m1", "whoami"),
		declaredBinary("svc_3", "m1", "caddy.service"),
	}
	elsewhere := composeContainer("m2", "nextcloud-app-1", "nextcloud", "app")
	gone := composeContainer("m1", "nextcloud-app-9", "nextcloud", "app")
	gone.ArchivedAt = time.Unix(1_800_000_000, 0)
	// Le nom d'une unité binaire n'est pas celui d'un conteneur.
	caddy := aloneContainer("m1", "main")

	matches := Match(declared, []service.Service{elsewhere, gone, caddy})

	if len(matches) != 0 {
		t.Fatalf("matches = %+v", matches)
	}
}

// Par prudence, si deux services se disputaient une clé, le plus petit
// identifiant gagnerait : la réponse ne dépend pas de l'ordre de lecture.
func TestMatch_AnAmbiguousKeyGoesToTheSmallestServiceID(t *testing.T) {
	declared := []Service{
		declaredCompose("svc_2", "m1", "web", "app"),
		declaredCompose("svc_1", "m1", "web", "app"),
	}
	app := composeContainer("m1", "web-app-1", "web", "app")

	got := Match(declared, []service.Service{app})[app.ID]

	if got.UnitID != "unit_web_app" {
		t.Fatalf("app = %+v", got)
	}
	declared[0].Units[0].ID, declared[1].Units[0].ID = "unit_from_2", "unit_from_1"
	if got := Match(declared, []service.Service{app})[app.ID]; got.UnitID != "unit_from_1" {
		t.Fatalf("le service svc_1 doit gagner: %+v", got)
	}
}
