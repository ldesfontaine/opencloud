package manifest_test

import (
	"context"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/ldesfontaine/opencloud/internal/directory"
	"github.com/ldesfontaine/opencloud/internal/manifest"
	"github.com/ldesfontaine/opencloud/internal/probe"
)

// replaceOnce édite l'export : le texte cherché doit y être, une seule fois.
func replaceOnce(t *testing.T, text, old, replacement string) string {
	t.Helper()
	if strings.Count(text, old) != 1 {
		t.Fatalf("%q must appear once in:\n%s", old, text)
	}
	return strings.Replace(text, old, replacement, 1)
}

func seeded(t *testing.T) (*bench, string) {
	t.Helper()
	b := newBench(t)
	b.seed(t, false)
	return b, string(b.export(t))
}

func hasProblem(plan manifest.Plan, key string) bool {
	return slices.Contains(problemKeys(plan), key)
}

// requireRefused vérifie qu'un fichier est jugé non applicable, avec la clé
// attendue, et que rien n'a été écrit, même en essayant d'appliquer.
func (b *bench) requireRefused(t *testing.T, text string, prune bool, key string) manifest.Plan {
	t.Helper()
	before := b.snapshot(t)
	plan := b.plan(t, text, prune)
	if plan.Applicable() || !hasProblem(plan, key) {
		t.Fatalf("want a refusal %s, got problems %v and changes %v", key, problemKeys(plan), changeLabels(plan))
	}
	_, _, err := b.tryApply(text, prune)
	if err != manifest.ErrNotApplicable {
		t.Fatalf("apply of a refused plan: %v", err)
	}
	if calls := b.writes.written(); len(calls) > 0 {
		t.Fatalf("a refused plan wrote: %v", calls)
	}
	if !reflect.DeepEqual(before, b.snapshot(t)) {
		t.Fatal("a refused plan moved the directory")
	}
	return plan
}

func (b *bench) probeByID(t *testing.T, id string) probe.Probe {
	t.Helper()
	found, err := b.db.GetProbe(context.Background(), id)
	if err != nil {
		t.Fatalf("probe %s: %v", id, err)
	}
	return found
}

func (b *bench) serviceByID(t *testing.T, id string) directory.Service {
	t.Helper()
	found, err := b.db.GetDeclaredService(context.Background(), id)
	if err != nil {
		t.Fatalf("service %s: %v", id, err)
	}
	return found
}

func (b *bench) addProbeResult(t *testing.T, probeID string) {
	t.Helper()
	_, err := b.db.ApplyProbeResults(context.Background(), probe.Changes{Now: testNow, Results: []probe.Result{
		{ProbeID: probeID, CheckedAt: testNow, Outcome: probe.OutcomeUp, DurationMs: 12},
	}})
	if err != nil {
		t.Fatal(err)
	}
}

func TestPlan_DuplicatedIDIsRefusedAndNothingWritten(t *testing.T) {
	b, data := seeded(t)
	// Le service « proxy » prend l'identifiant du service « wiki » ; l'autre
	// modification du fichier ne doit pas s'écrire non plus.
	data = replaceOnce(t, data, "id: svc_00000000000000b2", "id: svc_00000000000000b1")
	data = replaceOnce(t, data, "public_url: https://wiki.example.org", "public_url: https://wiki.example.net")
	plan := b.requireRefused(t, data, false, manifest.KeyIDDuplicate)
	for _, problem := range plan.Problems {
		if problem.Key == manifest.KeyIDDuplicate && problem.Line == 0 {
			t.Errorf("the refusal must point at a line: %+v", problem)
		}
	}
}

func TestPlan_UnknownIDIsRefused(t *testing.T) {
	b, data := seeded(t)
	for _, replacement := range [][2]string{
		{"id: prj_00000000000000a2", "id: prj_ffffffffffffffff"},
		{"id: svc_00000000000000b3", "id: svc_ffffffffffffffff"},
		{"id: 00000000000000c2", "id: ffffffffffffffff"},
		{"id: 00000000000000d2", "id: ffffffffffffffff"},
	} {
		t.Run(replacement[0], func(t *testing.T) {
			b.requireRefused(t, replaceOnce(t, data, replacement[0], replacement[1]), false, manifest.KeyIDUnknown)
		})
	}
}

