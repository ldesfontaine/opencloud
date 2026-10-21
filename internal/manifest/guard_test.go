package manifest_test

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/ldesfontaine/opencloud/internal/deletion"
	"github.com/ldesfontaine/opencloud/internal/directory"
	"github.com/ldesfontaine/opencloud/internal/manifest"
	"github.com/ldesfontaine/opencloud/internal/probe"
)

// Une édition de l'interface faite pendant l'application, entre le plan rejoué
// et l'écriture du service, ne s'écrase pas sans bruit : l'application s'arrête
// au service, dit pourquoi, et l'unité renommée dans l'interface garde son
// identifiant (elle n'est pas recréée).
func TestApply_UIEditDuringApplyStopsInsteadOfOverwriting(t *testing.T) {
	b, data := seeded(t)
	ctx := context.Background()
	data = replaceOnce(t, data, "    name: docs\n    title: Documentation\n", "    name: docs\n    title: Docs\n")
	data = replaceOnce(t, data, "public_url: https://wiki.example.org\n", "public_url: https://wiki.example.net\n")
	b.writes.hook = func(call int) {
		if call != 1 {
			return
		}
		service := b.serviceByID(t, "svc_00000000000000b1")
		definition := service.Definition()
		definition.Title = "Titre saisi dans l'interface"
		for i := range definition.Units {
			if definition.Units[i].Name == "db" {
				definition.Units[i].Name = "database"
			}
		}
		if _, err := b.directory.UpdateService(ctx, service.ID, definition); err != nil {
			t.Error(err)
		}
	}
	applied, _ := b.apply(t, data, false)
	if applied.State != manifest.ApplyFailed || applied.Error != manifest.KeyPlanChanged || applied.Done != 1 || applied.ErrorObject != "service/app-1/wiki" {
		t.Fatalf("the apply must stop at the service: %+v", applied)
	}
	service := b.serviceByID(t, "svc_00000000000000b1")
	if service.Title != "Titre saisi dans l'interface" || service.PublicURL != "https://wiki.example.org" {
		t.Errorf("the UI edit was overwritten: %+v", service)
	}
	var units []string
	for _, unit := range service.Units {
		units = append(units, unit.Name+"="+unit.ID)
	}
	slices.Sort(units)
	if want := []string{"app=unit_00000000000000a1", "database=unit_00000000000000a2"}; !slices.Equal(units, want) {
		t.Errorf("units %v, want %v: the renamed unit must not be recreated", units, want)
	}
	if calls := b.writes.written(); len(calls) != 1 {
		t.Errorf("nothing is written after the stop: %v", calls)
	}

	// La relance recalcule le plan sur ce que l'annuaire est devenu.
	b.writes.reset()
	again, _ := b.apply(t, data, false)
	if again.State != manifest.ApplyApplied {
		t.Fatalf("re-run: %+v", again)
	}
	if last := b.plan(t, data, false); len(last.Changes) > 0 {
		t.Fatalf("not converged: %v", changeLabels(last))
	}
}

func TestApply_ProbeEditedDuringApplyStops(t *testing.T) {
	b, data := seeded(t)
	ctx := context.Background()
	data = replaceOnce(t, data, "    name: docs\n    title: Documentation\n", "    name: docs\n    title: Docs\n")
	data = replaceOnce(t, data, "    interval: 90s\n", "    interval: 2m\n")
	b.writes.hook = func(call int) {
		if call != 1 {
			return
		}
		definition := b.probeByID(t, "00000000000000c2").Definition()
		definition.Timeout = 8 * time.Second
		if _, err := b.probes.Update(ctx, "00000000000000c2", definition); err != nil {
			t.Error(err)
		}
	}
	applied, _ := b.apply(t, data, false)
	if applied.State != manifest.ApplyFailed || applied.Error != manifest.KeyPlanChanged || applied.Done != 1 || applied.ErrorObject != "probe/mail-imaps" {
		t.Fatalf("apply: %+v", applied)
	}
	if got := b.probeByID(t, "00000000000000c2"); got.Timeout != 8*time.Second || got.Interval != 90*time.Second {
		t.Errorf("the UI edit was overwritten: %+v", got)
	}
}

