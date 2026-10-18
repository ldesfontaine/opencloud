package directory

import (
	"testing"
	"time"

	"github.com/ldesfontaine/opencloud/internal/container"
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
func composeContainer(machineID, name, project, composeService string) container.Container {
	return container.Container{
		ID: container.ID(machineID, name), MachineID: machineID, Kind: container.KindContainer, Name: name, Group: project,
		ComposeService: composeService, Image: composeService + ":1", State: container.StateRunning,
	}
}

func aloneContainer(machineID, name string) container.Container {
	return container.Container{
		ID: container.ID(machineID, name), MachineID: machineID, Kind: container.KindContainer, Name: name,
		Image: "traefik/whoami:v1", State: container.StateRunning,
	}
}

func TestMatch_ComposeKeyTakesTheProjectUnit(t *testing.T) {
	declared := []Service{declaredCompose("svc_1", "m1", "nextcloud", "app", "db")}
	app := composeContainer("m1", "nextcloud-app-1", "nextcloud", "app")
	// Un autre projet qui a le même service, et un service du projet qui n'est pas déclaré.
	other := composeContainer("m1", "blog-app-1", "blog", "app")
	cache := composeContainer("m1", "nextcloud-redis-1", "nextcloud", "redis")

	matches := Match(declared, []container.Container{app, other, cache})

	if got := matches[app.ID]; got.UnitID != "unit_nextcloud_app" || got.Level != container.MatchCompose {
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

	got := Match(declared, []container.Container{whoami})[whoami.ID]

	if got.UnitID != "unit_whoami" || got.Level != container.MatchName || !got.Level.IsWeak() {
		t.Fatalf("whoami = %+v", got)
	}
	if container.MatchCompose.IsWeak() {
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

	got := Match(declared, []container.Container{app})[app.ID]

	if got.UnitID != "unit_nextcloud_app" || got.Level != container.MatchCompose {
		t.Fatalf("app = %+v", got)
	}
}

func TestMatch_ReplicasShareTheUnit(t *testing.T) {
	declared := []Service{declaredCompose("svc_1", "m1", "web", "app")}
	first := composeContainer("m1", "web-app-1", "web", "app")
	second := composeContainer("m1", "web-app-2", "web", "app")

	matches := Match(declared, []container.Container{first, second})

	if matches[first.ID].UnitID != "unit_web_app" || matches[second.ID].UnitID != "unit_web_app" {
		t.Fatalf("matches = %+v", matches)
	}
}

// La clé est le projet : renommé, il ne retrouve plus rien, sans rattrapage.
func TestMatch_RenamedProjectLosesTheUnit(t *testing.T) {
	declared := []Service{declaredCompose("svc_1", "m1", "nextcloud", "app")}
	before := composeContainer("m1", "nextcloud-app-1", "nextcloud", "app")
	renamed := composeContainer("m1", "cloud-app-1", "cloud", "app")

	if _, matched := Match(declared, []container.Container{before})[before.ID]; !matched {
		t.Fatal("le projet déclaré doit se rattacher")
	}
	if got, matched := Match(declared, []container.Container{renamed})[renamed.ID]; matched {
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

	matches := Match(declared, []container.Container{elsewhere, gone, caddy})

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

	got := Match(declared, []container.Container{app})[app.ID]

	if got.UnitID != "unit_web_app" {
		t.Fatalf("app = %+v", got)
	}
	declared[0].Units[0].ID, declared[1].Units[0].ID = "unit_from_2", "unit_from_1"
	if got := Match(declared, []container.Container{app})[app.ID]; got.UnitID != "unit_from_1" {
		t.Fatalf("le service svc_1 doit gagner: %+v", got)
	}
}

// Un conteneur étiqueté vers une unité conteneur de sa machine. Le service
// est cherché par son identifiant, l'unité par son nom.
func labelledTo(item container.Container, serviceID, unitName string) container.Container {
	item.Labels = container.Labels{Service: serviceID, Unit: unitName}
	return item
}

// Le label gagne sur Compose : le conteneur d'un projet déclaré prend la
// place de l'unité qu'il vise, et l'unité Compose qu'il a quittée paraît
// absente (c'est ce que l'opérateur a demandé en posant le label).
func TestMatch_LabelTakesItsUnitBeforeCompose(t *testing.T) {
	declared := []Service{
		declaredCompose("svc_1", "m1", "nextcloud", "app"),
		declaredContainer("svc_2", "m1", "whoami"),
	}
	app := labelledTo(composeContainer("m1", "nextcloud-app-1", "nextcloud", "app"), "svc_2", "whoami")

	got := Match(declared, []container.Container{app})[app.ID]

	if got.UnitID != "unit_whoami" || got.Level != container.MatchLabel || got.Level.IsWeak() {
		t.Fatalf("app = %+v", got)
	}
	attachedApp := attached(app, got.UnitID, got.Level)
	report := Gaps(declared, []container.Container{attachedApp}, nil, nil)
	if report.Services[0].Units[0].State != GapAbsent {
		t.Fatalf("l'unité Compose quittée doit paraître absente : %+v", report.Services[0])
	}
	if report.Services[1].Weak || len(report.Undeclared) != 0 {
		t.Fatalf("un rattachement par label n'est pas faible : %+v", report)
	}
}

// Un label ne vise jamais une autre machine : le service existe, mais pas
// sur celle de l'agent qui a lu le conteneur.
func TestMatch_LabelToAnotherMachineNeverMatches(t *testing.T) {
	declared := []Service{declaredContainer("svc_2", "m2", "whoami")}
	onM1 := labelledTo(aloneContainer("m1", "whoami"), "svc_2", "whoami")
	onM2 := labelledTo(aloneContainer("m2", "other"), "svc_2", "whoami")

	matches := Match(declared, []container.Container{onM1, onM2})

	if _, matched := matches[onM1.ID]; matched {
		t.Fatalf("un label vers un service d'une autre machine : %+v", matches[onM1.ID])
	}
	if got := matches[onM2.ID]; got.UnitID != "unit_whoami" || got.Level != container.MatchLabel {
		t.Fatalf("sur sa propre machine, le label rattache : %+v", got)
	}
}

// Un label orphelin rend le conteneur non déclaré : ni Compose ni le nom
// ne rattrapent une affirmation fausse.
func TestMatch_OrphanLabelDoesNotFallBack(t *testing.T) {
	declared := []Service{
		declaredCompose("svc_1", "m1", "nextcloud", "app"),
		declaredContainer("svc_2", "m1", "whoami"),
	}
	byCompose := labelledTo(composeContainer("m1", "nextcloud-app-1", "nextcloud", "app"), "svc_inconnu", "app")
	byName := labelledTo(aloneContainer("m1", "whoami"), "svc_inconnu", "whoami")
	wrongUnit := labelledTo(aloneContainer("m1", "whoami-2"), "svc_2", "pas-une-unite")

	matches := Match(declared, []container.Container{byCompose, byName, wrongUnit})

	if len(matches) != 0 {
		t.Fatalf("un label orphelin ne se replie sur rien : %+v", matches)
	}
}

func TestMatch_IncompleteLabelIsOrphan(t *testing.T) {
	declared := []Service{declaredContainer("svc_2", "m1", "whoami")}
	onlyService := labelledTo(aloneContainer("m1", "whoami"), "svc_2", "")
	onlyUnit := labelledTo(aloneContainer("m1", "whoami-2"), "", "whoami")

	if matches := Match(declared, []container.Container{onlyService, onlyUnit}); len(matches) != 0 {
		t.Fatalf("il faut les deux labels : %+v", matches)
	}
}

func TestMatch_LabelToABinaryUnitNeverMatches(t *testing.T) {
	declared := []Service{declaredBinary("svc_3", "m1", "caddy.service")}
	item := labelledTo(aloneContainer("m1", "caddy"), "svc_3", "main")

	if matches := Match(declared, []container.Container{item}); len(matches) != 0 {
		t.Fatalf("une unité binaire ne rapproche jamais rien : %+v", matches)
	}
}

// L'identifiant survit au renommage du service, le nom du conteneur et le
// projet Compose aussi : c'est l'intérêt du label.
func TestMatch_LabelSurvivesServiceRename(t *testing.T) {
	declared := []Service{declaredContainer("svc_2", "m1", "whoami")}
	item := labelledTo(aloneContainer("m1", "tout-autre-nom"), "svc_2", "whoami")
	if got := Match(declared, []container.Container{item})[item.ID]; got.UnitID != "unit_whoami" {
		t.Fatalf("avant le renommage : %+v", got)
	}

	declared[0].Name = "renamed"
	declared[0].Units[0].ContainerName = "renamed"
	if got := Match(declared, []container.Container{item})[item.ID]; got.UnitID != "unit_whoami" || got.Level != container.MatchLabel {
		t.Fatalf("après le renommage du service : %+v", got)
	}
}

func TestMatch_ReplicasShareTheLabel(t *testing.T) {
	declared := []Service{declaredCompose("svc_1", "m1", "web", "app")}
	first := labelledTo(aloneContainer("m1", "web-1"), "svc_1", "app")
	second := labelledTo(aloneContainer("m1", "web-2"), "svc_1", "app")

	matches := Match(declared, []container.Container{first, second})

	if matches[first.ID].UnitID != "unit_web_app" || matches[second.ID].UnitID != "unit_web_app" {
		t.Fatalf("matches = %+v", matches)
	}
}

// La valeur d'un label est libre : un espace dedans ne doit jamais faire
// tomber un conteneur sur la clé d'un autre. La clé de l'index est une
// structure, pas une concaténation.
func TestMatch_LabelValueWithSpacesMatchesNothing(t *testing.T) {
	declared := []Service{declaredCompose("svc_1", "m1", "web", "app")}
	for name, labels := range map[string]container.Labels{
		"service puis unité collés":     {Service: "svc_1 app", Unit: "app"},
		"machine dans le service":       {Service: "m1 svc_1", Unit: "app"},
		"espace en fin d'unité":         {Service: "svc_1", Unit: "app "},
		"espace en tête de service":     {Service: " svc_1", Unit: "app"},
		"unité qui contient le service": {Service: "svc_1", Unit: "svc_1 app"},
	} {
		item := aloneContainer("m1", "x")
		item.Labels = labels
		if matches := Match(declared, []container.Container{item}); len(matches) != 0 {
			t.Errorf("%s : %+v", name, matches)
		}
	}
}

// Une clé collée par un séparateur, quel qu'il soit, ferait collisionner deux
// couples différents : l'unité « b<sep>c » du service « a » et l'unité « c » du
// service « a<sep>b ». Le test essaie les séparateurs qu'une concaténation
// aurait pu choisir (et aucun), dans les deux sens, jusque sur la machine.
func TestMatch_LabelKeyIsNeverAConcatenation(t *testing.T) {
	for _, sep := range []string{"", " ", "/", ":", "|", "\x00", "-", "_", ".", ",", "#", "@", "\n", "\t", "="} {
		unitWithTail := declaredCompose("svc_a", "m1", "web", "b"+sep+"c")
		item := labelledTo(aloneContainer("m1", "x"), "svc_a"+sep+"b", "c")
		if matches := Match([]Service{unitWithTail}, []container.Container{item}); len(matches) != 0 {
			t.Errorf("séparateur %q, service collé à l'unité : %+v", sep, matches)
		}

		serviceWithHead := declaredCompose("svc_a"+sep+"b", "m1", "web", "c")
		item = labelledTo(aloneContainer("m1", "x"), "svc_a", "b"+sep+"c")
		if matches := Match([]Service{serviceWithHead}, []container.Container{item}); len(matches) != 0 {
			t.Errorf("séparateur %q, unité collée au service : %+v", sep, matches)
		}

		// La machine du conteneur ne se confond pas avec un bout du service.
		onLongMachine := declaredCompose("b"+sep+"svc_a", "m1", "web", "c")
		item = labelledTo(aloneContainer("m1"+sep+"b", "x"), "svc_a", "c")
		if matches := Match([]Service{onLongMachine}, []container.Container{item}); len(matches) != 0 {
			t.Errorf("séparateur %q, machine collée au service : %+v", sep, matches)
		}
	}
	// Et le vrai couple, lui, se trouve.
	item := labelledTo(aloneContainer("m1", "x"), "svc_a", "b c")
	if matches := Match([]Service{declaredCompose("svc_a", "m1", "web", "b c")}, []container.Container{item}); len(matches) != 1 {
		t.Fatalf("le couple exact ne se rattache pas : %+v", matches)
	}
}

func TestLabelProblemOf_EachRuleInOrder(t *testing.T) {
	declared := []Service{
		declaredCompose("svc_1", "m1", "web", "app"),
		declaredBinary("svc_3", "m1", "caddy.service"),
		declaredContainer("svc_4", "m2", "elsewhere"),
	}
	cases := []struct {
		name   string
		labels container.Labels
		want   LabelProblem
	}{
		{"pas de label", container.Labels{}, LabelOK},
		{"labels mal formés", container.Labels{Invalid: true}, LabelInvalid},
		{"mal formés avant le reste", container.Labels{Service: "svc_1", Unit: "app", Invalid: true}, LabelInvalid},
		{"service seul", container.Labels{Service: "svc_1"}, LabelIncomplete},
		{"unité seule", container.Labels{Unit: "app"}, LabelIncomplete},
		{"incomplet avant inconnu", container.Labels{Service: "svc_inconnu"}, LabelIncomplete},
		{"service inconnu", container.Labels{Service: "svc_inconnu", Unit: "app"}, LabelServiceUnknown},
		{"valeur mal formée", container.Labels{Service: "svc_1 app", Unit: "app"}, LabelServiceUnknown},
		{"service d'une autre machine", container.Labels{Service: "svc_4", Unit: "elsewhere"}, LabelServiceUnknown},
		{"service inconnu avant unité inconnue", container.Labels{Service: "svc_inconnu", Unit: "pas-une-unite"}, LabelServiceUnknown},
		{"unité inconnue", container.Labels{Service: "svc_1", Unit: "pas-une-unite"}, LabelUnitUnknown},
		{"unité binaire", container.Labels{Service: "svc_3", Unit: "main"}, LabelUnitBinary},
		{"unité valide", container.Labels{Service: "svc_1", Unit: "app"}, LabelOK},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			item := aloneContainer("m1", "x")
			item.Labels = c.labels
			got := LabelProblemOf(declared, item)
			if got != c.want {
				t.Fatalf("problem = %q, want %q", got, c.want)
			}
			// La règle de recherche et celle du motif ne se contredisent jamais :
			// pas de motif si et seulement si le conteneur se rattache.
			_, matched := Match(declared, []container.Container{item})[item.ID]
			if !c.labels.IsEmpty() && matched != (got == LabelOK) {
				t.Fatalf("matched = %v alors que le motif est %q", matched, got)
			}
		})
	}
	if len(LabelProblems()) != 5 {
		t.Fatalf("LabelProblems() = %v", LabelProblems())
	}
}