// Renommer par l'identifiant garde l'objet : ses liens, son historique, son
// état. Une sonde renommée ne perd pas ses essais ; le service renommé reste
// celui que la sonde vise.
func TestPlan_RenameByIDKeepsTheObjectAndItsLinks(t *testing.T) {
	b, data := seeded(t)
	b.addProbeResult(t, "00000000000000c1")
	data = replaceOnce(t, data, "    name: wiki\n", "    name: wiki-docs\n")
	data = replaceOnce(t, data, "service: app-1/wiki\n", "service: app-1/wiki-docs\n")
	data = replaceOnce(t, data, "    name: wiki-http\n", "    name: wiki-health\n")

	plan := b.plan(t, data, false)
	if len(plan.Problems) > 0 {
		t.Fatalf("problems: %+v", plan.Problems)
	}
	if got := changeLabels(plan); !slices.Equal(got, []string{"update service/app-1/wiki-docs", "update probe/wiki-health"}) {
		t.Fatalf("changes: %v", got)
	}
	if plan.Unchanged != 14 || len(plan.Unmanaged) > 0 {
		t.Fatalf("unchanged %d, unmanaged %+v", plan.Unchanged, plan.Unmanaged)
	}
	for _, change := range plan.Changes {
		if change.Action == manifest.ActionUpdate && slices.Contains(change.Notes, manifest.NoteProbeResets) {
			t.Errorf("a rename does not reset the probe: %+v", change)
		}
	}
	applied, _ := b.apply(t, data, false)
	if applied.State != manifest.ApplyApplied || applied.Done != 2 || applied.Updated != 2 || applied.Created != 0 || applied.Deleted != 0 {
		t.Fatalf("apply: %+v", applied)
	}
	renamed := b.probeByID(t, "00000000000000c1")
	if renamed.Name != "wiki-health" || renamed.ServiceID != "svc_00000000000000b1" || renamed.Status != probe.StatusUp {
		t.Fatalf("probe: %+v", renamed)
	}
	results, err := b.db.ListProbeResults(context.Background(), "00000000000000c1", 10)
	if err != nil || len(results) != 1 {
		t.Fatalf("the history must stay with the probe: %v %v", results, err)
	}
	if service := b.serviceByID(t, "svc_00000000000000b1"); service.Name != "wiki-docs" || len(service.Units) != 2 {
		t.Fatalf("service: %+v", service)
	}
	// Et le fichier relu ne change plus rien.
	if again := b.plan(t, data, false); len(again.Changes) > 0 || len(again.Problems) > 0 {
		t.Fatalf("not converged: %v %+v", changeLabels(again), again.Problems)
	}
}

func TestPlan_NameWithoutIDMatchesExisting(t *testing.T) {
	b, data := seeded(t)
	for _, id := range []string{
		"  - id: prj_00000000000000a1\n    name: docs",
		"  - id: svc_00000000000000b1\n    machine: app-1\n    name: wiki",
		"  - id: 00000000000000c2\n    name: mail-imaps",
		"  - id: 00000000000000d1\n    name: wiki-dump",
	} {
		withoutID := replaceOnce(t, data, id, dropIDLine(id))
		plan := b.plan(t, withoutID, false)
		if len(plan.Problems) > 0 || len(plan.Changes) > 0 || plan.Unchanged != 16 {
			t.Errorf("an entry without id must match by name:\n%q\nproblems %+v changes %v unchanged %d", id, plan.Problems, changeLabels(plan), plan.Unchanged)
		}
	}
}

// dropIDLine retire la ligne « - id: … » d'une entrée en gardant le tiret sur
// la ligne suivante.
func dropIDLine(entry string) string {
	lines := strings.Split(entry, "\n")
	lines = lines[1:]
	lines[0] = "  - " + strings.TrimLeft(lines[0], " ")
	return strings.Join(lines, "\n")
}

func TestPlan_AmbiguousProbeNameNeedsTheID(t *testing.T) {
	b, data := seeded(t)
	// Deux « twin » : l'un réclamé par son identifiant, l'autre par son nom.
	oneID := replaceOnce(t, data, "  - id: 00000000000000c3\n    name: twin", "  - name: twin")
	if plan := b.plan(t, oneID, false); len(plan.Problems) > 0 || len(plan.Changes) > 0 {
		t.Fatalf("the unclaimed twin is the only candidate: %+v %v", plan.Problems, changeLabels(plan))
	}
	noID := replaceOnce(t, oneID, "  - id: 00000000000000c4\n    name: twin", "  - name: twin")
	b.requireRefused(t, noID, false, manifest.KeyNameAmbiguous)
}

func TestPlan_NameDuplicatedInTheFileIsRefused(t *testing.T) {
	b, data := seeded(t)
	data = replaceOnce(t, data, "  - id: prj_00000000000000a2\n    name: infra", "  - id: prj_00000000000000a2\n    name: docs")
	b.requireRefused(t, data, false, manifest.KeyNameDuplicate)
}

