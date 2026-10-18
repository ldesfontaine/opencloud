package status_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/ldesfontaine/opencloud/internal/directory"
	"github.com/ldesfontaine/opencloud/internal/machine"
	"github.com/ldesfontaine/opencloud/internal/service"
	"github.com/ldesfontaine/opencloud/internal/status"
)

// shop déclare un projet Compose de deux unités sur la machine openCloud.
func (f *fixture) shop(t *testing.T) directory.Service {
	t.Helper()
	created, err := f.directory.CreateService(context.Background(), directory.ServiceDefinition{
		MachineID: machine.LocalID, Name: "shop", Nature: directory.NatureApplication, Origin: directory.OriginCompose,
		ComposeProject: "shop",
		Units: []directory.UnitDefinition{
			{Name: "app", Image: "app", Version: "1"},
			{Name: "db", Image: "db", Version: "1"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return created
}

func (f *fixture) shopContainer(containerID, composeService string, state service.State) service.Container {
	return service.Container{
		ContainerID: containerID, Name: "shop-" + composeService + "-1", Group: "shop", ComposeService: composeService,
		Image: composeService + ":1", State: state, CreatedAt: f.now.Add(-time.Hour),
	}
}

func (f *fixture) inventory(t *testing.T, containers ...service.Container) {
	t.Helper()
	report := service.Report{Engine: &service.EngineReport{Present: true, Version: "29.8.0", APIVersion: "1.56"}, Complete: true, Inventory: containers}
	if err := f.services.Record(context.Background(), machine.LocalID, report); err != nil {
		t.Fatal(err)
	}
}

// Un service déclaré est aussi bien que sa pire unité conteneur : une unité
// que plus aucun conteneur ne porte est une panne, pas un silence.
func TestDeclaredServiceState_UnitWithoutContainerIsDown(t *testing.T) {
	f := newFixture(t)
	declared := f.shop(t)
	app, db := strings.Repeat("a", 64), strings.Repeat("b", 64)
	f.inventory(t, f.shopContainer(app, "app", service.StateRunning))

	component := f.component(t, "Boutique", status.MemberRef{Kind: status.KindService, ID: declared.ID})
	if component.Effective != status.StateDown {
		t.Fatalf("l'unité db n'a aucun conteneur : effective %q, want down", component.Effective)
	}
	if len(component.Members) != 1 || !component.Members[0].Present || !component.Members[0].Counts || component.Members[0].Name != "shop" {
		t.Fatalf("membre = %+v", component.Members)
	}

	f.inventory(t, f.shopContainer(app, "app", service.StateRunning), f.shopContainer(db, "db", service.StateRunning))
	again, err := f.status.GetComponent(context.Background(), component.ID)
	if err != nil || again.Effective != status.StateOperational {
		t.Fatalf("toutes les unités tournent : %q, %v", again.Effective, err)
	}

	f.inventory(t, f.shopContainer(app, "app", service.StateRunning), f.shopContainer(db, "db", service.StateExited))
	stopped, _ := f.status.GetComponent(context.Background(), component.ID)
	if stopped.Effective != status.StateDown {
		t.Fatalf("une unité arrêtée : %q, want down", stopped.Effective)
	}
}

// Aucun agent ne regarde encore un binaire : un service qui n'a que des unités
// binaires n'apporte rien au composant, comme un objet qui se tait.
func TestDeclaredServiceState_BinaryOnlyDoesNotCount(t *testing.T) {
	f := newFixture(t)
	binary, err := f.directory.CreateService(context.Background(), directory.ServiceDefinition{
		MachineID: machine.LocalID, Name: "caddy", Nature: directory.NatureInfra, Origin: directory.OriginBinary,
		Units: []directory.UnitDefinition{{Name: "caddy", SystemdUnit: "caddy.service"}},
	})
	if err != nil {
		t.Fatal(err)
	}

	component := f.component(t, "Proxy", status.MemberRef{Kind: status.KindService, ID: binary.ID})

	if component.Derived != status.StateUnknown || component.Members[0].Counts || !component.Members[0].Present {
		t.Fatalf("un binaire ne compte pas : derived %q, membre %+v", component.Derived, component.Members[0])
	}
}

// Recréer un conteneur change l'identifiant de sa fiche, pas son unité : le
// composant garde son membre et l'état suit le nouveau conteneur.
func TestComponent_SurvivesContainerRecreation(t *testing.T) {
	f := newFixture(t)
	declared := f.shop(t)
	app, db := strings.Repeat("a", 64), strings.Repeat("b", 64)
	f.inventory(t, f.shopContainer(app, "app", service.StateRunning), f.shopContainer(db, "db", service.StateRunning))
	component := f.component(t, "Boutique", status.MemberRef{Kind: status.KindService, ID: declared.ID})
	if component.Effective != status.StateOperational {
		t.Fatalf("avant : %q", component.Effective)
	}

	// L'ancien conteneur est détruit, un nouveau le remplace sous un autre identifiant.
	f.now = f.now.Add(time.Minute)
	recreated := strings.Repeat("c", 64)
	f.inventory(t, f.shopContainer(recreated, "app", service.StateRunning), f.shopContainer(db, "db", service.StateRunning))

	after, err := f.status.GetComponent(context.Background(), component.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(after.Members) != 1 || after.Members[0].ID != declared.ID || !after.Members[0].Present || after.Effective != status.StateOperational {
		t.Fatalf("après la recréation : %+v, effective %q", after.Members, after.Effective)
	}

	// Le conteneur recréé tombe : le composant le dit, sans que l'archivé ne le masque.
	f.inventory(t, f.shopContainer(recreated, "app", service.StateExited), f.shopContainer(db, "db", service.StateRunning))
	down, _ := f.status.GetComponent(context.Background(), component.ID)
	if down.Effective != status.StateDown {
		t.Fatalf("après l'arrêt : %q, want down", down.Effective)
	}
}

// L'alerte d'un conteneur vise la fiche de ce conteneur ; le composant, lui,
// vise le service déclaré. Une maintenance sur le composant couvre donc
// l'alerte de n'importe lequel des conteneurs du service, jamais celle d'un
// conteneur qu'aucune unité ne porte.
func TestUnderMaintenance_ContainerAlertFindsItsDeclaredService(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	declared := f.shop(t)
	app := strings.Repeat("a", 64)
	stranger := strings.Repeat("f", 64)
	f.inventory(t, f.shopContainer(app, "app", service.StateRunning), service.Container{
		ContainerID: stranger, Name: "stranger", Image: "stranger:1", State: service.StateRunning, CreatedAt: f.now.Add(-time.Hour),
	})
	component := f.component(t, "Boutique", status.MemberRef{Kind: status.KindService, ID: declared.ID})
	appFiche, strangerFiche := service.ID(machine.LocalID, app), service.ID(machine.LocalID, stranger)

	if under, err := f.status.UnderMaintenance(ctx, "service", appFiche); err != nil || under {
		t.Fatalf("sans maintenance : %v, %v", under, err)
	}
	_, err := f.status.OpenIncident(ctx, status.IncidentDefinition{
		Title: "Migration", Impact: status.ImpactMaintenance, Status: status.StatusScheduled, Message: "Lecture seule.",
		ComponentIDs: []string{component.ID}, StartsAt: f.now.Add(-time.Minute), EndsAt: f.now.Add(time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}

	if under, err := f.status.UnderMaintenance(ctx, "service", appFiche); err != nil || !under {
		t.Fatalf("l'alerte du conteneur du service doit être couverte : %v, %v", under, err)
	}
	if under, err := f.status.UnderMaintenance(ctx, "service", strangerFiche); err != nil || under {
		t.Fatalf("un conteneur non déclaré n'est dans aucun composant : %v, %v", under, err)
	}
	if under, err := f.status.UnderMaintenance(ctx, "service", "m:inconnu"); err != nil || under {
		t.Fatalf("une fiche inconnue : %v, %v", under, err)
	}
}

// Le conteneur d'initialisation d'un projet, arrêté en 0, que la reprise de
// l'existant n'a pas fait unité : il reste non déclaré et ne change rien à
// l'état du composant, qui reste vert.
func TestDeclaredServiceState_UndeclaredStoppedInitDoesNotTurnTheComponentRed(t *testing.T) {
	f := newFixture(t)
	declared, err := f.directory.CreateService(context.Background(), directory.ServiceDefinition{
		MachineID: machine.LocalID, Name: "shop", Nature: directory.NatureApplication, Origin: directory.OriginCompose,
		ComposeProject: "shop", Units: []directory.UnitDefinition{{Name: "app", Image: "app", Version: "1"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	app, initial := strings.Repeat("a", 64), strings.Repeat("b", 64)
	f.inventory(t, f.shopContainer(app, "app", service.StateRunning), f.shopContainer(initial, "init", service.StateExited))

	component := f.component(t, "Boutique", status.MemberRef{Kind: status.KindService, ID: declared.ID})

	if component.Effective != status.StateOperational {
		t.Fatalf("l'init non déclaré ne compte pas : %q", component.Effective)
	}
}
