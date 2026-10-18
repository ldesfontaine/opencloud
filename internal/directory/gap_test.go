package directory

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/ldesfontaine/opencloud/internal/container"
	"github.com/ldesfontaine/opencloud/internal/update"
)

var (
	digestA = "sha256:" + strings.Repeat("a", 64)
	digestB = "sha256:" + strings.Repeat("b", 64)
)

// Un constat sur l'image, rangé comme update.Checks le range : machine,
// espace, image telle que Docker l'écrit.
func checksWith(machineID, image, localDigest string) map[string]update.Check {
	return map[string]update.Check{machineID + " " + image: {MachineID: machineID, Image: image, LocalDigest: localDigest}}
}

func TestVerdict_EachRuleInOrder(t *testing.T) {
	cases := []struct {
		name        string
		unit        Unit
		image       string
		localDigest string
		state       GapState
		reason      GapReason
	}{
		{"1 image observée illisible", Unit{Image: "nextcloud", Version: "29.0.4"}, "Nextcloud:29", "", GapUnverifiable, ReasonImageUnreadable},
		{"2 image déclarée illisible", Unit{Image: "NextCloud", Version: "29.0.4"}, "nextcloud:29.0.4", "", GapUnverifiable, ReasonImageUnreadable},
		{"3 autre dépôt", Unit{Image: "nextcloud", Version: "29.0.4"}, "postgres:29.0.4", "", GapDiverged, ReasonImageDiffers},
		{"3 autre registre", Unit{Image: "nextcloud", Version: "29.0.4"}, "ghcr.io/nextcloud/nextcloud:29.0.4", "", GapDiverged, ReasonImageDiffers},
		{"3 avant 4", Unit{Image: "nextcloud", Version: "29.0.4", Digest: digestA}, "postgres@" + digestB, "", GapDiverged, ReasonImageDiffers},
		{"4 empreinte lue localement", Unit{Image: "nextcloud", Version: "29.0.4", Digest: digestA}, "nextcloud:29.0.4", digestB, GapDiverged, ReasonDigestDiffers},
		{"4 empreinte écrite dans l'image", Unit{Image: "nextcloud", Version: "29.0.4", Digest: digestA}, "nextcloud@" + digestB, "", GapDiverged, ReasonDigestDiffers},
		{"4 avant 6", Unit{Image: "nextcloud", Version: "29.0.4", Digest: digestA}, "nextcloud:latest", digestB, GapDiverged, ReasonDigestDiffers},
		{"5 empreinte seule, avec version", Unit{Image: "nextcloud", Version: "29.0.4", Digest: digestA}, "nextcloud@" + digestA, "", GapConform, ReasonNone},
		{"5 empreinte seule, sans version déclarée", Unit{Image: "nextcloud", Digest: digestA}, "nextcloud@" + digestA, "", GapConform, ReasonNone},
		{"5 empreinte écrite, quel que soit le tag", Unit{Image: "nextcloud", Version: "29.0.4", Digest: digestA}, "nextcloud:latest@" + digestA, "", GapConform, ReasonNone},
		{"5 avant 8", Unit{Image: "nextcloud", Version: "29.0.4", Digest: digestA}, "nextcloud:29.0.6@" + digestA, "", GapConform, ReasonNone},
		{"5 sans empreinte épinglée, rien ne l'emporte", Unit{Image: "nextcloud", Version: "29.0.4"}, "nextcloud@" + digestA, "", GapUnverifiable, ReasonTagLatest},
		{"6 sans tag", Unit{Image: "nextcloud", Version: "29.0.4"}, "nextcloud", "", GapUnverifiable, ReasonTagLatest},
		{"6 latest, casse ignorée", Unit{Image: "nextcloud", Version: "29.0.4"}, "nextcloud:LATEST", "", GapUnverifiable, ReasonTagLatest},
		{"6 latest, empreinte lue localement égale à l'épinglée", Unit{Image: "nginx", Version: "1.27", Digest: digestA}, "nginx:latest", digestA, GapUnverifiable, ReasonTagLatest},
		{"6 avant 7", Unit{Image: "nextcloud"}, "nextcloud:latest", "", GapUnverifiable, ReasonTagLatest},
		{"7 version non déclarée", Unit{Image: "nextcloud"}, "nextcloud:29.0.4", "", GapUnverifiable, ReasonVersionUnpinned},
		{"7 avant 8", Unit{Image: "nextcloud"}, "nextcloud:29.0.6", "", GapUnverifiable, ReasonVersionUnpinned},
		{"8 autre version", Unit{Image: "nextcloud", Version: "29.0.4"}, "nextcloud:29.0.6", "", GapDiverged, ReasonTagDiffers},
		{"8 avant 9", Unit{Image: "nextcloud", Version: "29.0.4", Digest: digestA}, "nextcloud:29.0.6", "", GapDiverged, ReasonTagDiffers},
		{"9 empreinte épinglée, inconnue", Unit{Image: "nextcloud", Version: "29.0.4", Digest: digestA}, "nextcloud:29.0.4", "", GapUnverifiable, ReasonDigestUnknown},
		{"10 conforme", Unit{Image: "nextcloud", Version: "29.0.4"}, "nextcloud:29.0.4", "", GapConform, ReasonNone},
		{"10 conforme, empreinte lue localement", Unit{Image: "nextcloud", Version: "29.0.4", Digest: digestA}, "nextcloud:29.0.4", digestA, GapConform, ReasonNone},
		{"10 conforme, registre explicite", Unit{Image: "ghcr.io/org/app", Version: "1.2"}, "ghcr.io/org/app:1.2", "", GapConform, ReasonNone},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			item := container.Container{ID: "m1:c", MachineID: "m1", Image: c.image, UnitID: "unit_1"}
			state, reason := verdict(c.unit, item, checksWith("m1", c.image, c.localDigest))
			if state != c.state || reason != c.reason {
				t.Fatalf("verdict = %s / %q, want %s / %q", state, reason, c.state, c.reason)
			}
		})
	}
}

