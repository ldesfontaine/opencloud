package directory

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/ldesfontaine/opencloud/internal/container"
)

func composeDefinition() ServiceDefinition {
	return ServiceDefinition{
		MachineID:      "machine-1",
		Name:           "nextcloud",
		Title:          "Nextcloud",
		Nature:         NatureApplication,
		Origin:         OriginCompose,
		ComposeProject: "nextcloud",
		PublicURL:      "https://cloud.exemple.fr/",
		Units: []UnitDefinition{
			{Name: "app", Image: "nextcloud", Version: "29.0.4", Ports: []string{"8080:80/tcp"}},
			{Name: "db", Image: "ghcr.io/exemple/postgres", Version: "16.4"},
		},
	}
}

func containerDefinition() ServiceDefinition {
	return ServiceDefinition{
		MachineID: "machine-1",
		Name:      "whoami",
		Nature:    NatureApplication,
		Origin:    OriginContainer,
		Units:     []UnitDefinition{{Name: "whoami", Image: "traefik/whoami", ContainerName: "whoami"}},
	}
}

func binaryDefinition() ServiceDefinition {
	return ServiceDefinition{
		MachineID: "machine-1",
		Name:      "caddy",
		Nature:    NatureInfra,
		Origin:    OriginBinary,
		Provides:  []Capability{CapabilityProxy, CapabilityTLS},
		Units:     []UnitDefinition{{Name: "caddy", SystemdUnit: "caddy.service"}},
	}
}

func TestValidate_AcceptsEachOrigin(t *testing.T) {
	for name, definition := range map[string]ServiceDefinition{
		"compose": composeDefinition(), "conteneur": containerDefinition(), "binaire": binaryDefinition(),
	} {
		if err := definition.Complete().Validate(); err != nil {
			t.Errorf("%s: définition valable refusée: %v", name, err)
		}
	}
}