func TestPlan_ReferencesResolveAgainstTheTargetState(t *testing.T) {
	b, data := seeded(t)
	// Un projet, un service qui y entre et une sonde qui vise ce service :
	// rien n'existe encore, tout se désigne par son nom.
	data = replaceOnce(t, data, "services:\n", "  - name: ops\nservices:\n")
	data = replaceOnce(t, data, "probes:\n", `  - machine: app-1
    name: ops-tool
    nature: application
    origin: container
    projects: [ops]
    units:
      - name: ops-tool
        image: example/ops
        version: "1.0"
        container_name: ops-tool
probes:
`)
	data = replaceOnce(t, data, "heartbeats:\n", `  - name: ops-ping
    runner: app-1
    service: app-1/ops-tool
    kind: tcp
    target: ops:80
heartbeats:
`)
	plan := b.plan(t, data, false)
	if len(plan.Problems) > 0 {
		t.Fatalf("problems: %+v", plan.Problems)
	}
	if got := changeLabels(plan); !slices.Equal(got, []string{"create project/ops", "create service/app-1/ops-tool", "create probe/ops-ping"}) {
		t.Fatalf("the order must put what is referenced first: %v", got)
	}
	b.apply(t, data, false)
	probes, err := b.db.ListProbes(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	var created probe.Probe
	for _, item := range probes {
		if item.Name == "ops-ping" {
			created = item
		}
	}
	service := b.serviceByID(t, created.ServiceID)
	if service.Name != "ops-tool" || len(service.ProjectIDs) != 1 {
		t.Fatalf("the probe must point at the created service: %+v", service)
	}
	projects, _ := b.db.ListProjects(context.Background())
	if !slices.ContainsFunc(projects, func(p directory.Project) bool { return p.ID == service.ProjectIDs[0] && p.Name == "ops" }) {
		t.Fatalf("the service must be in the created project: %+v %+v", service.ProjectIDs, projects)
	}
}

func TestPlan_UnknownReferencesAreRefused(t *testing.T) {
	b, data := seeded(t)
	cases := map[string]struct {
		edit func(string) string
		key  string
	}{
		"machine of a service": {func(s string) string {
			return replaceOnce(t, s, "    machine: opencloud\n    name: legacy", "    machine: ghost\n    name: legacy")
		}, manifest.KeyMachineUnknown},
		"runner of a probe": {func(s string) string {
			return replaceOnce(t, s, "    name: mail-imaps\n    runner: app-1", "    name: mail-imaps\n    runner: ghost")
		}, manifest.KeyMachineUnknown},
		"service of a probe": {func(s string) string {
			return replaceOnce(t, s, "service: app-1/wiki\n", "service: app-1/ghost\n")
		}, manifest.KeyServiceUnknown},
		"project of a service": {func(s string) string {
			return replaceOnce(t, s, "    projects: [docs]\n", "    projects: [nowhere]\n")
		}, manifest.KeyProjectUnknown},
		"machine of an ignored key": {func(s string) string {
			return replaceOnce(t, s, "  - machine: opencloud\n    container_name: portainer", "  - machine: ghost\n    container_name: portainer")
		}, manifest.KeyMachineUnknown},
		"provides on an application": {func(s string) string {
			return replaceOnce(t, s, "    nature: application\n    origin: compose", "    nature: application\n    provides: [proxy]\n    origin: compose")
		}, "directory.provides_need_infra"},
		"negative threshold": {func(s string) string {
			return replaceOnce(t, s, "    name: wiki-http\n    runner: opencloud\n    service: app-1/wiki\n    kind: http\n    target: https://wiki.example.org/healthz\n    interval: 1m\n    timeout: 10s\n    failure_threshold: 3", "    name: wiki-http\n    runner: opencloud\n    service: app-1/wiki\n    kind: http\n    target: https://wiki.example.org/healthz\n    interval: 1m\n    timeout: 10s\n    failure_threshold: -1")
		}, "probe.threshold_invalid"},
		"zero interval": {func(s string) string {
			return replaceOnce(t, s, "    name: mail-imaps\n    runner: app-1\n    kind: tcp\n    target: mail.example.org:993\n    interval: 90s", "    name: mail-imaps\n    runner: app-1\n    kind: tcp\n    target: mail.example.org:993\n    interval: 0s")
		}, manifest.KeyZeroValue},
		"machine listed twice": {func(s string) string {
			return replaceOnce(t, s, "machines:\n  - name: app-1\n", "machines:\n  - name: app-1\n  - name: app-1\n")
		}, manifest.KeyNameDuplicate},
		"machine unknown in the list": {func(s string) string {
			return replaceOnce(t, s, "machines:\n  - name: app-1\n", "machines:\n  - name: ghost\n  - name: app-1\n")
		}, manifest.KeyMachineUnknown},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			plan := b.requireRefused(t, tc.edit(data), false, tc.key)
			for _, problem := range plan.Problems {
				if problem.Key == tc.key && problem.Line == 0 {
					t.Errorf("the refusal must point at a line: %+v", problem)
				}
			}
		})
	}
}
