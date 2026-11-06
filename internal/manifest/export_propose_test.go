package manifest_test

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ldesfontaine/opencloud/internal/container"
	"github.com/ldesfontaine/opencloud/internal/directory"
	"github.com/ldesfontaine/opencloud/internal/goldentest"
	"github.com/ldesfontaine/opencloud/internal/manifest"
)

var proposeAnnotations = manifest.Annotations{
	Header:   testAnnotations.Header,
	Proposed: "Proposé depuis des conteneurs non déclarés : relisez avant apply.",
}

func (b *bench) exportPropose(t *testing.T, options manifest.ExportOptions) []byte {
	t.Helper()
	data, err := b.manifest.Export(context.Background(), proposeAnnotations, options)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func dockerID(digit string) string { return strings.Repeat(digit, 64) }

func observed(id, name, project, composeService, image string) container.Observed {
	return container.Observed{
		ContainerID: id, Name: name, Group: project, ComposeService: composeService, Image: image,
		State: container.StateRunning, CreatedAt: testNow.Add(-time.Hour),
	}
}

// seedUndeclared montre ce que les machines font tourner sans que rien le
// déclare : un projet Compose et un conteneur seul sur app-1, un service
// Compose de plus dans le projet déclaré « wiki », un conteneur sur la machine
// openCloud ; et ce qui ne se propose pas : les clés ignorées du seed, un
// conteneur dont le label ne vise rien.
func (b *bench) seedUndeclared(t *testing.T) {
	t.Helper()
	ctx := context.Background()
	orphan := observed(dockerID("a"), "orphan", "", "", "nginx:1.27")
	orphan.Labels = map[string]string{container.LabelService: "svc_0000000000000000", container.LabelUnit: "orphan"}
	report := container.Report{Engine: &container.EngineReport{Present: true}, Complete: true, Inventory: []container.Observed{
		observed(dockerID("1"), "blog-web-1", "blog", "web", "nginx:1.27"),
		observed(dockerID("2"), "blog-db-1", "blog", "db", "postgres:16.4"),
		observed(dockerID("3"), "front", "", "", "ghcr.io/example/front:3.2"),
		observed(dockerID("4"), "wiki-cache-1", "wiki", "cache", "redis:7.4"),
		observed(dockerID("5"), "scratch-web-1", "scratch", "web", "nginx"),
		observed(dockerID("6"), "tmp-debug", "", "", "busybox"),
		orphan,
	}}
	if err := b.containers.Record(ctx, remoteID, report); err != nil {
		t.Fatal(err)
	}
	local := container.Report{Engine: &container.EngineReport{Present: true}, Complete: true, Inventory: []container.Observed{
		observed(dockerID("7"), "another", "", "", "traefik/whoami:v1.10"),
		observed(dockerID("8"), "portainer", "", "", "portainer/portainer-ce:2.21"),
	}}
	if err := b.containers.Record(ctx, b.localID, local); err != nil {
		t.Fatal(err)
	}
}

// Sans -propose, l'export est l'annuaire et rien d'autre, octet pour octet :
// même quand les machines montrent de quoi proposer.
func TestExport_WithoutProposeIsUnchanged(t *testing.T) {
	plain := newBench(t)
	plain.seed(t, false)
	want := plain.export(t)
	goldentest.Compare(t, filepath.Join("testdata", "export.golden.yaml"), string(want))

	b := newBench(t)
	b.seed(t, false)
	b.seedUndeclared(t)
	if got := b.exportPropose(t, manifest.ExportOptions{}); string(got) != string(want) {
		t.Fatalf("l'export sans propositions a changé :\n%s", got)
	}
	if got := b.export(t); string(got) != string(want) {
		t.Fatalf("l'export avec les annotations de la 4a a changé :\n%s", got)
	}
}

// Une proposition nouvelle est une entrée sans identifiant, à la fin de la
// liste des services, triée par machine puis par nom. Les clés ignorées et
// les labels orphelins n'en font pas.
func TestExport_ProposeAppendsUndeclaredWithoutIDs(t *testing.T) {
	b := newBench(t)
	b.seed(t, false)
	b.seedUndeclared(t)
	data := b.exportPropose(t, manifest.ExportOptions{Propose: true})
	goldentest.Compare(t, filepath.Join("testdata", "export_propose.golden.yaml"), string(data))

	file, problems := manifest.Parse(data)
	if len(problems) > 0 {
		t.Fatalf("le fichier proposé ne se relit pas : %+v", problems)
	}
	var added []string
	for _, item := range file.Services {
		if item.ID == "" {
			added = append(added, item.Machine+"/"+item.Name)
		}
	}
	want := []string{"app-1/cache", "app-1/db", "app-1/front", "app-1/web", "opencloud/another"}
	if strings.Join(added, ",") != strings.Join(want, ",") {
		t.Fatalf("propositions = %v, want %v", added, want)
	}
	last := file.Services[len(file.Services)-len(want):]
	for index, item := range last {
		if item.ID != "" || item.Machine+"/"+item.Name != want[index] {
			t.Fatalf("les propositions doivent finir la liste, dans l'ordre : %+v", last)
		}
	}
	// Un service Compose par entrée : db et web sont deux entrées du projet blog.
	db, web := last[1], last[3]
	if db.Origin != directory.OriginCompose || db.ComposeProject != "blog" || len(db.Units) != 1 || db.Units[0].Name != "db" || db.Units[0].Updates != container.PolicyWatch {
		t.Fatalf("db = %+v", db)
	}
	if web.ComposeProject != "blog" || len(web.Units) != 1 || web.Units[0].Name != "web" {
		t.Fatalf("web = %+v", web)
	}
	text := string(data)
	// Les clés ignorées figurent dans la section « ignored », pas dans les services.
	services := text[strings.Index(text, "services:"):strings.Index(text, "probes:")]
	for _, absent := range []string{"scratch", "tmp-debug", "portainer", "orphan"} {
		if strings.Contains(services, absent) {
			t.Errorf("%q ne doit pas être proposé :\n%s", absent, services)
		}
	}
	if strings.Count(text, proposeAnnotations.Proposed) != 5 {
		t.Errorf("chaque nouvelle entrée porte son commentaire :\n%s", text)
	}
}

// Le service Compose qui manque à un projet déjà déclaré est une entrée à
// part ; l'entrée du service déclaré n'est pas touchée.
func TestExport_ProposeLeavesTheDeclaredEntryAlone(t *testing.T) {
	b := newBench(t)
	b.seed(t, false)
	b.seedUndeclared(t)
	data := b.exportPropose(t, manifest.ExportOptions{Propose: true})

	file, problems := manifest.Parse(data)
	if len(problems) > 0 {
		t.Fatal(problems)
	}
	var wiki manifest.Service
	var cache manifest.Service
	for _, item := range file.Services {
		if item.ID == "svc_00000000000000b1" {
			wiki = item
		}
		if item.Name == "cache" {
			cache = item
		}
	}
	names := []string{}
	for _, unit := range wiki.Units {
		names = append(names, unit.Name)
	}
	if strings.Join(names, ",") != "db,app" {
		t.Fatalf("units = %v : l'entrée déclarée ne gagne aucune unité", names)
	}
	if cache.ID != "" || cache.ComposeProject != "wiki" || len(cache.Units) != 1 || cache.Units[0].Image != "redis" || cache.Units[0].Version != "7.4" || cache.Units[0].Updates != container.PolicyWatch {
		t.Fatalf("cache = %+v", cache)
	}
}

// Le fichier proposé est un fichier comme un autre : apply le juge applicable,
// crée ce qui est proposé et rien de plus, et un second export ne propose plus
// rien (les passages convergent).
func TestExport_ProposedFileGivesAnApplicablePlan(t *testing.T) {
	b := newBench(t)
	b.seed(t, false)
	b.seedUndeclared(t)
	text := string(b.exportPropose(t, manifest.ExportOptions{Propose: true}))

	plan := b.plan(t, text, false)

	if len(plan.Problems) > 0 || !plan.Applicable() {
		t.Fatalf("le plan du fichier proposé n'est pas applicable : %+v", plan.Problems)
	}
	want := []string{"create service/app-1/cache", "create service/app-1/db", "create service/app-1/front", "create service/app-1/web", "create service/opencloud/another"}
	got := changeLabels(plan)
	if len(got) != len(want) {
		t.Fatalf("changes = %v, want %v", got, want)
	}
	for _, label := range want {
		if !containsString(got, label) {
			t.Errorf("le plan ne porte pas %q : %v", label, got)
		}
	}
	if _, _, deleted := plan.Counts(); deleted != 0 {
		t.Errorf("le plan supprime %d objets", deleted)
	}

	applied, _ := b.apply(t, text, false)
	if applied.State != manifest.ApplyApplied || applied.Created != 5 || applied.Updated != 0 {
		t.Fatalf("apply = %+v", applied)
	}
	// Les conteneurs sont rattachés par Compose et par le nom : plus rien à proposer.
	again := b.exportPropose(t, manifest.ExportOptions{Propose: true})
	if strings.Contains(string(again), proposeAnnotations.Proposed) {
		t.Fatalf("un second passage propose encore :\n%s", again)
	}
}

func containsString(list []string, want string) bool {
	for _, item := range list {
		if item == want {
			return true
		}
	}
	return false
}