func nextcloudWithRedis() Service {
	declared := declaredCompose("svc_1", "m1", "nextcloud", "nextcloud", "redis")
	declared.Units[0].Image, declared.Units[0].Version = "nextcloud", "29.0.4"
	declared.Units[1].Image, declared.Units[1].Version = "redis", "7.4"
	return declared
}

func attached(item container.Container, unitID string, level container.MatchLevel) container.Container {
	item.UnitID, item.MatchLevel = unitID, level
	return item
}

func TestGaps_UnitWithoutContainerIsAbsent(t *testing.T) {
	declared := []Service{nextcloudWithRedis()}
	web := attached(composeContainer("m1", "nextcloud-1", "nextcloud", "nextcloud"), "unit_nextcloud_nextcloud", container.MatchCompose)
	web.Image = "nextcloud:29.0.4"

	report := Gaps(declared, []container.Container{web}, nil, nil)

	gap := report.Services[0]
	if gap.State != GapAbsent || gap.Units[0].State != GapConform || gap.Units[1].State != GapAbsent {
		t.Fatalf("gap = %+v", gap)
	}
	if len(gap.Units[1].Containers) != 0 {
		t.Fatalf("une unité absente n'a aucun conteneur: %+v", gap.Units[1])
	}
}

// L'état Docker reste l'affaire de l'onglet Conteneurs : un conteneur
// arrêté compte comme présent, l'écart ne regarde que ce qu'il est.
func TestGaps_StoppedContainerIsPresent(t *testing.T) {
	declared := []Service{declaredContainer("svc_1", "m1", "whoami")}
	declared[0].Units[0].Image, declared[0].Units[0].Version = "traefik/whoami", "v1"
	stopped := attached(aloneContainer("m1", "whoami"), "unit_whoami", container.MatchName)
	stopped.State = container.StateExited

	report := Gaps(declared, []container.Container{stopped}, nil, nil)

	unit := report.Services[0].Units[0]
	if unit.State != GapConform || unit.Containers[0].State != container.StateExited {
		t.Fatalf("unit = %+v", unit)
	}
}

func TestGaps_BinaryIsUnobservedNeverConform(t *testing.T) {
	declared := []Service{declaredBinary("svc_1", "m1", "caddy.service")}

	report := Gaps(declared, nil, nil, nil)

	gap := report.Services[0]
	if gap.State != GapUnobserved || gap.Units[0].State != GapUnobserved || gap.Units[0].Kind != UnitBinary {
		t.Fatalf("gap = %+v", gap)
	}
}

