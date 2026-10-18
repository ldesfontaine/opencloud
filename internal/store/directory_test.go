package store

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/ldesfontaine/opencloud/internal/container"
	"github.com/ldesfontaine/opencloud/internal/directory"
	"github.com/ldesfontaine/opencloud/internal/machine"
)

// Une machine qui porte un service déclaré ne se retire pas, et rien n'est
// effacé du refus ; une fois le service supprimé, elle part.
func TestDeleteMachine_WithDeclaredServices_IsRefused(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	now := time.Unix(1_800_000_000, 0)
	token := issueToken(t, db, "vps", "", now)
	enrolled, err := db.Enroll(ctx, machine.HashToken(token), candidate(t), now)
	if err != nil {
		t.Fatal(err)
	}
	service := directory.Service{
		ID: "svc_0123456789abcdef", MachineID: enrolled.ID, Name: "whoami", Nature: directory.NatureApplication,
		Origin: directory.OriginContainer, DeclaredBy: directory.ByOperator, CreatedAt: now, UpdatedAt: now,
		Units: []directory.Unit{{
			ID: "unit_0123456789abcdef", ServiceID: "svc_0123456789abcdef", Name: "whoami", Kind: directory.UnitContainer,
			Image: "traefik/whoami", ContainerName: "whoami",
		}},
	}
	if err := db.InsertDeclaredService(ctx, service); err != nil {
		t.Fatal(err)
	}

	if err := db.DeleteMachine(ctx, enrolled.ID); !errors.Is(err, machine.ErrHasDeclaredServices) {
		t.Fatalf("machine avec un service déclaré: %v", err)
	}
	if _, err := db.GetMachine(ctx, enrolled.ID); err != nil {
		t.Fatalf("la machine a disparu malgré le refus: %v", err)
	}
	if _, err := db.GetDeclaredService(ctx, service.ID); err != nil {
		t.Fatalf("le service a disparu malgré le refus: %v", err)
	}

	if err := db.DeleteDeclaredService(ctx, service.ID); err != nil {
		t.Fatal(err)
	}
	if err := db.DeleteMachine(ctx, enrolled.ID); err != nil {
		t.Fatalf("machine sans service déclaré: %v", err)
	}
}

// À l'édition, une unité qui porte un identifiant doit être, dans la
// transaction, une unité du service : sinon elle serait créée sous un
// identifiant choisi par l'appelant. Les unités nouvelles arrivent sans.
func TestUpdateDeclaredService_RefusesAnUnitIDItDoesNotOwn(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	now := time.Unix(1_800_000_000, 0)
	token := issueToken(t, db, "vps", "", now)
	enrolled, err := db.Enroll(ctx, machine.HashToken(token), candidate(t), now)
	if err != nil {
		t.Fatal(err)
	}
	declare := func(serviceID, unitID, name string) directory.Service {
		service := directory.Service{
			ID: serviceID, MachineID: enrolled.ID, Name: name, Nature: directory.NatureApplication,
			Origin: directory.OriginContainer, DeclaredBy: directory.ByOperator, CreatedAt: now, UpdatedAt: now,
			Units: []directory.Unit{{
				ID: unitID, ServiceID: serviceID, Name: name, Kind: directory.UnitContainer,
				Image: "traefik/whoami", ContainerName: name,
			}},
		}
		if err := db.InsertDeclaredService(ctx, service); err != nil {
			t.Fatal(err)
		}
		return service
	}
	first := declare("svc_1111111111111111", "unit_1111111111111111", "first")
	second := declare("svc_2222222222222222", "unit_2222222222222222", "second")

	withUnits := func(units ...directory.Unit) directory.Service {
		edited := first
		edited.Units = units
		return edited
	}
	own := first.Units[0]
	cases := map[string]directory.Service{
		"identifiant inventé":        withUnits(own, directory.Unit{ID: "unit_fantome", Name: "ghost", Kind: directory.UnitContainer, Image: "x"}),
		"unité d'un autre service":   withUnits(second.Units[0]),
		"même identifiant deux fois": withUnits(own, directory.Unit{ID: own.ID, Name: "bis", Kind: directory.UnitContainer, Image: "x"}),
	}
	for name, edited := range cases {
		if err := db.UpdateDeclaredService(ctx, edited); !errors.Is(err, directory.ErrUnitInvalid) {
			t.Errorf("%s: got %v", name, err)
		}
	}
	kept, err := db.GetDeclaredService(ctx, first.ID)
	if err != nil || len(kept.Units) != 1 || kept.Units[0].ID != own.ID {
		t.Fatalf("un refus a changé les unités: %+v, %v", kept.Units, err)
	}
	other, err := db.GetDeclaredService(ctx, second.ID)
	if err != nil || len(other.Units) != 1 || other.Units[0].ServiceID != second.ID {
		t.Fatalf("un refus a touché l'autre service: %+v, %v", other.Units, err)
	}

	added := withUnits(own, directory.Unit{Name: "extra", Kind: directory.UnitContainer, Image: "x"})
	if err := db.UpdateDeclaredService(ctx, added); err != nil {
		t.Fatalf("unité nouvelle sans identifiant: %v", err)
	}
	kept, err = db.GetDeclaredService(ctx, first.ID)
	if err != nil || len(kept.Units) != 2 || !strings.HasPrefix(kept.Units[1].ID, directory.UnitPrefix) {
		t.Fatalf("la nouvelle unité n'a pas reçu d'identifiant: %+v, %v", kept.Units, err)
	}
}

