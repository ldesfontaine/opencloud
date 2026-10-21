package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/ldesfontaine/opencloud/internal/directory"
)

const declaredServiceColumns = `s.id, s.machine_id, m.name, s.name, s.title, s.nature, s.origin,
	s.compose_project, s.public_url, s.declared_by, s.needs_review, s.created_at, s.updated_at`

const declaredServiceFrom = ` FROM declared_services s JOIN machines m ON m.id = s.machine_id`

// InsertDeclaredService écrit le service, ses unités, ses fournitures et ses
// liens de projet d'un coup. Les refus se lisent avant d'écrire : un index
// unique violé ne dirait pas lequel des noms est pris.
func (db *DB) InsertDeclaredService(ctx context.Context, service directory.Service) error {
	return db.inTx(ctx, "insert declared service", func(tx *sql.Tx) error {
		if err := checkServiceWrite(ctx, tx, service); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `
		INSERT INTO declared_services (id, machine_id, name, title, nature, origin, compose_project,
			public_url, declared_by, needs_review, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			service.ID, service.MachineID, service.Name, service.Title, string(service.Nature), string(service.Origin),
			service.ComposeProject, service.PublicURL, string(service.DeclaredBy), boolToInt(service.NeedsReview),
			service.CreatedAt.Unix(), service.UpdatedAt.Unix())
		if err != nil {
			return fmt.Errorf("insert declared service: %w", err)
		}
		for position, unit := range service.Units {
			if err := insertUnit(ctx, tx, unit, position); err != nil {
				return err
			}
		}
		if err := writeProvides(ctx, tx, service); err != nil {
			return err
		}
		if err := writeProjectLinks(ctx, tx, service); err != nil {
			return err
		}
		return nil
	})
}

// UpdateDeclaredService réécrit la définition. Les unités dont l'identifiant
// est gardé sont modifiées sur place, les autres supprimées ou créées ; les
// fournitures et les liens sont remplacés, pas rapprochés. La provenance et
// « à vérifier » ne se réécrivent pas ici.
func (db *DB) UpdateDeclaredService(ctx context.Context, service directory.Service) error {
	return db.inTx(ctx, "update declared service", func(tx *sql.Tx) error {
		var found int
		err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM declared_services WHERE id = ?`, service.ID).Scan(&found)
		if err != nil {
			return fmt.Errorf("find declared service: %w", err)
		}
		if found == 0 {
			return directory.ErrNotFound
		}
		if err := checkUnitsBelong(ctx, tx, service); err != nil {
			return err
		}
		if err := checkServiceWrite(ctx, tx, service); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `
		UPDATE declared_services SET machine_id = ?, name = ?, title = ?, nature = ?, origin = ?,
			compose_project = ?, public_url = ?, updated_at = ?
		WHERE id = ?`,
			service.MachineID, service.Name, service.Title, string(service.Nature), string(service.Origin),
			service.ComposeProject, service.PublicURL, service.UpdatedAt.Unix(), service.ID)
		if err != nil {
			return fmt.Errorf("update declared service: %w", err)
		}
		if err := rewriteUnits(ctx, tx, service); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM service_provides WHERE service_id = ?`, service.ID); err != nil {
			return fmt.Errorf("clear provides: %w", err)
		}
		if err := writeProvides(ctx, tx, service); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM project_services WHERE service_id = ?`, service.ID); err != nil {
			return fmt.Errorf("clear project links: %w", err)
		}
		if err := writeProjectLinks(ctx, tx, service); err != nil {
			return err
		}
		return nil
	})
}

func writeProvides(ctx context.Context, tx *sql.Tx, service directory.Service) error {
	for _, capability := range service.Provides {
		_, err := tx.ExecContext(ctx,
			`INSERT INTO service_provides (service_id, capability) VALUES (?, ?)`, service.ID, string(capability))
		if err != nil {
			return fmt.Errorf("insert provide: %w", err)
		}
	}
	return nil
}

