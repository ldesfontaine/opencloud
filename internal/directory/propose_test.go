package directory

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/ldesfontaine/opencloud/internal/container"
)

func TestPropose_ComposeProposesTheWholeProject(t *testing.T) {
	web := composeContainer("m1", "blog-web-1", "blog", "web")
	web.Image = "ghcr.io/org/blog:2.1"
	web.Ports = []container.Port{
		{IP: "0.0.0.0", HostPort: 8080, ContainerPort: 80, Protocol: "tcp"},
		{IP: "::", HostPort: 8080, ContainerPort: 80, Protocol: "tcp"},
		{IP: "127.0.0.1", HostPort: 5353, ContainerPort: 53, Protocol: "udp"},
	}
	db := composeContainer("m1", "blog-db-1", "blog", "db")
	db.Image = "postgres:16.4"
	replica := composeContainer("m1", "blog-web-2", "blog", "web")
	other := composeContainer("m1", "wiki-web-1", "wiki", "web")
	elsewhere := composeContainer("m2", "blog-cache-1", "blog", "cache")
	gone := composeContainer("m1", "blog-old-1", "blog", "old")
	gone.ArchivedAt = replica.CreatedAt.AddDate(0, 0, 1)

	proposal, err := Propose(web, []container.Container{web, db, replica, other, elsewhere, gone}, nil)
	if err != nil {
		t.Fatal(err)
	}

	definition := proposal.Definition
	if proposal.ServiceID != "" || definition.Origin != OriginCompose || definition.ComposeProject != "blog" || definition.Name != "blog" || definition.Nature != NatureApplication || definition.MachineID != "m1" {
		t.Fatalf("definition = %+v", definition)
	}
	if len(definition.Units) != 2 || definition.Units[0].Name != "db" || definition.Units[1].Name != "web" {
		t.Fatalf("une unité par service Compose, par nom: %+v", definition.Units)
	}
	unit := definition.Units[1]
	if unit.Image != "ghcr.io/org/blog" || unit.Version != "2.1" || unit.Digest != "" || unit.ID != "" {
		t.Fatalf("web = %+v", unit)
	}
	if !slices.Equal(unit.Ports, []string{"8080:80", "5353:53/udp"}) {
		t.Fatalf("les ports IPv4 et IPv6 ne font qu'une ligne: %v", unit.Ports)
	}
}

func TestPropose_DeclaredProjectAddsMissingUnits(t *testing.T) {
	declared := declaredCompose("svc_1", "m1", "nextcloud", "app")
	declared.Title, declared.ProjectIDs = "Nextcloud", []string{"prj_1"}
	app := attached(composeContainer("m1", "nextcloud-app-1", "nextcloud", "app"), "unit_nextcloud_app", container.MatchCompose)
	db := composeContainer("m1", "nextcloud-db-1", "nextcloud", "db")
	db.Image = "postgres:16.4"

	proposal, err := Propose(db, []container.Container{app, db}, []Service{declared})
	if err != nil {
		t.Fatal(err)
	}

	if proposal.ServiceID != "svc_1" || proposal.Definition.Name != "nextcloud" || proposal.Definition.Title != "Nextcloud" || !slices.Equal(proposal.Definition.ProjectIDs, []string{"prj_1"}) {
		t.Fatalf("proposal = %+v", proposal)
	}
	units := proposal.Definition.Units
	if len(units) != 2 {
		t.Fatalf("units = %+v", units)
	}
	// L'unité déclarée garde son identifiant, l'ajoutée n'en a pas : l'édition
	// refuserait un identifiant qui n'est pas celui d'une unité du service.
	if units[0].ID != "unit_nextcloud_app" || units[1].ID != "" || units[1].Name != "db" || units[1].Image != "postgres" || units[1].Version != "16.4" {
		t.Fatalf("units = %+v", units)
	}
}