// Une ligne par règle : l'erreur est celle de la première règle violée, et
// c'est elle qui devient la clé que l'opérateur lit.
func TestValidate_RefusesEachRuleWithItsError(t *testing.T) {
	cases := []struct {
		name  string
		build func(ServiceDefinition) ServiceDefinition
		base  func() ServiceDefinition
		want  error
	}{
		{"sans machine", func(d ServiceDefinition) ServiceDefinition { d.MachineID = " "; return d }, composeDefinition, ErrMachineInvalid},
		{"nom vide", func(d ServiceDefinition) ServiceDefinition { d.Name = ""; return d }, composeDefinition, ErrNameInvalid},
		{"nom en majuscules", func(d ServiceDefinition) ServiceDefinition { d.Name = "Nextcloud"; return d }, composeDefinition, ErrNameInvalid},
		{"nom trop long", func(d ServiceDefinition) ServiceDefinition { d.Name = strings.Repeat("a", 64); return d }, composeDefinition, ErrNameInvalid},
		{"titre trop long", func(d ServiceDefinition) ServiceDefinition { d.Title = strings.Repeat("é", 81); return d }, composeDefinition, ErrTitleInvalid},
		{"nature inconnue", func(d ServiceDefinition) ServiceDefinition { d.Nature = "outil"; return d }, composeDefinition, ErrNatureInvalid},
		{"origine inconnue", func(d ServiceDefinition) ServiceDefinition { d.Origin = "vm"; return d }, composeDefinition, ErrOriginInvalid},
		{"origine réservée", func(d ServiceDefinition) ServiceDefinition { d.Origin = "deployed"; return d }, composeDefinition, ErrOriginInvalid},
		{"Compose sans projet", func(d ServiceDefinition) ServiceDefinition { d.ComposeProject = ""; return d }, composeDefinition, ErrComposeProjectInvalid},
		{"projet Compose illisible", func(d ServiceDefinition) ServiceDefinition { d.ComposeProject = "Mon Projet"; return d }, composeDefinition, ErrComposeProjectInvalid},
		{"projet Compose sur un conteneur", func(d ServiceDefinition) ServiceDefinition { d.ComposeProject = "x"; return d }, containerDefinition, ErrComposeProjectInvalid},
		{"adresse sans protocole", func(d ServiceDefinition) ServiceDefinition { d.PublicURL = "cloud.exemple.fr"; return d }, composeDefinition, ErrPublicURLInvalid},
		{"adresse ftp", func(d ServiceDefinition) ServiceDefinition { d.PublicURL = "ftp://cloud.exemple.fr"; return d }, composeDefinition, ErrPublicURLInvalid},
		{"adresse sans hôte", func(d ServiceDefinition) ServiceDefinition { d.PublicURL = "https://"; return d }, composeDefinition, ErrPublicURLInvalid},
		{"adresse trop longue", func(d ServiceDefinition) ServiceDefinition {
			d.PublicURL = "https://exemple.fr/" + strings.Repeat("a", 2048)
			return d
		}, composeDefinition, ErrPublicURLInvalid},
		{"fourniture inconnue", func(d ServiceDefinition) ServiceDefinition { d.Provides = []Capability{"dns"}; return d }, binaryDefinition, ErrProvidesInvalid},
		{"fourniture sur une application", func(d ServiceDefinition) ServiceDefinition { d.Provides = []Capability{CapabilitySSO}; return d }, composeDefinition, ErrProvidesNeedInfra},
		{"sans unité", func(d ServiceDefinition) ServiceDefinition { d.Units = nil; return d }, composeDefinition, ErrUnitsInvalid},
		{"trop d'unités", func(d ServiceDefinition) ServiceDefinition {
			d.Units = nil
			for i := range MaxUnits + 1 {
				d.Units = append(d.Units, UnitDefinition{Name: "u" + string(rune('a'+i%26)) + string(rune('a'+i/26)), Image: "x"})
			}
			return d
		}, composeDefinition, ErrUnitsInvalid},
		{"nom d'unité illisible", func(d ServiceDefinition) ServiceDefinition { d.Units[0].Name = "-app"; return d }, composeDefinition, ErrUnitNameInvalid},
		{"nom d'unité en double", func(d ServiceDefinition) ServiceDefinition { d.Units[1].Name = "app"; return d }, composeDefinition, ErrUnitNameTaken},
		{"conteneur sans image", func(d ServiceDefinition) ServiceDefinition { d.Units[0].Image = ""; return d }, composeDefinition, ErrImageInvalid},
		{"image avec une version", func(d ServiceDefinition) ServiceDefinition { d.Units[0].Image = "nextcloud:29"; return d }, composeDefinition, ErrImageInvalid},
		{"image avec une empreinte", func(d ServiceDefinition) ServiceDefinition {
			d.Units[0].Image = "nextcloud@sha256:" + strings.Repeat("a", 64)
			return d
		}, composeDefinition, ErrImageInvalid},
		{"image avec une majuscule", func(d ServiceDefinition) ServiceDefinition { d.Units[0].Image = "NextCloud"; return d }, composeDefinition, ErrImageInvalid},
		{"image avec une espace", func(d ServiceDefinition) ServiceDefinition { d.Units[0].Image = "next cloud"; return d }, composeDefinition, ErrImageInvalid},
		{"image trop longue", func(d ServiceDefinition) ServiceDefinition { d.Units[0].Image = strings.Repeat("a", 513); return d }, composeDefinition, ErrImageInvalid},
		{"binaire avec une image", func(d ServiceDefinition) ServiceDefinition { d.Units[0].Image = "caddy"; return d }, binaryDefinition, ErrImageInvalid},
		{"version illisible", func(d ServiceDefinition) ServiceDefinition { d.Units[0].Version = "29 0"; return d }, composeDefinition, ErrVersionInvalid},
		{"version latest", func(d ServiceDefinition) ServiceDefinition { d.Units[0].Version = "Latest"; return d }, composeDefinition, ErrVersionLatest},
		{"empreinte illisible", func(d ServiceDefinition) ServiceDefinition { d.Units[0].Digest = "sha256:abc"; return d }, composeDefinition, ErrDigestInvalid},
		{"conteneur sans nom de conteneur", func(d ServiceDefinition) ServiceDefinition { d.Units[0].ContainerName = ""; return d }, containerDefinition, ErrContainerNameInvalid},
		{"nom de conteneur sur Compose", func(d ServiceDefinition) ServiceDefinition { d.Units[0].ContainerName = "app"; return d }, composeDefinition, ErrContainerNameInvalid},
		{"binaire sans unité systemd", func(d ServiceDefinition) ServiceDefinition { d.Units[0].SystemdUnit = ""; return d }, binaryDefinition, ErrSystemdUnitInvalid},
		{"unité systemd sans .service", func(d ServiceDefinition) ServiceDefinition { d.Units[0].SystemdUnit = "caddy"; return d }, binaryDefinition, ErrSystemdUnitInvalid},
		{"unité systemd sur un conteneur", func(d ServiceDefinition) ServiceDefinition { d.Units[0].SystemdUnit = "x.service"; return d }, containerDefinition, ErrSystemdUnitInvalid},
		{"empreinte sur un binaire", func(d ServiceDefinition) ServiceDefinition {
			d.Units[0].Digest = "sha256:" + strings.Repeat("a", 64)
			return d
		}, binaryDefinition, ErrDigestInvalid},
		{"même conteneur pour deux unités", func(d ServiceDefinition) ServiceDefinition {
			d.Units = append(d.Units, UnitDefinition{Name: "bis", Image: "traefik/whoami", ContainerName: "whoami"})
			return d
		}, containerDefinition, ErrUnitKeyDuplicate},
		{"même unité systemd pour deux unités", func(d ServiceDefinition) ServiceDefinition {
			d.Units = append(d.Units, UnitDefinition{Name: "bis", SystemdUnit: "caddy.service"})
			return d
		}, binaryDefinition, ErrUnitKeyDuplicate},
		{"politique inconnue", func(d ServiceDefinition) ServiceDefinition { d.Units[0].UpdatePolicy = "pinned"; return d }, composeDefinition, ErrUpdatePolicyInvalid},
		{"politique hors watch sur un binaire", func(d ServiceDefinition) ServiceDefinition { d.Units[0].UpdatePolicy = container.PolicyHold; return d }, binaryDefinition, ErrUpdatePolicyInvalid},
		{"port illisible", func(d ServiceDefinition) ServiceDefinition { d.Units[0].Ports = []string{"http"}; return d }, composeDefinition, ErrPortsInvalid},
		{"port nul", func(d ServiceDefinition) ServiceDefinition { d.Units[0].Ports = []string{"0"}; return d }, composeDefinition, ErrPortsInvalid},
		{"port hors plage", func(d ServiceDefinition) ServiceDefinition { d.Units[0].Ports = []string{"8080:70000"}; return d }, composeDefinition, ErrPortsInvalid},
		{"protocole inconnu", func(d ServiceDefinition) ServiceDefinition { d.Units[0].Ports = []string{"80/sctp"}; return d }, composeDefinition, ErrPortsInvalid},
		{"trop de ports", func(d ServiceDefinition) ServiceDefinition {
			d.Units[0].Ports = slices.Repeat([]string{"80"}, MaxPorts+1)
			return d
		}, composeDefinition, ErrPortsInvalid},
		{"dossier de données relatif", func(d ServiceDefinition) ServiceDefinition { d.Units[0].DataPath = "data"; return d }, composeDefinition, ErrDataPathInvalid},
		{"dossier de données à la racine", func(d ServiceDefinition) ServiceDefinition { d.Units[0].DataPath = "/"; return d }, composeDefinition, ErrDataPathInvalid},
		{"dossier de données qui remonte", func(d ServiceDefinition) ServiceDefinition { d.Units[0].DataPath = "/var/../etc"; return d }, composeDefinition, ErrDataPathInvalid},
		{"dossier de données non nettoyé", func(d ServiceDefinition) ServiceDefinition { d.Units[0].DataPath = "/var/www/"; return d }, composeDefinition, ErrDataPathInvalid},
		{"dossier de données à espace", func(d ServiceDefinition) ServiceDefinition { d.Units[0].DataPath = "/var/my data"; return d }, composeDefinition, ErrDataPathInvalid},
		{"dossier de données sur un binaire", func(d ServiceDefinition) ServiceDefinition { d.Units[0].DataPath = "/var/lib/caddy"; return d }, binaryDefinition, ErrDataPathInvalid},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			err := testCase.build(testCase.base()).Complete().Validate()
			if !errors.Is(err, testCase.want) {
				t.Fatalf("refus attendu %v, obtenu %v", testCase.want, err)
			}
		})
	}
}

