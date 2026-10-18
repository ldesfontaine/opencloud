package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/ldesfontaine/opencloud/internal/directory"
	"github.com/ldesfontaine/opencloud/internal/machine"
	"github.com/ldesfontaine/opencloud/internal/probe"
	"github.com/ldesfontaine/opencloud/internal/service"
	"github.com/ldesfontaine/opencloud/internal/status"
)

var linksNow = time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)

// declaredOnLocal pose la machine openCloud et un service déclaré à une
// unité, que les tests de ce fichier relient à des sondes et des composants.
func declaredOnLocal(t *testing.T, db *DB) directory.Service {
	t.Helper()
	ctx := context.Background()
	err := db.SaveLocalMachine(ctx, machine.Machine{
		ID: machine.LocalID, Name: "opencloud", Kind: machine.KindLocal, Hostname: "host", EnrolledAt: linksNow, CreatedAt: linksNow,
	})
	if err != nil {
		t.Fatal(err)
	}
	declared := directory.Service{
		ID: "svc_0123456789abcdef", MachineID: machine.LocalID, Name: "whoami", Nature: directory.NatureApplication,
		Origin: directory.OriginContainer, DeclaredBy: directory.ByOperator, CreatedAt: linksNow, UpdatedAt: linksNow,
		Units: []directory.Unit{{
			ID: "unit_0123456789abcdef", ServiceID: "svc_0123456789abcdef", Name: "whoami", Kind: directory.UnitContainer,
			Image: "traefik/whoami", ContainerName: "whoami", UpdatePolicy: service.PolicyWatch,
		}},
	}
	if err := db.InsertDeclaredService(ctx, declared); err != nil {
		t.Fatal(err)
	}
	return declared
}

