package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/ldesfontaine/opencloud/internal/hostinfo"
	"github.com/ldesfontaine/opencloud/internal/machine"
)

// memberSeparator sépare les disques d'un pool dans la colonne ; aucun nom de
// disque n'en contient (hostinfo.namePattern).
const memberSeparator = ","

// SetPlacement pose la correction de type et l'hôte désigné ; vides, ils
// repassent à NULL.
func (db *DB) SetPlacement(ctx context.Context, machineID string, kindOverride machine.Kind, hostOverride string) error {
	result, err := db.sql.ExecContext(ctx, `UPDATE machines SET kind_override = ?, host_override = ? WHERE id = ?`,
		nullableString(string(kindOverride)), nullableString(hostOverride), machineID)
	if err != nil {
		return fmt.Errorf("set machine placement: %w", err)
	}
	return notFoundIfNoRow(result, machine.ErrNotFound)
}

// ReplaceHostReport remplace en une transaction le type, les cartes, les disques
// et les pools de la machine par ceux du rapport. Il dit si le reflet a changé,
// pour ne prévenir le direct que de ce qui est neuf.
func (db *DB) ReplaceHostReport(ctx context.Context, machineID string, kind machine.Kind, report hostinfo.Report) (bool, error) {
	changed := false
	err := db.inTx(ctx, "replace host report", func(tx *sql.Tx) error {
		before, err := readHostReport(ctx, tx, machineID)
		if err != nil {
			return err
		}
		stored := hostinfo.Report{Interfaces: report.Interfaces, Storage: report.Storage}
		if before.kind == kind && equalHostReports(before.report, stored) {
			return nil
		}
		changed = true
		return writeHostReport(ctx, tx, machineID, kind, report)
	})
	return changed, err
}

type storedHost struct {
	kind   machine.Kind
	report hostinfo.Report
}

func readHostReport(ctx context.Context, tx *sql.Tx, machineID string) (storedHost, error) {
	var kind string
	if err := tx.QueryRowContext(ctx, `SELECT kind FROM machines WHERE id = ?`, machineID).Scan(&kind); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return storedHost{}, machine.ErrNotFound
		}
		return storedHost{}, fmt.Errorf("read machine kind: %w", err)
	}
	interfaces, err := readInterfaces(ctx, tx, machineID)
	if err != nil {
		return storedHost{}, err
	}
	storage, err := readStorage(ctx, tx, machineID)
	if err != nil {
		return storedHost{}, err
	}
	return storedHost{kind: machine.Kind(kind), report: hostinfo.Report{Interfaces: interfaces, Storage: storage}}, nil
}

// equalHostReports compare deux rapports sans compter l'ordre : la base les rend
// par nom, l'agent les envoie comme il les a trouvés.
func equalHostReports(left, right hostinfo.Report) bool {
	return sameSet(left.Interfaces, right.Interfaces) && sameSet(left.Disks, right.Disks) && samePools(left.Pools, right.Pools)
}

func sameSet[T comparable](left, right []T) bool {
	return len(left) == len(right) && !slices.ContainsFunc(left, func(item T) bool { return !slices.Contains(right, item) })
}

func samePools(left, right []hostinfo.Pool) bool {
	if len(left) != len(right) {
		return false
	}
	for _, pool := range left {
		matches := slices.ContainsFunc(right, func(other hostinfo.Pool) bool {
			return pool.Name == other.Name && pool.Kind == other.Kind && pool.Level == other.Level &&
				pool.State == other.State && pool.Size == other.Size && slices.Equal(pool.Members, other.Members)
		})
		if !matches {
			return false
		}
	}
	return true
}

