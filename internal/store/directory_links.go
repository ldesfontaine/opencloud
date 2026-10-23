package store

import (
	"context"
	"fmt"

	"github.com/ldesfontaine/opencloud/internal/directory"
)

// ListServiceLinks rend les sondes et les composants de statut qui visent le
// service, par nom : ce qui retient sa suppression (DeclaredServiceDeletionPlan).
func (db *DB) ListServiceLinks(ctx context.Context, serviceID string) (directory.Links, error) {
	probes, err := db.linkedObjects(ctx, probesWatchingService, serviceID)
	if err != nil {
		return directory.Links{}, fmt.Errorf("list service probes: %w", err)
	}
	components, err := db.linkedObjects(ctx, componentsShowing("service_id"), serviceID)
	if err != nil {
		return directory.Links{}, fmt.Errorf("list service components: %w", err)
	}
	return directory.Links{Probes: probes, Components: components}, nil
}

// linkedObjects lit des lignes (id, nom).
func (db *DB) linkedObjects(ctx context.Context, query string, args ...any) ([]directory.LinkedObject, error) {
	rows, err := db.sql.QueryContext(ctx, query, args...)
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
