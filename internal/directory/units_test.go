package directory_test

import (
	"context"
	"errors"
	"testing"

	"github.com/ldesfontaine/opencloud/internal/directory"
	"github.com/ldesfontaine/opencloud/internal/systemdunits"
)

func running(name string) systemdunits.Unit {
	return systemdunits.Unit{Name: name, Load: systemdunits.LoadLoaded, Active: systemdunits.ActiveActive, Sub: systemdunits.SubRunning}
}

func binaryService(machineID, name, systemdUnit string) directory.Service {
	return directory.Service{
		ID: "svc_" + machineID + "_" + name, MachineID: machineID, Name: name, Origin: directory.OriginBinary,
		Units: []directory.Unit{{ID: "unit_" + machineID + "_" + name, Name: name, Kind: directory.UnitBinary, SystemdUnit: systemdUnit}},
	}
}

func TestObserveUnits_MatchesTheDeclaredBinaryByItsSystemdName(t *testing.T) {
	declared := []directory.Service{
		binaryService("m1", "forgejo", "forgejo.service"),
		binaryService("m1", "absent", "absent.service"),
		binaryService("m2", "forgejo", "forgejo.service"),
		binaryService("m3", "muet", "muet.service"),
		{ID: "svc_web", MachineID: "m1", Name: "web", Origin: directory.OriginCompose, Units: []directory.Unit{{ID: "unit_web", Name: "web", Kind: directory.UnitContainer}}},
	}
	reported := testNow
	snapshots := []systemdunits.Snapshot{
		{MachineID: "m1", ReportedAt: reported, Report: systemdunits.Report{Available: true, Units: []systemdunits.Unit{running("forgejo.service")}}},
		// m2 : l'unité de m1 ne compte pas pour m2.
		{MachineID: "m2", ReportedAt: reported, Report: systemdunits.Report{Available: true, Units: []systemdunits.Unit{running("autre.service")}}},
		// m3 : systemd illisible.
		{MachineID: "m3", ReportedAt: reported, Report: systemdunits.Report{Reason: systemdunits.ReasonNotBooted, Units: []systemdunits.Unit{}}},
	}
	got := directory.ObserveUnits(declared, snapshots)

	if got["unit_m1_forgejo"].Presence != directory.PresenceFound || got["unit_m1_forgejo"].Unit.Active != systemdunits.ActiveActive {
		t.Errorf("forgejo de m1 : %+v", got["unit_m1_forgejo"])
	}
	if got["unit_m1_absent"].Presence != directory.PresenceMissing {
		t.Errorf("une unité absente d'un relevé lu est « missing » : %+v", got["unit_m1_absent"])
	}
	if got["unit_m3_muet"].Presence != directory.PresenceUnknown {
		t.Errorf("systemd illisible : %+v", got["unit_m3_muet"])
	}
	if got["unit_m2_forgejo"].Presence != directory.PresenceMissing {
		t.Errorf("forgejo de m2, relevé sans lui : %+v", got["unit_m2_forgejo"])
	}
	if _, ok := got["unit_web"]; ok {
		t.Error("une unité conteneur n'est pas rapprochée d'une unité systemd")
	}
}

func TestObserveUnits_MachineWithoutReport_IsUnknownNotMissing(t *testing.T) {
	declared := []directory.Service{binaryService("m1", "forgejo", "forgejo.service")}
	got := directory.ObserveUnits(declared, []systemdunits.Snapshot{{MachineID: "m1", Report: systemdunits.Report{Units: []systemdunits.Unit{}}}})
	if got["unit_m1_forgejo"].Presence != directory.PresenceUnknown {
		t.Errorf("un agent qui n'a rien relevé ne rend pas l'unité absente : %+v", got["unit_m1_forgejo"])
	}
}

type fixedUnits []systemdunits.Snapshot

func (f fixedUnits) Machine(_ context.Context, machineID string) (systemdunits.Snapshot, error) {
	for _, snapshot := range f {
		if snapshot.MachineID == machineID {
			return snapshot, nil
		}
	}
	return systemdunits.Snapshot{}, errors.New("machine inconnue")
}

func (f fixedUnits) All(context.Context) ([]systemdunits.Snapshot, error) { return f, nil }

