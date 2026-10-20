package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/ldesfontaine/opencloud/internal/container"
	"github.com/ldesfontaine/opencloud/internal/directory"
)

const unitColumns = `id, service_id, name, kind, image, version, digest, container_name, systemd_unit, ports, update_policy`

// rewriteUnits garde les unités dont l'identifiant est donné, supprime les
// autres, crée les nouvelles. Le nom d'une unité est unique dans son
// service : deux unités qui échangent leurs noms se croiseraient en
// route, donc chaque unité gardée prend d'abord un nom que le motif des
// noms interdit (« ~ » et son identifiant), puis son vrai nom.
func rewriteUnits(ctx context.Context, tx *sql.Tx, service directory.Service) error {
	units := make([]directory.Unit, len(service.Units))
	given := make(map[string]bool, len(service.Units))
	for index, unit := range service.Units {
		if unit.ID == "" {
			id, err := directory.NewID(directory.UnitPrefix)
			if err != nil {
				return fmt.Errorf("generate unit id: %w", err)
			}
			unit.ID = id
		}
		unit.ServiceID = service.ID
		units[index] = unit
		given[unit.ID] = true
	}
	existing, err := unitIDsOf(ctx, tx, service.ID)
	if err != nil {
		return err
	}
	for id := range existing {
		if given[id] {
			continue
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM service_units WHERE id = ?`, id); err != nil {
			return fmt.Errorf("delete unit: %w", err)
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE service_units SET name = '~' || id WHERE service_id = ?`, service.ID); err != nil {
		return fmt.Errorf("park unit names: %w", err)
	}
	for position, unit := range units {
		write := insertUnit
		if existing[unit.ID] {
			write = updateUnit
		}
		if err := write(ctx, tx, unit, position); err != nil {
			return err
		}
	}
	return nil
}

func unitIDsOf(ctx context.Context, tx *sql.Tx, serviceID string) (map[string]bool, error) {
	rows, err := tx.QueryContext(ctx, `SELECT id FROM service_units WHERE service_id = ?`, serviceID)
	if err != nil {
		return nil, fmt.Errorf("list unit ids: %w", err)
	}
	defer rows.Close()
	ids := map[string]bool{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids[id] = true
	}
	return ids, rows.Err()
}

func portsText(ports []string) (string, error) {
	if ports == nil {
		ports = []string{}
	}
	encoded, err := json.Marshal(ports)
	if err != nil {
		return "", fmt.Errorf("encode unit ports: %w", err)
	}
	return string(encoded), nil
}

func insertUnit(ctx context.Context, tx *sql.Tx, unit directory.Unit, position int) error {
	ports, err := portsText(unit.Ports)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `
		INSERT INTO service_units (id, service_id, name, kind, image, version, digest,
			container_name, systemd_unit, ports, update_policy, position)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		unit.ID, unit.ServiceID, unit.Name, string(unit.Kind), unit.Image, unit.Version, unit.Digest,
		unit.ContainerName, unit.SystemdUnit, ports, string(unit.UpdatePolicy), position)
	if err != nil {
		return fmt.Errorf("insert unit: %w", err)
	}
	return nil
}

func updateUnit(ctx context.Context, tx *sql.Tx, unit directory.Unit, position int) error {
	ports, err := portsText(unit.Ports)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `
		UPDATE service_units SET name = ?, kind = ?, image = ?, version = ?, digest = ?,
			container_name = ?, systemd_unit = ?, ports = ?, update_policy = ?, position = ?
		WHERE id = ?`,
		unit.Name, string(unit.Kind), unit.Image, unit.Version, unit.Digest,
		unit.ContainerName, unit.SystemdUnit, ports, string(unit.UpdatePolicy), position, unit.ID)
	if err != nil {
		return fmt.Errorf("update unit: %w", err)
	}
	return nil
}

func (db *DB) listUnits(ctx context.Context, scope partsScope) ([]directory.Unit, error) {
	where, args := scope.where(`service_id`)
	query := `SELECT ` + unitColumns + ` FROM service_units` + where + ` ORDER BY service_id, position, id` // #nosec G202 -- des conditions fixes, les valeurs sont liées.
	rows, err := db.sql.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list units: %w", err)
	}
	return collect(rows, scanUnit)
}

func scanUnit(row scanner) (directory.Unit, error) {
	var unit directory.Unit
	var kind, ports, policy string
	err := row.Scan(&unit.ID, &unit.ServiceID, &unit.Name, &kind, &unit.Image, &unit.Version, &unit.Digest,
		&unit.ContainerName, &unit.SystemdUnit, &ports, &policy)
	if err != nil {
		return directory.Unit{}, err
	}
	unit.Kind = directory.UnitKind(kind)
	unit.UpdatePolicy = container.UpdatePolicy(policy)
	unit.Ports = []string{}
	if err := json.Unmarshal([]byte(ports), &unit.Ports); err != nil {
		return directory.Unit{}, fmt.Errorf("decode unit ports: %w", err)
	}
	return unit, nil
}

func (db *DB) GetUnit(ctx context.Context, id string) (directory.Unit, error) {
	unit, err := scanUnit(db.sql.QueryRowContext(ctx, `SELECT `+unitColumns+` FROM service_units WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return directory.Unit{}, directory.ErrUnitNotFound
	}
	return unit, err
}
