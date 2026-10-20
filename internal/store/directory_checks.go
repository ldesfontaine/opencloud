package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/ldesfontaine/opencloud/internal/directory"
)

// checkServiceWrite lit tout ce qui peut refuser l'écriture, du plus
// général au plus précis : la machine, le nom, le projet Compose, les clés
// par lesquelles le service sera retrouvé, les projets.
func checkServiceWrite(ctx context.Context, tx *sql.Tx, service directory.Service) error {
	var machines int
	err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM machines WHERE id = ?`, service.MachineID).Scan(&machines)
	if err != nil {
		return fmt.Errorf("find declared service machine: %w", err)
	}
	if machines == 0 {
		return directory.ErrMachineInvalid
	}
	taken, err := isServiceNameTaken(ctx, tx, service)
	if err != nil {
		return err
	}
	if taken {
		return directory.ErrNameTaken
	}
	taken, err = isComposeProjectTaken(ctx, tx, service)
	if err != nil {
		return err
	}
	if taken {
		return directory.ErrComposeProjectTaken
	}
	if err := checkMatchKeys(ctx, tx, service); err != nil {
		return err
	}
	return checkProjectsExist(ctx, tx, service)
}

func isServiceNameTaken(ctx context.Context, tx *sql.Tx, service directory.Service) (bool, error) {
	var others int
	err := tx.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM declared_services WHERE machine_id = ? AND name = ? AND id != ?`,
		service.MachineID, service.Name, service.ID).Scan(&others)
	if err != nil {
		return false, fmt.Errorf("check declared service name: %w", err)
	}
	return others > 0, nil
}

func isComposeProjectTaken(ctx context.Context, tx *sql.Tx, service directory.Service) (bool, error) {
	if service.Origin != directory.OriginCompose {
		return false, nil
	}
	var others int
	err := tx.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM declared_services
		WHERE machine_id = ? AND origin = ? AND compose_project = ? AND id != ?`,
		service.MachineID, string(directory.OriginCompose), service.ComposeProject, service.ID).Scan(&others)
	if err != nil {
		return false, fmt.Errorf("check compose project: %w", err)
	}
	return others > 0, nil
}

// Le nom du conteneur et l'unité systemd retrouvent le service parmi ce que
// la machine montre : deux services ne les partagent pas, ni deux unités du
// même service.
type matchKey struct {
	column string
	value  string
}

func matchKeysOf(unit directory.Unit) []matchKey {
	keys := []matchKey{}
	if unit.ContainerName != "" {
		keys = append(keys, matchKey{column: "container_name", value: unit.ContainerName})
	}
	if unit.SystemdUnit != "" {
		keys = append(keys, matchKey{column: "systemd_unit", value: unit.SystemdUnit})
	}
	return keys
}

func checkMatchKeys(ctx context.Context, tx *sql.Tx, service directory.Service) error {
	seen := map[matchKey]bool{}
	for _, unit := range service.Units {
		for _, key := range matchKeysOf(unit) {
			if seen[key] {
				return directory.ErrUnitKeyDuplicate
			}
			seen[key] = true
			taken, err := isMatchKeyTaken(ctx, tx, service, key)
			if err != nil {
				return err
			}
			if taken {
				return directory.ErrMatchKeyTaken
			}
		}
	}
	return nil
}

// Une requête par colonne, entière : le nom de la colonne ne se colle pas
// à une requête.
var matchKeyQueries = map[string]string{
	"container_name": `
		SELECT COUNT(*) FROM service_units u JOIN declared_services s ON s.id = u.service_id
		WHERE s.machine_id = ? AND s.id != ? AND u.container_name = ?`,
	"systemd_unit": `
		SELECT COUNT(*) FROM service_units u JOIN declared_services s ON s.id = u.service_id
		WHERE s.machine_id = ? AND s.id != ? AND u.systemd_unit = ?`,
}

func isMatchKeyTaken(ctx context.Context, tx *sql.Tx, service directory.Service, key matchKey) (bool, error) {
	var others int
	err := tx.QueryRowContext(ctx, matchKeyQueries[key.column], service.MachineID, service.ID, key.value).Scan(&others)
	if err != nil {
		return false, fmt.Errorf("check %s: %w", key.column, err)
	}
	return others > 0, nil
}

// À l'édition, une unité sans identifiant est nouvelle ; une unité qui en
// porte un doit être, dans cette transaction, une unité de ce service, et
// n'y figurer qu'une fois. Sinon elle serait créée sous un identifiant que
// l'appelant a choisi, ou volée à un autre service.
func checkUnitsBelong(ctx context.Context, tx *sql.Tx, service directory.Service) error {
	seen := make(map[string]bool, len(service.Units))
	for _, unit := range service.Units {
		if unit.ID == "" {
			continue
		}
		if seen[unit.ID] {
			return directory.ErrUnitInvalid
		}
		seen[unit.ID] = true
		var owner string
		err := tx.QueryRowContext(ctx, `SELECT service_id FROM service_units WHERE id = ?`, unit.ID).Scan(&owner)
		if errors.Is(err, sql.ErrNoRows) {
			return directory.ErrUnitInvalid
		}
		if err != nil {
			return fmt.Errorf("find unit owner: %w", err)
		}
		if owner != service.ID {
			return directory.ErrUnitInvalid
		}
	}
	return nil
}

func checkProjectsExist(ctx context.Context, tx *sql.Tx, service directory.Service) error {
	for _, projectID := range service.ProjectIDs {
		var found int
		err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM projects WHERE id = ?`, projectID).Scan(&found)
		if err != nil {
			return fmt.Errorf("find project: %w", err)
		}
		if found == 0 {
			return directory.ErrProjectInvalid
		}
	}
	return nil
}
