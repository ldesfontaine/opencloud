package manifest

import (
	"strings"
	"testing"
	"time"

	"github.com/ldesfontaine/opencloud/internal/container"
	"github.com/ldesfontaine/opencloud/internal/directory"
	"github.com/ldesfontaine/opencloud/internal/probe"
)

// documented est l'exemple du format, tel que l'opérateur l'écrit à la main :
// sans id pour ce qu'il crée, avec des durées d'une seule unité.
const documented = `version: 1
machines:
  - name: app-1
  - name: opencloud
projects:
  - id: prj_4m8d1x2a9b3c4d5e
    name: docs
    title: Documentation
services:
  - id: svc_7f3k2q0a1b2c3d4e
    machine: app-1
    name: wiki
    title: Wiki
    nature: application
    origin: compose
    compose_project: wiki
    public_url: https://wiki.example.org
    projects: [docs]
    units:
      - name: app
        image: ghcr.io/example/wiki
        version: "2.4.1"
        ports: ["8080:3000/tcp"]
        updates: watch
  - machine: app-1
    name: proxy
    nature: infra
    origin: container
    provides: [proxy, tls]
    units:
      - name: proxy
        image: docker.io/library/traefik
        version: "3.1.2"
        container_name: traefik
        updates: hold
probes:
  - id: 3f9a0c1d2e3f4a5b
    name: wiki-http
    runner: opencloud
    service: app-1/wiki
    kind: http
    target: https://wiki.example.org/healthz
    interval: 1m
    timeout: 10s
    failure_threshold: 3
    recovery_threshold: 2
    method: GET
    expected_status: 2xx
    follow_redirects: true
heartbeats:
  - id: 8c2d4e6f8a0b1c2d
    name: wiki-dump
    machine: app-1
    every: 24h
    grace: 1h
    token: { secret: kept }
ignored:
  - machine: app-1
    compose_project: scratch
  - machine: app-1
    container_name: tmp-debug
`

func mustParse(t *testing.T, text string) File {
	t.Helper()
	file, problems := Parse([]byte(text))
	if len(problems) > 0 {
		t.Fatalf("problems: %+v", problems)
	}
	return file
}

func problemsOf(t *testing.T, text string) []Problem {
	t.Helper()
	_, problems := Parse([]byte(text))
	if len(problems) == 0 {
		t.Fatalf("expected a refusal for:\n%s", text)
	}
	return problems
}

func TestParse_ReadsTheDocumentedExample(t *testing.T) {
	file := mustParse(t, documented)
	if len(file.Machines) != 2 || file.Machines[1].Name != "opencloud" {
		t.Fatalf("machines: %+v", file.Machines)
	}
	if len(file.Projects) != 1 || file.Projects[0].ID != "prj_4m8d1x2a9b3c4d5e" || file.Projects[0].Title != "Documentation" {
		t.Fatalf("projects: %+v", file.Projects)
	}
	if len(file.Services) != 2 {
		t.Fatalf("services: %+v", file.Services)
	}
	wiki, proxy := file.Services[0], file.Services[1]
	if wiki.ID != "svc_7f3k2q0a1b2c3d4e" || wiki.Origin != directory.OriginCompose || wiki.ComposeProject != "wiki" ||
		len(wiki.Projects) != 1 || wiki.Projects[0] != "docs" || wiki.Line != 10 {
		t.Fatalf("wiki: %+v", wiki)
	}
	if len(wiki.Units) != 1 || wiki.Units[0].Version != "2.4.1" || wiki.Units[0].Ports[0] != "8080:3000/tcp" || wiki.Units[0].Updates != container.PolicyWatch {
		t.Fatalf("wiki units: %+v", wiki.Units)
	}
	if proxy.ID != "" || len(proxy.Provides) != 2 || proxy.Units[0].ContainerName != "traefik" || proxy.Units[0].Updates != container.PolicyHold {
		t.Fatalf("proxy: %+v", proxy)
	}
	if len(file.Probes) != 1 {
		t.Fatalf("probes: %+v", file.Probes)
	}
	item := file.Probes[0]
	if item.Kind != probe.KindHTTP || item.Interval != time.Minute || item.Timeout != 10*time.Second || item.FailureThreshold != 3 ||
		item.Service != "app-1/wiki" || !item.FollowRedirects {
		t.Fatalf("probe: %+v", item)
	}
	if len(file.Heartbeats) != 1 || file.Heartbeats[0].Every != 24*time.Hour || file.Heartbeats[0].Grace != time.Hour {
		t.Fatalf("heartbeats: %+v", file.Heartbeats)
	}
	if len(file.Ignored) != 2 || file.Ignored[0].Kind != directory.IgnoreComposeProject || file.Ignored[1].Kind != directory.IgnoreContainerName {
		t.Fatalf("ignored: %+v", file.Ignored)
	}
}