// Filtrer la liste ne lit les unités, fournitures et projets que des
// services gardés, et rend tous les projets d'un service gardé.
func TestListDeclaredServices_FilterKeepsAllLinksOfKeptServices(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	now := time.Unix(1_800_000_000, 0)
	token := issueToken(t, db, "vps", "", now)
	enrolled, err := db.Enroll(ctx, machine.HashToken(token), candidate(t), now)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"prj_aaaaaaaaaaaaaaaa", "prj_bbbbbbbbbbbbbbbb"} {
		if err := db.InsertProject(ctx, directory.Project{ID: id, Name: id[4:6], CreatedAt: now, UpdatedAt: now}); err != nil {
			t.Fatal(err)
		}
	}
	declare := func(serviceID, name string, projects ...string) {
		service := directory.Service{
			ID: serviceID, MachineID: enrolled.ID, Name: name, Nature: directory.NatureInfra,
			Origin: directory.OriginContainer, DeclaredBy: directory.ByOperator, CreatedAt: now, UpdatedAt: now,
			Provides: []directory.Capability{directory.CapabilityProxy}, ProjectIDs: projects,
			Units: []directory.Unit{{
				ID: "unit_" + serviceID[4:], ServiceID: serviceID, Name: name, Kind: directory.UnitContainer,
				Image: "x", ContainerName: name,
			}},
		}
		if err := db.InsertDeclaredService(ctx, service); err != nil {
			t.Fatal(err)
		}
	}
	declare("svc_1111111111111111", "inside", "prj_aaaaaaaaaaaaaaaa", "prj_bbbbbbbbbbbbbbbb")
	declare("svc_2222222222222222", "outside", "prj_bbbbbbbbbbbbbbbb")

	listed, err := db.ListDeclaredServices(ctx, directory.Filter{ProjectID: "prj_aaaaaaaaaaaaaaaa"})
	if err != nil || len(listed) != 1 {
		t.Fatalf("%+v, %v", listed, err)
	}
	got := listed[0]
	if got.Name != "inside" || len(got.Units) != 1 || len(got.Provides) != 1 || len(got.ProjectIDs) != 2 {
		t.Fatalf("service filtré incomplet: %+v", got)
	}
}

// storedContainer écrit une fiche sur la machine, avec le rattachement
// donné ; ApplyChanges est le chemin d'un rapport.
func storedContainer(t *testing.T, db *DB, machineID, containerID string, unitID string, level container.MatchLevel) container.Container {
	t.Helper()
	now := time.Unix(1_800_000_000, 0)
	fiche := container.Container{
		ID: container.ID(machineID, containerID), MachineID: machineID, Kind: container.KindContainer, Name: containerID,
		ContainerID: containerID, Image: "nextcloud:29", State: container.StateRunning,
		CreatedAt: now, FirstSeenAt: now, LastSeenAt: now, UnitID: unitID, MatchLevel: level,
	}
	if err := db.ApplyChanges(context.Background(), container.Changes{MachineID: machineID, Now: now, Containers: []container.Container{fiche}}); err != nil {
		t.Fatal(err)
	}
	return fiche
}