func writeProjectLinks(ctx context.Context, tx *sql.Tx, service directory.Service) error {
	for _, projectID := range service.ProjectIDs {
		_, err := tx.ExecContext(ctx,
			`INSERT INTO project_services (project_id, service_id) VALUES (?, ?)`, projectID, service.ID)
		if err != nil {
			return fmt.Errorf("insert project link: %w", err)
		}
	}
	return nil
}

func (db *DB) GetDeclaredService(ctx context.Context, id string) (directory.Service, error) {
	row := db.sql.QueryRowContext(ctx, `SELECT `+declaredServiceColumns+declaredServiceFrom+` WHERE s.id = ?`, id)
	service, err := scanDeclaredService(row)
	if errors.Is(err, sql.ErrNoRows) {
		return directory.Service{}, directory.ErrNotFound
	}
	if err != nil {
		return directory.Service{}, err
	}
	services := []directory.Service{service}
	if err := db.attachServiceParts(ctx, services, partsScope{serviceID: id}); err != nil {
		return directory.Service{}, err
	}
	return services[0], nil
}

// ListDeclaredServices rend les services par nom, ceux d'une machine ou
// d'un projet si le filtre le dit.
func (db *DB) ListDeclaredServices(ctx context.Context, filter directory.Filter) ([]directory.Service, error) {
	query := `SELECT ` + declaredServiceColumns + declaredServiceFrom + ` WHERE 1 = 1`
	var args []any
	if filter.MachineID != "" {
		query += ` AND s.machine_id = ?`
		args = append(args, filter.MachineID)
	}
	if filter.ProjectID != "" {
		query += ` AND EXISTS (SELECT 1 FROM project_services ps WHERE ps.service_id = s.id AND ps.project_id = ?)`
		args = append(args, filter.ProjectID)
	}
	rows, err := db.sql.QueryContext(ctx, query+` ORDER BY s.name, m.name, s.id`, args...)
	if err != nil {
		return nil, fmt.Errorf("list declared services: %w", err)
	}
	services, err := collect(rows, scanDeclaredService)
	if err != nil {
		return nil, err
	}
	if err := db.attachServiceParts(ctx, services, partsScope{filter: filter}); err != nil {
		return nil, err
	}
	return services, nil
}

func scanDeclaredService(row scanner) (directory.Service, error) {
	var service directory.Service
	var nature, origin, declaredBy string
	var needsReview int
	var createdAt, updatedAt int64
	err := row.Scan(&service.ID, &service.MachineID, &service.MachineName, &service.Name, &service.Title,
		&nature, &origin, &service.ComposeProject, &service.PublicURL, &declaredBy, &needsReview,
		&createdAt, &updatedAt)
	if err != nil {
		return directory.Service{}, err
	}
	service.Nature = directory.Nature(nature)
	service.Origin = directory.Origin(origin)
	service.DeclaredBy = directory.DeclaredBy(declaredBy)
	service.NeedsReview = needsReview != 0
	service.CreatedAt = time.Unix(createdAt, 0)
	service.UpdatedAt = time.Unix(updatedAt, 0)
	service.Provides = []directory.Capability{}
	service.Units = []directory.Unit{}
	service.ProjectIDs = []string{}
	return service, nil
}

// partsScope dit de quels services lire les unités, les fournitures et les
// liens de projet : un seul, ceux d'un filtre, ou tous.
type partsScope struct {
	serviceID string
	filter    directory.Filter
}

// where rend la condition sur la colonne qui porte l'identifiant du
// service, précédée de « WHERE », ou rien si tous les services sont lus.
func (scope partsScope) where(column string) (string, []any) {
	var conditions []string
	var args []any
	if scope.serviceID != "" {
		conditions = append(conditions, column+` = ?`)
		args = append(args, scope.serviceID)
	}
	if scope.filter.MachineID != "" {
		conditions = append(conditions, column+` IN (SELECT id FROM declared_services WHERE machine_id = ?)`)
		args = append(args, scope.filter.MachineID)
	}
	if scope.filter.ProjectID != "" {
		conditions = append(conditions, column+` IN (SELECT service_id FROM project_services WHERE project_id = ?)`)
		args = append(args, scope.filter.ProjectID)
	}
	if len(conditions) == 0 {
		return "", nil
	}
	return ` WHERE ` + strings.Join(conditions, ` AND `), args
}