func writeHostReport(ctx context.Context, tx *sql.Tx, machineID string, kind machine.Kind, report hostinfo.Report) error {
	if _, err := tx.ExecContext(ctx, `UPDATE machines SET kind = ? WHERE id = ?`, string(kind), machineID); err != nil {
		return fmt.Errorf("update machine kind: %w", err)
	}
	for _, table := range []string{"machine_interfaces", "machine_block_devices", "machine_storage_pools"} {
		if _, err := tx.ExecContext(ctx, `DELETE FROM `+table+` WHERE machine_id = ?`, machineID); err != nil {
			return fmt.Errorf("delete %s: %w", table, err)
		}
	}
	for _, iface := range report.Interfaces {
		if _, err := tx.ExecContext(ctx, `INSERT INTO machine_interfaces (machine_id, name, mac) VALUES (?, ?, ?)`,
			machineID, iface.Name, iface.MAC); err != nil {
			return fmt.Errorf("insert machine interface %s: %w", iface.Name, err)
		}
	}
	for _, disk := range report.Disks {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO machine_block_devices (machine_id, name, model, size, media, removable) VALUES (?, ?, ?, ?, ?, ?)`,
			machineID, disk.Name, disk.Model, disk.Size, disk.Media, disk.Removable); err != nil {
			return fmt.Errorf("insert machine disk %s: %w", disk.Name, err)
		}
	}
	for _, pool := range report.Pools {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO machine_storage_pools (machine_id, name, kind, level, state, size, members) VALUES (?, ?, ?, ?, ?, ?, ?)`,
			machineID, pool.Name, pool.Kind, pool.Level, pool.State, pool.Size, strings.Join(pool.Members, memberSeparator)); err != nil {
			return fmt.Errorf("insert machine pool %s: %w", pool.Name, err)
		}
	}
	return nil
}

func readInterfaces(ctx context.Context, q querier, machineID string) ([]hostinfo.Interface, error) {
	rows, err := q.QueryContext(ctx, `SELECT name, mac FROM machine_interfaces WHERE machine_id = ? ORDER BY name`, machineID)
	if err != nil {
		return nil, fmt.Errorf("list machine interfaces: %w", err)
	}
	return collect(rows, func(row scanner) (hostinfo.Interface, error) {
		var iface hostinfo.Interface
		err := row.Scan(&iface.Name, &iface.MAC)
		return iface, err
	})
}

func readStorage(ctx context.Context, q querier, machineID string) (hostinfo.Storage, error) {
	disks, err := readDisks(ctx, q, machineID)
	if err != nil {
		return hostinfo.Storage{}, err
	}
	pools, err := readPools(ctx, q, machineID)
	if err != nil {
		return hostinfo.Storage{}, err
	}
	return hostinfo.Storage{Disks: disks, Pools: pools}, nil
}

func readDisks(ctx context.Context, q querier, machineID string) ([]hostinfo.Disk, error) {
	rows, err := q.QueryContext(ctx, `SELECT name, model, size, media, removable FROM machine_block_devices WHERE machine_id = ? ORDER BY name`, machineID)
	if err != nil {
		return nil, fmt.Errorf("list machine disks: %w", err)
	}
	return collect(rows, func(row scanner) (hostinfo.Disk, error) {
		var disk hostinfo.Disk
		err := row.Scan(&disk.Name, &disk.Model, &disk.Size, &disk.Media, &disk.Removable)
		return disk, err
	})
}

func readPools(ctx context.Context, q querier, machineID string) ([]hostinfo.Pool, error) {
	rows, err := q.QueryContext(ctx, `SELECT name, kind, level, state, size, members FROM machine_storage_pools WHERE machine_id = ? ORDER BY name`, machineID)
	if err != nil {
		return nil, fmt.Errorf("list machine pools: %w", err)
	}
	return collect(rows, func(row scanner) (hostinfo.Pool, error) {
		var pool hostinfo.Pool
		var members string
		err := row.Scan(&pool.Name, &pool.Kind, &pool.Level, &pool.State, &pool.Size, &members)
		pool.Members = []string{}
		if members != "" {
			pool.Members = strings.Split(members, memberSeparator)
		}
		return pool, err
	})
}

// MachineStorage rend les disques physiques et les pools que l'agent de la
// machine a rapportés ; des listes vides tant qu'il n'a rien dit.
func (db *DB) MachineStorage(ctx context.Context, machineID string) (hostinfo.Storage, error) {
	return readStorage(ctx, db.sql, machineID)
}

// attachMACs pose sur chaque machine les adresses MAC que son agent a
// rapportées, en une requête pour toute la liste.
func attachMACs(ctx context.Context, q querier, machines []machine.Machine) error {
	rows, err := q.QueryContext(ctx, `SELECT machine_id, mac FROM machine_interfaces ORDER BY machine_id, name`)
	if err != nil {
		return fmt.Errorf("list machine macs: %w", err)
	}
	type owned struct{ machineID, mac string }
	entries, err := collect(rows, func(row scanner) (owned, error) {
		var entry owned
		err := row.Scan(&entry.machineID, &entry.mac)
		return entry, err
	})
	if err != nil {
		return fmt.Errorf("scan machine macs: %w", err)
	}
	for _, entry := range entries {
		for index := range machines {
			if machines[index].ID == entry.machineID {
				machines[index].MACs = append(machines[index].MACs, entry.mac)
			}
		}
	}
	return nil
}
