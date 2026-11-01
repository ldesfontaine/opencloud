package directory

import (
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/ldesfontaine/opencloud/internal/container"
)

// Un service Compose est un service : le projet « blog » donne une
// proposition pour web et une pour db, jamais un service « blog ». Les
// répliques d'un service ne font qu'une unité.
func TestPropose_ComposeProposesOneServicePerComposeService(t *testing.T) {
	web := composeContainer("m1", "blog-web-1", "blog", "web")
	web.Image = "ghcr.io/org/blog:2.1"
	web.Ports = []container.Port{
		{IP: "0.0.0.0", HostPort: 8080, ContainerPort: 80, Protocol: "tcp"},
		{IP: "::", HostPort: 8080, ContainerPort: 80, Protocol: "tcp"},
		{IP: "127.0.0.1", HostPort: 5353, ContainerPort: 53, Protocol: "udp"},
	}

	proposal, err := Propose(web, nil)
	if err != nil {
		t.Fatal(err)
	}

	definition := proposal.Definition
	if definition.Origin != OriginCompose || definition.ComposeProject != "blog" || definition.Name != "web" || definition.Nature != NatureApplication || definition.MachineID != "m1" {
		t.Fatalf("definition = %+v", definition)
	}
	if len(definition.Units) != 1 || definition.Units[0].Name != "web" {
		t.Fatalf("une unité, celle du service Compose: %+v", definition.Units)
	}
	unit := definition.Units[0]
	if unit.Image != "ghcr.io/org/blog" || unit.Version != "2.1" || unit.Digest != "" || unit.ID != "" {
		t.Fatalf("web = %+v", unit)
	}
	if !slices.Equal(unit.Ports, []string{"8080:80", "5353:53/udp"}) {
		t.Fatalf("les ports IPv4 et IPv6 ne font qu'une ligne: %v", unit.Ports)
	}
}

// Un service Compose dont le nom est pris sur la machine (un « db » d'un
// autre projet, ou le service « edge » d'avant) reçoit le nom du projet devant.
func TestPropose_ComposeServiceNameFallsBackToProjectPrefix(t *testing.T) {
	declared := []Service{{ID: "svc_1", MachineID: "m1", Name: "db"}}
	db := composeContainer("m1", "blog-db-1", "blog", "db")

	proposal, err := Propose(db, declared)
	if err != nil {
		t.Fatal(err)
	}
	if proposal.Definition.Name != "blog-db" {
		t.Fatalf("name = %q", proposal.Definition.Name)
	}
}