func TestValidate_AcceptsPortsAndRegistryPorts(t *testing.T) {
	definition := composeDefinition()
	definition.Units[0].Image = "registre.exemple.fr:5000/equipe/app"
	definition.Units[0].Ports = []string{"80", "8080:80", "53/udp", "127:1/tcp", "65535"}
	if err := definition.Complete().Validate(); err != nil {
		t.Fatalf("image d'un registre à port ou ports valables refusés: %v", err)
	}
}

func TestValidate_AcceptsADataPath(t *testing.T) {
	definition := composeDefinition()
	definition.Units[0].DataPath = " /var/www/html "
	completed := definition.Complete()
	if err := completed.Validate(); err != nil {
		t.Fatalf("dossier de données valable refusé: %v", err)
	}
	if completed.Units[0].DataPath != "/var/www/html" {
		t.Fatalf("dossier de données %q, espaces non retirés", completed.Units[0].DataPath)
	}
}

func TestComplete_DerivesUnitKindFromOrigin(t *testing.T) {
	cases := map[Origin]UnitKind{OriginCompose: UnitContainer, OriginContainer: UnitContainer, OriginBinary: UnitBinary}
	for origin, want := range cases {
		completed := ServiceDefinition{Origin: origin, Units: []UnitDefinition{{Name: "a", Kind: "autre"}}}.Complete()
		if got := completed.Units[0].Kind; got != want {
			t.Errorf("origine %s: genre %q, attendu %q", origin, got, want)
		}
	}
}

