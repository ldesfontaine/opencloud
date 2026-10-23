package manifest_test

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ldesfontaine/opencloud/internal/directory"
	"github.com/ldesfontaine/opencloud/internal/heartbeat"
	"github.com/ldesfontaine/opencloud/internal/manifest"
)

var testAnnotations = manifest.Annotations{
	Header: "Annuaire exporté.\nLes identifiants rattachent chaque entrée à son objet : gardez-les.",
}

func (b *bench) export(t *testing.T) []byte {
	t.Helper()
	data, err := b.manifest.Export(context.Background(), testAnnotations, manifest.ExportOptions{})
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestExport_IsDeterministicAndSorted(t *testing.T) {
	first, second := newBench(t), newBench(t)
	first.seed(t, false)
	second.seed(t, true)

	again := first.export(t)
	if string(again) != string(first.export(t)) {
		t.Fatal("two exports of the same database differ")
	}
	if string(again) != string(second.export(t)) {
		t.Fatalf("the insertion order changes the file:\n%s\n----\n%s", again, second.export(t))
	}
	compareGolden(t, filepath.Join("testdata", "export.golden.yaml"), again)

	file, problems := manifest.Parse(again)
	if len(problems) > 0 {
		t.Fatalf("the export does not parse: %+v", problems)
	}
	var machines, services, probes, heartbeats []string
	for _, item := range file.Machines {
		machines = append(machines, item.Name)
	}
	for _, item := range file.Services {
		services = append(services, item.Machine+"/"+item.Name)
	}
	for _, item := range file.Probes {
		probes = append(probes, item.Name+" "+item.ID)
	}
	for _, item := range file.Heartbeats {
		heartbeats = append(heartbeats, item.Name)
	}
	want := map[string][]string{
		"machines":   {"app-1", "opencloud"},
		"services":   {"app-1/backup", "app-1/proxy", "app-1/wiki", "opencloud/legacy"},
		"probes":     {"api-post 00000000000000c5", "mail-imaps 00000000000000c2", "twin 00000000000000c3", "twin 00000000000000c4", "wiki-http 00000000000000c1"},
		"heartbeats": {"certbot", "wiki-dump"},
	}
	got := map[string][]string{"machines": machines, "services": services, "probes": probes, "heartbeats": heartbeats}
	for name, expected := range want {
		if strings.Join(got[name], ",") != strings.Join(expected, ",") {
			t.Errorf("%s: %v, want %v", name, got[name], expected)
		}
	}
	proxy := file.Services[1]
	if len(proxy.Provides) != 2 || proxy.Provides[0] != "proxy" || proxy.Provides[1] != "tls" || strings.Join(proxy.Projects, ",") != "docs,infra" {
		t.Errorf("provides and projects must be in a fixed order: %+v", proxy)
	}
}

func TestExport_QuotesWhatWouldNotStayAString(t *testing.T) {
	b := newBench(t)
	b.seed(t, false)
	text := string(b.export(t))
	for _, want := range []string{
		`version: "16.4"`,                // un nombre à virgule
		`version: "2.4.1"`,               // une version est toujours guillemetée
		`ports: ["8080:3000/tcp", "80"]`, // un port nu est un entier
	} {
		if !strings.Contains(text, want) {
			t.Errorf("export lacks %q:\n%s", want, text)
		}
	}
	// Le piège de la relecture : tout reste du texte une fois relu.
	file, problems := manifest.Parse([]byte(text))
	if len(problems) > 0 {
		t.Fatal(problems)
	}
	if wiki := file.Services[2]; wiki.Name != "wiki" || wiki.Units[0].Version != "16.4" || wiki.Units[1].Ports[1] != "80" {
		t.Fatalf("services: %+v", file.Services)
	}
}

func TestEncode_QuotesBooleansAndNumbersWrittenAsText(t *testing.T) {
	file := manifest.File{Services: []manifest.Service{{
		Machine: "m", Name: "s", Title: "true", Nature: "application", Origin: "container",
		Units: []manifest.Unit{{Name: "u", Version: "1", Image: "x", Ports: []string{"53/udp", "8080:80"}}},
	}}}
	data, err := manifest.Encode(file, manifest.Annotations{})
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, want := range []string{`title: "true"`, `version: "1"`, `ports: ["53/udp", "8080:80"]`} {
		if !strings.Contains(text, want) {
			t.Errorf("lacks %q:\n%s", want, text)
		}
	}
	back, problems := manifest.Parse(data)
	if len(problems) > 0 || back.Services[0].Title != "true" || back.Services[0].Units[0].Version != "1" {
		t.Fatalf("not a string again: %+v %+v", back, problems)
	}
}

func TestExport_WritesNonEmptyDefaultsExplicitly(t *testing.T) {
	b := newBench(t)
	b.seed(t, false)
	file, problems := manifest.Parse(b.export(t))
	if len(problems) > 0 {
		t.Fatal(problems)
	}
	for _, item := range file.Probes {
		if item.Interval == 0 || item.Timeout == 0 || item.FailureThreshold == 0 || item.RecoveryThreshold == 0 || item.Runner == "" {
			t.Errorf("%s: a default is missing from the file: %+v", item.Name, item)
		}
	}
	text := string(b.export(t))
	for _, want := range []string{"    method: GET\n", "    expected_status: 2xx\n", "    failure_threshold: 3\n", "    recovery_threshold: 2\n"} {
		if !strings.Contains(text, want) {
			t.Errorf("export lacks %q", want)
		}
	}
	// Ce qui n'a pas de sens pour le genre de la sonde n'est pas écrit.
	for _, item := range file.Probes {
		if item.Kind == "tcp" && (item.Method != "" || item.ExpectedStatus != "" || item.FollowRedirects) {
			t.Errorf("%s: HTTP fields on a tcp probe: %+v", item.Name, item)
		}
		if item.Kind == "http" && item.TLS {
			t.Errorf("%s: tls on an http probe", item.Name)
		}
	}
	// Et le vide à défaut vide reste absent.
	if strings.Contains(text, "follow_redirects: false") || strings.Contains(text, "tls: false") {
		t.Error("a false flag must not be written")
	}
}

func TestExport_BinaryUnitsHaveNoPolicyAndContainersAlwaysDo(t *testing.T) {
	b := newBench(t)
	b.seed(t, false)
	file, problems := manifest.Parse(b.export(t))
	if len(problems) > 0 {
		t.Fatal(problems)
	}
	for _, service := range file.Services {
		for _, unit := range service.Units {
			hasPolicy := unit.Updates != ""
			if (service.Origin == "binary") == hasPolicy {
				t.Errorf("%s/%s: updates %q for origin %s", service.Name, unit.Name, unit.Updates, service.Origin)
			}
		}
	}
	if file.Services[2].Units[0].Updates != "hold" || file.Services[1].Units[0].Updates != "ignore" {
		t.Errorf("policies lost: %+v", file.Services)
	}
}

func TestExport_NeverWritesASecret(t *testing.T) {
	b := newBench(t)
	b.seed(t, false)
	text := string(b.export(t))
	secrets := []string{
		pingSecret, "hb_bbbbbbbbbbbbbbbbbbbbbbbbbb", // gitleaks:allow -- faux secret de test
		heartbeat.HashToken(pingSecret), heartbeat.HashToken("hb_bbbbbbbbbbbbbbbbbbbbbbbbbb"), // gitleaks:allow -- faux secret de test
		"hb_", "/ping/",
	}
	for _, secret := range secrets {
		if strings.Contains(text, secret) {
			t.Errorf("the export leaks %q", secret)
		}
	}
	if strings.Count(text, "token: {secret: kept}") != 2 {
		t.Errorf("each job must carry the marker:\n%s", text)
	}
}

func TestExport_ParseGivesBackTheSameFile(t *testing.T) {
	b := newBench(t)
	b.seed(t, false)
	annotations := testAnnotations
	data, err := b.manifest.Export(context.Background(), annotations, manifest.ExportOptions{})
	if err != nil {
		t.Fatal(err)
	}
	file, problems := manifest.Parse(data)
	if len(problems) > 0 {
		t.Fatalf("problems: %+v", problems)
	}
	again, err := manifest.Encode(file, annotations)
	if err != nil {
		t.Fatal(err)
	}
	if string(again) != string(data) {
		t.Fatalf("export, parse, encode differ:\n%s\n----\n%s", data, again)
	}
	// Chaque entrée porte l'id que l'export a écrit.
	for _, item := range file.Services {
		if !strings.HasPrefix(item.ID, "svc_") {
			t.Errorf("service %s has no id", item.Name)
		}
	}
	for _, item := range file.Probes {
		if item.ID == "" {
			t.Errorf("probe %s has no id", item.Name)
		}
	}
	for _, item := range file.Heartbeats {
		if item.ID == "" {
			t.Errorf("job %s has no id", item.Name)
		}
	}
	for _, item := range file.Projects {
		if !strings.HasPrefix(item.ID, "prj_") {
			t.Errorf("project %s has no id", item.Name)
		}
	}
}

func TestExport_StartsWithItsHeader(t *testing.T) {
	b := newBench(t)
	b.seed(t, false)
	text := string(b.export(t))
	if !strings.HasPrefix(text, "# Annuaire exporté.\n# Les identifiants") {
		t.Fatalf("header missing:\n%s", text)
	}
}

func TestExport_AnEmptyDirectoryWritesEverySection(t *testing.T) {
	b := newBench(t)
	text := string(b.export(t))
	for _, want := range []string{"version: 1\n", "projects: []\n", "services: []\n", "probes: []\n", "heartbeats: []\n", "ignored: []\n", "  - name: app-1\n", "  - name: opencloud\n"} {
		if !strings.Contains(text, want) {
			t.Errorf("lacks %q:\n%s", want, text)
		}
	}
	if _, problems := manifest.Parse([]byte(text)); len(problems) > 0 {
		t.Fatal(problems)
	}
}

func TestEncode_QuotesWhatOtherYAMLReadersWouldMisread(t *testing.T) {
	// « << » est la clé de fusion, « yes » et « 1:30 » un booléen et un nombre
	// en YAML 1.1 : tous trois s'écrivent entre guillemets et se relisent texte.
	values := []string{"<<", "yes", "No", "ON", "off", "y", "N", "1:30", "-1:30:15", "+0:5.5"}
	for _, value := range values {
		t.Run(value, func(t *testing.T) {
			file := manifest.File{Projects: []manifest.Project{{ID: "prj_00000000000000a1", Name: "docs", Title: value}}}
			data, err := manifest.Encode(file, manifest.Annotations{})
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(data), `title: "`+value+`"`) {
				t.Fatalf("not quoted:\n%s", data)
			}
			back, problems := manifest.Parse(data)
			if len(problems) > 0 || back.Projects[0].Title != value {
				t.Fatalf("does not read back: %+v %+v", back, problems)
			}
			again, err := manifest.Encode(back, manifest.Annotations{})
			if err != nil || string(again) != string(data) {
				t.Fatalf("round trip differs: %v\n%s\n----\n%s", err, data, again)
			}
		})
	}
	// Ce qui n'est qu'un mot reste nu.
	data, err := manifest.Encode(manifest.File{Projects: []manifest.Project{{Name: "docs", Title: "maybe 1:30 pm"}}}, manifest.Annotations{})
	if err != nil || !strings.Contains(string(data), "title: maybe 1:30 pm") {
		t.Fatalf("over-quoted: %v\n%s", err, data)
	}
}

func TestExport_FailsClearlyWhenTheFileWouldNotReadBack(t *testing.T) {
	b := newBench(t)
	ctx := context.Background()
	filler := strings.Repeat("x", 1000)
	for i := 0; i < manifest.MaxFileBytes/1000+20; i++ {
		id := fmt.Sprintf("prj_%016x", i+1)
		if err := b.db.InsertProject(ctx, directory.Project{ID: id, Name: "p" + id, Title: filler, CreatedAt: testNow, UpdatedAt: testNow}); err != nil {
			t.Fatal(err)
		}
	}
	data, err := b.manifest.Export(ctx, manifest.Annotations{}, manifest.ExportOptions{})
	if !errors.Is(err, manifest.ErrExportTooLarge) || data != nil {
		t.Fatalf("got %d bytes, %v", len(data), err)
	}
}
