package store

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/ldesfontaine/opencloud/internal/deletion"
	"github.com/ldesfontaine/opencloud/internal/machine"
	"github.com/ldesfontaine/opencloud/internal/systemdunits"
)

func aUnit(name string, active systemdunits.Active, sub systemdunits.Sub) systemdunits.Unit {
	return systemdunits.Unit{Name: name, Load: systemdunits.LoadLoaded, Active: active, Sub: sub}
}

// Le relevé remplace le précédent en entier, une machine sans relevé se lit
// vide et sans date, et les unités partent avec la machine (le plan les compte).
func TestSystemdUnits_ReplaceReadAndGoWithTheMachine(t *testing.T) {
	ctx := context.Background()
	db := deletionBench(t)

	before, err := db.MachineSystemdUnits(ctx, "m1")
	if err != nil || !before.ReportedAt.IsZero() || before.Available || len(before.Units) != 0 {
		t.Fatalf("avant tout relevé : %+v, %v", before, err)
	}

	first := systemdunits.Report{Available: true, Units: []systemdunits.Unit{
		aUnit("forgejo.service", systemdunits.ActiveActive, systemdunits.SubRunning),
		aUnit("ssh.service", systemdunits.ActiveActive, systemdunits.SubRunning),
	}}
	if err := db.ReplaceSystemdUnits(ctx, "m1", first, time.Unix(100, 0)); err != nil {
		t.Fatal(err)
	}
	second := systemdunits.Report{Available: true, Truncated: true, Units: []systemdunits.Unit{aUnit("forgejo.service", systemdunits.ActiveFailed, systemdunits.SubFailed)}}
	if err := db.ReplaceSystemdUnits(ctx, "m1", second, time.Unix(200, 0)); err != nil {
		t.Fatal(err)
	}

	got, err := db.MachineSystemdUnits(ctx, "m1")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got.Units, second.Units) || !got.Truncated || got.ReportedAt.Unix() != 200 {
		t.Fatalf("le second relevé remplace le premier : %+v", got)
	}

	unreadable := systemdunits.Report{Reason: systemdunits.ReasonNotBooted, Units: []systemdunits.Unit{}}
	if err := db.ReplaceSystemdUnits(ctx, "m1", unreadable, time.Unix(300, 0)); err != nil {
		t.Fatal(err)
	}
	got, _ = db.MachineSystemdUnits(ctx, "m1")
	if got.Available || got.Reason != systemdunits.ReasonNotBooted || len(got.Units) != 0 {
		t.Fatalf("systemd illisible : %+v", got)
	}

	if err := db.ReplaceSystemdUnits(ctx, "m1", first, time.Unix(400, 0)); err != nil {
		t.Fatal(err)
	}
	all, err := db.SystemdUnitsOfMachines(ctx)
	if err != nil || len(all) == 0 {
		t.Fatalf("toutes les machines : %+v, %v", all, err)
	}

	plan, err := db.MachineDeletionPlan(ctx, "m1")
	if err != nil {
		t.Fatal(err)
	}
	if want := (deletion.Count{Kind: deletion.KindSystemdUnits, Count: 2}); !containsCount(plan.Removes, want) {
		t.Fatalf("part avec : %+v", plan.Removes)
	}
	if err := db.DeleteMachine(ctx, "m1"); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"systemd_reports", "systemd_units"} {
		if got := count(t, db, `SELECT COUNT(*) FROM `+table); got != 0 {
			t.Errorf("%s : %d ligne(s) survivent à la machine", table, got)
		}
	}
}

func containsCount(counts []deletion.Count, want deletion.Count) bool {
	for _, item := range counts {
		if item == want {
			return true
		}
	}
	return false
}

func TestSystemdUnits_UnknownMachineIsNotFound(t *testing.T) {
	db := deletionBench(t)
	if _, err := db.MachineSystemdUnits(context.Background(), "inconnue"); err != machine.ErrNotFound {
		t.Fatalf("erreur : %v", err)
	}
}

func TestMachineModules_SystemdAppearsOnlyOnceSystemdWasRead(t *testing.T) {
	ctx := context.Background()
	db := deletionBench(t)
	hasModule := func() bool {
		modules, err := db.MachineModules(ctx)
		if err != nil {
			t.Fatal(err)
		}
		for _, id := range modules["m1"] {
			if id == "systemd_units" {
				return true
			}
		}
		return false
	}
	if hasModule() {
		t.Fatal("sans relevé, pas de module")
	}
	_ = db.ReplaceSystemdUnits(ctx, "m1", systemdunits.Report{Reason: systemdunits.ReasonUnreadable, Units: []systemdunits.Unit{}}, time.Unix(1, 0))
	if hasModule() {
		t.Fatal("systemd illisible : pas de module")
	}
	_ = db.ReplaceSystemdUnits(ctx, "m1", systemdunits.Report{Available: true, Units: []systemdunits.Unit{}}, time.Unix(2, 0))
	if !hasModule() {
		t.Fatal("systemd lu : le module est porté")
	}
}