func TestApply_HeartbeatEditedDuringApplyStops(t *testing.T) {
	b, data := seeded(t)
	ctx := context.Background()
	data = replaceOnce(t, data, "    name: docs\n    title: Documentation\n", "    name: docs\n    title: Docs\n")
	data = replaceOnce(t, data, "    every: 90m\n", "    every: 2h\n")
	b.writes.hook = func(call int) {
		if call != 1 {
			return
		}
		definition := b.heartbeatByID(t, "00000000000000d2").Definition()
		definition.Grace = 7 * time.Minute
		if _, err := b.heartbeats.Update(ctx, "00000000000000d2", definition); err != nil {
			t.Error(err)
		}
	}
	applied, _ := b.apply(t, data, false)
	if applied.State != manifest.ApplyFailed || applied.Error != manifest.KeyPlanChanged || applied.Done != 1 || applied.ErrorObject != "heartbeat/certbot" {
		t.Fatalf("apply: %+v", applied)
	}
	if got := b.heartbeatByID(t, "00000000000000d2"); got.Grace != 7*time.Minute || got.Interval != 90*time.Minute {
		t.Errorf("the UI edit was overwritten: %+v", got)
	}
}

// Une suppression ne part pas non plus sur la foi d'un objet que l'interface
// vient de changer.
func TestApply_DeleteOfAnObjectEditedMeanwhileStops(t *testing.T) {
	b, _ := seeded(t)
	ctx := context.Background()
	data := "version: 1\nmachines:\n  - name: opencloud\n"
	// Les suppressions de sondes passent par nom : api-post (1), mail-imaps
	// (2), puis le premier « twin » (3), que l'interface édite pendant le
	// premier geste.
	b.writes.hook = func(call int) {
		if call != 1 {
			return
		}
		definition := b.probeByID(t, "00000000000000c3").Definition()
		definition.Timeout = 9 * time.Second
		if _, err := b.probes.Update(ctx, "00000000000000c3", definition); err != nil {
			t.Error(err)
		}
	}
	applied, _ := b.apply(t, data, true)
	if applied.State != manifest.ApplyFailed || applied.Error != manifest.KeyPlanChanged || applied.Done != 2 || applied.ErrorObject != "probe/twin" {
		t.Fatalf("apply: %+v", applied)
	}
	if got := b.probeByID(t, "00000000000000c3"); got.Timeout != 9*time.Second {
		t.Errorf("the probe edited in the UI is gone or reset: %+v", got)
	}
}

// Un nom que le plan avait trouvé libre et qu'une autre main prend avant
// l'écriture est un plan périmé, pas un refus du domaine.
func TestApply_CreatedNameTakenMeanwhileIsAStalePlan(t *testing.T) {
	b, data := seeded(t)
	ctx := context.Background()
	data = replaceOnce(t, data, "services:\n", `services:
  - machine: app-1
    name: fresh
    nature: application
    origin: container
    units:
      - name: web
        image: nginx
        version: "1.27"
        container_name: fresh-web
`)
	b.writes.hook = func(call int) {
		if call != 1 {
			return
		}
		_, err := b.directory.CreateService(ctx, directory.ServiceDefinition{
			MachineID: remoteID, Name: "fresh", Nature: directory.NatureApplication, Origin: directory.OriginContainer,
			Units: []directory.UnitDefinition{{Name: "main", Image: "redis", Version: "7", ContainerName: "fresh-main"}},
		})
		if err != nil {
			t.Error(err)
		}
	}
	applied, _ := b.apply(t, data, false)
	if applied.State != manifest.ApplyFailed || applied.Error != manifest.KeyPlanChanged || applied.ErrorObject != "service/app-1/fresh" {
		t.Fatalf("apply: %+v", applied)
	}
	services, err := b.db.ListDeclaredServices(ctx, directory.Filter{})
	if err != nil {
		t.Fatal(err)
	}
	found := 0
	for _, service := range services {
		if service.Name == "fresh" {
			found++
			if len(service.Units) != 1 || service.Units[0].Name != "main" {
				t.Errorf("the service made in the UI was touched: %+v", service)
			}
		}
	}
	if found != 1 {
		t.Errorf("%d services named fresh", found)
	}
}