func TestGaps_ServiceTakesTheWorstUnit(t *testing.T) {
	cases := []struct {
		name  string
		units []GapState
		want  GapState
	}{
		{"tout conforme", []GapState{GapConform, GapConform}, GapConform},
		{"non observé avant conforme", []GapState{GapConform, GapUnobserved}, GapUnobserved},
		{"non vérifiable avant non observé", []GapState{GapUnobserved, GapUnverifiable}, GapUnverifiable},
		{"divergent avant non vérifiable", []GapState{GapUnverifiable, GapDiverged}, GapDiverged},
		{"absent avant divergent", []GapState{GapDiverged, GapAbsent}, GapAbsent},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := GapConform
			for _, state := range c.units {
				got = worstOfService(got, state)
			}
			if got != c.want {
				t.Fatalf("got %s, want %s", got, c.want)
			}
		})
	}

	// Et de bout en bout : un conteneur divergent, un non vérifiable, une absente.
	declared := []Service{nextcloudWithRedis()}
	web := attached(composeContainer("m1", "nextcloud-1", "nextcloud", "nextcloud"), "unit_nextcloud_nextcloud", container.MatchCompose)
	web.Image = "nextcloud:29.0.6"
	report := Gaps(declared, []container.Container{web}, nil, nil)
	if got := report.Services[0]; got.State != GapAbsent || got.Units[0].State != GapDiverged || got.Units[0].Containers[0].Reason != ReasonTagDiffers {
		t.Fatalf("gap = %+v", got)
	}
}

func TestGaps_UnitTakesTheWorstContainerAndIsWeakIfOneIs(t *testing.T) {
	declared := []Service{declaredCompose("svc_1", "m1", "web", "app")}
	declared[0].Units[0].Image, declared[0].Units[0].Version = "app", "1"
	good := attached(composeContainer("m1", "web-app-1", "web", "app"), "unit_web_app", container.MatchCompose)
	odd := attached(composeContainer("m1", "web-app-2", "web", "app"), "unit_web_app", container.MatchName)
	odd.Image = "app:2"

	unit := Gaps(declared, []container.Container{odd, good}, nil, nil).Services[0].Units[0]

	if unit.State != GapDiverged || !unit.Weak {
		t.Fatalf("unit = %+v", unit)
	}
	if unit.Containers[0].Name != "web-app-1" || unit.Containers[1].Name != "web-app-2" || unit.Containers[1].Verdict != GapDiverged {
		t.Fatalf("containers = %+v", unit.Containers)
	}
	if !Gaps(declared, []container.Container{odd, good}, nil, nil).Services[0].Weak {
		t.Fatal("le service est faible si l'une de ses unités l'est")
	}
}

func TestGaps_UndeclaredGroupedByKeyAndIgnoredFlagged(t *testing.T) {
	declared := []Service{declaredCompose("svc_1", "m1", "nextcloud", "app")}
	blogWeb := composeContainer("m1", "blog-web-1", "blog", "web")
	blogDB := composeContainer("m1", "blog-db-1", "blog", "db")
	whoami := aloneContainer("m1", "whoami")
	elsewhere := aloneContainer("m2", "whoami")
	gone := composeContainer("m1", "old-1", "old", "app")
	gone.ArchivedAt = time.Unix(1_800_000_000, 0)
	ignored := []IgnoredKey{{MachineID: "m1", Kind: IgnoreContainerName, Value: "whoami"}}

	report := Gaps(declared, []container.Container{whoami, blogWeb, elsewhere, blogDB, gone}, nil, ignored)

	if len(report.Undeclared) != 3 {
		t.Fatalf("undeclared = %+v", report.Undeclared)
	}
	blog, onM1, onM2 := report.Undeclared[0], report.Undeclared[1], report.Undeclared[2]
	if blog.Key != (IgnoredKey{MachineID: "m1", Kind: IgnoreComposeProject, Value: "blog"}) || blog.Ignored || blog.ServiceID != "" {
		t.Fatalf("blog = %+v", blog)
	}
	if names := containerNames(blog.Containers); !slices.Equal(names, []string{"blog-db-1", "blog-web-1"}) {
		t.Fatalf("les conteneurs d'un projet sont par nom: %v", names)
	}
	if onM1.Key.Kind != IgnoreContainerName || onM1.Key.Value != "whoami" || !onM1.Ignored {
		t.Fatalf("whoami de m1 = %+v", onM1)
	}
	if onM2.MachineID != "m2" || onM2.Ignored {
		t.Fatalf("une clé ignorée sur une machine ne vaut pas pour une autre: %+v", onM2)
	}
}

