package directory_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"log/slog"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ldesfontaine/opencloud/internal/directory"
	"github.com/ldesfontaine/opencloud/internal/machine"
	"github.com/ldesfontaine/opencloud/internal/seal"
	"github.com/ldesfontaine/opencloud/internal/service"
	"github.com/ldesfontaine/opencloud/internal/store"
	"github.com/ldesfontaine/opencloud/internal/update"
)

var testNow = time.Date(2026, 10, 18, 9, 0, 0, 0, time.UTC)

const remoteID = "11111111-2222-4333-8444-555555555555"

type bench struct {
	directory  *directory.Directory
	containers *service.Tracker
	db         *store.DB
	machines   *machine.Service
	mu         sync.Mutex
	changes    int
}

func (b *bench) DirectoryChanged() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.changes++
}

func (b *bench) changeCount() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.changes
}

// L'annuaire se teste sur la vraie base SQLite, temporaire, avec la machine
// openCloud et une machine enrôlée en place.
func newBench(t *testing.T) *bench {
	t.Helper()
	root, err := os.OpenRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { root.Close() })
	db, err := store.Open(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	db.SetSealer(seal.Generate())
	t.Cleanup(func() { db.Close() })
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	machines := machine.New(db, machine.NewSessions(), logger)
	machines.SetClock(func() time.Time { return testNow })
	err = machines.EnsureLocal(context.Background(), machine.LocalInfo{Hostname: "opencloud-host", Address: "10.8.0.1", OS: "Debian 12", Arch: "amd64", Version: "v0.0.1"})
	if err != nil {
		t.Fatal(err)
	}
	b := &bench{db: db, machines: machines}
	enrollRemote(t, b)
	b.directory = directory.New(db, logger)
	b.directory.SetClock(func() time.Time { return testNow })
	b.directory.SetWatcher(b)
	// Le même câblage que serve : l'annuaire lit les conteneurs et les
	// constats, et le suivi des services lui demande les unités.
	b.containers = service.New(db, logger)
	b.containers.SetClock(func() time.Time { return testNow })
	b.containers.SetUnits(b.directory)
	b.directory.SetObserved(b.containers, update.New(db, logger))
	b.directory.SetRematcher(b.containers)
	return b
}

func enrollRemote(t *testing.T, b *bench) {
	t.Helper()
	cleartext, _, err := b.machines.CreateToken(context.Background(), "vps-paris-1")
	if err != nil {
		t.Fatal(err)
	}
	public, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	_, err = b.machines.Enroll(context.Background(), machine.Enrollment{
		MachineID: remoteID, PublicKey: public, Token: cleartext,
		Hostname: "vps-paris-1", Address: "51.15.20.114", OS: "Debian 12", Arch: "amd64", AgentVersion: "v0.0.1",
	})
	if err != nil {
		t.Fatal(err)
	}
}

func composeDefinition(machineID string) directory.ServiceDefinition {
	return directory.ServiceDefinition{
		MachineID:      machineID,
		Name:           "nextcloud",
		Title:          "Nextcloud",
		Nature:         directory.NatureApplication,
		Origin:         directory.OriginCompose,
		ComposeProject: "nextcloud",
		PublicURL:      "https://cloud.exemple.fr",
		Units: []directory.UnitDefinition{
			{Name: "app", Image: "nextcloud", Version: "29.0.4", Ports: []string{"8080:80"}},
			{Name: "db", Image: "postgres", Version: "16.4"},
		},
	}
}

func containerDefinition(machineID, name, containerName string) directory.ServiceDefinition {
	return directory.ServiceDefinition{
		MachineID: machineID,
		Name:      name,
		Nature:    directory.NatureApplication,
		Origin:    directory.OriginContainer,
		Units:     []directory.UnitDefinition{{Name: name, Image: "traefik/whoami", ContainerName: containerName}},
	}
}

func mustCreate(t *testing.T, b *bench, definition directory.ServiceDefinition) directory.Service {
	t.Helper()
	created, err := b.directory.CreateService(context.Background(), definition)
	if err != nil {
		t.Fatalf("create %s: %v", definition.Name, err)
	}
	return created
}

func mustCreateProject(t *testing.T, b *bench, name string) directory.Project {
	t.Helper()
	created, err := b.directory.CreateProject(context.Background(), directory.ProjectDefinition{Name: name, Title: name})
	if err != nil {
		t.Fatal(err)
	}
	return created
}

func unitNames(units []directory.Unit) []string {
	names := make([]string, 0, len(units))
	for _, unit := range units {
		names = append(names, unit.Name)
	}
	return names
}

