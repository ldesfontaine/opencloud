package store

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/ldesfontaine/opencloud/internal/role"
)

var _ role.Store = (*DB)(nil)

// ReplaceRoles remplace les rôles de la machine par ceux du rapport, en une
// transaction : un rôle que l'agent ne voit plus disparaît. Il dit si le
// reflet a changé, pour ne prévenir le direct que de ce qui est neuf.
func (db *DB) ReplaceRoles(ctx context.Context, machineID string, roles []role.Role) (bool, error) {
	changed := false
	err := db.inTx(ctx, "replace machine roles", func(tx *sql.Tx) error {
		before, err := readMachineRoles(ctx, tx, machineID)
		if err != nil {
			return err
		}
		if equalRoles(before, roles) {
			return nil
		}
		changed = true
		if _, err := tx.ExecContext(ctx, `DELETE FROM machine_roles WHERE machine_id = ?`, machineID); err != nil {
			return fmt.Errorf("delete machine roles: %w", err)
		}
		for _, entry := range roles {
			_, err := tx.ExecContext(ctx, `INSERT INTO machine_roles (machine_id, role, version) VALUES (?, ?, ?)`,
				machineID, string(entry.Name), entry.Version)
			if err != nil {
				return fmt.Errorf("insert machine role %s: %w", entry.Name, err)
			}
		}
		return nil
	})
	return changed, err
}

// equalRoles compare deux listes sans compter l'ordre : la base les rend par
// nom, l'agent les envoie comme il les a trouvées.
func equalRoles(left, right []role.Role) bool {
	if len(left) != len(right) {
		return false
	}
	for _, entry := range left {
		found := false
		for _, other := range right {
			if entry == other {
				found = true
			}
		}
		if !found {
			return false
		}
	}
	return true
}

// MachineRoles rend, par machine, les rôles que son agent y a reconnus ; une
// machine sans rôle n'y figure pas.
func (db *DB) MachineRoles(ctx context.Context) (map[string][]role.Role, error) {
	rows, err := db.sql.QueryContext(ctx, `SELECT machine_id, role, version FROM machine_roles ORDER BY machine_id, role`)
	if err != nil {
		return nil, fmt.Errorf("list machine roles: %w", err)
	}
	type owned struct {
		machineID string
		role      role.Role
	}
	entries, err := collect(rows, func(row scanner) (owned, error) {
		var entry owned
		var name string
		err := row.Scan(&entry.machineID, &name, &entry.role.Version)
		entry.role.Name = role.Name(name)
		return entry, err
	})
	if err != nil {
		return nil, fmt.Errorf("scan machine roles: %w", err)
	}
	byMachine := map[string][]role.Role{}
	for _, entry := range entries {
		byMachine[entry.machineID] = append(byMachine[entry.machineID], entry.role)
	}
	return byMachine, nil
}

func readMachineRoles(ctx context.Context, tx *sql.Tx, machineID string) ([]role.Role, error) {
	rows, err := tx.QueryContext(ctx, `SELECT role, version FROM machine_roles WHERE machine_id = ?`, machineID)
	if err != nil {
		return nil, fmt.Errorf("read machine roles: %w", err)
	}
	roles, err := collect(rows, func(row scanner) (role.Role, error) {
		var entry role.Role
		var name string
		err := row.Scan(&name, &entry.Version)
		entry.Name = role.Name(name)
		return entry, err
	})
	if err != nil {
		return nil, fmt.Errorf("scan machine roles: %w", err)
	}
	return roles, nil
}
