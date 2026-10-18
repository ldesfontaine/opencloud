package directory_test

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"slices"
	"sync"
	"testing"

	"github.com/ldesfontaine/opencloud/internal/container"
	"github.com/ldesfontaine/opencloud/internal/directory"
	"github.com/ldesfontaine/opencloud/internal/machine"
	"github.com/ldesfontaine/opencloud/internal/probe"
	"github.com/ldesfontaine/opencloud/internal/status"
)

// recordingAssigner tient lieu de la mise à jour : il note les machines à qui
// l'annuaire dit de revoir leurs images.
type recordingAssigner struct {
	mu       sync.Mutex
	machines []string
}

func (r *recordingAssigner) Assign(_ context.Context, machineID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.machines = append(r.machines, machineID)
}

func (r *recordingAssigner) told() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.machines)
}

// watch rattache à un service déclaré une sonde et un composant de statut :
// les deux choses qui, ensemble, l'empêchent d'être supprimé.
func watch(t *testing.T, b *bench, serviceID string) (probe.Probe, status.Component) {
	t.Helper()
	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	probes := probe.New(b.db, logger)
	watched, err := probes.Create(ctx, probe.Definition{
		Name: "site", Kind: probe.KindHTTP, Target: "https://cloud.exemple.fr/", MachineID: machine.LocalID, ServiceID: serviceID,
	})
	if err != nil {
		t.Fatalf("créer la sonde: %v", err)
	}
	statuses := status.New(b.db, b.machines, b.db, logger)
	component, err := statuses.CreateComponent(ctx, status.ComponentDefinition{
		Name: "Nuage", Members: []status.MemberRef{{Kind: status.KindService, ID: serviceID}},
	})
	if err != nil {
		t.Fatalf("créer le composant: %v", err)
	}
	return watched, component
}