func TestCreateService_WritesUnitsProvidesAndProjects(t *testing.T) {
	b := newBench(t)
	ctx := context.Background()
	project := mustCreateProject(t, b, "client-a")
	definition := composeDefinition(remoteID)
	definition.Nature = directory.NatureInfra
	definition.Provides = []directory.Capability{directory.CapabilityTLS, directory.CapabilityProxy}
	definition.ProjectIDs = []string{project.ID}

	created := mustCreate(t, b, definition)

	read, err := b.directory.GetService(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if read.DeclaredBy != directory.ByOperator || read.NeedsReview {
		t.Fatalf("provenance %q, à vérifier %v", read.DeclaredBy, read.NeedsReview)
	}
	if read.MachineName != "vps-paris-1" || !read.CreatedAt.Equal(testNow) {
		t.Fatalf("machine %q, créé %v", read.MachineName, read.CreatedAt)
	}
	if !slices.Equal(unitNames(read.Units), []string{"app", "db"}) || read.Units[0].Kind != directory.UnitContainer {
		t.Fatalf("unités %+v", read.Units)
	}
	if read.Units[0].ID == "" || read.Units[0].ServiceID != created.ID || !slices.Equal(read.Units[0].Ports, []string{"8080:80"}) {
		t.Fatalf("première unité %+v", read.Units[0])
	}
	// Les fournitures se relisent par ordre alphabétique : aucune position.
	if !slices.Equal(read.Provides, []directory.Capability{directory.CapabilityProxy, directory.CapabilityTLS}) {
		t.Fatalf("fournitures %v", read.Provides)
	}
	if !slices.Equal(read.ProjectIDs, []string{project.ID}) {
		t.Fatalf("projets %v", read.ProjectIDs)
	}
	linked, err := b.directory.GetProject(ctx, project.ID)
	if err != nil || !slices.Equal(linked.ServiceIDs, []string{created.ID}) {
		t.Fatalf("services du projet %+v, %v", linked, err)
	}
}

func TestUpdateService_KeepsUnitIDsAndDropsMissingOnes(t *testing.T) {
	b := newBench(t)
	ctx := context.Background()
	created := mustCreate(t, b, composeDefinition(remoteID))
	app, db := created.Units[0], created.Units[1]

	definition := composeDefinition(remoteID)
	definition.Title = "Nextcloud (prod)"
	definition.Units = []directory.UnitDefinition{
		{ID: app.ID, Name: "app", Image: "nextcloud", Version: "29.0.6"},
		{Name: "redis", Image: "redis", Version: "7.4"},
	}
	updated, err := b.directory.UpdateService(ctx, created.ID, definition)
	if err != nil {
		t.Fatal(err)
	}

	if !slices.Equal(unitNames(updated.Units), []string{"app", "redis"}) {
		t.Fatalf("unités %+v", updated.Units)
	}
	if updated.Units[0].ID != app.ID || updated.Units[0].Version != "29.0.6" {
		t.Fatalf("l'unité gardée a changé d'identifiant ou n'a pas été éditée: %+v", updated.Units[0])
	}
	if updated.Units[1].ID == db.ID || updated.Units[1].ID == "" {
		t.Fatalf("la nouvelle unité a repris l'identifiant de l'ancienne: %+v", updated.Units[1])
	}
	if updated.ID != created.ID || updated.Title != "Nextcloud (prod)" || !updated.CreatedAt.Equal(created.CreatedAt) {
		t.Fatalf("service %+v", updated)
	}
}

func TestUpdateService_SwapsUnitNamesWithoutCollision(t *testing.T) {
	b := newBench(t)
	created := mustCreate(t, b, composeDefinition(remoteID))
	first, second := created.Units[0], created.Units[1]

	definition := composeDefinition(remoteID)
	definition.Units = []directory.UnitDefinition{
		{ID: first.ID, Name: "db", Image: "postgres", Version: "16.4"},
		{ID: second.ID, Name: "app", Image: "nextcloud", Version: "29.0.4"},
	}
	updated, err := b.directory.UpdateService(context.Background(), created.ID, definition)
	if err != nil {
		t.Fatalf("des unités qui échangent leurs noms sont refusées: %v", err)
	}
	if updated.Units[0].ID != first.ID || updated.Units[0].Name != "db" || updated.Units[1].ID != second.ID || updated.Units[1].Name != "app" {
		t.Fatalf("unités %+v", updated.Units)
	}
}

func TestUpdateService_RefusesAUnitOfAnotherService(t *testing.T) {
	b := newBench(t)
	first := mustCreate(t, b, composeDefinition(remoteID))
	other := mustCreate(t, b, containerDefinition(remoteID, "whoami", "whoami"))

	definition := composeDefinition(remoteID)
	definition.Units[0].ID = other.Units[0].ID
	if _, err := b.directory.UpdateService(context.Background(), first.ID, definition); !errors.Is(err, directory.ErrUnitInvalid) {
		t.Fatalf("unité d'un autre service: %v", err)
	}
	definition.Units[0].ID = "unit_inconnue"
	if _, err := b.directory.UpdateService(context.Background(), first.ID, definition); !errors.Is(err, directory.ErrUnitInvalid) {
		t.Fatalf("identifiant d'unité inventé: %v", err)
	}
}

func TestUpdateService_RefusesTheSameUnitIDTwice(t *testing.T) {
	b := newBench(t)
	created := mustCreate(t, b, composeDefinition(remoteID))

	definition := composeDefinition(remoteID)
	definition.Units = []directory.UnitDefinition{
		{ID: created.Units[0].ID, Name: "app", Image: "nextcloud", Version: "29.0.4"},
		{ID: created.Units[0].ID, Name: "db", Image: "postgres", Version: "16.4"},
	}
	if _, err := b.directory.UpdateService(context.Background(), created.ID, definition); !errors.Is(err, directory.ErrUnitInvalid) {
		t.Fatalf("le même identifiant d'unité deux fois: %v", err)
	}
	kept, err := b.directory.GetService(context.Background(), created.ID)
	if err != nil || len(kept.Units) != 2 {
		t.Fatalf("le refus a changé les unités: %+v, %v", kept.Units, err)
	}
}

// Changer d'origine garde le service et ses unités : le genre suit
// l'origine, le projet Compose se vide, les identifiants restent.
func TestUpdateService_ChangingOrigin_KeepsIDsAndFollowsKind(t *testing.T) {
	b := newBench(t)
	ctx := context.Background()
	created := mustCreate(t, b, composeDefinition(remoteID))
	app, db := created.Units[0], created.Units[1]

	asContainers := composeDefinition(remoteID)
	asContainers.Origin = directory.OriginContainer
	asContainers.ComposeProject = ""
	asContainers.Units = []directory.UnitDefinition{
		{ID: app.ID, Name: "app", Image: "nextcloud", Version: "29.0.4", ContainerName: "nextcloud-app"},
		{ID: db.ID, Name: "db", Image: "postgres", Version: "16.4", ContainerName: "nextcloud-db"},
	}
	updated, err := b.directory.UpdateService(ctx, created.ID, asContainers)
	if err != nil {
		t.Fatalf("compose vers conteneurs: %v", err)
	}
	if updated.Origin != directory.OriginContainer || updated.ComposeProject != "" ||
		updated.Units[0].ID != app.ID || updated.Units[1].ID != db.ID ||
		updated.Units[0].Kind != directory.UnitContainer || updated.Units[0].ContainerName != "nextcloud-app" {
		t.Fatalf("service %+v", updated)
	}

	asBinaries := composeDefinition(remoteID)
	asBinaries.Origin = directory.OriginBinary
	asBinaries.ComposeProject = ""
	asBinaries.Units = []directory.UnitDefinition{
		{ID: app.ID, Name: "app", Version: "29.0.4", SystemdUnit: "nextcloud-app.service"},
		{ID: db.ID, Name: "db", Version: "16.4", SystemdUnit: "nextcloud-db.service"},
	}
	updated, err = b.directory.UpdateService(ctx, created.ID, asBinaries)
	if err != nil {
		t.Fatalf("conteneurs vers binaires: %v", err)
	}
	if updated.Origin != directory.OriginBinary || updated.Units[0].Kind != directory.UnitBinary ||
		updated.Units[0].ID != app.ID || updated.Units[0].Image != "" || updated.Units[0].ContainerName != "" ||
		updated.Units[1].SystemdUnit != "nextcloud-db.service" {
		t.Fatalf("service %+v", updated)
	}

	backToCompose, err := b.directory.UpdateService(ctx, created.ID, composeDefinition(remoteID))
	if err != nil {
		t.Fatalf("binaires vers Compose: %v", err)
	}
	if backToCompose.ComposeProject != "nextcloud" || backToCompose.Units[0].Kind != directory.UnitContainer ||
		backToCompose.Units[0].SystemdUnit != "" {
		t.Fatalf("service %+v", backToCompose)
	}
}

// Déplacer un service vers une machine, c'est y déclarer son nom, son projet
// Compose et ses clés : chacun peut y être déjà pris.
func TestUpdateService_MovingToAMachineWhereItsKeysAreTaken_Refused(t *testing.T) {
	b := newBench(t)
	ctx := context.Background()
	mustCreate(t, b, composeDefinition(machine.LocalID))
	mustCreate(t, b, containerDefinition(machine.LocalID, "whoami", "whoami"))

	sameName := mustCreate(t, b, containerDefinition(remoteID, "whoami", "whoami-remote"))
	composeRemote := composeDefinition(remoteID)
	composeRemote.Name = "cloud-distant"
	sameProject := mustCreate(t, b, composeRemote)
	sameContainer := mustCreate(t, b, containerDefinition(remoteID, "autre", "whoami"))

	cases := []struct {
		name    string
		service directory.Service
		moved   directory.ServiceDefinition
		want    error
	}{
		{"nom", sameName, containerDefinition(machine.LocalID, "whoami", "whoami-remote"), directory.ErrNameTaken},
		{"projet Compose", sameProject, func() directory.ServiceDefinition {
			d := composeDefinition(machine.LocalID)
			d.Name = "cloud-distant"
			return d
		}(), directory.ErrComposeProjectTaken},
		{"nom de conteneur", sameContainer, containerDefinition(machine.LocalID, "autre", "whoami"), directory.ErrMatchKeyTaken},
	}
	for _, testCase := range cases {
		if _, err := b.directory.UpdateService(ctx, testCase.service.ID, testCase.moved); !errors.Is(err, testCase.want) {
			t.Errorf("%s: got %v, want %v", testCase.name, err, testCase.want)
		}
		kept, err := b.directory.GetService(ctx, testCase.service.ID)
		if err != nil || kept.MachineID != remoteID {
			t.Errorf("%s: le refus a déplacé le service: %+v, %v", testCase.name, kept, err)
		}
	}
}

func TestUpdateService_UnitsSwapContainerNames_Accepted(t *testing.T) {
	b := newBench(t)
	definition := containerDefinition(remoteID, "paire", "un")
	definition.Units = []directory.UnitDefinition{
		{Name: "a", Image: "traefik/whoami", ContainerName: "un"},
		{Name: "b", Image: "traefik/whoami", ContainerName: "deux"},
	}
	created := mustCreate(t, b, definition)

	definition.Units = []directory.UnitDefinition{
		{ID: created.Units[0].ID, Name: "a", Image: "traefik/whoami", ContainerName: "deux"},
		{ID: created.Units[1].ID, Name: "b", Image: "traefik/whoami", ContainerName: "un"},
	}
	updated, err := b.directory.UpdateService(context.Background(), created.ID, definition)
	if err != nil {
		t.Fatalf("des unités qui échangent leurs noms de conteneur sont refusées: %v", err)
	}
	if updated.Units[0].ContainerName != "deux" || updated.Units[1].ContainerName != "un" {
		t.Fatalf("unités %+v", updated.Units)
	}
}

func TestUpdateService_CanMoveToAnotherMachine(t *testing.T) {
	b := newBench(t)
	created := mustCreate(t, b, composeDefinition(remoteID))
	moved, err := b.directory.UpdateService(context.Background(), created.ID, composeDefinition(machine.LocalID))
	if err != nil {
		t.Fatal(err)
	}
	if moved.MachineID != machine.LocalID || moved.ID != created.ID {
		t.Fatalf("service %+v", moved)
	}
}

func TestUpdateService_UnknownService_IsNotFound(t *testing.T) {
	b := newBench(t)
	if _, err := b.directory.UpdateService(context.Background(), "svc_inconnu", composeDefinition(remoteID)); !errors.Is(err, directory.ErrNotFound) {
		t.Fatalf("got %v", err)
	}
	if err := b.directory.DeleteService(context.Background(), "svc_inconnu"); !errors.Is(err, directory.ErrNotFound) {
		t.Fatalf("got %v", err)
	}
}

func TestCreateService_SameNameSameMachine_Refused_OtherMachine_Accepted(t *testing.T) {
	b := newBench(t)
	mustCreate(t, b, containerDefinition(remoteID, "whoami", "whoami"))

	_, err := b.directory.CreateService(context.Background(), containerDefinition(remoteID, "whoami", "whoami-2"))
	if !errors.Is(err, directory.ErrNameTaken) {
		t.Fatalf("même nom, même machine: %v", err)
	}
	if _, err := b.directory.CreateService(context.Background(), containerDefinition(machine.LocalID, "whoami", "whoami")); err != nil {
		t.Fatalf("même nom, autre machine: %v", err)
	}
}

func TestCreateService_ComposeProjectTaken_Refused(t *testing.T) {
	b := newBench(t)
	mustCreate(t, b, composeDefinition(remoteID))

	second := composeDefinition(remoteID)
	second.Name = "nextcloud-preprod"
	if _, err := b.directory.CreateService(context.Background(), second); !errors.Is(err, directory.ErrComposeProjectTaken) {
		t.Fatalf("même projet Compose, même machine: %v", err)
	}
	if _, err := b.directory.CreateService(context.Background(), composeDefinition(machine.LocalID)); err != nil {
		t.Fatalf("même projet Compose, autre machine: %v", err)
	}
}

func TestCreateService_ContainerNameTakenOnMachine_Refused(t *testing.T) {
	b := newBench(t)
	mustCreate(t, b, containerDefinition(remoteID, "whoami", "whoami"))

	if _, err := b.directory.CreateService(context.Background(), containerDefinition(remoteID, "autre", "whoami")); !errors.Is(err, directory.ErrMatchKeyTaken) {
		t.Fatalf("même conteneur, même machine: %v", err)
	}
	if _, err := b.directory.CreateService(context.Background(), containerDefinition(machine.LocalID, "autre", "whoami")); err != nil {
		t.Fatalf("même conteneur, autre machine: %v", err)
	}

	twice := containerDefinition(remoteID, "double", "un")
	twice.Units = append(twice.Units, directory.UnitDefinition{Name: "bis", Image: "traefik/whoami", ContainerName: "un"})
	if _, err := b.directory.CreateService(context.Background(), twice); !errors.Is(err, directory.ErrUnitKeyDuplicate) {
		t.Fatalf("même conteneur pour deux unités du même service: %v", err)
	}
}

func TestCreateService_SystemdUnitTakenOnMachine_Refused(t *testing.T) {
	b := newBench(t)
	caddy := directory.ServiceDefinition{
		MachineID: remoteID, Name: "caddy", Nature: directory.NatureInfra, Origin: directory.OriginBinary,
		Provides: []directory.Capability{directory.CapabilityProxy},
		Units:    []directory.UnitDefinition{{Name: "caddy", SystemdUnit: "caddy.service"}},
	}
	mustCreate(t, b, caddy)
	caddy.Name = "proxy"
	if _, err := b.directory.CreateService(context.Background(), caddy); !errors.Is(err, directory.ErrMatchKeyTaken) {
		t.Fatalf("même unité systemd, même machine: %v", err)
	}
}

func TestUpdateService_KeepingItsOwnNameAndKeys_IsAccepted(t *testing.T) {
	b := newBench(t)
	created := mustCreate(t, b, containerDefinition(remoteID, "whoami", "whoami"))
	definition := containerDefinition(remoteID, "whoami", "whoami")
	definition.Units[0].ID = created.Units[0].ID
	if _, err := b.directory.UpdateService(context.Background(), created.ID, definition); err != nil {
		t.Fatalf("un service est refusé à cause de lui-même: %v", err)
	}
}

func TestCreateService_UnknownMachineOrProject_Refused(t *testing.T) {
	b := newBench(t)
	if _, err := b.directory.CreateService(context.Background(), composeDefinition("machine-inconnue")); !errors.Is(err, directory.ErrMachineInvalid) {
		t.Fatalf("machine inconnue: %v", err)
	}
	definition := composeDefinition(remoteID)
	definition.ProjectIDs = []string{"prj_inconnu"}
	if _, err := b.directory.CreateService(context.Background(), definition); !errors.Is(err, directory.ErrProjectInvalid) {
		t.Fatalf("projet inconnu: %v", err)
	}
	listed, err := b.directory.ListServices(context.Background(), directory.Filter{})
	if err != nil || len(listed) != 0 {
		t.Fatalf("un refus a laissé une trace: %+v, %v", listed, err)
	}
}

func TestListServices_FiltersByMachineAndProject(t *testing.T) {
	b := newBench(t)
	ctx := context.Background()
	project := mustCreateProject(t, b, "client-a")
	inProject := composeDefinition(remoteID)
	inProject.ProjectIDs = []string{project.ID}
	mustCreate(t, b, inProject)
	mustCreate(t, b, containerDefinition(machine.LocalID, "whoami", "whoami"))

	cases := map[string]struct {
		filter directory.Filter
		want   []string
	}{
		"tout":     {directory.Filter{}, []string{"nextcloud", "whoami"}},
		"machine":  {directory.Filter{MachineID: machine.LocalID}, []string{"whoami"}},
		"projet":   {directory.Filter{ProjectID: project.ID}, []string{"nextcloud"}},
		"les deux": {directory.Filter{MachineID: machine.LocalID, ProjectID: project.ID}, []string{}},
	}
	for name, testCase := range cases {
		listed, err := b.directory.ListServices(ctx, testCase.filter)
		if err != nil {
			t.Fatal(err)
		}
		got := []string{}
		for _, service := range listed {
			got = append(got, service.Name)
			if len(service.Units) == 0 {
				t.Errorf("%s: %s sans unités", name, service.Name)
			}
		}
		if !slices.Equal(got, testCase.want) {
			t.Errorf("%s: got %v, want %v", name, got, testCase.want)
		}
	}
}

func TestDeleteProject_KeepsItsServices(t *testing.T) {
	b := newBench(t)
	ctx := context.Background()
	project := mustCreateProject(t, b, "client-a")
	definition := composeDefinition(remoteID)
	definition.ProjectIDs = []string{project.ID}
	created := mustCreate(t, b, definition)

	if err := b.directory.DeleteProject(ctx, project.ID); err != nil {
		t.Fatal(err)
	}
	kept, err := b.directory.GetService(ctx, created.ID)
	if err != nil {
		t.Fatalf("le service a suivi son projet: %v", err)
	}
	if len(kept.ProjectIDs) != 0 || len(kept.Units) != 2 {
		t.Fatalf("service %+v", kept)
	}
	if _, err := b.directory.GetProject(ctx, project.ID); !errors.Is(err, directory.ErrProjectNotFound) {
		t.Fatalf("le projet existe encore: %v", err)
	}
}

func TestDeleteService_TakesItsPartsAndKeepsTheProject(t *testing.T) {
	b := newBench(t)
	ctx := context.Background()
	project := mustCreateProject(t, b, "client-a")
	definition := composeDefinition(remoteID)
	definition.ProjectIDs = []string{project.ID}
	created := mustCreate(t, b, definition)

	if err := b.directory.DeleteService(ctx, created.ID); err != nil {
		t.Fatal(err)
	}
	kept, err := b.directory.GetProject(ctx, project.ID)
	if err != nil || len(kept.ServiceIDs) != 0 {
		t.Fatalf("projet %+v, %v", kept, err)
	}
	// Le nom, le projet Compose et les clés d'observation sont libres.
	if _, err := b.directory.CreateService(ctx, composeDefinition(remoteID)); err != nil {
		t.Fatalf("recréer après suppression: %v", err)
	}
}

func TestProjects_NameUniqueAndRefusals(t *testing.T) {
	b := newBench(t)
	ctx := context.Background()
	first := mustCreateProject(t, b, "client-a")
	second := mustCreateProject(t, b, "client-b")

	if _, err := b.directory.CreateProject(ctx, directory.ProjectDefinition{Name: "client-a"}); !errors.Is(err, directory.ErrProjectNameTaken) {
		t.Fatalf("nom pris: %v", err)
	}
	if _, err := b.directory.UpdateProject(ctx, second.ID, directory.ProjectDefinition{Name: "client-a"}); !errors.Is(err, directory.ErrProjectNameTaken) {
		t.Fatalf("renommage sur un nom pris: %v", err)
	}
	renamed, err := b.directory.UpdateProject(ctx, first.ID, directory.ProjectDefinition{Name: "client-a", Title: "Client A"})
	if err != nil || renamed.Title != "Client A" {
		t.Fatalf("garder son nom: %+v, %v", renamed, err)
	}
	if _, err := b.directory.UpdateProject(ctx, "prj_inconnu", directory.ProjectDefinition{Name: "x"}); !errors.Is(err, directory.ErrProjectNotFound) {
		t.Fatalf("projet inconnu: %v", err)
	}
	if _, err := b.directory.CreateProject(ctx, directory.ProjectDefinition{Name: "Client A"}); !errors.Is(err, directory.ErrProjectNameInvalid) {
		t.Fatalf("nom invalide: %v", err)
	}
}

func TestWrites_TellTheWatcher(t *testing.T) {
	b := newBench(t)
	ctx := context.Background()
	project := mustCreateProject(t, b, "client-a")
	created := mustCreate(t, b, composeDefinition(remoteID))
	if _, err := b.directory.UpdateService(ctx, created.ID, composeDefinition(remoteID)); err != nil {
		t.Fatal(err)
	}
	if _, err := b.directory.UpdateProject(ctx, project.ID, directory.ProjectDefinition{Name: "client-b"}); err != nil {
		t.Fatal(err)
	}
	if err := b.directory.DeleteService(ctx, created.ID); err != nil {
		t.Fatal(err)
	}
	if err := b.directory.DeleteProject(ctx, project.ID); err != nil {
		t.Fatal(err)
	}
	if got := b.changeCount(); got != 6 {
		t.Fatalf("%d changements annoncés, attendu 6", got)
	}
	// Un refus n'annonce rien.
	if _, err := b.directory.CreateService(ctx, composeDefinition("machine-inconnue")); err == nil {
		t.Fatal("refus attendu")
	}
	if got := b.changeCount(); got != 6 {
		t.Fatalf("un refus a été annoncé: %d", got)
	}
}

// Le rapprochement se relance sur les machines que l'écriture touche, une
// fois l'écriture validée.
type rematchSpy struct {
	machines []string
}

func (s *rematchSpy) Rematch(_ context.Context, machineID string) error {
	s.machines = append(s.machines, machineID)
	return nil
}

func TestWrites_RematchTheMachines(t *testing.T) {
	b := newBench(t)
	ctx := context.Background()
	spy := &rematchSpy{}
	b.directory.SetRematcher(spy)

	created := mustCreate(t, b, composeDefinition(remoteID))
	if !slices.Equal(spy.machines, []string{remoteID}) {
		t.Fatalf("création: %v", spy.machines)
	}

	spy.machines = nil
	if _, err := b.directory.UpdateService(ctx, created.ID, composeDefinition(remoteID)); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(spy.machines, []string{remoteID}) {
		t.Fatalf("édition sur place: %v", spy.machines)
	}

	// Changer de machine : celle qu'on quitte perd ses rattachements, l'autre en gagne.
	spy.machines = nil
	moved := composeDefinition(machine.LocalID)
	if _, err := b.directory.UpdateService(ctx, created.ID, moved); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(spy.machines, []string{remoteID, machine.LocalID}) {
		t.Fatalf("changement de machine: %v", spy.machines)
	}

	spy.machines = nil
	if err := b.directory.DeleteService(ctx, created.ID); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(spy.machines, []string{machine.LocalID}) {
		t.Fatalf("suppression: %v", spy.machines)
	}

	// Un refus n'écrit rien, il ne relance rien non plus.
	spy.machines = nil
	if _, err := b.directory.CreateService(ctx, composeDefinition("machine-inconnue")); err == nil {
		t.Fatal("refus attendu")
	}
	if len(spy.machines) != 0 {
		t.Fatalf("refus: %v", spy.machines)
	}
}

type failingRematcher struct{}

func (failingRematcher) Rematch(context.Context, string) error { return errors.New("boom") }

// L'opérateur a écrit : un rapprochement qui échoue ne défait pas son geste.
func TestWrites_ARematchFailureDoesNotUndoTheWrite(t *testing.T) {
	b := newBench(t)
	b.directory.SetRematcher(failingRematcher{})

	created, err := b.directory.CreateService(context.Background(), composeDefinition(remoteID))
	if err != nil {
		t.Fatalf("création: %v", err)
	}
	if _, err := b.directory.GetService(context.Background(), created.ID); err != nil {
		t.Fatalf("lecture: %v", err)
	}
}

func TestIgnore_RefusesBadKindAndUnknownMachine(t *testing.T) {
	b := newBench(t)
	ctx := context.Background()
	cases := []struct {
		name string
		key  directory.IgnoredKey
		want error
	}{
		{"genre inconnu", directory.IgnoredKey{MachineID: remoteID, Kind: "id", Value: "whoami"}, directory.ErrIgnoreInvalid},
		{"machine vide", directory.IgnoredKey{Kind: directory.IgnoreContainerName, Value: "whoami"}, directory.ErrIgnoreInvalid},
		{"projet en majuscules", directory.IgnoredKey{MachineID: remoteID, Kind: directory.IgnoreComposeProject, Value: "Blog"}, directory.ErrIgnoreInvalid},
		{"nom de conteneur avec une barre", directory.IgnoredKey{MachineID: remoteID, Kind: directory.IgnoreContainerName, Value: "a/b"}, directory.ErrIgnoreInvalid},
		{"valeur vide", directory.IgnoredKey{MachineID: remoteID, Kind: directory.IgnoreContainerName}, directory.ErrIgnoreInvalid},
		{"machine inconnue", directory.IgnoredKey{MachineID: "machine-inconnue", Kind: directory.IgnoreContainerName, Value: "whoami"}, directory.ErrMachineInvalid},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if err := b.directory.Ignore(ctx, c.key); !errors.Is(err, c.want) {
				t.Fatalf("err = %v, want %v", err, c.want)
			}
		})
	}
	if got := b.changeCount(); got != 0 {
		t.Fatalf("un refus a été annoncé: %d", got)
	}
}

