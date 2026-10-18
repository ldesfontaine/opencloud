package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/ldesfontaine/opencloud/internal/directory"
	"github.com/ldesfontaine/opencloud/internal/service"
)

const declaredServiceColumns = `s.id, s.machine_id, m.name, s.name, s.title, s.nature, s.origin,
	s.compose_project, s.public_url, s.declared_by, s.needs_review, s.created_at, s.updated_at`

const declaredServiceFrom = ` FROM declared_services s JOIN machines m ON m.id = s.machine_id`

const unitColumns = `id, service_id, name, kind, image, version, digest, container_name, systemd_unit, ports, update_policy`

// --- Services déclarés ---

// InsertDeclaredService écrit le service, ses unités, ses fournitures et ses
// liens de projet d'un coup. Les refus se lisent avant d'écrire : un index
// unique violé ne dirait pas lequel des noms est pris.
func (db *DB) InsertDeclaredService(ctx context.Context, service directory.Service) error {
	tx, err := db.sql.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin insert declared service: %w", err)
	}
	defer tx.Rollback()
	if err := checkServiceWrite(ctx, tx, service); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `
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
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit insert declared service: %w", err)
	}
	return nil
}

// UpdateDeclaredService réécrit la définition. Les unités dont l'identifiant
// est gardé sont modifiées sur place, les autres supprimées ou créées ; les
// fournitures et les liens sont remplacés, pas rapprochés. La provenance et
// « à vérifier » ne se réécrivent pas ici.
func (db *DB) UpdateDeclaredService(ctx context.Context, service directory.Service) error {
	tx, err := db.sql.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin update declared service: %w", err)
	}
	defer tx.Rollback()
	var found int
	err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM declared_services WHERE id = ?`, service.ID).Scan(&found)
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
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit update declared service: %w", err)
	}
	return nil
}

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
	defer rows.Close()
	services := []directory.Service{}
	for rows.Next() {
		service, err := scanDeclaredService(rows)
		if err != nil {
			return nil, err
		}
		services = append(services, service)
	}
	if err := rows.Err(); err != nil {
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

func (db *DB) listUnits(ctx context.Context, scope partsScope) ([]directory.Unit, error) {
	where, args := scope.where(`service_id`)
	query := `SELECT ` + unitColumns + ` FROM service_units` + where + ` ORDER BY service_id, position, id` // #nosec G202 -- des conditions fixes, les valeurs sont liées.
	rows, err := db.sql.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list units: %w", err)
	}
	defer rows.Close()
	units := []directory.Unit{}
	for rows.Next() {
		unit, err := scanUnit(rows)
		if err != nil {
			return nil, err
		}
		units = append(units, unit)
	}
	return units, rows.Err()
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
	unit.UpdatePolicy = service.UpdatePolicy(policy)
	unit.Ports = []string{}
	if err := json.Unmarshal([]byte(ports), &unit.Ports); err != nil {
		return directory.Unit{}, fmt.Errorf("decode unit ports: %w", err)
	}
	return unit, nil
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
	defer rows.Close()
	links := []serviceLink{}
	for rows.Next() {
		var link serviceLink
		if err := rows.Scan(&link.owner, &link.member); err != nil {
			return nil, err
		}
		links = append(links, link)
	}
	return links, rows.Err()
}

// DeleteDeclaredService efface le service ; ses unités, ses fournitures et
// ses liens de projet partent avec lui, les projets restent. Des sondes ou
// des composants de statut qui le visent le retiennent : les compter et
// supprimer se font dans la même transaction, pour qu'un lien posé entre les
// deux ne passe pas. La clé étrangère en restrict reste le filet.
func (db *DB) DeleteDeclaredService(ctx context.Context, id string) error {
	tx, err := db.sql.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin delete declared service: %w", err)
	}
	defer tx.Rollback()
	var found int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM declared_services WHERE id = ?`, id).Scan(&found); err != nil {
		return fmt.Errorf("find declared service: %w", err)
	}
	if found == 0 {
		return directory.ErrNotFound
	}
	links, err := listServiceLinks(ctx, tx, id)
	if err != nil {
		return err
	}
	if !links.IsEmpty() {
		return &directory.LinkedError{Links: links}
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM declared_services WHERE id = ?`, id); err != nil {
		return fmt.Errorf("delete declared service: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit delete declared service: %w", err)
	}
	return nil
}

// ListServiceLinks rend les sondes et les composants de statut qui visent le
// service, par nom.
func (db *DB) ListServiceLinks(ctx context.Context, serviceID string) (directory.Links, error) {
	return listServiceLinks(ctx, db.sql, serviceID)
}

