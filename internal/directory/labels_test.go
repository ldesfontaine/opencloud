package directory_test

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/ldesfontaine/opencloud/internal/brand"
	"github.com/ldesfontaine/opencloud/internal/container"
	"github.com/ldesfontaine/opencloud/internal/directory"
	"github.com/ldesfontaine/opencloud/internal/machine"
)

func withLabels(observed container.Observed, service, unit string) container.Observed {
	observed.Labels = map[string]string{container.LabelService: service, container.LabelUnit: unit}
	return observed
}

// directorySnapshot est tout ce que l'annuaire garde, tel que ses lectures le
// rendent : services et unités (dates et « à vérifier » compris), projets,
// clés ignorées.
type directorySnapshot struct {
	Services []directory.Service
	Projects []directory.Project
	Ignored  []directory.IgnoredKey
}

func snapshotOf(t *testing.T, b *bench) directorySnapshot {
	t.Helper()
	ctx := context.Background()
	services, err := b.db.ListDeclaredServices(ctx, directory.Filter{})
	if err != nil {
		t.Fatal(err)
	}
	projects, err := b.db.ListProjects(ctx)
	if err != nil {
		t.Fatal(err)
	}
	ignored, err := b.db.ListIgnoredKeys(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	return directorySnapshot{Services: services, Projects: projects, Ignored: ignored}
}

// Le test de sécurité de la PR : un label cherche, il n'écrit jamais dans
// l'annuaire. Des conteneurs étiquetés vers un identifiant inconnu, vers un
// service d'une autre machine, vers un binaire, vers une moitié de paire, des
// labels mal formés, puis vers une unité valide, dont trois orphelins que
// Compose ou le nom auraient rattachés sans leur label (pour qu'un repli,
// même silencieux, soit attrapé) : après le rapport, le rapprochement refait, l'écart,
// la fiche, la proposition et son refus, l'annuaire est celui d'avant, au
// champ près, et ne s'est pas annoncé changé.
func TestLabels_NeverWriteTheDirectory(t *testing.T) {
	b := newBench(t)
	ctx := context.Background()
	onRemote := mustCreate(t, b, containerDefinition(remoteID, "whoami", "whoami"))
	onLocal := mustCreate(t, b, containerDefinition(machine.LocalID, "elsewhere", "elsewhere"))
	binary := mustCreate(t, b, directory.ServiceDefinition{
		MachineID: remoteID, Name: "caddy", Nature: directory.NatureInfra, Origin: directory.OriginBinary,
		Units: []directory.UnitDefinition{{Name: "main", SystemdUnit: "caddy.service"}},
	})
	// Ce que Compose, puis le nom, rattacheraient si le label n'était pas là.
	nextcloud := mustCreate(t, b, composeDefinition(remoteID))
	before := snapshotOf(t, b)
	changesBefore := b.changeCount()

	ids := map[string]string{
		"unknown":    strings.Repeat("1", 64),
		"otherhost":  strings.Repeat("2", 64),
		"binary":     strings.Repeat("3", 64),
		"half":       strings.Repeat("4", 64),
		"badunit":    strings.Repeat("5", 64),
		"valid":      strings.Repeat("6", 64),
		"extra":      strings.Repeat("7", 64),
		"unlabelled": strings.Repeat("8", 64),
		// Orphelins qui retomberaient sur Compose, sur le nom, ou sur rien.
		"composefallback": strings.Repeat("a", 64),
		"namefallback":    strings.Repeat("b", 64),
		"malformed":       strings.Repeat("c", 64),
	}
	malformed := composeContainerOf(ids["malformed"], "nextcloud-db-1", "nextcloud", "db", "postgres")
	malformed.Labels = map[string]string{container.LabelInvalid: "1"}
	namefallback := composeContainerOf(ids["namefallback"], "whoami", "", "", "traefik/whoami")
	namefallback.Labels = map[string]string{container.LabelService: onRemote.ID}
	extra := withLabels(composeContainerOf(ids["extra"], "extra", "", "", "nginx"), onRemote.ID, "whoami")
	// Des suffixes que ce serveur ne lit pas ne déclarent rien non plus.
	extra.Labels["service.name"] = "inventé"
	extra.Labels["declare"] = "oui"
	inventory := inventoryOf(
		withLabels(composeContainerOf(ids["unknown"], "unknown", "", "", "nginx"), "svc_0000000000000000", "whoami"),
		withLabels(composeContainerOf(ids["otherhost"], "otherhost", "", "", "nginx"), onLocal.ID, "elsewhere"),
		withLabels(composeContainerOf(ids["binary"], "binary", "", "", "nginx"), binary.ID, "main"),
		withLabels(composeContainerOf(ids["half"], "half", "", "", "nginx"), onRemote.ID, ""),
		withLabels(composeContainerOf(ids["badunit"], "badunit", "", "", "nginx"), onRemote.ID, "pas-une-unite"),
		withLabels(composeContainerOf(ids["valid"], "valid", "", "", "nginx"), onRemote.ID, "whoami"),
		extra,
		composeContainerOf(ids["unlabelled"], "unlabelled", "", "", "nginx"),
		withLabels(composeContainerOf(ids["composefallback"], "nextcloud-app-1", "nextcloud", "app", "nextcloud"), "svc_0000000000000000", "app"),
		namefallback,
		malformed,
	)
	if err := b.containers.Record(ctx, remoteID, inventory); err != nil {
		t.Fatal(err)
	}
	if err := b.containers.Rematch(ctx, remoteID); err != nil {
		t.Fatal(err)
	}
	b.containers.RematchAll(ctx)

	wantAttached := map[string]bool{"valid": true, "extra": true}
	for name, containerID := range ids {
		found, err := b.containers.Get(ctx, container.ID(remoteID, containerID))
		if err != nil {
			t.Fatal(err)
		}
		attached := found.UnitID != ""
		if attached != wantAttached[name] {
			t.Errorf("%s : rattaché = %v (%+v)", name, attached, found)
		}
		if attached && (found.UnitID != onRemote.Units[0].ID || found.MatchLevel != container.MatchLabel) {
			t.Errorf("%s : %+v, want l'unité de whoami par label", name, found)
		}
		link, err := b.directory.ContainerLink(ctx, found)
		if err != nil {
			t.Fatal(err)
		}
		if !attached && name != "unlabelled" && link.LabelProblem == directory.LabelOK {
			t.Errorf("%s : un label orphelin doit dire pourquoi", name)
		}
	}
	if _, err := b.directory.Gaps(ctx, directory.Filter{}); err != nil {
		t.Fatal(err)
	}
	if len(nextcloud.Units) != 2 {
		t.Fatal("le service Compose de contrôle doit avoir ses unités")
	}
	malformedFound, err := b.containers.Get(ctx, container.ID(remoteID, ids["malformed"]))
	if err != nil {
		t.Fatal(err)
	}
	if link, err := b.directory.ContainerLink(ctx, malformedFound); err != nil || link.LabelProblem != directory.LabelInvalid {
		t.Fatalf("des labels mal formés disent leur motif : %+v, %v", link, err)
	}
	for _, name := range []string{"unknown", "otherhost", "binary", "half", "badunit", "composefallback", "namefallback", "malformed"} {
		if _, err := b.directory.Propose(ctx, container.ID(remoteID, ids[name])); err == nil {
			t.Errorf("%s : adopter un label orphelin doit être refusé", name)
		}
	}
	proposals, err := b.directory.Proposals(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(proposals) != 1 || proposals[0].Definition.ComposeProject != "" || proposals[0].Definition.Units[0].ContainerName != "unlabelled" {
		t.Fatalf("seul le conteneur sans label se propose : %+v", proposals)
	}

	after := snapshotOf(t, b)
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("l'annuaire a changé sous l'effet de labels :\navant %+v\naprès %+v", before, after)
	}
	if len(after.Services) != 4 {
		t.Fatalf("services = %d, un label n'en crée jamais", len(after.Services))
	}
	if b.changeCount() != changesBefore {
		t.Fatalf("l'annuaire s'est annoncé changé %d fois", b.changeCount()-changesBefore)
	}
}

// Un service qui change de machine perd ses conteneurs étiquetés de l'ancienne
// machine : ils deviennent orphelins au rapprochement des deux machines.
func TestMatchUnits_LabelOfAMovedServiceBecomesOrphan(t *testing.T) {
	b := newBench(t)
	ctx := context.Background()
	created := mustCreate(t, b, containerDefinition(remoteID, "whoami", "whoami"))
	if err := b.containers.Record(ctx, remoteID, inventoryOf(withLabels(composeContainerOf(containerID1, "whoami-1", "", "", "traefik/whoami"), created.ID, "whoami"))); err != nil {
		t.Fatal(err)
	}
	id := container.ID(remoteID, containerID1)
	if found, _ := b.containers.Get(ctx, id); found.UnitID != created.Units[0].ID || found.MatchLevel != container.MatchLabel {
		t.Fatalf("avant le déménagement : %+v", found)
	}

	definition := containerDefinition(machine.LocalID, "whoami", "whoami")
	definition.Units[0].ID = created.Units[0].ID
	if _, err := b.directory.UpdateService(ctx, created.ID, definition); err != nil {
		t.Fatal(err)
	}

	found, _ := b.containers.Get(ctx, id)
	if found.UnitID != "" || found.MatchLevel != container.MatchNone {
		t.Fatalf("après le déménagement, le label ne vise plus sa machine : %+v", found)
	}
	link, err := b.directory.ContainerLink(ctx, found)
	if err != nil || link.LabelProblem != directory.LabelServiceUnknown {
		t.Fatalf("lien = %+v, %v", link, err)
	}
}

func TestContainerLink_OrphanSaysWhereTheServiceIs(t *testing.T) {
	b := newBench(t)
	ctx := context.Background()
	onLocal := mustCreate(t, b, containerDefinition(machine.LocalID, "elsewhere", "elsewhere"))
	if err := b.containers.Record(ctx, remoteID, inventoryOf(
		withLabels(composeContainerOf(containerID1, "a", "", "", "nginx"), onLocal.ID, "elsewhere"),
		withLabels(composeContainerOf(containerID2, "b", "", "", "nginx"), "svc_0000000000000000", "elsewhere"),
	)); err != nil {
		t.Fatal(err)
	}

	known, _ := b.containers.Get(ctx, container.ID(remoteID, containerID1))
	link, err := b.directory.ContainerLink(ctx, known)
	if err != nil {
		t.Fatal(err)
	}
	if link.LabelProblem != directory.LabelServiceUnknown || link.LabelMachineName != brand.ID || link.UnitID != "" {
		t.Fatalf("le service est déclaré sur une autre machine : %+v", link)
	}
	if link.Labels != (container.Labels{Service: onLocal.ID, Unit: "elsewhere"}) {
		t.Fatalf("labels = %+v", link.Labels)
	}

	unknown, _ := b.containers.Get(ctx, container.ID(remoteID, containerID2))
	link, err = b.directory.ContainerLink(ctx, unknown)
	if err != nil || link.LabelProblem != directory.LabelServiceUnknown || link.LabelMachineName != "" {
		t.Fatalf("un identifiant inconnu partout ne nomme aucune machine : %+v, %v", link, err)
	}
}