func TestIgnore_IsIdempotentAndUnignoreNotFound(t *testing.T) {
	b := newBench(t)
	ctx := context.Background()
	key := directory.IgnoredKey{MachineID: remoteID, Kind: directory.IgnoreComposeProject, Value: " blog "}

	if err := b.directory.Ignore(ctx, key); err != nil {
		t.Fatal(err)
	}
	if err := b.directory.Ignore(ctx, key); err != nil {
		t.Fatalf("ignorer deux fois: %v", err)
	}
	stored, err := b.db.ListIgnoredKeys(ctx, remoteID)
	if err != nil || len(stored) != 1 || stored[0].Value != "blog" || !stored[0].CreatedAt.Equal(testNow) {
		t.Fatalf("clés = %+v, %v", stored, err)
	}

	key.Value = "blog"
	if err := b.directory.Unignore(ctx, key); err != nil {
		t.Fatal(err)
	}
	if err := b.directory.Unignore(ctx, key); !errors.Is(err, directory.ErrIgnoredNotFound) {
		t.Fatalf("retirer deux fois: %v", err)
	}
}

// Les identifiants de conteneurs de Docker : 64 hexadécimaux.
var (
	containerID1 = strings.Repeat("1", 64)
	containerID2 = strings.Repeat("2", 64)
)