// querier est ce que la base et une transaction ont en commun pour lire.
type querier interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
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
	defer rows.Close()
	objects := []directory.LinkedObject{}
	for rows.Next() {
		var object directory.LinkedObject
		if err := rows.Scan(&object.ID, &object.Name); err != nil {
			return nil, err
		}
		objects = append(objects, object)
	}
	return objects, rows.Err()
}

// MarkServiceReviewed baisse « à vérifier » : l'opérateur a relu ce que la
// reprise de l'existant avait créé.
func (db *DB) MarkServiceReviewed(ctx context.Context, id string, at time.Time) error {
	result, err := db.sql.ExecContext(ctx,
		`UPDATE declared_services SET needs_review = 0, updated_at = ? WHERE id = ?`, at.Unix(), id)
	if err != nil {
		return fmt.Errorf("mark declared service reviewed: %w", err)
	}
	return directoryNotFoundIfNoRow(result, directory.ErrNotFound)
}

func (db *DB) GetUnit(ctx context.Context, id string) (directory.Unit, error) {
	unit, err := scanUnit(db.sql.QueryRowContext(ctx, `SELECT `+unitColumns+` FROM service_units WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return directory.Unit{}, directory.ErrUnitNotFound
	}
	return unit, err
}

func directoryNotFoundIfNoRow(result sql.Result, notFound error) error {
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return notFound
	}
	return nil
}

// --- Projets ---

func (db *DB) InsertProject(ctx context.Context, project directory.Project) error {
	tx, err := db.sql.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin insert project: %w", err)
	}
	defer tx.Rollback()
	if err := checkProjectNameFree(ctx, tx, project); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx,
		`INSERT INTO projects (id, name, title, created_at, updated_at) VALUES (?, ?, ?, ?, ?)`,
		project.ID, project.Name, project.Title, project.CreatedAt.Unix(), project.UpdatedAt.Unix())
	if err != nil {
		return fmt.Errorf("insert project: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit insert project: %w", err)
	}
	return nil
}

// UpdateProject réécrit le nom et le titre ; les liens de services ne se
// règlent que depuis le service.
func (db *DB) UpdateProject(ctx context.Context, project directory.Project) error {
	tx, err := db.sql.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin update project: %w", err)
	}
	defer tx.Rollback()
	var found int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM projects WHERE id = ?`, project.ID).Scan(&found); err != nil {
		return fmt.Errorf("find project: %w", err)
	}
	if found == 0 {
		return directory.ErrProjectNotFound
	}
	if err := checkProjectNameFree(ctx, tx, project); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE projects SET name = ?, title = ?, updated_at = ? WHERE id = ?`,
		project.Name, project.Title, project.UpdatedAt.Unix(), project.ID)
	if err != nil {
		return fmt.Errorf("update project: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit update project: %w", err)
	}
	return nil
}

func checkProjectNameFree(ctx context.Context, tx *sql.Tx, project directory.Project) error {
	var others int
	err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM projects WHERE name = ? AND id != ?`,
		project.Name, project.ID).Scan(&others)
	if err != nil {
		return fmt.Errorf("check project name: %w", err)
	}
	if others > 0 {
		return directory.ErrProjectNameTaken
	}
	return nil
}

func (db *DB) GetProject(ctx context.Context, id string) (directory.Project, error) {
	row := db.sql.QueryRowContext(ctx, `SELECT id, name, title, created_at, updated_at FROM projects WHERE id = ?`, id)
	project, err := scanProject(row)
	if errors.Is(err, sql.ErrNoRows) {
		return directory.Project{}, directory.ErrProjectNotFound
	}
	if err != nil {
		return directory.Project{}, err
	}
	projects := []directory.Project{project}
	if err := db.attachProjectServices(ctx, projects, id); err != nil {
		return directory.Project{}, err
	}
	return projects[0], nil
}