func TestParse_AbsentFieldsStayEmptyForTheDomainToDefault(t *testing.T) {
	file := mustParse(t, "version: 1\nprobes:\n  - name: bare\n    kind: tcp\n    target: db:5432\nservices:\n  - name: s\n    machine: m\n    units:\n      - name: u\n")
	bare := file.Probes[0]
	if bare.Interval != 0 || bare.Timeout != 0 || bare.FailureThreshold != 0 || bare.Method != "" {
		t.Fatalf("a missing field must stay empty so the domain gives its default: %+v", bare)
	}
	if file.Services[0].Units[0].Updates != "" {
		t.Fatalf("updates: %q", file.Services[0].Units[0].Updates)
	}
}

func TestParse_RefusesUnknownKeysWithTheirLine(t *testing.T) {
	cases := map[string]struct {
		text      string
		line      int
		fieldName string
	}{
		"racine":  {"version: 1\nservces: []\n", 2, "servces"},
		"service": {"version: 1\nservices:\n  - name: wiki\n    machin: app-1\n", 4, "machin"},
		"unité":   {"version: 1\nservices:\n  - name: wiki\n    units:\n      - name: app\n        imagee: x\n", 6, "imagee"},
		"sonde":   {"version: 1\nprobes:\n  - name: p\n    intervall: 1m\n", 4, "intervall"},
		"tâche":   {"version: 1\nheartbeats:\n  - name: j\n    evry: 1h\n", 4, "evry"},
		"clé":     {"version: 1\nignored:\n  - machine: m\n    container_name: c\n    other: x\n", 5, "other"},
	}
	for name, test := range cases {
		t.Run(name, func(t *testing.T) {
			problems := problemsOf(t, test.text)
			if problems[0].Key != KeyUnknownKey || problems[0].Line != test.line || problems[0].Value != test.fieldName {
				t.Fatalf("got %+v, want unknown_key line %d %q", problems[0], test.line, test.fieldName)
			}
		})
	}
}

func TestParse_ReportsEveryProblemNotOnlyTheFirst(t *testing.T) {
	problems := problemsOf(t, "version: 1\nservices:\n  - nam: a\n  - machin: b\n")
	if len(problems) != 2 || problems[0].Line != 3 || problems[1].Line != 4 {
		t.Fatalf("problems: %+v", problems)
	}
}

func TestParse_RefusesADuplicateKey(t *testing.T) {
	problems := problemsOf(t, "version: 1\nservices:\n  - name: a\n    name: b\n")
	if problems[0].Key != KeyDuplicateKey || problems[0].Line != 4 || problems[0].Value != "name" {
		t.Fatalf("problems: %+v", problems)
	}
}

func TestParse_RefusesUnsupportedSections(t *testing.T) {
	cases := map[string]struct {
		text string
		line int
		name string
	}{
		"status_pages": {"version: 1\nstatus_pages: []\n", 2, "status_pages"},
		"channels":     {"version: 1\nchannels: []\n", 2, "channels"},
		"backups":      {"version: 1\nbackups: []\n", 2, "backups"},
		"settings":     {"version: 1\nsettings: {}\n", 2, "settings"},
		"destinations": {"version: 1\nbackup_destinations: []\n", 2, "backup_destinations"},
		"protected":    {"version: 1\nservices:\n  - name: s\n    protected: true\n", 4, "protected"},
		"open_paths":   {"version: 1\nservices:\n  - name: s\n    open_paths: []\n", 4, "open_paths"},
		"eol_product":  {"version: 1\nservices:\n  - name: s\n    eol_product: debian\n", 4, "eol_product"},
		"task kind":    {"version: 1\nheartbeats:\n  - name: j\n    kind: backup\n", 4, "kind"},
		"task service": {"version: 1\nheartbeats:\n  - name: j\n    service: m/s\n", 4, "service"},
	}
	for name, test := range cases {
		t.Run(name, func(t *testing.T) {
			problems := problemsOf(t, test.text)
			if problems[0].Key != KeySectionUnsupported || problems[0].Line != test.line || problems[0].Value != test.name {
				t.Fatalf("got %+v", problems[0])
			}
		})
	}
}