func TestProposeUnit_BinaryServiceWithoutPort_RefusesWhatIsDeclaredOrUnseen(t *testing.T) {
	b := newBench(t)
	ctx := context.Background()
	b.directory.SetUnits(fixedUnits{{
		MachineID: remoteID, ReportedAt: testNow,
		Report: systemdunits.Report{Available: true, Units: []systemdunits.Unit{running("forgejo.service"), running("ssh.service")}},
	}})

	proposal, err := b.directory.ProposeUnit(ctx, remoteID, "forgejo.service")
	if err != nil {
		t.Fatal(err)
	}
	definition := proposal.Definition
	unit := definition.Units[0]
	if definition.Origin != directory.OriginBinary || definition.Name != "forgejo" || unit.SystemdUnit != "forgejo.service" || unit.Kind != directory.UnitBinary || len(unit.Ports) != 0 {
		t.Fatalf("proposition : %+v", definition)
	}

	if _, err := b.directory.ProposeUnit(ctx, remoteID, "inconnue.service"); !errors.Is(err, directory.ErrSystemdUnitNotObserved) {
		t.Errorf("unité absente du relevé : %v", err)
	}

	// La proposition s'accepte telle quelle, puis l'unité est déclarée.
	created, err := b.directory.CreateService(ctx, definition)
	if err != nil {
		t.Fatalf("la proposition doit être acceptable : %v", err)
	}
	if _, err := b.directory.ProposeUnit(ctx, remoteID, "forgejo.service"); !errors.Is(err, directory.ErrSystemdUnitDeclared) {
		t.Errorf("unité déjà déclarée : %v", err)
	}

	// Le rapprochement lit l'unité déclarée, et la machine la montre avec elle.
	_, units, err := b.directory.MachineUnits(ctx, remoteID)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range units {
		isForgejo := item.Unit.Name == "forgejo.service"
		if isForgejo != (item.Declared != nil) || (isForgejo && item.Declared.ServiceID != created.ID) {
			t.Errorf("%s : déclaré=%+v", item.Unit.Name, item.Declared)
		}
	}
	observed, err := b.directory.ObservedUnits(ctx, []directory.Service{created})
	if err != nil || observed[created.Units[0].ID].Presence != directory.PresenceFound {
		t.Errorf("observation : %+v, %v", observed, err)
	}
}

func TestProposeUnit_NameTakenOnTheMachine_GetsASuffix(t *testing.T) {
	b := newBench(t)
	ctx := context.Background()
	b.directory.SetUnits(fixedUnits{{MachineID: remoteID, ReportedAt: testNow, Report: systemdunits.Report{Available: true, Units: []systemdunits.Unit{running("forgejo.service")}}}})
	mustCreate(t, b, directory.ServiceDefinition{
		MachineID: remoteID, Name: "forgejo", Nature: directory.NatureApplication, Origin: directory.OriginContainer,
		Units: []directory.UnitDefinition{{Name: "forgejo", ContainerName: "forgejo", Image: "codeberg.org/forgejo/forgejo"}},
	})
	proposal, err := b.directory.ProposeUnit(ctx, remoteID, "forgejo.service")
	if err != nil || proposal.Definition.Name != "forgejo-2" {
		t.Fatalf("nom : %+v, %v", proposal.Definition.Name, err)
	}
}

func TestApplyUnitObservations_BinaryUnitsAreJudgedByTheReport(t *testing.T) {
	declared := []directory.Service{
		binaryService("m1", "forgejo", "forgejo.service"),
		binaryService("m1", "absent", "absent.service"),
		binaryService("m3", "muet", "muet.service"),
	}
	snapshots := []systemdunits.Snapshot{
		{MachineID: "m1", ReportedAt: testNow, Report: systemdunits.Report{Available: true, Units: []systemdunits.Unit{running("forgejo.service")}}},
	}
	report := directory.Gaps(declared, nil, nil, nil)
	for _, serviceGap := range report.Services {
		if serviceGap.State != directory.GapUnobserved {
			t.Fatalf("avant : une unité binaire est non observée, reçu %q", serviceGap.State)
		}
	}
	report = directory.ApplyUnitObservations(report, directory.ObserveUnits(declared, snapshots))
	want := []directory.GapState{directory.GapConform, directory.GapAbsent, directory.GapUnobserved}
	for index, serviceGap := range report.Services {
		if serviceGap.State != want[index] || serviceGap.Units[0].State != want[index] {
			t.Errorf("%s : service %q, unité %q, attendu %q", serviceGap.ServiceID, serviceGap.State, serviceGap.Units[0].State, want[index])
		}
	}
}
