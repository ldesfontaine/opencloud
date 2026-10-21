package manifest_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/ldesfontaine/opencloud/internal/container"
	"github.com/ldesfontaine/opencloud/internal/directory"
	"github.com/ldesfontaine/opencloud/internal/heartbeat"
	"github.com/ldesfontaine/opencloud/internal/manifest"
	"github.com/ldesfontaine/opencloud/internal/probe"
	"github.com/ldesfontaine/opencloud/internal/refusal"
	"github.com/ldesfontaine/opencloud/internal/status"
	"github.com/ldesfontaine/opencloud/internal/store/storetest"
)

var testActor = manifest.Actor{Kind: manifest.ActorCLI, Name: "opencloud"}

// Un refus d'application porte la clé que le journal et les clients citent
// par sa constante : les deux écritures du même mot ne divergent pas.
func TestRefusals_CarryTheirKeyConstant(t *testing.T) {
	cases := map[error]string{
		manifest.ErrPlanChanged:    manifest.KeyPlanChanged,
		manifest.ErrNotApplicable:  manifest.KeyNotApplicable,
		manifest.ErrBusy:           manifest.KeyBusy,
		manifest.ErrNothingToApply: manifest.KeyNothingToApply,
		manifest.ErrExportTooLarge: manifest.KeyExportTooLarge,
	}
	for err, want := range cases {
		if key, ok := refusal.KeyOf(err); !ok || key != want {
			t.Errorf("%v: key %q, want %q", err, key, want)
		}
	}
}

func (b *bench) plan(t *testing.T, text string, prune bool) manifest.Plan {
	t.Helper()
	plan, err := b.manifest.Plan(context.Background(), manifest.Request{File: []byte(text), FileName: "opencloud.yaml", Prune: prune})
	if err != nil {
		t.Fatal(err)
	}
	return plan
}

// apply juge le fichier puis l'applique avec l'empreinte du plan qu'il vient
// de lire : ce que fait l'opérateur qui confirme.
func (b *bench) apply(t *testing.T, text string, prune bool) (manifest.Apply, manifest.Plan) {
	t.Helper()
	apply, plan, err := b.tryApply(text, prune)
	if err != nil {
		t.Fatalf("apply: %v\nproblems: %+v", err, plan.Problems)
	}
	return apply, plan
}

func (b *bench) tryApply(text string, prune bool) (manifest.Apply, manifest.Plan, error) {
	ctx := context.Background()
	plan, err := b.manifest.Plan(ctx, manifest.Request{File: []byte(text), FileName: "opencloud.yaml", Prune: prune})
	if err != nil {
		return manifest.Apply{}, plan, err
	}
	return b.manifest.Apply(ctx, manifest.Request{File: []byte(text), FileName: "opencloud.yaml", Prune: prune, PlanHash: plan.Hash}, testActor)
}

// snapshot est tout l'annuaire tel que la base le garde, dates comprises :
// deux photos identiques disent qu'aucune écriture n'a eu lieu.
type snapshot struct {
	Projects   []directory.Project
	Services   []directory.Service
	Probes     []probe.Probe
	Heartbeats []heartbeat.Heartbeat
	Ignored    []directory.IgnoredKey
}

func (b *bench) snapshot(t *testing.T) snapshot {
	t.Helper()
	ctx := context.Background()
	var shot snapshot
	var err error
	if shot.Projects, err = b.db.ListProjects(ctx); err != nil {
		t.Fatal(err)
	}
	if shot.Services, err = b.db.ListDeclaredServices(ctx, directory.Filter{}); err != nil {
		t.Fatal(err)
	}
	if shot.Probes, err = b.db.ListProbes(ctx, ""); err != nil {
		t.Fatal(err)
	}
	if shot.Heartbeats, err = b.db.ListHeartbeats(ctx); err != nil {
		t.Fatal(err)
	}
	if shot.Ignored, err = b.db.ListIgnoredKeys(ctx, ""); err != nil {
		t.Fatal(err)
	}
	return shot
}

func problemKeys(plan manifest.Plan) []string {
	keys := []string{}
	for _, problem := range plan.Problems {
		keys = append(keys, problem.Key)
	}
	return keys
}

func changeLabels(plan manifest.Plan) []string {
	labels := []string{}
	for _, change := range plan.Changes {
		labels = append(labels, string(change.Action)+" "+change.Label())
	}
	return labels
}

