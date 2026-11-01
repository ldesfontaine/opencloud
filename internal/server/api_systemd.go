package server

import (
	"net/http"
	"time"

	"github.com/ldesfontaine/opencloud/internal/directory"
	"github.com/ldesfontaine/opencloud/internal/machine"
	"github.com/ldesfontaine/opencloud/internal/server/httpx"
	"github.com/ldesfontaine/opencloud/internal/systemdunits"
)

// Les unités systemd d'une machine, telles que son agent les a relevées :
// le nom et les trois états de chaque unité de service, rien d'autre. Chaque
// unité dit le service déclaré qui la porte, s'il y en a un. reported_at est
// nul tant que l'agent n'a rien envoyé ; available est faux quand systemd n'a
// pas pu être lu (reason dit pourquoi).
type systemdUnitJSON struct {
	Name     string               `json:"name"`
	Load     string               `json:"load"`
	Active   string               `json:"active"`
	Sub      string               `json:"sub"`
	Declared *declaredUnitRefJSON `json:"declared"`
}

type declaredUnitRefJSON struct {
	ServiceID   string `json:"service_id"`
	ServiceName string `json:"service_name"`
	UnitID      string `json:"unit_id"`
}

type systemdReportJSON struct {
	MachineID   string            `json:"machine_id"`
	MachineName string            `json:"machine_name"`
	ReportedAt  *time.Time        `json:"reported_at"`
	Available   bool              `json:"available"`
	Reason      string            `json:"reason"`
	Truncated   bool              `json:"truncated"`
	Units       []systemdUnitJSON `json:"units"`
}

// Ce que le relevé dit d'une unité binaire déclarée : trouvée (avec ses
// états), absente d'un relevé lu, ou inconnue (rien relevé).
type observedUnitJSON struct {
	Presence   string     `json:"presence"`
	Load       string     `json:"load"`
	Active     string     `json:"active"`
	Sub        string     `json:"sub"`
	ReportedAt *time.Time `json:"reported_at"`
}

func (s *Server) getMachineSystemdUnits(w http.ResponseWriter, r *http.Request) {
	snapshot, units, err := s.directory.MachineUnits(r.Context(), r.PathValue("id"))
	if s.failed(w, r, err, machine.ErrNotFound) {
		return
	}
	s.writeAPI(w, http.StatusOK, systemdReportOf(snapshot, units))
}

func systemdReportOf(snapshot systemdunits.Snapshot, units []directory.MachineUnit) systemdReportJSON {
	return systemdReportJSON{
		MachineID:   snapshot.MachineID,
		MachineName: snapshot.MachineName,
		ReportedAt:  httpx.TimeOrNil(snapshot.ReportedAt),
		Available:   snapshot.Available,
		Reason:      string(snapshot.Reason),
		Truncated:   snapshot.Truncated,
		Units:       httpx.ConvertAll(units, machineUnitOf),
	}
}

func machineUnitOf(item directory.MachineUnit) systemdUnitJSON {
	unit := systemdUnitJSON{Name: item.Unit.Name, Load: string(item.Unit.Load), Active: string(item.Unit.Active), Sub: string(item.Unit.Sub)}
	if item.Declared != nil {
		unit.Declared = &declaredUnitRefJSON{ServiceID: item.Declared.ServiceID, ServiceName: item.Declared.ServiceName, UnitID: item.Declared.UnitID}
	}
	return unit
}

func observedUnitOf(observation directory.UnitObservation) *observedUnitJSON {
	return &observedUnitJSON{
		Presence:   string(observation.Presence),
		Load:       string(observation.Unit.Load),
		Active:     string(observation.Unit.Active),
		Sub:        string(observation.Unit.Sub),
		ReportedAt: httpx.TimeOrNil(observation.ReportedAt),
	}
}