func inventoryOf(containers ...service.Container) service.Report {
	return service.Report{Engine: &service.EngineReport{Present: true}, Complete: true, Inventory: containers}
}

func composeContainerOf(id, name, project, composeService, image string) service.Container {
	return service.Container{
		ContainerID: id, Name: name, Group: project, ComposeService: composeService, Image: image,
		State: service.StateRunning, CreatedAt: testNow.Add(-time.Hour),
	}
}

// Un conteneur non déclaré n'a pas de projet : filtrer par projet ne dit
// que ce qui est déclaré.
func TestGaps_ProjectFilterHidesUndeclared(t *testing.T) {
	b := newBench(t)
	ctx := context.Background()
	project := mustCreateProject(t, b, "client-a")
	definition := composeDefinition(remoteID)
	definition.ProjectIDs = []string{project.ID}
	created := mustCreate(t, b, definition)
	report := inventoryOf(
		composeContainerOf(containerID1, "nextcloud-app-1", "nextcloud", "app", "nextcloud:29.0.4"),
		composeContainerOf(containerID2, "blog-web-1", "blog", "web", "nginx:1.27"),
	)
	if err := b.containers.Record(ctx, remoteID, report); err != nil {
		t.Fatal(err)
	}

	onMachine, err := b.directory.Gaps(ctx, directory.Filter{MachineID: remoteID})
	if err != nil {
		t.Fatal(err)
	}
	if len(onMachine.Services) != 1 || len(onMachine.Undeclared) != 1 || onMachine.Undeclared[0].Key.Value != "blog" {
		t.Fatalf("machine: %+v", onMachine)
	}
	if got := onMachine.Services[0]; got.ServiceID != created.ID || got.State != directory.GapAbsent || got.Units[0].State != directory.GapConform {
		t.Fatalf("service: %+v", got)
	}

	inProject, err := b.directory.Gaps(ctx, directory.Filter{ProjectID: project.ID})
	if err != nil {
		t.Fatal(err)
	}
	if len(inProject.Services) != 1 || inProject.Undeclared == nil || len(inProject.Undeclared) != 0 {
		t.Fatalf("projet: %+v", inProject)
	}
}

