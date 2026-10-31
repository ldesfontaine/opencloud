package store

import (
	"context"
	"fmt"
	"strings"

	"github.com/ldesfontaine/opencloud/internal/module"
)

// La règle de présence de chaque module, en SQL : un prédicat sur la machine
// m (ou sur l'installation, sans table de départ). Une règle par module de la
// portée ; modules_test.go refuse une liste qui en oublie une.
//
// Le rôle choisi à l'ajout apporte ses modules ; les faits que l'agent
// observe en apportent d'autres, sans rôle : un module qui s'est montré ne
// disparaît pas parce que le rôle dit autre chose.
var machineModuleRules = map[module.ID]string{
	module.Containers: `m.role = 'docker' OR EXISTS (SELECT 1 FROM containers c WHERE c.machine_id = m.id)`,
	module.Hypervisor: `m.role = 'proxmox' OR EXISTS (SELECT 1 FROM machine_proxmox h WHERE h.machine_id = m.id)`,
	module.Guest:      `EXISTS (SELECT 1 FROM proxmox_guests g WHERE g.linked_machine_id = m.id)`,
	module.SSHKeys:    `EXISTS (SELECT 1 FROM ssh_reports r WHERE r.machine_id = m.id)`,
	module.Probes:     `EXISTS (SELECT 1 FROM probes p WHERE p.machine_id = m.id)`,
}

// « Configuré » : au moins un objet du module existe.
var installationModuleRules = map[module.ID]string{
	module.Firewall: `EXISTS (SELECT 1 FROM firewalls)`,
	module.Backups:  `EXISTS (SELECT 1 FROM backup_keyring) OR EXISTS (SELECT 1 FROM backup_plans)`,
	module.Jobs:     `EXISTS (SELECT 1 FROM heartbeats)`,
	module.Probes:   `EXISTS (SELECT 1 FROM probes)`,
	module.Status:   `EXISTS (SELECT 1 FROM status_pages)`,
}

// MachineModules rend, par machine, les modules qu'elle porte, dans l'ordre
// de la liste fermée.
func (db *DB) MachineModules(ctx context.Context) (map[string][]module.ID, error) {
	ids := module.In(module.ScopeMachine)
	columns, err := ruleColumns(ids, machineModuleRules)
	if err != nil {
		return nil, err
	}
	rows, err := db.sql.QueryContext(ctx, `SELECT m.id, `+columns+` FROM machines m`) // #nosec G202 -- les règles sont des constantes du paquet.
	if err != nil {
		return nil, fmt.Errorf("read machine modules: %w", err)
	}
	defer rows.Close()
	modules := map[string][]module.ID{}
	for rows.Next() {
		var machineID string
		present := make([]bool, len(ids))
		targets := []any{&machineID}
		for position := range present {
			targets = append(targets, &present[position])
		}
		if err := rows.Scan(targets...); err != nil {
			return nil, fmt.Errorf("scan machine modules: %w", err)
		}
		carried := []module.ID{}
		for position, id := range ids {
			if present[position] {
				carried = append(carried, id)
			}
		}
		modules[machineID] = carried
	}
	return modules, rows.Err()
}

// InstallationModules dit, pour chaque module de l'installation, s'il est
// configuré.
func (db *DB) InstallationModules(ctx context.Context) ([]module.State, error) {
	ids := module.In(module.ScopeInstallation)
	columns, err := ruleColumns(ids, installationModuleRules)
	if err != nil {
		return nil, err
	}
	configured := make([]bool, len(ids))
	targets := make([]any, 0, len(ids))
	for position := range configured {
		targets = append(targets, &configured[position])
	}
	if err := db.sql.QueryRowContext(ctx, `SELECT `+columns).Scan(targets...); err != nil { // #nosec G202 -- les règles sont des constantes du paquet.
		return nil, fmt.Errorf("read installation modules: %w", err)
	}
	states := make([]module.State, 0, len(ids))
	for position, id := range ids {
		states = append(states, module.State{ID: id, Configured: configured[position]})
	}
	return states, nil
}

// ruleColumns écrit une colonne par module, dans l'ordre de la liste fermée ;
// un module sans règle est une erreur de programme, pas une absence.
func ruleColumns(ids []module.ID, rules map[module.ID]string) (string, error) {
	columns := make([]string, 0, len(ids))
	for _, id := range ids {
		rule, found := rules[id]
		if !found {
			return "", fmt.Errorf("module %s has no presence rule", id)
		}
		columns = append(columns, "("+rule+")")
	}
	return strings.Join(columns, ", "), nil
}