func TestPropose_LatestLeavesVersionEmpty(t *testing.T) {
	cases := map[string]struct{ image, version, digest string }{
		"nginx:latest":            {"nginx", "", ""},
		"nginx":                   {"nginx", "", ""},
		"nginx:1.27":              {"nginx", "1.27", ""},
		"nginx@" + digestA:        {"nginx", "", digestA},
		"ghcr.io/org/app:v1":      {"ghcr.io/org/app", "v1", ""},
		"localhost:5000/app:2":    {"localhost:5000/app", "2", ""},
		"docker.io/library/redis": {"redis", "", ""},
	}
	for image, want := range cases {
		t.Run(image, func(t *testing.T) {
			item := aloneContainer("m1", "front")
			item.Image = image
			// Une empreinte lue après coup n'est jamais proposée.
			proposal, err := Propose(item, nil, nil)
			if err != nil {
				t.Fatal(err)
			}
			unit := proposal.Definition.Units[0]
			if unit.Image != want.image || unit.Version != want.version || unit.Digest != want.digest {
				t.Fatalf("unit = %+v, want %+v", unit, want)
			}
		})
	}
}

func TestPropose_AloneContainerDeclaresItByName(t *testing.T) {
	item := aloneContainer("m1", "My_Front.1")

	proposal, err := Propose(item, nil, nil)
	if err != nil {
		t.Fatal(err)
	}

	definition := proposal.Definition
	if definition.Origin != OriginContainer || definition.Name != "my_front-1" || definition.ComposeProject != "" || len(definition.Units) != 1 {
		t.Fatalf("definition = %+v", definition)
	}
	if unit := definition.Units[0]; unit.Name != "My_Front.1" || unit.ContainerName != "My_Front.1" {
		t.Fatalf("unit = %+v", unit)
	}
}

func TestPropose_FreeNameAvoidsTaken(t *testing.T) {
	declared := []Service{
		{ID: "svc_1", MachineID: "m1", Name: "whoami"},
		{ID: "svc_2", MachineID: "m1", Name: "whoami-2"},
		{ID: "svc_3", MachineID: "m2", Name: "whoami-3"},
	}
	item := aloneContainer("m1", "whoami")

	proposal, err := Propose(item, nil, declared)
	if err != nil {
		t.Fatal(err)
	}
	if proposal.Definition.Name != "whoami-3" {
		t.Fatalf("name = %q", proposal.Definition.Name)
	}

	long := aloneContainer("m1", strings.Repeat("b", 63))
	declared = []Service{{ID: "svc_1", MachineID: "m1", Name: long.Name}}
	proposal, err = Propose(long, nil, declared)
	if err != nil {
		t.Fatal(err)
	}
	if len(proposal.Definition.Name) > 63 || proposal.Definition.Name == long.Name {
		t.Fatalf("un nom de 63 caractères pris reste un nom valable: %q", proposal.Definition.Name)
	}
}

func TestSlug_ProducesAValidNameFromAnything(t *testing.T) {
	cases := map[string]string{
		"Blog":        "blog",
		"my app.v2":   "my-app-v2",
		"--front":     "front",
		"_front":      "front",
		"éàç":         "service",
		"":            "service",
		"NEXTCLOUD_1": "nextcloud_1",
	}
	for text, want := range cases {
		if got := slug(text); got != want || !namePattern.MatchString(got) {
			t.Errorf("slug(%q) = %q, want %q", text, got, want)
		}
	}
}

func TestPropose_DeclaredContainerRefused(t *testing.T) {
	item := attached(aloneContainer("m1", "whoami"), "unit_whoami", container.MatchName)
	if _, err := Propose(item, nil, nil); !errors.Is(err, ErrContainerDeclared) {
		t.Fatalf("err = %v", err)
	}
}