// Un fichier qui met des identifiants dans une cible est refusé, comme dans
// l'interface. Une sonde d'avant le refus qui en porte encore se relit
// « inchangée » depuis son propre export, et les perd par le fichier : le
// plan le dit sans jamais montrer le mot de passe.
func TestPlan_TargetCredentialsAreRefusedAndOldOnesRemovedUnseen(t *testing.T) {
	b, data := seeded(t)
	b.requireRefused(t, replaceOnce(t, data, "target: https://wiki.example.org/healthz", "target: https://u:one@wiki.example.org/healthz"), false, "probe.target_credentials")
	b.requireRefused(t, replaceOnce(t, data, "heartbeats:\n", `  - name: private-feed
    runner: opencloud
    kind: http
    target: https://reader:one@feed.example.org/status
heartbeats:
`), false, "probe.target_credentials")

	legacy := b.probeByID(t, "00000000000000c1")
	legacy.Target = "https://u:one@wiki.example.org/healthz"
	if err := b.db.UpdateProbe(context.Background(), probe.Revision{Probe: legacy}); err != nil {
		t.Fatal(err)
	}
	exported := string(b.export(t))
	if !strings.Contains(exported, "https://u:one@") {
		t.Fatalf("the operator's export keeps the target as stored:\n%s", exported)
	}
	if again := b.plan(t, exported, false); !again.Applicable() || len(again.Changes) > 0 {
		t.Fatalf("an old probe re-read from its own export is unchanged: %v %v", problemKeys(again), changeLabels(again))
	}

	plan := b.plan(t, data, false)
	if len(plan.Changes) != 1 || plan.Changes[0].Kind != manifest.KindProbe {
		t.Fatalf("removing the credentials must be planned: %v", changeLabels(plan))
	}
	change := plan.Changes[0]
	if !slices.Contains(change.Notes, manifest.NotePasswordChanged) {
		t.Errorf("the plan must say the password changes: %+v", change.Notes)
	}
	if len(change.Fields) != 1 || change.Fields[0].Before != "https://u:…@wiki.example.org/healthz" || change.Fields[0].After != "https://wiki.example.org/healthz" {
		t.Errorf("the target is shown masked: %+v", change.Fields)
	}
	applied, _ := b.apply(t, data, false)
	if applied.State != manifest.ApplyApplied {
		t.Fatalf("apply: %+v", applied)
	}
	if got := b.probeByID(t, "00000000000000c1").Target; got != "https://wiki.example.org/healthz" {
		t.Errorf("the credentials were not removed: %s", got)
	}
	stored, err := b.manifest.GetApply(context.Background(), applied.ID)
	if err != nil {
		t.Fatal(err)
	}
	for name, value := range map[string]any{"plan": plan, "applied": applied, "stored": stored} {
		encoded, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(encoded), ":one@") || strings.Contains(fmt.Sprintf("%+v", change), ":one@") {
			t.Errorf("%s carries a password: %s", name, encoded)
		}
	}
	if again := b.plan(t, data, false); len(again.Changes) > 0 {
		t.Errorf("not converged: %v", changeLabels(again))
	}
}

// Une édition sur place ne perd ni la pause d'une sonde, ni la revue d'un
// service, ni l'historique.
func TestApply_UpdateKeepsPauseReviewAndHistory(t *testing.T) {
	b, data := seeded(t)
	data = replaceOnce(t, data, "    interval: 90s\n", "    interval: 2m\n")
	data = replaceOnce(t, data, "    name: legacy\n", "    name: legacy\n    title: Ancien\n")
	b.addProbeResult(t, "00000000000000c2")
	b.apply(t, data, false)
	if p := b.probeByID(t, "00000000000000c2"); p.Status != probe.StatusPaused || p.Interval != 2*time.Minute {
		t.Errorf("pause lost or interval not written: %+v", p)
	}
	s := b.serviceByID(t, "svc_00000000000000b4")
	if !s.NeedsReview || s.DeclaredBy != directory.ByMigration || s.Title != "Ancien" {
		t.Errorf("state lost: %+v", s)
	}
	kept, err := b.db.ProbeDeletionPlan(context.Background(), "00000000000000c2")
	if want := []deletion.Count{{Kind: deletion.KindProbeResults, Count: 1}}; err != nil || !slices.Equal(kept.Removes, want) {
		t.Errorf("history lost: %+v %v", kept.Removes, err)
	}
}