// Le chemin entier : l'annuaire écrit, le suivi des services rattache ; un
// rapport suivant garde le rattachement ; l'unité supprimée rend le
// conteneur non déclaré ; un conteneur détruit garde son unité.
func TestContainers_FollowTheDirectory(t *testing.T) {
	b := newBench(t)
	ctx := context.Background()
	if err := b.containers.Record(ctx, remoteID, inventoryOf(
		composeContainerOf(containerID1, "nextcloud-app-1", "nextcloud", "app", "nextcloud:29.0.4"),
		composeContainerOf(containerID2, "nextcloud-db-1", "nextcloud", "db", "postgres:16.4"),
	)); err != nil {
		t.Fatal(err)
	}
	app := service.ID(remoteID, containerID1)
	if found, _ := b.containers.Get(ctx, app); found.UnitID != "" {
		t.Fatalf("rien n'est déclaré: %+v", found)
	}

	created := mustCreate(t, b, composeDefinition(remoteID))
	found, err := b.containers.Get(ctx, app)
	if err != nil || found.UnitID != created.Units[0].ID || found.MatchLevel != service.MatchCompose {
		t.Fatalf("après la déclaration: %+v, %v", found, err)
	}
	link, err := b.directory.ContainerLink(ctx, found)
	if err != nil || link.ServiceName != "nextcloud" || link.UnitName != "app" || link.Level != service.MatchCompose {
		t.Fatalf("lien: %+v, %v", link, err)
	}

	// Un rapport qui retouche la fiche garde le rattachement.
	if err := b.containers.Record(ctx, remoteID, inventoryOf(
		composeContainerOf(containerID1, "nextcloud-app-1", "nextcloud", "app", "nextcloud:29.0.4"),
		composeContainerOf(containerID2, "nextcloud-db-1", "nextcloud", "db", "postgres:16.4"),
	)); err != nil {
		t.Fatal(err)
	}
	if found, _ := b.containers.Get(ctx, app); found.UnitID != created.Units[0].ID {
		t.Fatalf("après un rapport: %+v", found)
	}

	// Le conteneur est détruit : sa fiche archivée garde son unité.
	if err := b.containers.Record(ctx, remoteID, inventoryOf(
		composeContainerOf(containerID2, "nextcloud-db-1", "nextcloud", "db", "postgres:16.4"),
	)); err != nil {
		t.Fatal(err)
	}
	gone, _ := b.containers.Get(ctx, app)
	if !gone.IsArchived() || gone.UnitID != created.Units[0].ID {
		t.Fatalf("archivé: %+v", gone)
	}

	// L'unité disparaît de la définition : le conteneur vivant devient non déclaré.
	definition := composeDefinition(remoteID)
	definition.Units = []directory.UnitDefinition{{ID: created.Units[0].ID, Name: "app", Image: "nextcloud", Version: "29.0.4"}}
	if _, err := b.directory.UpdateService(ctx, created.ID, definition); err != nil {
		t.Fatal(err)
	}
	db, _ := b.containers.Get(ctx, service.ID(remoteID, containerID2))
	if db.UnitID != "" || db.MatchLevel != service.MatchNone {
		t.Fatalf("db: %+v", db)
	}
	if err := b.directory.DeleteService(ctx, created.ID); err != nil {
		t.Fatal(err)
	}
	gone, _ = b.containers.Get(ctx, app)
	if gone.UnitID != "" || gone.MatchLevel != service.MatchNone {
		t.Fatalf("la fiche archivée perd l'unité supprimée: %+v", gone)
	}
}

