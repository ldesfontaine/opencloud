package role_test

import (
	"context"
	"errors"
	"log/slog"
	"testing"

	"github.com/ldesfontaine/opencloud/internal/machine"
	"github.com/ldesfontaine/opencloud/internal/role"
	"github.com/ldesfontaine/opencloud/internal/store"
	"github.com/ldesfontaine/opencloud/internal/store/storetest"
)

func enrollMachine(t *testing.T, db *store.DB) string {
	t.Helper()
	machines := machine.New(db, machine.NewSessions(), slog.New(slog.DiscardHandler))
	return storetest.EnrollMachine(t, machines, "11111111-2222-4333-8444-555555555555", "pve-1").ID
}

type countingWatcher struct{ changes []string }

func (w *countingWatcher) RolesChanged(machineID string) { w.changes = append(w.changes, machineID) }

func TestRecord_StoresTheRoleAndForgetsALostOne(t *testing.T) {
	db := storetest.Open(t)
	machineID := enrollMachine(t, db)
	watcher := &countingWatcher{}
	service := role.New(db, slog.New(slog.DiscardHandler))
	service.SetWatcher(watcher)
	ctx := context.Background()

	if err := service.Record(ctx, machineID, role.Report{Roles: []role.Role{{Name: role.ProxmoxVE, Version: "8.2.4"}}}); err != nil {
		t.Fatal(err)
	}
	stored, err := db.MachineRoles(ctx)
	if err != nil || len(stored[machineID]) != 1 || stored[machineID][0].Version != "8.2.4" {
		t.Fatalf("rôles : %+v (%v)", stored, err)
	}
	// Le même rapport redit à chaque flux ne prévient personne.
	if err := service.Record(ctx, machineID, role.Report{Roles: []role.Role{{Name: role.ProxmoxVE, Version: "8.2.4"}}}); err != nil {
		t.Fatal(err)
	}
	if len(watcher.changes) != 1 {
		t.Fatalf("%d notifications après un rapport identique, attendu 1", len(watcher.changes))
	}
	// Une version changée est un changement.
	if err := service.Record(ctx, machineID, role.Report{Roles: []role.Role{{Name: role.ProxmoxVE, Version: "8.3.0"}}}); err != nil {
		t.Fatal(err)
	}
	// L'agent ne voit plus Proxmox : le rôle part.
	if err := service.Record(ctx, machineID, role.Report{Roles: []role.Role{}}); err != nil {
		t.Fatal(err)
	}
	stored, _ = db.MachineRoles(ctx)
	if _, found := stored[machineID]; found || len(watcher.changes) != 3 {
		t.Fatalf("rôles : %+v, %d notifications", stored, len(watcher.changes))
	}
}

func TestRecord_AnInvalidReport_IsRefusedWholeAndWritesNothing(t *testing.T) {
	db := storetest.Open(t)
	machineID := enrollMachine(t, db)
	service := role.New(db, slog.New(slog.DiscardHandler))
	ctx := context.Background()
	if err := service.Record(ctx, machineID, role.Report{Roles: []role.Role{{Name: role.ProxmoxVE, Version: "8.2.4"}}}); err != nil {
		t.Fatal(err)
	}

	err := service.Record(ctx, machineID, role.Report{Roles: []role.Role{{Name: "root-shell"}}})
	if !errors.Is(err, role.ErrReportInvalid) {
		t.Fatalf("erreur : %v", err)
	}
	stored, _ := db.MachineRoles(ctx)
	if len(stored[machineID]) != 1 {
		t.Fatalf("un rapport refusé a changé le reflet : %+v", stored)
	}
}

func TestMachineRoles_GoWithTheirMachine(t *testing.T) {
	db := storetest.Open(t)
	machineID := enrollMachine(t, db)
	service := role.New(db, slog.New(slog.DiscardHandler))
	ctx := context.Background()
	if err := service.Record(ctx, machineID, role.Report{Roles: []role.Role{{Name: role.ProxmoxVE}}}); err != nil {
		t.Fatal(err)
	}
	if err := db.DeleteMachine(ctx, machineID); err != nil {
		t.Fatal(err)
	}
	if stored, _ := db.MachineRoles(ctx); len(stored) != 0 {
		t.Fatalf("des rôles survivent à leur machine : %+v", stored)
	}
}
