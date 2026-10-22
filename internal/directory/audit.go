package directory

import (
	"strconv"

	"github.com/ldesfontaine/opencloud/internal/audit"
	"github.com/ldesfontaine/opencloud/internal/container"
)

// SetAuditor branche le journal d'audit ; nil est toléré.
func (d *Directory) SetAuditor(auditor *audit.Recorder) {
	d.auditor = auditor
}

func serviceEvent(action audit.Action, written Service) audit.Event {
	return audit.Event{
		Action: action, ObjectID: written.ID, ObjectName: written.Name,
		Details: audit.Details{
			"machine_id": written.MachineID,
			"nature":     string(written.Nature),
			"origin":     string(written.Origin),
			"units":      strconv.Itoa(len(written.Units)),
		},
	}
}

func projectEvent(action audit.Action, written Project) audit.Event {
	return audit.Event{
		Action: action, ObjectID: written.ID, ObjectName: written.Name,
		Details: audit.Details{"title": written.Title},
	}
}

// unitPolicyEvent vise l'unité ; son service se lit dans les détails.
func unitPolicyEvent(unit Unit, owner Service, policy container.UpdatePolicy) audit.Event {
	return audit.Event{
		Action: audit.UnitUpdatePolicy, ObjectID: unit.ID, ObjectName: owner.Name + "/" + unit.Name,
		Details: audit.Details{"service_id": owner.ID, "policy": string(policy)},
	}
}

// ignoredKeyEvent : une clé n'a pas d'identifiant, elle est sa machine, sa
// sorte et sa valeur.
func ignoredKeyEvent(action audit.Action, key IgnoredKey) audit.Event {
	return audit.Event{
		Action: action, ObjectID: key.MachineID + "/" + string(key.Kind) + "/" + key.Value, ObjectName: key.Value,
		Details: audit.Details{"machine_id": key.MachineID, "kind": string(key.Kind)},
	}
}

// unitVersionEvent : une version changée est une mise à jour du service ; le
// geste « passer à » le dit dans via, l'unité et les deux versions dans les
// détails.
func unitVersionEvent(unit Unit, owner Service, version string) audit.Event {
	return audit.Event{
		Action: audit.ServiceUpdate, ObjectID: owner.ID, ObjectName: owner.Name,
		Details: audit.Details{
			"machine_id": owner.MachineID, "unit": unit.Name,
			"version_before": unit.Version, "version_after": version, "via": "upgrade",
		},
	}
}