func containerNames(containers []container.Container) []string {
	names := make([]string, 0, len(containers))
	for _, item := range containers {
		names = append(names, item.Name)
	}
	return names
}

// nextcloud-db tourne dans un projet déclaré, sans unité qui le nomme :
// « Adopter » le met dans ce service.
func TestGaps_UndeclaredInDeclaredProjectPointsToIt(t *testing.T) {
	declared := []Service{nextcloudWithRedis()}
	web := attached(composeContainer("m1", "nextcloud-1", "nextcloud", "nextcloud"), "unit_nextcloud_nextcloud", container.MatchCompose)
	db := composeContainer("m1", "nextcloud-db-1", "nextcloud", "db")
	sameNameElsewhere := composeContainer("m2", "nextcloud-db-1", "nextcloud", "db")

	report := Gaps(declared, []container.Container{web, db, sameNameElsewhere}, nil, nil)

	if len(report.Undeclared) != 2 {
		t.Fatalf("undeclared = %+v", report.Undeclared)
	}
	if report.Undeclared[0].ServiceID != "svc_1" || report.Undeclared[0].Containers[0].ID != db.ID {
		t.Fatalf("m1 = %+v", report.Undeclared[0])
	}
	if report.Undeclared[1].ServiceID != "" {
		t.Fatalf("le projet déclaré sur m1 ne vaut pas pour m2: %+v", report.Undeclared[1])
	}
}

// Un conteneur dont le label ne vise rien reste non déclaré, avec le motif :
// même s'il a un projet Compose déclaré, il ne s'y rattache pas.
func TestGaps_OrphanLabelIsUndeclaredWithItsProblem(t *testing.T) {
	declared := []Service{nextcloudWithRedis()}
	typo := labelledTo(composeContainer("m1", "nextcloud-1", "nextcloud", "nextcloud"), "svc_inconnu", "nextcloud")
	typo.Image = "nextcloud:29.0.4"

	report := Gaps(declared, []container.Container{typo}, nil, nil)

	if report.Services[0].Units[0].State != GapAbsent {
		t.Fatalf("l'unité Compose reste absente, le label ne se replie pas : %+v", report.Services[0])
	}
	if len(report.Undeclared) != 1 {
		t.Fatalf("undeclared = %+v", report.Undeclared)
	}
	group := report.Undeclared[0]
	if group.LabelProblems[typo.ID] != LabelServiceUnknown || group.Containers[0].ID != typo.ID {
		t.Fatalf("group = %+v", group)
	}
}

// Adopter un conteneur étiqueté ne changerait rien : seul un groupe où l'un
// au moins n'a pas de label s'adopte.
func TestGaps_GroupOfOrphansIsNotAdoptable(t *testing.T) {
	orphanWeb := labelledTo(composeContainer("m1", "blog-web-1", "blog", "web"), "svc_inconnu", "web")
	orphanDB := labelledTo(composeContainer("m1", "blog-db-1", "blog", "db"), "svc_inconnu", "db")
	plain := composeContainer("m1", "blog-cache-1", "blog", "cache")
	alone := aloneContainer("m1", "whoami")

	onlyOrphans := Gaps(nil, []container.Container{orphanWeb, orphanDB, alone}, nil, nil).Undeclared
	if len(onlyOrphans) != 2 {
		t.Fatalf("undeclared = %+v", onlyOrphans)
	}
	blog, whoami := onlyOrphans[0], onlyOrphans[1]
	if blog.Key.Value != "blog" || blog.Adoptable || len(blog.LabelProblems) != 2 {
		t.Fatalf("blog = %+v", blog)
	}
	if whoami.Key.Value != "whoami" || !whoami.Adoptable || len(whoami.LabelProblems) != 0 {
		t.Fatalf("whoami = %+v", whoami)
	}

	mixed := Gaps(nil, []container.Container{orphanWeb, plain}, nil, nil).Undeclared[0]
	if !mixed.Adoptable || len(mixed.LabelProblems) != 1 || mixed.LabelProblems[orphanWeb.ID] != LabelServiceUnknown {
		t.Fatalf("mixed = %+v", mixed)
	}
}