// edge = traefik + authelia + crowdsec : trois propositions, pas une ; un
// service déjà déclaré du projet (même d'avant, de trois unités) n'empêche
// pas les autres services du projet d'être proposés.
func TestProposeAll_OneProposalPerComposeServiceOfTheSameProject(t *testing.T) {
	traefik := composeContainer("m1", "edge-traefik-1", "edge", "traefik")
	traefik.Image = "traefik:v3.1"
	authelia := composeContainer("m1", "edge-authelia-1", "edge", "authelia")
	authelia.Image = "authelia/authelia:4.38"
	crowdsec := composeContainer("m1", "edge-crowdsec-1", "edge", "crowdsec")
	crowdsec.Image = "crowdsecurity/crowdsec:v1.6"

	proposals := ProposeAll(nil, []container.Container{crowdsec, traefik, authelia}, nil)

	if got := proposalNames(proposals); !slices.Equal(got, []string{"authelia", "crowdsec", "traefik"}) {
		t.Fatalf("proposals = %v", got)
	}
	for _, proposal := range proposals {
		if proposal.Definition.ComposeProject != "edge" || len(proposal.Definition.Units) != 1 {
			t.Errorf("un service, un projet edge : %+v", proposal.Definition)
		}
	}

	declared := declaredCompose("svc_1", "m1", "edge", "traefik")
	attachedTraefik := attached(traefik, "unit_edge_traefik", container.MatchCompose)
	declared.Units[0].ID = "unit_edge_traefik"
	rest := ProposeAll([]Service{declared}, []container.Container{attachedTraefik, authelia, crowdsec}, nil)
	if got := proposalNames(rest); !slices.Equal(got, []string{"authelia", "crowdsec"}) {
		t.Fatalf("le service déclaré ne retient pas les autres : %v", got)
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
			proposal, err := Propose(item, nil)
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

	proposal, err := Propose(item, nil)
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

	proposal, err := Propose(item, declared)
	if err != nil {
		t.Fatal(err)
	}
	if proposal.Definition.Name != "whoami-3" {
		t.Fatalf("name = %q", proposal.Definition.Name)
	}

	long := aloneContainer("m1", strings.Repeat("b", 63))
	declared = []Service{{ID: "svc_1", MachineID: "m1", Name: long.Name}}
	proposal, err = Propose(long, declared)
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
	if _, err := Propose(item, nil); !errors.Is(err, ErrContainerDeclared) {
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

	for name, proposal := range map[string]func() (Proposal, error){
		"service Compose":     func() (Proposal, error) { return Propose(web, nil) },
		"conteneur seul":      func() (Proposal, error) { return Propose(alone, nil) },
		"image par empreinte": func() (Proposal, error) { return Propose(db, nil) },
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

// Un conteneur étiqueté n'est non déclaré que parce que son label ne vise
// rien : l'unité qu'on lui créerait ne le rattacherait pas.
func TestPropose_LabelledContainerRefused(t *testing.T) {
	for name, item := range map[string]container.Container{
		"compose": labelledTo(composeContainer("m1", "blog-web-1", "blog", "web"), "svc_inconnu", "web"),
		"seul":    labelledTo(aloneContainer("m1", "whoami"), "svc_inconnu", "whoami"),
		"moitié":  labelledTo(aloneContainer("m1", "whoami"), "svc_inconnu", ""),
	} {
		if _, err := Propose(item, nil); !errors.Is(err, ErrContainerLabelled) {
			t.Errorf("%s : err = %v", name, err)
		}
	}
}

func TestProposeAll_SkipsIgnoredAndOrphans(t *testing.T) {
	blog := composeContainer("m1", "blog-web-1", "blog", "web")
	front := aloneContainer("m1", "front")
	ignoredOne := aloneContainer("m1", "scratch")
	orphan := labelledTo(aloneContainer("m1", "lonely"), "svc_inconnu", "lonely")
	gone := aloneContainer("m1", "gone")
	gone.ArchivedAt = time.Unix(1_800_000_000, 0)
	ignored := []IgnoredKey{{MachineID: "m1", Kind: IgnoreContainerName, Value: "scratch"}}

	proposals := ProposeAll(nil, []container.Container{blog, front, ignoredOne, orphan, gone}, ignored)

	got := proposalNames(proposals)
	if !slices.Equal(got, []string{"front", "web"}) {
		t.Fatalf("proposals = %v : ni l'ignoré, ni l'orphelin, ni l'archivé", got)
	}
}

// Deux groupes que le nom confond ne reçoivent pas le même nom : chaque
// définition entre dans la liste des services connus avant la suivante.
func TestProposeAll_NamesNeverCollide(t *testing.T) {
	project := composeContainer("m1", "x-1", "Blog", "web")
	alone := aloneContainer("m1", "blog")
	other := aloneContainer("m1", "BLOG")
	declared := []Service{{ID: "svc_9", MachineID: "m1", Name: "blog"}}
	onOtherMachine := aloneContainer("m2", "blog")

	proposals := ProposeAll(declared, []container.Container{project, alone, other, onOtherMachine}, nil)

	seen := map[string]bool{}
	for _, proposal := range proposals {
		key := proposal.Definition.MachineID + " " + proposal.Definition.Name
		if seen[key] {
			t.Fatalf("deux propositions s'appellent %q : %v", key, proposalNames(proposals))
		}
		seen[key] = true
	}
	if len(proposals) != 4 || !seen["m2 blog"] {
		t.Fatalf("proposals = %v", proposalNames(proposals))
	}
}

func TestProposeAll_DefinitionsPassValidate(t *testing.T) {
	web := composeContainer("m1", "blog-web-1", "blog", "web")
	web.Image = "ghcr.io/org/blog:2.1"
	db := composeContainer("m1", "blog-db-1", "blog", "db")
	db.Image = "postgres@" + digestA
	alone := aloneContainer("m1", "My_Front.1")
	alone.Image = "nginx"
	declared := declaredCompose("svc_1", "m1", "nextcloud", "app")
	declared.Units[0].Image = "nextcloud"
	nextcloudDB := composeContainer("m1", "nextcloud-db-1", "nextcloud", "db")
	nextcloudDB.Image = "postgres:16.4"

	proposals := ProposeAll([]Service{declared}, []container.Container{web, db, alone, nextcloudDB}, nil)

	if len(proposals) != 4 {
		t.Fatalf("proposals = %v", proposalNames(proposals))
	}
	for _, proposal := range proposals {
		if err := proposal.Definition.Complete().Validate(); err != nil {
			t.Errorf("%s : refusée par Validate : %v (%+v)", proposal.Definition.Name, err, proposal.Definition)
		}
	}
}

// Le même état donne les mêmes propositions, dans le même ordre, quel que
// soit l'ordre dans lequel les conteneurs sont lus.
func TestProposeAll_IsDeterministic(t *testing.T) {
	containers := []container.Container{
		composeContainer("m2", "blog-web-1", "blog", "web"),
		aloneContainer("m1", "front"),
		composeContainer("m1", "wiki-web-1", "wiki", "web"),
		composeContainer("m1", "wiki-db-1", "wiki", "db"),
		aloneContainer("m2", "back"),
		aloneContainer("m1", "Front"),
	}
	want := ProposeAll(nil, containers, nil)
	if len(want) != 6 {
		t.Fatalf("proposals = %v", proposalNames(want))
	}
	for round := range 8 {
		shuffled := slices.Clone(containers)
		rotation := round % len(shuffled)
		shuffled = append(shuffled[rotation:], shuffled[:rotation]...)
		slices.Reverse(shuffled[:round%4+1])
		if got := ProposeAll(nil, shuffled, nil); !reflect.DeepEqual(got, want) {
			t.Fatalf("tour %d : %v, want %v", round, proposalNames(got), proposalNames(want))
		}
	}
}

func proposalNames(proposals []Proposal) []string {
	names := make([]string, 0, len(proposals))
	for _, proposal := range proposals {
		names = append(names, proposal.Definition.Name)
	}
	return names
}