func TestPropose_ReadsTheContainerAndRefusesWhatIsGoneOrDeclared(t *testing.T) {
	b := newBench(t)
	ctx := context.Background()
	if err := b.containers.Record(ctx, remoteID, inventoryOf(
		composeContainerOf(containerID1, "blog-web-1", "blog", "web", "nginx:1.27"),
		composeContainerOf(containerID2, "gone-1", "gone", "web", "nginx:1.27"),
	)); err != nil {
		t.Fatal(err)
	}
	if err := b.containers.Record(ctx, remoteID, inventoryOf(composeContainerOf(containerID1, "blog-web-1", "blog", "web", "nginx:1.27"))); err != nil {
		t.Fatal(err)
	}

	proposal, err := b.directory.Propose(ctx, service.ID(remoteID, containerID1))
	if err != nil || proposal.Definition.ComposeProject != "blog" || proposal.Definition.Units[0].Version != "1.27" {
		t.Fatalf("proposition: %+v, %v", proposal, err)
	}
	if _, err := b.directory.Propose(ctx, service.ID(remoteID, containerID2)); !errors.Is(err, directory.ErrContainerNotFound) {
		t.Fatalf("conteneur détruit: %v", err)
	}
	if _, err := b.directory.Propose(ctx, "inconnu"); !errors.Is(err, directory.ErrContainerNotFound) {
		t.Fatalf("conteneur inconnu: %v", err)
	}

	// Une fois adopté, le conteneur est rattaché : il n'y a plus rien à proposer.
	mustCreate(t, b, proposal.Definition)
	if _, err := b.directory.Propose(ctx, service.ID(remoteID, containerID1)); !errors.Is(err, directory.ErrContainerDeclared) {
		t.Fatalf("conteneur déclaré: %v", err)
	}
}