// attachServiceParts remplit les unités, les fournitures et les projets des
// services lus : trois lectures pour la liste entière, jamais trois par
// ligne.
func (db *DB) attachServiceParts(ctx context.Context, services []directory.Service, scope partsScope) error {
	position := make(map[string]int, len(services))
	for index, service := range services {
		position[service.ID] = index
	}
	units, err := db.listUnits(ctx, scope)
	if err != nil {
		return err
	}
	for _, unit := range units {
		if index, ok := position[unit.ServiceID]; ok {
			services[index].Units = append(services[index].Units, unit)
		}
	}
	provides, err := db.listProvides(ctx, scope)
	if err != nil {
		return err
	}
	for _, link := range provides {
		if index, ok := position[link.owner]; ok {
			services[index].Provides = append(services[index].Provides, directory.Capability(link.member))
		}
	}
	projects, err := db.listProjectsOfServices(ctx, scope)
	if err != nil {
		return err
	}
	for _, link := range projects {
		if index, ok := position[link.owner]; ok {
			services[index].ProjectIDs = append(services[index].ProjectIDs, link.member)
		}
	}
	return nil
}

type serviceLink struct {
	owner  string
	member string
}

func (db *DB) listProvides(ctx context.Context, scope partsScope) ([]serviceLink, error) {
	where, args := scope.where(`service_id`)
	return db.listServiceLinks(ctx,
		`SELECT service_id, capability FROM service_provides`+where+` ORDER BY service_id, capability`, args...)
}

func (db *DB) listProjectsOfServices(ctx context.Context, scope partsScope) ([]serviceLink, error) {
	where, args := scope.where(`ps.service_id`)
	return db.listServiceLinks(ctx,
		`SELECT ps.service_id, ps.project_id FROM project_services ps JOIN projects p ON p.id = ps.project_id`+
			where+` ORDER BY ps.service_id, p.name, p.id`, args...)
}

func (db *DB) listServiceLinks(ctx context.Context, query string, args ...any) ([]serviceLink, error) {
	rows, err := db.sql.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list service links: %w", err)
	}
	return collect(rows, scanServiceLink)
}

func scanServiceLink(row scanner) (serviceLink, error) {
	var link serviceLink
	if err := row.Scan(&link.owner, &link.member); err != nil {
		return serviceLink{}, err
	}
	return link, nil
}

// DeleteDeclaredService efface le service ; ses unités, ses fournitures et
// ses liens de projet partent avec lui, les projets restent, ses conteneurs
// redeviennent non déclarés. Des sondes ou des composants de statut qui le
// visent le retiennent : le plan est relu et la suppression faite dans la
// même transaction, pour qu'un lien posé entre les deux ne passe pas. La clé
// étrangère en restrict reste le filet.
func (db *DB) DeleteDeclaredService(ctx context.Context, id string) error {
	return db.inTx(ctx, "delete declared service", func(tx *sql.Tx) error {
		var found int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM declared_services WHERE id = ?`, id).Scan(&found); err != nil {
			return fmt.Errorf("find declared service: %w", err)
		}
		if found == 0 {
			return directory.ErrNotFound
		}
		plan, err := declaredServiceDeletionPlan(ctx, tx, id)
		if err != nil {
			return err
		}
		if err := directory.RefuseServiceDeletion(plan); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM declared_services WHERE id = ?`, id); err != nil {
			return fmt.Errorf("delete declared service: %w", err)
		}
		return nil
	})
}

// MarkServiceReviewed baisse « à vérifier » : l'opérateur a relu ce que la
// reprise de l'existant avait créé.
func (db *DB) MarkServiceReviewed(ctx context.Context, id string, at time.Time) error {
	result, err := db.sql.ExecContext(ctx,
		`UPDATE declared_services SET needs_review = 0, updated_at = ? WHERE id = ?`, at.Unix(), id)
	if err != nil {
		return fmt.Errorf("mark declared service reviewed: %w", err)
	}
	return notFoundIfNoRow(result, directory.ErrNotFound)
}