func (db *DB) ListProjects(ctx context.Context) ([]directory.Project, error) {
	rows, err := db.sql.QueryContext(ctx, `SELECT id, name, title, created_at, updated_at FROM projects ORDER BY name, id`)
	if err != nil {
		return nil, fmt.Errorf("list projects: %w", err)
	}
	defer rows.Close()
	projects := []directory.Project{}
	for rows.Next() {
		project, err := scanProject(rows)
		if err != nil {
			return nil, err
		}
		projects = append(projects, project)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := db.attachProjectServices(ctx, projects, ""); err != nil {
		return nil, err
	}
	return projects, nil
}

func scanProject(row scanner) (directory.Project, error) {
	var project directory.Project
	var createdAt, updatedAt int64
	if err := row.Scan(&project.ID, &project.Name, &project.Title, &createdAt, &updatedAt); err != nil {
		return directory.Project{}, err
	}
	project.ServiceIDs = []string{}
	project.CreatedAt = time.Unix(createdAt, 0)
	project.UpdatedAt = time.Unix(updatedAt, 0)
	return project, nil
}

// attachProjectServices remplit les services de chaque projet lu, par nom
// de service ; projectID vide les lit pour tous.
func (db *DB) attachProjectServices(ctx context.Context, projects []directory.Project, projectID string) error {
	query := `SELECT ps.project_id, ps.service_id FROM project_services ps JOIN declared_services s ON s.id = ps.service_id`
	var args []any
	if projectID != "" {
		query += ` WHERE ps.project_id = ?`
		args = append(args, projectID)
	}
	links, err := db.listServiceLinks(ctx, query+` ORDER BY ps.project_id, s.name, s.machine_id, s.id`, args...)
	if err != nil {
		return err
	}
	position := make(map[string]int, len(projects))
	for index, project := range projects {
		position[project.ID] = index
	}
	for _, link := range links {
		if index, ok := position[link.owner]; ok {
			projects[index].ServiceIDs = append(projects[index].ServiceIDs, link.member)
		}
	}
	return nil
}

// DeleteProject défait le groupe : ses liens partent, ses services restent.
func (db *DB) DeleteProject(ctx context.Context, id string) error {
	result, err := db.sql.ExecContext(ctx, `DELETE FROM projects WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete project: %w", err)
	}
	return directoryNotFoundIfNoRow(result, directory.ErrProjectNotFound)
}

// --- Clés ignorées ---

// ListIgnoredKeys rend les clés ignorées d'une machine, de toutes si
// machineID est vide, par machine, genre et valeur.
func (db *DB) ListIgnoredKeys(ctx context.Context, machineID string) ([]directory.IgnoredKey, error) {
	query := `SELECT machine_id, kind, value, created_at FROM ignored_keys`
	var args []any
	if machineID != "" {
		query += ` WHERE machine_id = ?`
		args = append(args, machineID)
	}
	rows, err := db.sql.QueryContext(ctx, query+` ORDER BY machine_id, kind, value`, args...)
	if err != nil {
		return nil, fmt.Errorf("list ignored keys: %w", err)
	}
	defer rows.Close()
	keys := []directory.IgnoredKey{}
	for rows.Next() {
		var key directory.IgnoredKey
		var kind string
		var createdAt int64
		if err := rows.Scan(&key.MachineID, &kind, &key.Value, &createdAt); err != nil {
			return nil, err
		}
		key.Kind = directory.IgnoreKind(kind)
		key.CreatedAt = time.Unix(createdAt, 0)
		keys = append(keys, key)
	}
	return keys, rows.Err()
}

// InsertIgnoredKey est idempotent : dire deux fois qu'une clé est ignorée
// garde la première date. Lire la machine avant d'écrire donne le refus
// précis, là où une clé étrangère violée ne dirait rien.
func (db *DB) InsertIgnoredKey(ctx context.Context, key directory.IgnoredKey) error {
	tx, err := db.sql.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin insert ignored key: %w", err)
	}
	defer tx.Rollback()
	var machines int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM machines WHERE id = ?`, key.MachineID).Scan(&machines); err != nil {
		return fmt.Errorf("find ignored key machine: %w", err)
	}
	if machines == 0 {
		return directory.ErrMachineInvalid
	}
	_, err = tx.ExecContext(ctx,
		`INSERT OR IGNORE INTO ignored_keys (machine_id, kind, value, created_at) VALUES (?, ?, ?, ?)`,
		key.MachineID, string(key.Kind), key.Value, key.CreatedAt.Unix())
	if err != nil {
		return fmt.Errorf("insert ignored key: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit insert ignored key: %w", err)
	}
	return nil
}

func (db *DB) DeleteIgnoredKey(ctx context.Context, key directory.IgnoredKey) error {
	result, err := db.sql.ExecContext(ctx,
		`DELETE FROM ignored_keys WHERE machine_id = ? AND kind = ? AND value = ?`,
		key.MachineID, string(key.Kind), key.Value)
	if err != nil {
		return fmt.Errorf("delete ignored key: %w", err)
	}
	return directoryNotFoundIfNoRow(result, directory.ErrIgnoredNotFound)
}
