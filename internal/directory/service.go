package directory

import (
	"context"
	"fmt"
	"log/slog"
	"time"
)

// Ce que l'annuaire attend de la base ; le paquet store le fournit. Chaque
// écriture est une transaction, et rend l'erreur précise du refus.
type Store interface {
	InsertDeclaredService(ctx context.Context, service Service) error
	// UpdateDeclaredService réécrit la définition entière : unités,
	// fournitures et projets. Une unité qui garde son identifiant reste la
	// même ; celles qui manquent sont supprimées.
	UpdateDeclaredService(ctx context.Context, service Service) error
	GetDeclaredService(ctx context.Context, id string) (Service, error)
	ListDeclaredServices(ctx context.Context, filter Filter) ([]Service, error)
	DeleteDeclaredService(ctx context.Context, id string) error
	InsertProject(ctx context.Context, project Project) error
	UpdateProject(ctx context.Context, project Project) error
	GetProject(ctx context.Context, id string) (Project, error)
	ListProjects(ctx context.Context) ([]Project, error)
	DeleteProject(ctx context.Context, id string) error
}

// Watcher reçoit chaque changement visible : le direct s'y branche. nil
// est toléré.
type Watcher interface {
	DirectoryChanged()
}

type Directory struct {
	store   Store
	watcher Watcher
	logger  *slog.Logger
	now     func() time.Time
}

func New(store Store, logger *slog.Logger) *Directory {
	return &Directory{store: store, logger: logger, now: time.Now}
}

func (d *Directory) SetClock(now func() time.Time) {
	d.now = now
}

func (d *Directory) SetWatcher(watcher Watcher) {
	d.watcher = watcher
}

func (d *Directory) changed() {
	if d.watcher != nil {
		d.watcher.DirectoryChanged()
	}
}

func (d *Directory) ListServices(ctx context.Context, filter Filter) ([]Service, error) {
	return d.store.ListDeclaredServices(ctx, filter)
}

func (d *Directory) GetService(ctx context.Context, id string) (Service, error) {
	return d.store.GetDeclaredService(ctx, id)
}

// CreateService déclare un service. Il naît de l'opérateur et sans rien à
// vérifier ; les identifiants, ceux des unités comprises, sont tirés ici :
// un identifiant d'unité donné par l'appelant n'a pas de sens avant que le
// service existe, il est ignoré.
func (d *Directory) CreateService(ctx context.Context, definition ServiceDefinition) (Service, error) {
	definition = definition.Complete()
	if err := definition.Validate(); err != nil {
		return Service{}, err
	}
	id, err := NewID(ServicePrefix)
	if err != nil {
		return Service{}, fmt.Errorf("generate service id: %w", err)
	}
	units, err := freshUnits(id, definition)
	if err != nil {
		return Service{}, err
	}
	now := d.now()
	created := serviceOf(id, definition, units)
	created.DeclaredBy = ByOperator
	created.CreatedAt = now
	created.UpdatedAt = now
	if err := d.store.InsertDeclaredService(ctx, created); err != nil {
		return Service{}, err
	}
	d.logger.Info("declared service created", "service_id", id, "name", created.Name, "machine_id", created.MachineID)
	d.changed()
	return d.store.GetDeclaredService(ctx, id)
}

// UpdateService remplace la définition, sur place : l'identifiant, la
// provenance, « à vérifier » et la date de création restent. Changer de
// machine est permis ; une unité absente de la définition est supprimée.
func (d *Directory) UpdateService(ctx context.Context, id string, definition ServiceDefinition) (Service, error) {
	before, err := d.store.GetDeclaredService(ctx, id)
	if err != nil {
		return Service{}, err
	}
	definition = definition.Complete()
	if err := definition.Validate(); err != nil {
		return Service{}, err
	}
	units, err := keptUnits(id, definition, before.Units)
	if err != nil {
		return Service{}, err
	}
	updated := serviceOf(id, definition, units)
	updated.DeclaredBy = before.DeclaredBy
	updated.NeedsReview = before.NeedsReview
	updated.CreatedAt = before.CreatedAt
	updated.UpdatedAt = d.now()
	if err := d.store.UpdateDeclaredService(ctx, updated); err != nil {
		return Service{}, err
	}
	d.logger.Info("declared service updated", "service_id", id, "name", updated.Name, "machine_id", updated.MachineID)
	d.changed()
	return d.store.GetDeclaredService(ctx, id)
}

func serviceOf(id string, definition ServiceDefinition, units []Unit) Service {
	return Service{
		ID:             id,
		MachineID:      definition.MachineID,
		Name:           definition.Name,
		Title:          definition.Title,
		Nature:         definition.Nature,
		Origin:         definition.Origin,
		ComposeProject: definition.ComposeProject,
		PublicURL:      definition.PublicURL,
		Provides:       definition.Provides,
		Units:          units,
		ProjectIDs:     definition.ProjectIDs,
	}
}

