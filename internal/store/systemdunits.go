package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/ldesfontaine/opencloud/internal/machine"
	"github.com/ldesfontaine/opencloud/internal/systemdunits"
)

// ReplaceSystemdUnits remplace tout ce que la machine avait par son dernier
// relevé, en une transaction : une unité que le relevé ne dit plus disparaît.
func (db *DB) ReplaceSystemdUnits(ctx context.Context, machineID string, report systemdunits.Report, at time.Time) error {
	return db.inTx(ctx, "replace systemd units", func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `DELETE FROM systemd_units WHERE machine_id = ?`, machineID); err != nil {
			return fmt.Errorf("delete systemd units: %w", err)
		}
		_, err := tx.ExecContext(ctx, `
			INSERT INTO systemd_reports (machine_id, available, reason, truncated, reported_at) VALUES (?, ?, ?, ?, ?)
			ON CONFLICT (machine_id) DO UPDATE SET available = excluded.available, reason = excluded.reason,
				truncated = excluded.truncated, reported_at = excluded.reported_at`,
			machineID, report.Available, string(report.Reason), report.Truncated, at.Unix())
		if err != nil {
			return fmt.Errorf("upsert systemd report: %w", err)
		}
		for _, unit := range report.Units {
			_, err := tx.ExecContext(ctx, `
				INSERT INTO systemd_units (machine_id, name, load_state, active_state, sub_state) VALUES (?, ?, ?, ?, ?)`,
				machineID, unit.Name, string(unit.Load), string(unit.Active), string(unit.Sub))
			if err != nil {
				return fmt.Errorf("insert systemd unit %s: %w", unit.Name, err)
			}
		}
		return nil
	})
}

// MachineSystemdUnits rend le relevé d'une machine, vide et sans date tant que
// son agent n'en a envoyé aucun.
func (db *DB) MachineSystemdUnits(ctx context.Context, machineID string) (systemdunits.Snapshot, error) {
	var exists int
	err := db.sql.QueryRowContext(ctx, `SELECT 1 FROM machines WHERE id = ?`, machineID).Scan(&exists)
	if errors.Is(err, sql.ErrNoRows) {
		return systemdunits.Snapshot{}, machine.ErrNotFound
	}
	if err != nil {
		return systemdunits.Snapshot{}, fmt.Errorf("get machine %s: %w", machineID, err)
	}
	snapshots, err := db.readSystemdSnapshots(ctx, `WHERE m.id = ?`, machineID)
	if err != nil {
		return systemdunits.Snapshot{}, err
	}
	return snapshots[0], nil
}

// SystemdUnitsOfMachines rend le relevé de chaque machine, par nom.
func (db *DB) SystemdUnitsOfMachines(ctx context.Context) ([]systemdunits.Snapshot, error) {
	return db.readSystemdSnapshots(ctx, ``)
}

// readSystemdSnapshots lit les machines choisies par where (une clause
// constante de ce fichier) avec leur rapport et leurs unités : une requête par
// niveau, regroupées en Go.
func (db *DB) readSystemdSnapshots(ctx context.Context, where string, args ...any) ([]systemdunits.Snapshot, error) {
	rows, err := db.sql.QueryContext(ctx, `
		SELECT m.id, m.name, r.reported_at, COALESCE(r.available, 0), COALESCE(r.reason, ''), COALESCE(r.truncated, 0)
		FROM machines m LEFT JOIN systemd_reports r ON r.machine_id = m.id `+where+` ORDER BY m.name, m.id`, args...) // #nosec G202 -- where est une constante de ce fichier.
	if err != nil {
		return nil, fmt.Errorf("list systemd reports: %w", err)
	}
	snapshots, err := collect(rows, scanSystemdSnapshot)
	if err != nil {
		return nil, fmt.Errorf("scan systemd reports: %w", err)
	}
	units, err := db.readSystemdUnits(ctx, where, args...)
	if err != nil {
		return nil, err
	}
	for index := range snapshots {
		snapshots[index].Units = units[snapshots[index].MachineID]
		if snapshots[index].Units == nil {
			snapshots[index].Units = []systemdunits.Unit{}
		}
	}
	return snapshots, nil
}

func scanSystemdSnapshot(row scanner) (systemdunits.Snapshot, error) {
	var snapshot systemdunits.Snapshot
	var reportedAt sql.NullInt64
	var reason string
	if err := row.Scan(&snapshot.MachineID, &snapshot.MachineName, &reportedAt, &snapshot.Available, &reason, &snapshot.Truncated); err != nil {
		return systemdunits.Snapshot{}, err
	}
	snapshot.Reason = systemdunits.Reason(reason)
	if reportedAt.Valid {
		snapshot.ReportedAt = time.Unix(reportedAt.Int64, 0).UTC()
	}
	return snapshot, nil
}

// readSystemdUnits rend les unités des machines choisies, par machine, dans
// l'ordre des noms.
func (db *DB) readSystemdUnits(ctx context.Context, where string, args ...any) (map[string][]systemdunits.Unit, error) {
	rows, err := db.sql.QueryContext(ctx, `
		SELECT u.machine_id, u.name, u.load_state, u.active_state, u.sub_state
		FROM systemd_units u JOIN machines m ON m.id = u.machine_id `+where+` ORDER BY u.machine_id, u.name`, args...) // #nosec G202 -- where est une constante de ce fichier.
	if err != nil {
		return nil, fmt.Errorf("list systemd units: %w", err)
	}
	type owned struct {
		machineID string
		unit      systemdunits.Unit
	}
	listed, err := collect(rows, func(row scanner) (owned, error) {
		var item owned
		var load, active, sub string
		err := row.Scan(&item.machineID, &item.unit.Name, &load, &active, &sub)
		item.unit.Load, item.unit.Active, item.unit.Sub = systemdunits.Load(load), systemdunits.Active(active), systemdunits.Sub(sub)
		return item, err
	})
	if err != nil {
		return nil, fmt.Errorf("scan systemd units: %w", err)
	}
	byMachine := map[string][]systemdunits.Unit{}
	for _, item := range listed {
		byMachine[item.machineID] = append(byMachine[item.machineID], item.unit)
	}
	return byMachine, nil
}