// L'aller-retour est le contrat du format : ce que l'export écrit, apply le
// relit sans rien changer. Plan vide, aucun geste d'écriture, aucune date qui
// bouge, rien « hors fichier ».
func TestRoundTrip_ExportThenApplyChangesNothing(t *testing.T) {
	b := newBench(t)
	b.seed(t, false)
	data := string(b.export(t))
	before := b.snapshot(t)

	plan := b.plan(t, data, false)
	if len(plan.Problems) > 0 || len(plan.Changes) > 0 || len(plan.Unmanaged) > 0 {
		t.Fatalf("the export must read back as a no-op:\nproblems %+v\nchanges %v\nunmanaged %+v", plan.Problems, changeLabels(plan), plan.Unmanaged)
	}
	// 2 projets, 4 services, 4 sondes, 2 tâches, 3 clés ignorées.
	if plan.Unchanged != 16 {
		t.Fatalf("unchanged %d, want 16", plan.Unchanged)
	}
	_, _, err := b.tryApply(data, false)
	if err != manifest.ErrNothingToApply {
		t.Fatalf("applying a no-op: %v", err)
	}
	// Avec --prune aussi : rien n'est hors du fichier.
	if pruned := b.plan(t, data, true); len(pruned.Changes) > 0 || len(pruned.Problems) > 0 {
		t.Fatalf("prune of nothing: %v %+v", changeLabels(pruned), pruned.Problems)
	}
	if calls := b.writes.written(); len(calls) > 0 {
		t.Fatalf("a no-op wrote: %v", calls)
	}
	if after := b.snapshot(t); !reflect.DeepEqual(before, after) {
		t.Fatal("the directory moved")
	}
	applies, err := b.manifest.Applies(context.Background(), 10)
	if err != nil || len(applies) != 0 {
		t.Fatalf("a no-op leaves no trace in the journal: %v %v", applies, err)
	}
	if strings.Contains(data, "hb_") {
		t.Fatal("test setup: the export carries a token")
	}
}

// cut retire d'un export le bloc qui va de from (inclus) à to (exclu).
func cut(t *testing.T, text, from, to string) string {
	t.Helper()
	start := strings.Index(text, from)
	if start < 0 {
		t.Fatalf("%q not in:\n%s", from, text)
	}
	end := strings.Index(text[start:], to)
	if end < 0 {
		t.Fatalf("%q not after %q in:\n%s", to, from, text)
	}
	return text[:start] + text[start+end:]
}

func (b *bench) heartbeatByID(t *testing.T, id string) heartbeat.Heartbeat {
	t.Helper()
	found, err := b.db.GetHeartbeat(context.Background(), id)
	if err != nil {
		t.Fatalf("heartbeat %s: %v", id, err)
	}
	return found
}

