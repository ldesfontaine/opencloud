package store

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

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