// Un service que des sondes ou des composants visent ne se supprime pas : le
// refus porte la liste des deux, la base n'a pas bougé. Détaché, il part.
func TestDeleteDeclaredService_LinkedIsRefusedWithItsLinks(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	declared := declaredOnLocal(t, db)
	err := db.InsertProbe(ctx, probe.Probe{
		ID: "p1", Name: "site", Kind: probe.KindHTTP, Target: "https://a.fr/", MachineID: machine.LocalID, ServiceID: declared.ID,
		Status: probe.StatusNew, Interval: time.Minute, Timeout: 10 * time.Second,
		FailureThreshold: 3, RecoveryThreshold: 2, Method: "GET", ExpectedStatus: "2xx", CreatedAt: linksNow,
	})
	if err != nil {
		t.Fatal(err)
	}
	component := status.Component{
		ID: "cmp1", Name: "Whoami", CreatedAt: linksNow,
		Members: []status.Member{{Kind: status.KindService, ID: declared.ID}},
	}
	if err := db.InsertComponent(ctx, component); err != nil {
		t.Fatal(err)
	}

	err = db.DeleteDeclaredService(ctx, declared.ID)

	var linked *directory.LinkedError
	if !errors.As(err, &linked) || !errors.Is(err, directory.ErrServiceLinked) {
		t.Fatalf("attendu un LinkedError, obtenu %v", err)
	}
	if len(linked.Links.Probes) != 1 || linked.Links.Probes[0] != (directory.LinkedObject{ID: "p1", Name: "site"}) {
		t.Fatalf("sondes = %+v", linked.Links.Probes)
	}
	if len(linked.Links.Components) != 1 || linked.Links.Components[0] != (directory.LinkedObject{ID: "cmp1", Name: "Whoami"}) {
		t.Fatalf("composants = %+v", linked.Links.Components)
	}
	if _, err := db.GetDeclaredService(ctx, declared.ID); err != nil {
		t.Fatalf("le service a disparu malgré le refus: %v", err)
	}
	if err := db.DeleteDeclaredService(ctx, "svc_inconnu"); !errors.Is(err, directory.ErrNotFound) {
		t.Fatalf("service inconnu: %v", err)
	}

	// La clé étrangère en restrict est le filet : un DELETE direct échoue aussi.
	if _, err := db.sql.ExecContext(ctx, `DELETE FROM declared_services WHERE id = ?`, declared.ID); err == nil {
		t.Fatal("la clé étrangère doit retenir un service visé")
	}

	if _, err := db.sql.ExecContext(ctx, `DELETE FROM probes WHERE id = 'p1'`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.sql.ExecContext(ctx, `DELETE FROM status_components WHERE id = 'cmp1'`); err != nil {
		t.Fatal(err)
	}
	if err := db.DeleteDeclaredService(ctx, declared.ID); err != nil {
		t.Fatalf("détaché, le service doit partir: %v", err)
	}
}

// La fiche d'un conteneur lit la politique de son unité, et le service qui la
// porte ; sans unité elle se suit. Recréé sous un autre identifiant, un
// conteneur rattaché à la même unité lit la même politique.
func TestListServices_ReadsThePolicyOfItsUnit(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	declared := declaredOnLocal(t, db)
	unitID := declared.Units[0].ID
	container := func(id, name string) service.Service {
		return service.Service{
			ID: id, MachineID: machine.LocalID, Kind: service.KindContainer, Name: name, ContainerID: id,
			State: service.StateRunning, CreatedAt: linksNow, FirstSeenAt: linksNow, LastSeenAt: linksNow,
		}
	}
	err := db.ApplyChanges(ctx, service.Changes{
		MachineID: machine.LocalID, Now: linksNow,
		Services: []service.Service{container("local:old", "whoami"), container("local:other", "other")},
	})
	if err != nil {
		t.Fatal(err)
	}
	err = db.SetServiceUnits(ctx, []service.UnitAssignment{{ServiceID: "local:old", UnitID: unitID, Level: service.MatchName}})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.SetUnitUpdatePolicy(ctx, unitID, service.PolicyIgnore); err != nil {
		t.Fatal(err)
	}

	old, err := db.GetService(ctx, "local:old")
	if err != nil || old.UpdatePolicy != service.PolicyIgnore || old.DeclaredServiceID != declared.ID {
		t.Fatalf("fiche rattachée = %+v, %v", old, err)
	}
	other, err := db.GetService(ctx, "local:other")
	if err != nil || other.UpdatePolicy != service.PolicyWatch || other.DeclaredServiceID != "" {
		t.Fatalf("fiche non déclarée = %+v, %v", other, err)
	}

	// Le conteneur est recréé : une autre fiche, la même unité, la même politique.
	err = db.ApplyChanges(ctx, service.Changes{MachineID: machine.LocalID, Now: linksNow, Services: []service.Service{container("local:new", "whoami")}})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.SetServiceUnits(ctx, []service.UnitAssignment{{ServiceID: "local:new", UnitID: unitID, Level: service.MatchName}}); err != nil {
		t.Fatal(err)
	}
	listed, err := db.ListServices(ctx, machine.LocalID)
	if err != nil {
		t.Fatal(err)
	}
	policies := map[string]service.UpdatePolicy{}
	for _, found := range listed {
		policies[found.ID] = found.UpdatePolicy
	}
	if policies["local:new"] != service.PolicyIgnore || policies["local:old"] != service.PolicyIgnore || policies["local:other"] != service.PolicyWatch {
		t.Fatalf("politiques = %v", policies)
	}

	// Une politique ne s'écrit que sur l'unité : l'écriture de la fiche n'y touche pas.
	if err := db.ApplyChanges(ctx, service.Changes{MachineID: machine.LocalID, Now: linksNow, Services: []service.Service{container("local:new", "whoami")}}); err != nil {
		t.Fatal(err)
	}
	if got := text(t, db, `SELECT update_policy FROM service_units WHERE id = ?`, unitID); got != "ignore" {
		t.Fatalf("politique de l'unité = %q", got)
	}
}

func TestSetUnitUpdatePolicy_UnknownUnit(t *testing.T) {
	db := openTestDB(t)
	declared := declaredOnLocal(t, db)

	if err := db.SetUnitUpdatePolicy(context.Background(), "unit_inconnue", service.PolicyHold); !errors.Is(err, directory.ErrUnitNotFound) {
		t.Fatalf("attendu %v, obtenu %v", directory.ErrUnitNotFound, err)
	}
	if _, err := db.GetUnit(context.Background(), "unit_inconnue"); !errors.Is(err, directory.ErrUnitNotFound) {
		t.Fatalf("GetUnit: %v", err)
	}
	if err := db.SetUnitUpdatePolicy(context.Background(), declared.Units[0].ID, service.PolicyHold); err != nil {
		t.Fatal(err)
	}
	if unit, err := db.GetUnit(context.Background(), declared.Units[0].ID); err != nil || unit.UpdatePolicy != service.PolicyHold {
		t.Fatalf("unité = %+v, %v", unit, err)
	}
}
