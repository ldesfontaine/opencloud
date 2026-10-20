package store

import (
	"context"
	"fmt"

	"github.com/ldesfontaine/opencloud/internal/directory"
)

// ListServiceLinks rend les sondes et les composants de statut qui visent le
// service, par nom.
func (db *DB) ListServiceLinks(ctx context.Context, serviceID string) (directory.Links, error) {
	return listServiceLinks(ctx, db.sql, serviceID)
}

func listServiceLinks(ctx context.Context, db querier, serviceID string) (directory.Links, error) {
	probes, err := linkedObjects(ctx, db, `SELECT id, name FROM probes WHERE service_id = ? ORDER BY name, id`, serviceID)
	if err != nil {
		return directory.Links{}, fmt.Errorf("list service probes: %w", err)
	}
	components, err := linkedObjects(ctx, db, `
		SELECT id, name FROM status_components
		WHERE id IN (SELECT component_id FROM status_component_members WHERE service_id = ?)
		ORDER BY position, name, id`, serviceID)
	if err != nil {
		return directory.Links{}, fmt.Errorf("list service components: %w", err)
	}
	return directory.Links{Probes: probes, Components: components}, nil
}

// linkedObjects lit des lignes (id, nom).
func linkedObjects(ctx context.Context, db querier, query string, args ...any) ([]directory.LinkedObject, error) {
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	return collect(rows, scanLinkedObject)
}

func scanLinkedObject(row scanner) (directory.LinkedObject, error) {
	var object directory.LinkedObject
	if err := row.Scan(&object.ID, &object.Name); err != nil {
		return directory.LinkedObject{}, err
	}
	return object, nil
}