func TestParse_RefusesAnotherVersionAndAliases(t *testing.T) {
	cases := map[string]struct {
		text string
		key  string
		line int
	}{
		"sans version":     {"machines: []\n", KeyVersionMissing, 1},
		"fichier vide":     {"", KeyVersionMissing, 1},
		"commentaire seul": {"# rien\n", KeyVersionMissing, 1},
		"version 2":        {"version: 2\n", KeyVersionUnsupported, 1},
		"version texte":    {"version: \"1\"\n", KeyTypeInvalid, 1},
		"ancre":            {"version: 1\nmachines:\n  - &m\n    name: a\n", KeyAliasNotAllowed, 3},
		"alias":            {"version: 1\nx: &a [1]\n", KeyAliasNotAllowed, 2},
		"deux documents":   {"version: 1\n---\nversion: 1\n", KeyDocumentMultiple, 3},
		"yaml cassé":       {"version: 1\nmachines: [\n", KeyYAMLInvalid, 2},
		"pas un bloc":      {"- a\n- b\n", KeyTypeInvalid, 1},
	}
	for name, test := range cases {
		t.Run(name, func(t *testing.T) {
			problems := problemsOf(t, test.text)
			if problems[0].Key != test.key {
				t.Fatalf("got %+v, want %s", problems[0], test.key)
			}
			if test.line != 0 && problems[0].Line != test.line {
				t.Fatalf("line %d, want %d (%+v)", problems[0].Line, test.line, problems[0])
			}
		})
	}
}

func TestParse_AnAliasAndAMergeKeyNeverExpandAnything(t *testing.T) {
	problems := problemsOf(t, "version: 1\nbase: &b {name: a}\nprobes:\n  - <<: *b\n")
	for _, problem := range problems {
		if problem.Key != KeyAliasNotAllowed {
			t.Fatalf("problem: %+v", problem)
		}
	}
}

func TestParse_DurationsNeedAUnitAndTheirGrain(t *testing.T) {
	probeWith := func(interval string) string {
		return "version: 1\nprobes:\n  - name: p\n    interval: " + interval + "\n"
	}
	heartbeatWith := func(every string) string {
		return "version: 1\nheartbeats:\n  - name: j\n    every: " + every + "\n"
	}
	valid := map[string]string{
		probeWith("90s"):     "90s",
		probeWith("1m"):      "1m",
		probeWith("1h30m"):   "1h30m",
		heartbeatWith("90m"): "90m",
		heartbeatWith("24h"): "24h",
		heartbeatWith("0s"):  "0s",
	}
	for text, name := range valid {
		t.Run("valide "+name, func(t *testing.T) { mustParse(t, text) })
	}
	invalid := map[string]string{
		"sans unité":        probeWith("60"),
		"nombre à virgule":  probeWith("1.5"),
		"fraction de s":     probeWith("1500ms"),
		"négative":          probeWith("-1m"),
		"texte":             probeWith("bientôt"),
		"vide d'unité":      probeWith("\"\""),
		"tâche en secondes": heartbeatWith("90s"),
		"tâche demi-minute": heartbeatWith("1m30s"),
		"tâche sans unité":  heartbeatWith("5"),
	}
	for name, text := range invalid {
		t.Run(name, func(t *testing.T) {
			problems := problemsOf(t, text)
			if problems[0].Key != KeyDurationInvalid || problems[0].Line != 4 {
				t.Fatalf("got %+v", problems[0])
			}
		})
	}
	problems := problemsOf(t, probeWith("[1, 2]"))
	if problems[0].Key != KeyTypeInvalid {
		t.Fatalf("a list is not a duration: %+v", problems[0])
	}
}