// Un service que des sondes ou des composants visent ne se supprime pas : le
// refus porte la liste, et rien n'a bougé. Détaché, il part.
func TestDeleteService_LinkedIsRefusedWithTheList(t *testing.T) {
	b := newBench(t)
	ctx := context.Background()
	created := mustCreate(t, b, composeDefinition(remoteID))
	watched, component := watch(t, b, created.ID)

	err := b.directory.DeleteService(ctx, created.ID)

	if !errors.Is(err, directory.ErrServiceLinked) {
		t.Fatalf("attendu %v, obtenu %v", directory.ErrServiceLinked, err)
	}
	var linked *directory.LinkedError
	if !errors.As(err, &linked) {
		t.Fatalf("le refus doit porter la liste: %T", err)
	}
	if len(linked.Links.Probes) != 1 || linked.Links.Probes[0].ID != watched.ID || linked.Links.Probes[0].Name != "site" {
		t.Fatalf("sondes = %+v", linked.Links.Probes)
	}
	if len(linked.Links.Components) != 1 || linked.Links.Components[0].ID != component.ID || linked.Links.Components[0].Name != "Nuage" {
		t.Fatalf("composants = %+v", linked.Links.Components)
	}
	if _, err := b.directory.GetService(ctx, created.ID); err != nil {
		t.Fatalf("le service a disparu malgré le refus: %v", err)
	}
	links, err := b.directory.ServiceLinks(ctx, created.ID)
	if err != nil || links.IsEmpty() {
		t.Fatalf("ServiceLinks = %+v, %v", links, err)
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	if err := probe.New(b.db, logger).Delete(ctx, watched.ID); err != nil {
		t.Fatal(err)
	}
	if err := status.New(b.db, b.machines, b.db, logger).DeleteComponent(ctx, component.ID); err != nil {
		t.Fatal(err)
	}
	if err := b.directory.DeleteService(ctx, created.ID); err != nil {
		t.Fatalf("détaché, le service doit partir: %v", err)
	}
}

// Valider un service repris de l'existant exige une définition qui passe la
// validation ; modifier le service ne le valide pas ; valider deux fois ne
// change rien.
func TestReview_ClearsTheFlagOnlyForAValidDefinition(t *testing.T) {
	b := newBench(t)
	ctx := context.Background()
	taken := directory.Service{
		ID: "svc_0123456789abcdef", MachineID: remoteID, Name: "whoami", Nature: directory.NatureApplication,
		Origin: directory.OriginContainer, DeclaredBy: directory.ByMigration, NeedsReview: true,
		CreatedAt: testNow, UpdatedAt: testNow,
		Units: []directory.Unit{{
			ID: "unit_0123456789abcdef", ServiceID: "svc_0123456789abcdef", Name: "whoami", Kind: directory.UnitContainer,
			// Une image que Docker montrait sous une forme que l'annuaire refuse.
			Image: "NOT A VALID IMAGE", ContainerName: "whoami", UpdatePolicy: container.PolicyWatch,
		}},
	}
	if err := b.db.InsertDeclaredService(ctx, taken); err != nil {
		t.Fatal(err)
	}

	if _, err := b.directory.Review(ctx, taken.ID); !errors.Is(err, directory.ErrImageInvalid) {
		t.Fatalf("attendu %v, obtenu %v", directory.ErrImageInvalid, err)
	}
	if still, _ := b.directory.GetService(ctx, taken.ID); !still.NeedsReview {
		t.Fatal("un refus ne valide rien")
	}

	definition := containerDefinition(remoteID, "whoami", "whoami")
	definition.Units[0].ID = "unit_0123456789abcdef"
	if _, err := b.directory.UpdateService(ctx, taken.ID, definition); err != nil {
		t.Fatal(err)
	}
	if edited, _ := b.directory.GetService(ctx, taken.ID); !edited.NeedsReview || edited.DeclaredBy != directory.ByMigration {
		t.Fatalf("modifier ne valide pas, et la provenance reste: %+v", edited)
	}

	reviewed, err := b.directory.Review(ctx, taken.ID)
	if err != nil || reviewed.NeedsReview {
		t.Fatalf("Review = %+v, %v", reviewed, err)
	}
	again, err := b.directory.Review(ctx, taken.ID)
	if err != nil || again.NeedsReview || !again.UpdatedAt.Equal(reviewed.UpdatedAt) {
		t.Fatalf("valider deux fois ne change rien: %+v, %v", again, err)
	}
	if _, err := b.directory.Review(ctx, "svc_inconnu"); !errors.Is(err, directory.ErrNotFound) {
		t.Fatalf("service inconnu: %v", err)
	}
}

// La politique se règle sur l'unité, la machine qui porte le service est
// prévenue, l'annuaire le dit à ceux qui regardent. Une politique inconnue,
// une unité disparue ou un binaire qui voudrait autre chose que watch sont
// refusés sans toucher à rien.
func TestSetUnitUpdatePolicy_TellsTheMachine(t *testing.T) {
	b := newBench(t)
	ctx := context.Background()
	assigner := &recordingAssigner{}
	b.directory.SetAssigner(assigner)
	created := mustCreate(t, b, composeDefinition(remoteID))
	binary := mustCreate(t, b, directory.ServiceDefinition{
		MachineID: remoteID, Name: "caddy", Nature: directory.NatureInfra, Origin: directory.OriginBinary,
		Units: []directory.UnitDefinition{{Name: "caddy", SystemdUnit: "caddy.service"}},
	})
	told, changes := len(assigner.told()), b.changeCount()

	updated, err := b.directory.SetUnitUpdatePolicy(ctx, created.Units[0].ID, container.PolicyHold)

	if err != nil {
		t.Fatal(err)
	}
	if updated.ID != created.ID || updated.Units[0].UpdatePolicy != container.PolicyHold || updated.Units[1].UpdatePolicy != container.PolicyWatch {
		t.Fatalf("service = %+v", updated.Units)
	}
	if got := assigner.told(); len(got) != told+1 || got[len(got)-1] != remoteID {
		t.Fatalf("la machine du service doit être prévenue: %v", got)
	}
	if b.changeCount() != changes+1 {
		t.Fatalf("l'annuaire n'a pas prévenu ceux qui regardent: %d -> %d", changes, b.changeCount())
	}

	told = len(assigner.told())
	if _, err := b.directory.SetUnitUpdatePolicy(ctx, created.Units[0].ID, "pinned"); !errors.Is(err, directory.ErrUpdatePolicyInvalid) {
		t.Fatalf("politique inconnue: %v", err)
	}
	if _, err := b.directory.SetUnitUpdatePolicy(ctx, binary.Units[0].ID, container.PolicyIgnore); !errors.Is(err, directory.ErrUpdatePolicyInvalid) {
		t.Fatalf("binaire: %v", err)
	}
	if _, err := b.directory.SetUnitUpdatePolicy(ctx, "unit_inconnue", container.PolicyHold); !errors.Is(err, directory.ErrUnitNotFound) {
		t.Fatalf("unité inconnue: %v", err)
	}
	if len(assigner.told()) != told {
		t.Fatalf("un refus ne prévient personne: %v", assigner.told())
	}
	if kept, _ := b.directory.GetService(ctx, created.ID); kept.Units[0].UpdatePolicy != container.PolicyHold {
		t.Fatalf("un refus ne change rien: %+v", kept.Units)
	}
}

// Modifier le service redit la politique de chacune de ses unités, et prévient
// la machine : l'ancienne comme la nouvelle quand il en change.
func TestUpdateService_WritesThePolicyAndTellsBothMachines(t *testing.T) {
	b := newBench(t)
	ctx := context.Background()
	assigner := &recordingAssigner{}
	b.directory.SetAssigner(assigner)
	created := mustCreate(t, b, composeDefinition(remoteID))

	definition := composeDefinition(machine.LocalID)
	definition.Units[0].ID = created.Units[0].ID
	definition.Units[0].UpdatePolicy = container.PolicyIgnore
	definition.Units[1].ID = created.Units[1].ID
	before := len(assigner.told())

	updated, err := b.directory.UpdateService(ctx, created.ID, definition)

	if err != nil {
		t.Fatal(err)
	}
	if updated.Units[0].UpdatePolicy != container.PolicyIgnore || updated.Units[1].UpdatePolicy != container.PolicyWatch {
		t.Fatalf("unités = %+v", updated.Units)
	}
	told := assigner.told()[before:]
	if !slices.Contains(told, remoteID) || !slices.Contains(told, machine.LocalID) {
		t.Fatalf("l'ancienne et la nouvelle machine doivent être prévenues: %v", told)
	}
}