func declareOneUnit(t *testing.T, db *DB, machineID, name string) directory.Service {
	t.Helper()
	now := time.Unix(1_800_000_000, 0)
	declared := directory.Service{
		ID: "svc_" + name, MachineID: machineID, Name: name, Nature: directory.NatureApplication,
		Origin: directory.OriginContainer, DeclaredBy: directory.ByOperator, CreatedAt: now, UpdatedAt: now,
		Units: []directory.Unit{{ID: "unit_" + name, ServiceID: "svc_" + name, Name: name, Kind: directory.UnitContainer, Image: "nextcloud", ContainerName: name}},
	}
	if err := db.InsertDeclaredService(context.Background(), declared); err != nil {
		t.Fatal(err)
	}
	return declared
}

func enrolledMachine(t *testing.T, db *DB, name string) string {
	t.Helper()
	now := time.Unix(1_800_000_000, 0)
	token := issueToken(t, db, name, "", now)
	enrolled, err := db.Enroll(context.Background(), machine.HashToken(token), candidate(t), now)
	if err != nil {
		t.Fatal(err)
	}
	return enrolled.ID
}

// Supprimer une unité, par son service ou par l'édition, rend ses
// conteneurs « non déclarés » : la clé étrangère met l'unité à NULL, et
// la lecture ne garde pas le niveau d'un rattachement qui n'existe plus.
func TestDeletingAUnit_LeavesItsContainersUndeclared(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	machineID := enrolledMachine(t, db, "vps")
	declared := declareOneUnit(t, db, machineID, "web")
	storedContainer(t, db, machineID, "c1", declared.Units[0].ID, container.MatchName)

	got, err := db.GetContainer(ctx, container.ID(machineID, "c1"))
	if err != nil || got.UnitID != declared.Units[0].ID || got.MatchLevel != container.MatchName {
		t.Fatalf("avant: %+v, %v", got, err)
	}
	if err := db.DeleteDeclaredService(ctx, declared.ID); err != nil {
		t.Fatal(err)
	}
	got, err = db.GetContainer(ctx, container.ID(machineID, "c1"))
	if err != nil || got.UnitID != "" || got.MatchLevel != container.MatchNone {
		t.Fatalf("après: %+v, %v", got, err)
	}
	listed, err := db.ListContainers(ctx, machineID)
	if err != nil || len(listed) != 1 || listed[0].UnitID != "" || listed[0].MatchLevel != container.MatchNone {
		t.Fatalf("liste: %+v, %v", listed, err)
	}
}

// Le rapprochement lit l'annuaire hors de la transaction du rapport : si
// l'unité disparaît entre les deux, la fiche s'écrit non déclarée plutôt
// que de faire échouer la clé étrangère et perdre le rapport.
func TestUpsertService_UnitGoneMeanwhileWritesNull(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	machineID := enrolledMachine(t, db, "vps")

	storedContainer(t, db, machineID, "c1", "unit_disparue", container.MatchCompose)
	got, err := db.GetContainer(ctx, container.ID(machineID, "c1"))
	if err != nil || got.UnitID != "" || got.MatchLevel != container.MatchNone {
		t.Fatalf("insertion: %+v, %v", got, err)
	}

	declared := declareOneUnit(t, db, machineID, "web")
	storedContainer(t, db, machineID, "c1", declared.Units[0].ID, container.MatchCompose)
	if got, _ := db.GetContainer(ctx, container.ID(machineID, "c1")); got.UnitID != declared.Units[0].ID {
		t.Fatalf("mise à jour vers une unité qui existe: %+v", got)
	}
	storedContainer(t, db, machineID, "c1", "unit_disparue", container.MatchCompose)
	got, err = db.GetContainer(ctx, container.ID(machineID, "c1"))
	if err != nil || got.UnitID != "" || got.MatchLevel != container.MatchNone {
		t.Fatalf("mise à jour vers une unité disparue: %+v, %v", got, err)
	}

	// La même règle pour le rapprochement seul.
	storedContainer(t, db, machineID, "c1", declared.Units[0].ID, container.MatchName)
	err = db.SetContainerUnits(ctx, []container.UnitAssignment{{ContainerID: container.ID(machineID, "c1"), UnitID: "unit_disparue", Level: container.MatchName}})
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := db.GetContainer(ctx, container.ID(machineID, "c1")); got.UnitID != "" || got.MatchLevel != container.MatchNone {
		t.Fatalf("SetContainerUnits vers une unité disparue: %+v", got)
	}
}