func TestParse_TypesMustBeWhatTheFieldSays(t *testing.T) {
	cases := map[string]string{
		"version non guillemetée": "version: 1\nservices:\n  - name: s\n    units:\n      - name: u\n        version: 16.4\n",
		"nom nombre":              "version: 1\nservices:\n  - name: 123\n",
		"booléen texte":           "version: 1\nservices:\n  - name: true\n",
		"seuil texte":             "version: 1\nprobes:\n  - name: p\n    failure_threshold: three\n",
		"seuil virgule":           "version: 1\nprobes:\n  - name: p\n    failure_threshold: 1.5\n",
		"drapeau texte":           "version: 1\nprobes:\n  - name: p\n    tls: \"yes\"\n",
		"liste attendue":          "version: 1\nservices:\n  - name: s\n    provides: tls\n",
		"entrée scalaire":         "version: 1\nmachines:\n  - app-1\n",
		"section scalaire":        "version: 1\nservices: none\n",
		"port nombre":             "version: 1\nservices:\n  - name: s\n    units:\n      - name: u\n        ports: [80]\n",
	}
	for name, text := range cases {
		t.Run(name, func(t *testing.T) {
			if problems := problemsOf(t, text); problems[0].Key != KeyTypeInvalid {
				t.Fatalf("got %+v", problems[0])
			}
		})
	}
}

func TestParse_EmptyKeysAreAbsentFields(t *testing.T) {
	file := mustParse(t, "version: 1\nprojects:\nservices:\n  - name: s\n    title:\n    provides:\n")
	if len(file.Projects) != 0 || file.Services[0].Title != "" || len(file.Services[0].Provides) != 0 {
		t.Fatalf("file: %+v", file)
	}
}

func TestParse_SecretMustBeKept(t *testing.T) {
	heartbeatWith := func(token string) string {
		return "version: 1\nheartbeats:\n  - name: j\n    every: 1h\n" + token
	}
	mustParse(t, heartbeatWith(""))
	mustParse(t, heartbeatWith("    token: { secret: kept }\n"))
	mustParse(t, heartbeatWith("    token:\n      secret: kept\n"))

	// Un scalaire est un jeton en clair : le fichier va dans Git.
	inFile := map[string]string{
		"jeton en clair":   "    token: hb_abcdefghijklmnopqrstuvwxyz\n",
		"jeton guillemeté": "    token: \"hb_abcdefghijklmnopqrstuvwxyz\"\n",
		"booléen":          "    token: true\n",
		"nombre":           "    token: 42\n",
	}
	for name, token := range inFile {
		t.Run(name, func(t *testing.T) {
			problems := problemsOf(t, heartbeatWith(token))
			if problems[0].Key != KeySecretInFile || problems[0].Line != 5 {
				t.Fatalf("got %+v, want secret_in_file on line 5", problems[0])
			}
		})
	}
	invalid := map[string]string{
		"valeur en clair":       "    token: { secret: hb_abcdefghijklmnopqrstuvwxyz }\n",
		"valeur autre que kept": "    token: { secret: regenerate }\n",
		"clé en plus":           "    token: { secret: kept, value: x }\n",
		"autre clé":             "    token: { value: kept }\n",
		"liste":                 "    token: [kept]\n",
		"vide":                  "    token:\n",
		"valeur absente":        "    token: { secret: }\n",
	}
	for name, token := range invalid {
		t.Run(name, func(t *testing.T) {
			problems := problemsOf(t, heartbeatWith(token))
			if problems[0].Key != KeySecretInvalid || problems[0].Line != 5 {
				t.Fatalf("got %+v, want secret_invalid on line 5", problems[0])
			}
		})
	}
}

func TestParse_IgnoredKeyNeedsExactlyOneKind(t *testing.T) {
	for name, entry := range map[string]string{
		"les deux": "  - machine: m\n    compose_project: a\n    container_name: b\n",
		"aucun":    "  - machine: m\n",
	} {
		t.Run(name, func(t *testing.T) {
			if problems := problemsOf(t, "version: 1\nignored:\n"+entry); problems[0].Key != KeyIgnoredInvalid || problems[0].Line != 3 {
				t.Fatalf("got %+v", problems[0])
			}
		})
	}
}

func TestParse_TooLargeIsRefused(t *testing.T) {
	big := "version: 1\n# " + strings.Repeat("x", MaxFileBytes) + "\n"
	problems := problemsOf(t, big)
	if len(problems) != 1 || problems[0].Key != KeyFileTooLarge || problems[0].Value != "256 Kio" {
		t.Fatalf("problems: %+v", problems)
	}
	at := "version: 1\n#" + strings.Repeat("x", MaxFileBytes-len("version: 1\n#")-1) + "\n"
	if len(at) != MaxFileBytes {
		t.Fatalf("fixture is %d bytes", len(at))
	}
	mustParse(t, at)
}