// L'empreinte d'un plan de suppression lit la structure qu'il emporte : une
// unité ajoutée dans l'interface au service que --prune supprime change ce
// que l'opérateur a confirmé. L'historique, lui, grossit sans la changer.
func TestPlan_HashReadsWhatADeletionCarriesStructurally(t *testing.T) {
	b, _ := seeded(t)
	ctx := context.Background()
	data := "version: 1\nmachines:\n  - name: opencloud\n"
	first := b.plan(t, data, true).Hash

	b.addProbeResult(t, "00000000000000c1")
	if grown := b.plan(t, data, true).Hash; grown != first {
		t.Fatal("history growing must not change the hash")
	}

	service := b.serviceByID(t, "svc_00000000000000b1")
	definition := service.Definition()
	definition.Units = append(definition.Units, directory.UnitDefinition{Name: "cache", Image: "redis", Version: "7"})
	if _, err := b.directory.UpdateService(ctx, service.ID, definition); err != nil {
		t.Fatal(err)
	}
	changed := b.plan(t, data, true)
	if changed.Hash == first {
		t.Fatal("a unit added in the UI to a service that --prune deletes must change the hash")
	}
	_, _, err := b.manifest.Apply(ctx, manifest.Request{File: []byte(data), FileName: "opencloud.yaml", Prune: true, PlanHash: first}, testActor)
	if err != manifest.ErrPlanChanged {
		t.Fatalf("a hash confirmed before the unit was added must be refused: %v", err)
	}
}

// Le nom d'une sonde se lit sans ses espaces, comme celui d'une tâche : une
// sonde nommée « " wiki-http " » est la sonde existante, pas un doublon.
func TestPlan_ProbeNameIsTrimmedBeforeTheLookup(t *testing.T) {
	b, data := seeded(t)
	withoutID := replaceOnce(t, data, "  - id: 00000000000000c1\n    name: wiki-http", "  - name: \" wiki-http \"")
	plan := b.plan(t, withoutID, false)
	if len(plan.Problems) > 0 || len(plan.Changes) > 0 || plan.Unchanged != 16 {
		t.Fatalf("problems %+v changes %v unchanged %d", plan.Problems, changeLabels(plan), plan.Unchanged)
	}
}

// Un ensemble relu dans un autre ordre (provides, projects) n'est pas un
// changement : la garde n'arrête pas l'application.
func TestApply_SetsInAnotherOrderAreNotAnEdit(t *testing.T) {
	b, data := seeded(t)
	ctx := context.Background()
	data = replaceOnce(t, data, "    name: docs\n    title: Documentation\n", "    name: docs\n    title: Docs\n")
	data = replaceOnce(t, data, "    name: proxy\n", "    name: proxy\n    title: Proxy\n")
	b.writes.hook = func(call int) {
		if call != 1 {
			return
		}
		service := b.serviceByID(t, "svc_00000000000000b2")
		definition := service.Definition()
		slices.Reverse(definition.Provides)
		slices.Reverse(definition.ProjectIDs)
		if _, err := b.directory.UpdateService(ctx, service.ID, definition); err != nil {
			t.Error(err)
		}
	}
	applied, _ := b.apply(t, data, false)
	if applied.State != manifest.ApplyApplied || applied.Done != 2 {
		t.Fatalf("a reordered set is not an edit: %+v", applied)
	}
	if got := b.serviceByID(t, "svc_00000000000000b2"); got.Title != "Proxy" {
		t.Errorf("title %q", got.Title)
	}
}
