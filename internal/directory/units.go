package directory

import (
	"context"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/ldesfontaine/opencloud/internal/machine"
	"github.com/ldesfontaine/opencloud/internal/refusal"
	"github.com/ldesfontaine/opencloud/internal/systemdunits"
)

var (
	// L'unité systemd n'est pas dans le dernier relevé de la machine.
	ErrSystemdUnitNotObserved = errors.New("systemd unit not found on the machine report")
	// L'unité systemd est déjà celle d'un service déclaré de la machine.
	ErrSystemdUnitDeclared = refusal.Conflict("directory.systemd_unit_declared", "systemd unit already belongs to a declared service")
)

// Units est ce que l'annuaire lit du relevé des unités systemd : le dernier
// rapport de chaque machine. Lecture seule : l'annuaire n'écrit jamais ce
// que l'agent observe.
type Units interface {
	Machine(ctx context.Context, machineID string) (systemdunits.Snapshot, error)
	All(ctx context.Context) ([]systemdunits.Snapshot, error)
}

// SetUnits branche le relevé des unités systemd : sans lui, aucune unité
// binaire n'est ni vue ni absente, son état reste « inconnu ».
func (d *Directory) SetUnits(units Units) {
	d.units = units
}

// Presence dit ce que la machine montre d'une unité binaire déclarée.
type Presence string

const (
	// PresenceUnknown : la machine n'a pas dit ce qu'elle a (agent plus ancien,
	// systemd illisible).
	PresenceUnknown Presence = "unknown"
	// PresenceMissing : systemd a été lu, et l'unité n'y est pas.
	PresenceMissing Presence = "missing"
	// PresenceFound : l'unité est là, avec son état.
	PresenceFound Presence = "found"
)

// UnitObservation est ce que le dernier relevé de la machine dit d'une unité
// binaire déclarée. Unit et ReportedAt ne valent que si la machine a parlé.
type UnitObservation struct {
	Presence   Presence
	Unit       systemdunits.Unit
	ReportedAt time.Time
}

// ObserveUnits rapproche les unités binaires déclarées du relevé de leur
// machine, par le nom de l'unité systemd, et rend l'observation de chacune
// par identifiant d'unité. Une unité d'un autre type, ou sans nom systemd, est
// absente du résultat. La règle est pure : l'annuaire d'un côté, ce que les
// machines montrent de l'autre.
func ObserveUnits(declared []Service, snapshots []systemdunits.Snapshot) map[string]UnitObservation {
	byMachine := make(map[string]systemdunits.Snapshot, len(snapshots))
	for _, snapshot := range snapshots {
		byMachine[snapshot.MachineID] = snapshot
	}
	observed := map[string]UnitObservation{}
	for _, service := range declared {
		for _, unit := range service.Units {
			if unit.Kind != UnitBinary || unit.SystemdUnit == "" {
				continue
			}
			observed[unit.ID] = observeUnit(byMachine[service.MachineID], unit.SystemdUnit)
		}
	}
	return observed
}

func observeUnit(snapshot systemdunits.Snapshot, name string) UnitObservation {
	if snapshot.ReportedAt.IsZero() || !snapshot.Available {
		return UnitObservation{Presence: PresenceUnknown}
	}
	index := slices.IndexFunc(snapshot.Units, func(unit systemdunits.Unit) bool { return unit.Name == name })
	if index < 0 {
		return UnitObservation{Presence: PresenceMissing, ReportedAt: snapshot.ReportedAt}
	}
	return UnitObservation{Presence: PresenceFound, Unit: snapshot.Units[index], ReportedAt: snapshot.ReportedAt}
}

// DeclaredUnit est l'unité déclarée qui répond à une unité systemd.
type DeclaredUnit struct {
	ServiceID   string
	ServiceName string
	UnitID      string
}

// DeclaredBySystemdName range par nom d'unité systemd les unités binaires
// déclarées sur une machine. Le nom est unique par machine (index de la base) ;
// par prudence, le service à l'identifiant le plus petit gagnerait.
func DeclaredBySystemdName(declared []Service, machineID string) map[string]DeclaredUnit {
	ordered := slices.SortedFunc(slices.Values(declared), func(a, b Service) int { return strings.Compare(a.ID, b.ID) })
	byName := map[string]DeclaredUnit{}
	for _, service := range ordered {
		if service.MachineID != machineID {
			continue
		}
		for _, unit := range service.Units {
			if unit.Kind != UnitBinary || unit.SystemdUnit == "" {
				continue
			}
			if _, taken := byName[unit.SystemdUnit]; !taken {
				byName[unit.SystemdUnit] = DeclaredUnit{ServiceID: service.ID, ServiceName: service.Name, UnitID: unit.ID}
			}
		}
	}
	return byName
}