func TestPropose_DefinitionPassesValidate(t *testing.T) {
	web := composeContainer("m1", "blog-web-1", "blog", "web")
	web.Image = "ghcr.io/org/blog:2.1"
	web.Ports = []container.Port{{HostPort: 8080, ContainerPort: 80, Protocol: "tcp"}}
	db := composeContainer("m1", "blog-db-1", "blog", "db")
	db.Image = "postgres@" + digestA
	alone := aloneContainer("m1", "My_Front.1")
	alone.Image = "nginx"
	declared := declaredCompose("svc_1", "m1", "nextcloud", "app")
	declared.Units[0].Image = "nextcloud"
	nextcloudDB := composeContainer("m1", "nextcloud-db-1", "nextcloud", "db")
	nextcloudDB.Image = "postgres:16.4"

	for name, proposal := range map[string]func() (Proposal, error){
		"projet Compose": func() (Proposal, error) { return Propose(web, []container.Container{web, db}, nil) },
		"projet déclaré": func() (Proposal, error) {
			return Propose(nextcloudDB, []container.Container{nextcloudDB}, []Service{declared})
		},
		"conteneur seul":      func() (Proposal, error) { return Propose(alone, nil, nil) },
		"image par empreinte": func() (Proposal, error) { return Propose(db, nil, nil) },
	} {
		t.Run(name, func(t *testing.T) {
			got, err := proposal()
			if err != nil {
				t.Fatal(err)
			}
			if err := got.Definition.Complete().Validate(); err != nil {
				t.Fatalf("la proposition est refusée par Validate: %v (%+v)", err, got.Definition)
			}
		})
	}
}

// Un gros projet s'adopte en plusieurs fois plutôt que de produire une
// définition que Validate refuserait ; le conteneur sur lequel l'opérateur a
// cliqué reste dans la coupe, même s'il est le dernier par nom.
func TestPropose_StopsAtTheUnitLimitKeepingTheRequestedContainer(t *testing.T) {
	var siblings []container.Container
	for index := range MaxUnits + 5 {
		name := string(rune('a'+index/26)) + string(rune('a'+index%26))
		siblings = append(siblings, composeContainer("m1", "big-"+name, "big", name))
	}
	last := siblings[len(siblings)-1]

	for _, requested := range []container.Container{siblings[0], last} {
		proposal, err := Propose(requested, siblings, nil)
		if err != nil {
			t.Fatal(err)
		}
		units := proposal.Definition.Units
		if len(units) != MaxUnits {
			t.Fatalf("%d unités", len(units))
		}
		if !slices.ContainsFunc(units, func(unit UnitDefinition) bool { return unit.Name == requested.ComposeService }) {
			t.Fatalf("l'unité demandée %q a été coupée: %+v", requested.ComposeService, units)
		}
		if err := proposal.Definition.Complete().Validate(); err != nil {
			t.Fatal(err)
		}
	}
}

// Le service du projet a déjà le plafond d'unités : une définition inchangée
// laisserait croire que le conteneur est adopté.
func TestPropose_FullDeclaredServiceRefuses(t *testing.T) {
	names := make([]string, 0, MaxUnits)
	for index := range MaxUnits {
		names = append(names, string(rune('a'+index/26))+string(rune('a'+index%26)))
	}
	declared := declaredCompose("svc_1", "m1", "big", names...)
	extra := composeContainer("m1", "big-zz", "big", "zz")

	if _, err := Propose(extra, []container.Container{extra}, []Service{declared}); !errors.Is(err, ErrUnitsFull) {
		t.Fatalf("err = %v", err)
	}
}

// Une unité ajoutée à un service presque plein tient dans le plafond.
func TestPropose_DeclaredServiceOneBelowTheLimitTakesTheRequestedUnit(t *testing.T) {
	names := make([]string, 0, MaxUnits-1)
	for index := range MaxUnits - 1 {
		names = append(names, string(rune('a'+index/26))+string(rune('a'+index%26)))
	}
	declared := declaredCompose("svc_1", "m1", "big", names...)
	extra := composeContainer("m1", "big-zz", "big", "zz")
	other := composeContainer("m1", "big-zy", "big", "zy")

	proposal, err := Propose(extra, []container.Container{extra, other}, []Service{declared})
	if err != nil {
		t.Fatal(err)
	}
	units := proposal.Definition.Units
	if len(units) != MaxUnits || units[len(units)-1].Name != "zz" {
		t.Fatalf("le conteneur demandé passe avant ses voisins: %d unités, dernière %q", len(units), units[len(units)-1].Name)
	}
}