func TestSetContainerUnits_WritesOnlyTheNamedRows(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	machineID := enrolledMachine(t, db, "vps")
	declared := declareOneUnit(t, db, machineID, "web")
	storedContainer(t, db, machineID, "c1", "", container.MatchNone)
	storedContainer(t, db, machineID, "c2", "", container.MatchNone)

	err := db.SetContainerUnits(ctx, []container.UnitAssignment{{ContainerID: container.ID(machineID, "c1"), UnitID: declared.Units[0].ID, Level: container.MatchCompose}})
	if err != nil {
		t.Fatal(err)
	}
	first, _ := db.GetContainer(ctx, container.ID(machineID, "c1"))
	second, _ := db.GetContainer(ctx, container.ID(machineID, "c2"))
	if first.UnitID != declared.Units[0].ID || first.MatchLevel != container.MatchCompose || second.UnitID != "" {
		t.Fatalf("first = %+v, second = %+v", first, second)
	}
}

func TestIgnoredKeys_RoundTrip(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	first, second := enrolledMachine(t, db, "vps-1"), enrolledMachine(t, db, "vps-2")
	at := time.Unix(1_800_000_000, 0)
	project := directory.IgnoredKey{MachineID: first, Kind: directory.IgnoreComposeProject, Value: "blog", CreatedAt: at}
	alone := directory.IgnoredKey{MachineID: first, Kind: directory.IgnoreContainerName, Value: "whoami", CreatedAt: at}
	elsewhere := directory.IgnoredKey{MachineID: second, Kind: directory.IgnoreContainerName, Value: "whoami", CreatedAt: at}
	for _, key := range []directory.IgnoredKey{project, alone, elsewhere, project} {
		if err := db.InsertIgnoredKey(ctx, key); err != nil {
			t.Fatalf("%+v: %v", key, err)
		}
	}

	onFirst, err := db.ListIgnoredKeys(ctx, first)
	if err != nil || len(onFirst) != 2 || onFirst[0].Kind != directory.IgnoreComposeProject || onFirst[1].Value != "whoami" || !onFirst[0].CreatedAt.Equal(at) {
		t.Fatalf("première machine: %+v, %v", onFirst, err)
	}
	all, err := db.ListIgnoredKeys(ctx, "")
	if err != nil || len(all) != 3 {
		t.Fatalf("toutes: %+v, %v", all, err)
	}

	later := project
	later.CreatedAt = at.Add(time.Hour)
	if err := db.InsertIgnoredKey(ctx, later); err != nil {
		t.Fatal(err)
	}
	if kept, _ := db.ListIgnoredKeys(ctx, first); !kept[0].CreatedAt.Equal(at) {
		t.Fatalf("ignorer deux fois garde la première date: %v", kept[0].CreatedAt)
	}

	missing := directory.IgnoredKey{MachineID: "inconnue", Kind: directory.IgnoreContainerName, Value: "x", CreatedAt: at}
	if err := db.InsertIgnoredKey(ctx, missing); !errors.Is(err, directory.ErrMachineInvalid) {
		t.Fatalf("machine inconnue: %v", err)
	}
	if err := db.DeleteIgnoredKey(ctx, alone); err != nil {
		t.Fatal(err)
	}
	if err := db.DeleteIgnoredKey(ctx, alone); !errors.Is(err, directory.ErrIgnoredNotFound) {
		t.Fatalf("retirer deux fois: %v", err)
	}

	// Retirer la machine emporte ses clés.
	if err := db.DeleteMachine(ctx, second); err != nil {
		t.Fatal(err)
	}
	if left, _ := db.ListIgnoredKeys(ctx, ""); len(left) != 1 {
		t.Fatalf("après le retrait de la machine: %+v", left)
	}
}