func TestComplete_TrimsAndDropsDuplicates(t *testing.T) {
	completed := ServiceDefinition{
		MachineID: " m ", Name: " nextcloud ", Title: " Mon cloud ", ComposeProject: " p ", PublicURL: " https://a.fr ",
		Provides:   []Capability{CapabilityProxy, CapabilityProxy, " tls "},
		ProjectIDs: []string{"prj_1", " prj_1", "prj_2"},
		Units:      []UnitDefinition{{Name: " app ", Image: " nextcloud ", Version: " 29 ", Ports: []string{" 80 "}}},
	}.Complete()
	if completed.MachineID != "m" || completed.Name != "nextcloud" || completed.Title != "Mon cloud" ||
		completed.ComposeProject != "p" || completed.PublicURL != "https://a.fr" {
		t.Fatalf("espaces non retirés: %+v", completed)
	}
	if !slices.Equal(completed.Provides, []Capability{CapabilityProxy, CapabilityTLS}) {
		t.Fatalf("fournitures: %v", completed.Provides)
	}
	if !slices.Equal(completed.ProjectIDs, []string{"prj_1", "prj_2"}) {
		t.Fatalf("projets: %v", completed.ProjectIDs)
	}
	unit := completed.Units[0]
	if unit.Name != "app" || unit.Image != "nextcloud" || unit.Version != "29" || unit.Ports[0] != "80" {
		t.Fatalf("unité non rognée: %+v", unit)
	}
}

func TestProjectDefinition_Validate_RefusesBadNameAndTitle(t *testing.T) {
	if err := (ProjectDefinition{Name: " client-a ", Title: "Client A"}).Complete().Validate(); err != nil {
		t.Fatalf("projet valable refusé: %v", err)
	}
	if err := (ProjectDefinition{Name: "Client A"}).Complete().Validate(); !errors.Is(err, ErrProjectNameInvalid) {
		t.Fatalf("nom: %v", err)
	}
	if err := (ProjectDefinition{Name: "a", Title: strings.Repeat("a", 81)}).Complete().Validate(); !errors.Is(err, ErrProjectTitleInvalid) {
		t.Fatalf("titre: %v", err)
	}
}

func TestNewID_CarriesItsPrefix(t *testing.T) {
	id, err := NewID(ServicePrefix)
	if err != nil || !strings.HasPrefix(id, "svc_") || len(id) != len("svc_")+16 {
		t.Fatalf("id %q, err %v", id, err)
	}
}

// Sans politique dite, une unité se suit ; les trois politiques passent, y
// compris sur une unité de conteneur déclarée à la main.
func TestComplete_DefaultsPolicyToWatch(t *testing.T) {
	completed := composeDefinition().Complete()
	for index, unit := range completed.Units {
		if unit.UpdatePolicy != container.PolicyWatch {
			t.Errorf("unité %d : politique %q, attendu watch", index, unit.UpdatePolicy)
		}
	}
	spaced := ServiceDefinition{Origin: OriginCompose, Units: []UnitDefinition{{Name: "a", UpdatePolicy: " hold "}}}.Complete()
	if spaced.Units[0].UpdatePolicy != container.PolicyHold {
		t.Fatalf("les espaces d'une politique se retirent: %q", spaced.Units[0].UpdatePolicy)
	}
	for _, policy := range container.UpdatePolicies() {
		definition := composeDefinition()
		definition.Units[0].UpdatePolicy = policy
		if err := definition.Complete().Validate(); err != nil {
			t.Errorf("politique %q refusée: %v", policy, err)
		}
	}
}