// MachineUnit est une unité relevée sur une machine, avec le service déclaré
// qui la porte si l'opérateur en a un.
type MachineUnit struct {
	Unit     systemdunits.Unit
	Declared *DeclaredUnit
}

// MachineUnits rend les unités relevées d'une machine, chacune rapprochée de
// l'unité déclarée de même nom ; machine.ErrNotFound si elle n'existe pas.
func (d *Directory) MachineUnits(ctx context.Context, machineID string) (systemdunits.Snapshot, []MachineUnit, error) {
	if d.units == nil {
		return systemdunits.Snapshot{}, nil, machine.ErrNotFound
	}
	snapshot, err := d.units.Machine(ctx, machineID)
	if err != nil {
		return systemdunits.Snapshot{}, nil, err
	}
	declared, err := d.store.ListDeclaredServices(ctx, Filter{MachineID: machineID})
	if err != nil {
		return systemdunits.Snapshot{}, nil, err
	}
	byName := DeclaredBySystemdName(declared, machineID)
	units := make([]MachineUnit, 0, len(snapshot.Units))
	for _, unit := range snapshot.Units {
		item := MachineUnit{Unit: unit}
		if found, ok := byName[unit.Name]; ok {
			item.Declared = &found
		}
		units = append(units, item)
	}
	return snapshot, units, nil
}

// ObservedUnits rend l'observation des unités binaires des services donnés.
// Sans relevé branché, aucune observation : chaque unité reste « inconnue ».
func (d *Directory) ObservedUnits(ctx context.Context, services []Service) (map[string]UnitObservation, error) {
	if d.units == nil {
		return ObserveUnits(services, nil), nil
	}
	snapshots, err := d.units.All(ctx)
	if err != nil {
		return nil, err
	}
	return ObserveUnits(services, snapshots), nil
}

// ProposeUnit tire d'une unité systemd relevée la définition à accepter : un
// service « binaire » d'une unité, sans port (le relevé ne le dit pas : il se
// déclare). Une unité absente du relevé est introuvable ; une unité déjà
// déclarée est un conflit. Rien n'est écrit.
func (d *Directory) ProposeUnit(ctx context.Context, machineID, unitName string) (Proposal, error) {
	snapshot, units, err := d.MachineUnits(ctx, machineID)
	if err != nil {
		return Proposal{}, err
	}
	index := slices.IndexFunc(units, func(item MachineUnit) bool { return item.Unit.Name == unitName })
	if index < 0 || snapshot.ReportedAt.IsZero() {
		return Proposal{}, ErrSystemdUnitNotObserved
	}
	if units[index].Declared != nil {
		return Proposal{}, ErrSystemdUnitDeclared
	}
	declared, err := d.store.ListDeclaredServices(ctx, Filter{MachineID: machineID})
	if err != nil {
		return Proposal{}, err
	}
	return proposeSystemdUnit(machineID, unitName, declared), nil
}

// proposeSystemdUnit nomme le service comme l'unité sans son suffixe « .service ».
func proposeSystemdUnit(machineID, unitName string, declared []Service) Proposal {
	base := strings.TrimSuffix(unitName, ".service")
	return Proposal{Definition: ServiceDefinition{
		MachineID: machineID,
		Name:      freeName(slug(base), machineID, declared),
		Nature:    NatureApplication,
		Origin:    OriginBinary,
		Units: []UnitDefinition{{
			Name:        unitNameOf(base),
			Kind:        UnitBinary,
			SystemdUnit: unitName,
			Ports:       []string{},
		}},
	}}
}

// ApplyUnitObservations met dans l'écart ce que le relevé systemd dit des
// unités binaires : trouvée, l'unité est conforme ; absente d'un relevé lu,
// « déclarée, absente » ; sans relevé, elle reste « non observée ». L'état
// de l'unité (en marche, en échec) n'est pas un écart : il se lit à côté,
// dans UnitObservation. Les unités conteneur ne bougent pas.
func ApplyUnitObservations(report GapReport, observed map[string]UnitObservation) GapReport {
	for serviceIndex := range report.Services {
		serviceGap := &report.Services[serviceIndex]
		serviceGap.State = GapConform
		serviceGap.Weak = false
		for unitIndex := range serviceGap.Units {
			unitGap := &serviceGap.Units[unitIndex]
			if observation, ok := observed[unitGap.UnitID]; ok && unitGap.Kind == UnitBinary {
				unitGap.State = gapOfObservation(observation.Presence)
			}
			serviceGap.State = worstOfService(serviceGap.State, unitGap.State)
			serviceGap.Weak = serviceGap.Weak || unitGap.Weak
		}
	}
	return report
}

func gapOfObservation(presence Presence) GapState {
	switch presence {
	case PresenceFound:
		return GapConform
	case PresenceMissing:
		return GapAbsent
	default:
		return GapUnobserved
	}
}