func TestParse_StopsReportingAfterTheLimit(t *testing.T) {
	text := "version: 1\nservices:\n" + strings.Repeat("  - nope: 1\n", 300)
	if problems := problemsOf(t, text); len(problems) != maxProblems {
		t.Fatalf("%d problems", len(problems))
	}
}

func TestProblem_ArgumentsMatchWhatTheKeyNeeds(t *testing.T) {
	line := Problem{Key: KeyUnknownKey, Line: 4, Value: "x"}.Arguments()
	if len(line) != 2 || line[0] != 4 || line[1] != "x" {
		t.Fatalf("line and value: %v", line)
	}
	if only := (Problem{Key: KeySecretInFile, Line: 6}).Arguments(); len(only) != 1 || only[0] != 6 {
		t.Fatalf("line only: %v", only)
	}
	if large := (Problem{Key: KeyFileTooLarge, Value: "256 Kio"}).Arguments(); len(large) != 1 || large[0] != "256 Kio" {
		t.Fatalf("value only: %v", large)
	}
}

func TestParse_BooleansAreOnlyTrueOrFalse(t *testing.T) {
	// La bibliothèque lit « True » et « TRUE » comme des booléens ; le format
	// n'admet que la forme que l'export écrit.
	for _, value := range []string{"True", "TRUE", "False", "FALSE"} {
		for _, key := range []string{"tls", "follow_redirects"} {
			t.Run(key+" "+value, func(t *testing.T) {
				problems := problemsOf(t, "version: 1\nprobes:\n  - name: p\n    "+key+": "+value+"\n")
				if problems[0].Key != KeyTypeInvalid || problems[0].Line != 4 || problems[0].Value != key {
					t.Fatalf("got %+v", problems[0])
				}
			})
		}
	}
	file := mustParse(t, "version: 1\nprobes:\n  - name: p\n    tls: true\n    follow_redirects: false\n")
	if !file.Probes[0].TLS || file.Probes[0].FollowRedirects {
		t.Fatalf("got %+v", file.Probes[0])
	}
}

func TestParse_RefusesCustomTagsOnBlocksListsAndDurations(t *testing.T) {
	cases := map[string]string{
		"bloc":         "version: 1\nservices: !custom\n  - name: s\n",
		"entrée":       "version: 1\nmachines:\n  - !custom {name: app-1}\n",
		"liste":        "version: 1\nservices:\n  - name: s\n    provides: !custom [tls]\n",
		"jeton":        "version: 1\nheartbeats:\n  - name: j\n    token: !custom {secret: kept}\n",
		"durée":        "version: 1\nprobes:\n  - name: p\n    interval: !custom 60s\n",
		"racine":       "version: 1\n--- !custom\n",
		"clé ignorée":  "version: 1\nignored: !custom\n  - machine: m\n    container_name: c\n",
		"unité (bloc)": "version: 1\nservices:\n  - name: s\n    units: !custom\n      - name: u\n",
	}
	for name, text := range cases {
		t.Run(name, func(t *testing.T) {
			if _, problems := Parse([]byte(text)); len(problems) == 0 {
				t.Fatalf("accepted:\n%s", text)
			}
		})
	}
}

func TestParse_IgnoredKeyNeedsItsMachine(t *testing.T) {
	problems := problemsOf(t, "version: 1\nignored:\n  - compose_project: scratch\n")
	if problems[0].Key != KeyIgnoredInvalid || problems[0].Line != 3 {
		t.Fatalf("got %+v", problems[0])
	}
}

func TestParse_YAMLErrorDoesNotEchoTheFile(t *testing.T) {
	problems := problemsOf(t, "version: 1\nmachines: *secret_anchor_name\n")
	if len(problems) != 1 || problems[0].Key != KeyYAMLInvalid || problems[0].Line != 2 {
		t.Fatalf("got %+v", problems)
	}
	if strings.Contains(problems[0].Value, "secret_anchor_name") {
		t.Fatalf("the anchor name leaks: %q", problems[0].Value)
	}
	if problems[0].Column != 0 {
		t.Fatalf("the library gives no column, got %d", problems[0].Column)
	}
}