// Le fichier n'écrit pas d'identifiant d'unité : apply les retrouve par nom
// avant de modifier le service, sinon l'unité serait recréée et ses
// conteneurs lâchés.
func TestApply_UnitsMatchedByNameKeepIDsPolicyAndContainers(t *testing.T) {
	b, data := seeded(t)
	ctx := context.Background()
	err := b.containers.Record(ctx, remoteID, container.Report{
		Engine: &container.EngineReport{Present: true}, Complete: true,
		Inventory: []container.Observed{
			{ContainerID: strings.Repeat("1", 64), Name: "wiki-app-1", Group: "wiki", ComposeService: "app", Image: "ghcr.io/example/wiki:2.4.1", State: container.StateRunning, CreatedAt: testNow.Add(-time.Hour)},
			{ContainerID: strings.Repeat("2", 64), Name: "wiki-db-1", Group: "wiki", ComposeService: "db", Image: "docker.io/library/postgres:16.4", State: container.StateRunning, CreatedAt: testNow.Add(-time.Hour)},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := b.containers.Rematch(ctx, remoteID); err != nil {
		t.Fatal(err)
	}
	appID, dbID := container.ID(remoteID, strings.Repeat("1", 64)), container.ID(remoteID, strings.Repeat("2", 64))
	for id, unit := range map[string]string{appID: "unit_00000000000000a1", dbID: "unit_00000000000000a2"} {
		if found, err := b.containers.Get(ctx, id); err != nil || found.UnitID != unit {
			t.Fatalf("setup: the container %s must follow %s: %+v %v", id, unit, found, err)
		}
	}

	data = replaceOnce(t, data, `version: "2.4.1"`, `version: "2.5.0"`)
	data = replaceOnce(t, data, `version: "16.4"`, `version: "16.5"`)
	applied, plan := b.apply(t, data, false)
	if applied.State != manifest.ApplyApplied || len(plan.Changes) != 1 {
		t.Fatalf("apply: %+v / %v", applied, changeLabels(plan))
	}
	if !slices.Contains(b.writes.written(), "UpdateService wiki") {
		t.Fatalf("writes: %v", b.writes.written())
	}
	service := b.serviceByID(t, "svc_00000000000000b1")
	if len(service.Units) != 2 {
		t.Fatalf("units: %+v", service.Units)
	}
	for _, unit := range service.Units {
		switch unit.Name {
		case "app":
			if unit.ID != "unit_00000000000000a1" || unit.Version != "2.5.0" || unit.UpdatePolicy != container.PolicyWatch {
				t.Errorf("app: %+v", unit)
			}
		case "db":
			if unit.ID != "unit_00000000000000a2" || unit.Version != "16.5" || unit.UpdatePolicy != container.PolicyHold {
				t.Errorf("db: %+v", unit)
			}
		}
	}
	for id, unit := range map[string]string{appID: "unit_00000000000000a1", dbID: "unit_00000000000000a2"} {
		if found, err := b.containers.Get(ctx, id); err != nil || found.UnitID != unit {
			t.Errorf("the container %s lost its unit %s: %+v %v", id, unit, found, err)
		}
	}
}

func TestApply_SecretKeptLeavesTheTokenAlone(t *testing.T) {
	b, data := seeded(t)
	before := b.heartbeatByID(t, "00000000000000d1")
	data = replaceOnce(t, data, "    every: 24h\n", "    every: 12h\n")
	applied, plan := b.apply(t, data, false)
	if applied.State != manifest.ApplyApplied || len(plan.Changes) != 1 || !slices.Contains(plan.Changes[0].Notes, manifest.NoteHeartbeatDeadlineMoves) {
		t.Fatalf("apply: %+v / %+v", applied, plan.Changes)
	}
	after := b.heartbeatByID(t, "00000000000000d1")
	if after.Token != pingSecret || after.Token != before.Token || after.Interval != 12*time.Hour {
		t.Fatalf("the token must be the one the jobs already ping with: %+v", after)
	}
	if slices.Contains(plan.Changes[0].Notes, manifest.NoteTokenGenerated) {
		t.Fatal("an existing job generates no token")
	}
}

func TestApply_NewHeartbeatGetsAGeneratedTokenAndNoSecretReachesThePlanOrTheJournal(t *testing.T) {
	b, data := seeded(t)
	data = replaceOnce(t, data, "ignored:\n", `  - name: nightly
    every: 24h
    grace: 1h
ignored:
`)
	plan := b.plan(t, data, false)
	if len(plan.Problems) > 0 {
		t.Fatalf("problems: %+v", plan.Problems)
	}
	var generated bool
	for _, change := range plan.Changes {
		if change.Kind == manifest.KindHeartbeat && change.Name == "nightly" {
			generated = slices.Contains(change.Notes, manifest.NoteTokenGenerated)
		}
	}
	if !generated {
		t.Fatalf("a created job must say that the server draws its token: %+v", plan.Changes)
	}
	applied, _ := b.apply(t, data, false)
	if applied.State != manifest.ApplyApplied {
		t.Fatalf("apply: %+v", applied)
	}
	var token string
	heartbeats, err := b.db.ListHeartbeats(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range heartbeats {
		if item.Name == "nightly" {
			token = item.Token
		}
	}
	if !strings.HasPrefix(token, "hb_") || token == pingSecret {
		t.Fatalf("token: %q", token)
	}
	stored, err := b.manifest.GetApply(context.Background(), applied.ID)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal([]any{applied, stored, plan.Changes, plan.Problems})
	if err != nil {
		t.Fatal(err)
	}
	everything := string(encoded)
	for _, secret := range []string{token, pingSecret, "hb_", heartbeat.HashToken(token)} {
		if strings.Contains(everything, secret) {
			t.Errorf("a secret reached the plan or the journal: %q", secret)
		}
	}
}

func prunable(t *testing.T, data string) string {
	t.Helper()
	data = cut(t, data, "  - id: svc_00000000000000b4\n", "probes:\n")
	data = cut(t, data, "  - id: 00000000000000c2\n", "  - id: 00000000000000c3\n")
	data = cut(t, data, "  - id: 00000000000000d2\n", "  - id: 00000000000000d1\n")
	return replaceOnce(t, data, "  - machine: opencloud\n    container_name: portainer\n", "")
}

func TestApply_WithoutPruneKeepsWhatIsNotInTheFile(t *testing.T) {
	b, data := seeded(t)
	data = prunable(t, data)
	before := b.snapshot(t)
	plan := b.plan(t, data, false)
	if len(plan.Problems) > 0 || len(plan.Changes) > 0 {
		t.Fatalf("plan: %+v %v", plan.Problems, changeLabels(plan))
	}
	var unmanaged []string
	for _, ref := range plan.Unmanaged {
		unmanaged = append(unmanaged, string(ref.Kind)+"/"+ref.Name)
	}
	slices.Sort(unmanaged)
	if want := []string{"heartbeat/certbot", "ignored/opencloud/container_name:portainer", "probe/mail-imaps", "service/opencloud/legacy"}; !slices.Equal(unmanaged, want) {
		t.Fatalf("unmanaged %v, want %v", unmanaged, want)
	}
	if _, _, err := b.tryApply(data, false); err != manifest.ErrNothingToApply {
		t.Fatalf("apply: %v", err)
	}
	if !reflect.DeepEqual(before, b.snapshot(t)) {
		t.Fatal("the directory moved")
	}
}

func TestApply_PruneDeletesAndSaysWhatItCarries(t *testing.T) {
	b, data := seeded(t)
	b.addProbeResult(t, "00000000000000c2")
	data = prunable(t, data)
	plan := b.plan(t, data, true)
	if len(plan.Problems) > 0 || len(plan.Unmanaged) > 0 {
		t.Fatalf("plan: %+v %+v", plan.Problems, plan.Unmanaged)
	}
	var carries []string
	for _, change := range plan.Changes {
		if change.Action != manifest.ActionDelete {
			t.Fatalf("only deletions: %v", changeLabels(plan))
		}
		for _, carry := range change.Carries {
			carries = append(carries, fmt.Sprintf("%s %s=%d", change.Label(), carry.What, carry.Count))
		}
	}
	if len(plan.Changes) != 4 || !slices.Contains(carries, "probe/mail-imaps "+manifest.CarryProbeResults+"=1") {
		t.Fatalf("changes %v carries %v", changeLabels(plan), carries)
	}
	applied, _ := b.apply(t, data, true)
	if applied.State != manifest.ApplyApplied || applied.Deleted != 4 || !applied.Prune {
		t.Fatalf("apply: %+v", applied)
	}
	if after := b.plan(t, data, true); len(after.Changes) > 0 {
		t.Fatalf("not converged: %v", changeLabels(after))
	}
	if _, err := b.db.GetProbe(context.Background(), "00000000000000c2"); err == nil {
		t.Fatal("the probe is still there")
	}
}

func TestPlan_PruneOfAComponentMemberIsBlocked(t *testing.T) {
	b, data := seeded(t)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	statuses := status.New(b.db, b.db, b.machines, logger)
	_, err := statuses.CreateComponent(context.Background(), status.ComponentDefinition{PageID: storetest.StatusPage(t, b.db, "statut"),
		Name: "Site", Members: []status.MemberRef{{Kind: status.KindService, ID: "svc_00000000000000b1"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	data = cut(t, data, "  - id: svc_00000000000000b1\n", "  - id: svc_00000000000000b4\n")
	data = cut(t, data, "  - id: 00000000000000c1\n", "heartbeats:\n")
	// Sans --prune, le service reste et rien ne bloque.
	if plan := b.plan(t, data, false); !plan.Applicable() {
		t.Fatalf("without prune: %+v", plan.Problems)
	}
	plan := b.requireRefused(t, data, true, manifest.KeyPruneBlocked)
	for _, problem := range plan.Problems {
		if problem.Key == manifest.KeyPruneBlocked && (problem.Value != "Site" || !strings.Contains(problem.Path, "wiki")) {
			t.Errorf("the refusal must name the component: %+v", problem)
		}
	}
}

// Une sonde ou une tâche que la page de statut affiche ne se supprime pas
// par --prune : le plan le refuse en nommant le composant, comme le geste du
// domaine le refuserait (même plan, même règle), plutôt que de s'arrêter au
// milieu de l'écriture.
func TestPlan_PruneOfAProbeOrJobShownOnStatusIsBlockedLikeTheGesture(t *testing.T) {
	b, _ := seeded(t)
	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	statuses := status.New(b.db, b.db, b.machines, logger)
	_, err := statuses.CreateComponent(ctx, status.ComponentDefinition{PageID: storetest.StatusPage(t, b.db, "statut"),
		Name:    "Courrier",
		Members: []status.MemberRef{{Kind: status.KindProbe, ID: "00000000000000c2"}, {Kind: status.KindHeartbeat, ID: "00000000000000d1"}},
	})
	if err != nil {
		t.Fatal(err)
	}

	plan := b.requireRefused(t, "version: 1\nmachines:\n  - name: opencloud\n", true, manifest.KeyPruneBlocked)

	var blocked []string
	for _, problem := range plan.Problems {
		if problem.Key == manifest.KeyPruneBlocked {
			blocked = append(blocked, problem.Path+" "+problem.Value)
		}
	}
	slices.Sort(blocked)
	if !slices.Equal(blocked, []string{"mail-imaps Courrier", "wiki-dump Courrier"}) {
		t.Fatalf("refus = %v", blocked)
	}
	if err := b.probes.Delete(ctx, "00000000000000c2"); !errors.Is(err, probe.ErrShownOnStatus) {
		t.Fatalf("le geste doit refuser de même: %v", err)
	}
	if err := b.heartbeats.Delete(ctx, "00000000000000d1"); !errors.Is(err, heartbeat.ErrShownOnStatus) {
		t.Fatalf("le geste doit refuser de même: %v", err)
	}
}

// Un projet qui a sa page de statut ne se supprime pas par --prune : le
// fichier ne porte pas les pages, il ne peut pas l'en détacher. Le plan le
// refuse en nommant la page, comme le geste du domaine le refuserait.
func TestPlan_PruneOfAProjectWithAStatusPageIsBlocked(t *testing.T) {
	b, _ := seeded(t)
	ctx := context.Background()
	project, err := b.directory.CreateProject(ctx, directory.ProjectDefinition{Name: "vitrine"})
	if err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	if _, err := status.New(b.db, b.db, b.machines, logger).CreatePage(ctx, status.PageDefinition{Slug: "vitrine", Title: "Vitrine", ProjectID: project.ID}); err != nil {
		t.Fatal(err)
	}

	plan := b.requireRefused(t, "version: 1\nmachines:\n  - name: opencloud\n", true, manifest.KeyPruneStatusPage)

	for _, problem := range plan.Problems {
		if problem.Key == manifest.KeyPruneStatusPage && (problem.Path != "vitrine" || problem.Value != "Vitrine") {
			t.Errorf("the refusal must name the project and its page: %+v", problem)
		}
	}
	if err := b.directory.DeleteProject(ctx, project.ID); !errors.Is(err, directory.ErrProjectHasStatusPage) {
		t.Fatalf("le geste doit refuser de même: %v", err)
	}
}

func TestApply_PruneNeverTouchesMachines(t *testing.T) {
	b, _ := seeded(t)
	data := "version: 1\nmachines:\n  - name: opencloud\n"
	plan := b.plan(t, data, true)
	if len(plan.Problems) > 0 {
		t.Fatalf("problems: %+v", plan.Problems)
	}
	for _, change := range plan.Changes {
		if change.Action != manifest.ActionDelete || change.Kind == "machine" {
			t.Fatalf("unexpected change %s %s", change.Action, change.Label())
		}
	}
	applied, _ := b.apply(t, data, true)
	if applied.State != manifest.ApplyApplied || applied.Deleted != 16 {
		t.Fatalf("apply: %+v", applied)
	}
	names, err := b.db.MachineNames(context.Background())
	if err != nil || len(names) != 2 {
		t.Fatalf("machines: %v %v", names, err)
	}
	if shot := b.snapshot(t); len(shot.Services)+len(shot.Probes)+len(shot.Heartbeats)+len(shot.Projects)+len(shot.Ignored) != 0 {
		t.Fatalf("left over: %+v", shot)
	}
}

func fiveChanges(t *testing.T, data string) string {
	t.Helper()
	data = replaceOnce(t, data, "    name: docs\n    title: Documentation\n", "    name: docs\n    title: Docs\n")
	data = replaceOnce(t, data, "public_url: https://wiki.example.org\n", "public_url: https://wiki.example.net\n")
	data = replaceOnce(t, data, "    every: 90m\n", "    every: 2h\n")
	data = replaceOnce(t, data, "    interval: 90s\n", "    interval: 2m\n")
	return replaceOnce(t, data, "    name: infra\n", "    name: infra\n    title: Infra\n")
}

// Un geste refusé au milieu arrête tout : ce qui est écrit reste écrit, le
// journal dit où on en est, et la relance du même fichier ne refait que le
// reste.
func TestApply_FailureInTheMiddleStopsRecordsAndRerunConverges(t *testing.T) {
	b, data := seeded(t)
	data = fiveChanges(t, data)
	plan := b.plan(t, data, false)
	if len(plan.Problems) > 0 || len(plan.Changes) != 5 {
		t.Fatalf("setup: %v %+v", changeLabels(plan), plan.Problems)
	}
	b.writes.failAt = 3
	failed, _, err := b.tryApply(data, false)
	if err != nil {
		t.Fatal(err)
	}
	if failed.State != manifest.ApplyFailed || failed.Done != 2 || failed.Error != "test.injected" || failed.ErrorObject != plan.Changes[2].Label() {
		t.Fatalf("failed: %+v, third change %s", failed, plan.Changes[2].Label())
	}
	if calls := b.writes.written(); len(calls) != 3 {
		t.Fatalf("the apply must stop at the first refusal: %v", calls)
	}
	stored, err := b.manifest.GetApply(context.Background(), failed.ID)
	if err != nil || stored.State != manifest.ApplyFailed || stored.Done != 2 || len(stored.Changes) != 5 {
		t.Fatalf("journal: %+v %v", stored, err)
	}

	b.writes.reset()
	again := b.plan(t, data, false)
	if len(again.Changes) != 3 || again.Unchanged != plan.Unchanged+2 {
		t.Fatalf("the re-run must only have the rest: %v unchanged %d", changeLabels(again), again.Unchanged)
	}
	done, _ := b.apply(t, data, false)
	if done.State != manifest.ApplyApplied || done.Done != 3 {
		t.Fatalf("re-run: %+v", done)
	}
	if last := b.plan(t, data, false); len(last.Changes) > 0 {
		t.Fatalf("not converged: %v", changeLabels(last))
	}
	applies, err := b.manifest.Applies(context.Background(), 10)
	if err != nil || len(applies) != 2 || applies[0].ID != done.ID || applies[1].ID != failed.ID {
		t.Fatalf("journal list (newest first): %+v %v", applies, err)
	}
	if len(applies[0].Changes) != 0 {
		t.Error("the list does not carry the plans")
	}
}

func TestApply_StalePlanIsRefused(t *testing.T) {
	b, data := seeded(t)
	ctx := context.Background()
	data = replaceOnce(t, data, "public_url: https://wiki.example.org\n", "public_url: https://wiki.example.net\n")
	plan := b.plan(t, data, false)
	// Pendant que l'opérateur lit le plan, quelqu'un édite le même service.
	service := b.serviceByID(t, "svc_00000000000000b1")
	definition := directory.ServiceDefinition{
		MachineID: service.MachineID, Name: service.Name, Title: "Autre titre", Nature: service.Nature, Origin: service.Origin,
		ComposeProject: service.ComposeProject, PublicURL: service.PublicURL, ProjectIDs: service.ProjectIDs,
	}
	for _, unit := range service.Units {
		definition.Units = append(definition.Units, directory.UnitDefinition{
			ID: unit.ID, Name: unit.Name, Kind: unit.Kind, Image: unit.Image, Version: unit.Version, Digest: unit.Digest,
			Ports: unit.Ports, UpdatePolicy: unit.UpdatePolicy,
		})
	}
	if _, err := b.directory.UpdateService(ctx, service.ID, definition); err != nil {
		t.Fatal(err)
	}
	snapshot := b.snapshot(t)
	_, newer, err := b.manifest.Apply(ctx, manifest.Request{File: []byte(data), FileName: "opencloud.yaml", PlanHash: plan.Hash}, testActor)
	if err != manifest.ErrPlanChanged {
		t.Fatalf("got %v", err)
	}
	if newer.Hash == plan.Hash || len(newer.Changes) == 0 {
		t.Fatalf("the refusal must carry today's plan: %+v", newer)
	}
	if !reflect.DeepEqual(snapshot, b.snapshot(t)) {
		t.Fatal("a stale plan wrote")
	}
	if applies, _ := b.manifest.Applies(ctx, 10); len(applies) != 0 {
		t.Fatalf("a refused apply leaves no line: %+v", applies)
	}
	// Une empreinte vide n'applique rien non plus.
	if _, _, err := b.manifest.Apply(ctx, manifest.Request{File: []byte(data), FileName: "opencloud.yaml"}, testActor); err != manifest.ErrPlanChanged {
		t.Fatalf("no hash: %v", err)
	}
	// Et avec l'empreinte d'aujourd'hui, ça passe.
	if _, _, err := b.manifest.Apply(ctx, manifest.Request{File: []byte(data), FileName: "opencloud.yaml", PlanHash: newer.Hash}, testActor); err != nil {
		t.Fatal(err)
	}
}

func TestApply_BusyWhileAnotherRuns(t *testing.T) {
	b, data := seeded(t)
	data = fiveChanges(t, data)
	started, release := make(chan struct{}), make(chan struct{})
	b.writes.hook = func(call int) {
		if call == 1 {
			close(started)
			<-release
		}
	}
	finished := make(chan manifest.Apply, 1)
	go func() {
		applied, _, err := b.tryApply(data, false)
		if err != nil {
			t.Error(err)
		}
		finished <- applied
	}()
	<-started
	_, _, err := b.manifest.Apply(context.Background(), manifest.Request{File: []byte(data), FileName: "opencloud.yaml", PlanHash: "sha256:x"}, testActor)
	if err != manifest.ErrBusy {
		t.Fatalf("second apply: %v", err)
	}
	close(release)
	if applied := <-finished; applied.State != manifest.ApplyApplied || applied.Done != 5 {
		t.Fatalf("first apply: %+v", applied)
	}
}

// Le client qui coupe (Ctrl-C, délai) ne coupe pas l'écriture au milieu.
func TestApply_ClientGoneDoesNotCutTheApply(t *testing.T) {
	b, data := seeded(t)
	data = fiveChanges(t, data)
	plan := b.plan(t, data, false)
	ctx, gone := context.WithCancel(context.Background())
	b.writes.hook = func(call int) {
		if call == 2 {
			gone()
		}
	}
	applied, _, err := b.manifest.Apply(ctx, manifest.Request{File: []byte(data), FileName: "opencloud.yaml", PlanHash: plan.Hash}, testActor)
	if err != nil || applied.State != manifest.ApplyApplied || applied.Done != 5 {
		t.Fatalf("apply: %+v %v", applied, err)
	}
}

// L'arrêt du serveur n'interrompt pas un changement commencé, il empêche le
// suivant, et la ligne dit pourquoi.
func TestApply_ServerStopBetweenChangesIsJournalled(t *testing.T) {
	b, data := seeded(t)
	data = fiveChanges(t, data)
	lifetime, stop := context.WithCancel(context.Background())
	b.manifest.SetLifetime(lifetime)
	b.writes.hook = func(call int) {
		if call == 2 {
			stop()
		}
	}
	applied, _, err := b.tryApply(data, false)
	if err != nil {
		t.Fatal(err)
	}
	if applied.State != manifest.ApplyFailed || applied.Done != 2 || applied.Error != manifest.KeyInterrupted || applied.ErrorObject == "" {
		t.Fatalf("apply: %+v", applied)
	}
	if len(b.writes.written()) != 2 {
		t.Fatalf("writes: %v", b.writes.written())
	}
}

func TestFailInterrupted_MarksTheLinesAStopLeftConfirmed(t *testing.T) {
	b, _ := seeded(t)
	ctx := context.Background()
	left := manifest.Apply{ID: "apl_00000000000000f1", At: testNow, Actor: testActor, State: manifest.ApplyConfirmed, Changes: []manifest.Change{}}
	if err := b.db.InsertApply(ctx, left); err != nil {
		t.Fatal(err)
	}
	count, err := b.manifest.FailInterrupted(ctx)
	if err != nil || count != 1 {
		t.Fatalf("count %d, %v", count, err)
	}
	stored, err := b.manifest.GetApply(ctx, left.ID)
	if err != nil || stored.State != manifest.ApplyFailed || stored.Error != manifest.KeyInterrupted || stored.FinishedAt.IsZero() {
		t.Fatalf("stored: %+v %v", stored, err)
	}
	if count, _ := b.manifest.FailInterrupted(ctx); count != 0 {
		t.Fatalf("second pass: %d", count)
	}
	if _, err := b.manifest.GetApply(ctx, "apl_unknown"); !errors.Is(err, manifest.ErrApplyNotFound) {
		t.Fatalf("unknown id: %v", err)
	}
}

func TestApply_JournalKeepsWhoWhatAndHowItEnded(t *testing.T) {
	b, data := seeded(t)
	data = fiveChanges(t, data)
	applied, plan := b.apply(t, data, false)
	stored, err := b.manifest.GetApply(context.Background(), applied.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Actor != testActor || stored.FileName != "opencloud.yaml" || stored.FileHash != manifest.HashBytes([]byte(data)) ||
		stored.PlanHash != plan.Hash || stored.State != manifest.ApplyApplied || stored.Updated != 5 || stored.Done != 5 ||
		stored.Unchanged != plan.Unchanged || len(stored.Changes) != 5 || !strings.HasPrefix(stored.ID, manifest.ApplyPrefix) {
		t.Fatalf("stored: %+v", stored)
	}
	if !reflect.DeepEqual(stored.Changes, plan.Changes) {
		t.Fatalf("the journal must keep the confirmed plan:\n%+v\n%+v", stored.Changes, plan.Changes)
	}
}

func TestOrder_FreesANameBeforeTakingIt(t *testing.T) {
	b, data := seeded(t)
	// « backup » passe à « proxy » que « proxy » quitte : dans le fichier,
	// celui qui prend vient avant celui qui libère.
	data = replaceOnce(t, data, "    machine: app-1\n    name: backup\n", "    machine: app-1\n    name: proxy\n")
	data = replaceOnce(t, data, "    machine: app-1\n    name: proxy\n    nature: infra\n    origin: container\n", "    machine: app-1\n    name: zeta\n    nature: infra\n    origin: container\n")
	plan := b.plan(t, data, false)
	if len(plan.Problems) > 0 {
		t.Fatalf("problems: %+v", plan.Problems)
	}
	if got := changeLabels(plan); !slices.Equal(got, []string{"update service/app-1/zeta", "update service/app-1/proxy"}) {
		t.Fatalf("the name must be freed first: %v", got)
	}
	applied, _ := b.apply(t, data, false)
	if applied.State != manifest.ApplyApplied {
		t.Fatalf("apply: %+v", applied)
	}
	if backup, proxy := b.serviceByID(t, "svc_00000000000000b3"), b.serviceByID(t, "svc_00000000000000b2"); backup.Name != "proxy" || proxy.Name != "zeta" {
		t.Fatalf("names: %s %s", backup.Name, proxy.Name)
	}
}

func TestOrder_SwapIsRefused(t *testing.T) {
	b, data := seeded(t)
	data = replaceOnce(t, data, "    machine: app-1\n    name: backup\n", "    machine: app-1\n    name: swapped\n")
	data = replaceOnce(t, data, "    machine: app-1\n    name: proxy\n", "    machine: app-1\n    name: backup\n")
	data = replaceOnce(t, data, "    machine: app-1\n    name: swapped\n", "    machine: app-1\n    name: proxy\n")
	plan := b.requireRefused(t, data, false, manifest.KeySwapRefused)
	for _, problem := range plan.Problems {
		if problem.Key == manifest.KeySwapRefused && !strings.Contains(problem.Value, "backup") {
			t.Errorf("the refusal must name the services: %+v", problem)
		}
	}
}

func TestOrder_AKeyHeldByAPrunedObjectCannotBeTakenInTheSamePass(t *testing.T) {
	b, data := seeded(t)
	// « wiki » sort du fichier (donc de l'annuaire avec --prune) et un autre
	// service reprend son projet Compose.
	data = cut(t, data, "  - id: svc_00000000000000b1\n", "  - id: svc_00000000000000b4\n")
	data = cut(t, data, "  - id: 00000000000000c1\n", "heartbeats:\n")
	data = replaceOnce(t, data, "probes:\n", `  - machine: app-1
    name: wiki2
    nature: application
    origin: compose
    compose_project: wiki
    units:
      - name: app
        image: example/wiki
probes:
`)
	b.requireRefused(t, data, true, manifest.KeyKeyFromPruned)
	// Sans --prune, c'est le conflit que la base refuserait à l'écriture.
	b.requireRefused(t, data, false, "directory.compose_project_taken")
}