// freshUnits donne un identifiant neuf à chaque unité, y compris à celle
// qui en porte un : à la création il n'y a encore rien à garder.
func freshUnits(serviceID string, definition ServiceDefinition) ([]Unit, error) {
	units := make([]Unit, 0, len(definition.Units))
	for _, unit := range definition.Units {
		id, err := NewID(UnitPrefix)
		if err != nil {
			return nil, fmt.Errorf("generate unit id: %w", err)
		}
		units = append(units, unitOf(id, serviceID, unit))
	}
	return units, nil
}

// keptUnits garde l'identifiant d'une unité qui en porte un ; les nouvelles
// restent sans identifiant, le magasin leur en tire un en écrivant. Un
// identifiant qui n'est pas celui d'une unité du service, ou donné deux
// fois, est refusé : l'appelant ne choisit pas les identifiants.
func keptUnits(serviceID string, definition ServiceDefinition, existing []Unit) ([]Unit, error) {
	units := make([]Unit, 0, len(definition.Units))
	seen := make(map[string]bool, len(definition.Units))
	for _, unit := range definition.Units {
		if unit.ID != "" {
			if !hasUnit(existing, unit.ID) || seen[unit.ID] {
				return nil, ErrUnitInvalid
			}
			seen[unit.ID] = true
		}
		units = append(units, unitOf(unit.ID, serviceID, unit))
	}
	return units, nil
}

func hasUnit(units []Unit, id string) bool {
	for _, unit := range units {
		if unit.ID == id {
			return true
		}
	}
	return false
}

func unitOf(id, serviceID string, definition UnitDefinition) Unit {
	return Unit{
		ID:            id,
		ServiceID:     serviceID,
		Name:          definition.Name,
		Kind:          definition.Kind,
		Image:         definition.Image,
		Version:       definition.Version,
		Digest:        definition.Digest,
		ContainerName: definition.ContainerName,
		SystemdUnit:   definition.SystemdUnit,
		Ports:         definition.Ports,
	}
}

func (d *Directory) DeleteService(ctx context.Context, id string) error {
	found, err := d.store.GetDeclaredService(ctx, id)
	if err != nil {
		return err
	}
	if err := d.store.DeleteDeclaredService(ctx, id); err != nil {
		return err
	}
	d.logger.Info("declared service deleted", "service_id", id, "name", found.Name)
	d.changed()
	return nil
}

func (d *Directory) ListProjects(ctx context.Context) ([]Project, error) {
	return d.store.ListProjects(ctx)
}

func (d *Directory) GetProject(ctx context.Context, id string) (Project, error) {
	return d.store.GetProject(ctx, id)
}

func (d *Directory) CreateProject(ctx context.Context, definition ProjectDefinition) (Project, error) {
	definition = definition.Complete()
	if err := definition.Validate(); err != nil {
		return Project{}, err
	}
	id, err := NewID(ProjectPrefix)
	if err != nil {
		return Project{}, fmt.Errorf("generate project id: %w", err)
	}
	now := d.now()
	created := Project{ID: id, Name: definition.Name, Title: definition.Title, CreatedAt: now, UpdatedAt: now}
	if err := d.store.InsertProject(ctx, created); err != nil {
		return Project{}, err
	}
	d.logger.Info("project created", "project_id", id, "name", created.Name)
	d.changed()
	return d.store.GetProject(ctx, id)
}

func (d *Directory) UpdateProject(ctx context.Context, id string, definition ProjectDefinition) (Project, error) {
	before, err := d.store.GetProject(ctx, id)
	if err != nil {
		return Project{}, err
	}
	definition = definition.Complete()
	if err := definition.Validate(); err != nil {
		return Project{}, err
	}
	before.Name = definition.Name
	before.Title = definition.Title
	before.UpdatedAt = d.now()
	if err := d.store.UpdateProject(ctx, before); err != nil {
		return Project{}, err
	}
	d.logger.Info("project updated", "project_id", id, "name", before.Name)
	d.changed()
	return d.store.GetProject(ctx, id)
}

// DeleteProject défait le groupe et rien d'autre : les services qu'il
// regroupait restent dans l'annuaire.
func (d *Directory) DeleteProject(ctx context.Context, id string) error {
	found, err := d.store.GetProject(ctx, id)
	if err != nil {
		return err
	}
	if err := d.store.DeleteProject(ctx, id); err != nil {
		return err
	}
	d.logger.Info("project deleted", "project_id", id, "name", found.Name, "services", len(found.ServiceIDs))
	d.changed()
	return nil
}
