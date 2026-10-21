package directory

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/ldesfontaine/opencloud/internal/container"
	"github.com/ldesfontaine/opencloud/internal/deletion"
	"github.com/ldesfontaine/opencloud/internal/update"
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
	// DeleteDeclaredService relit le plan du service et le supprime dans la
	// même transaction : si des sondes ou des composants le visent encore,
	// rien n'est supprimé et l'erreur est un *deletion.RefusedError.
	DeleteDeclaredService(ctx context.Context, id string) error
	DeclaredServiceDeletionPlan(ctx context.Context, id string) (deletion.Plan, error)
	// MarkServiceReviewed baisse « à vérifier » et date la validation.
	MarkServiceReviewed(ctx context.Context, id string, at time.Time) error
	// GetUnit rend une unité, ErrUnitNotFound si elle n'existe plus.
	GetUnit(ctx context.Context, id string) (Unit, error)
	// SetUnitUpdatePolicy écrit la politique de l'unité et rien d'autre ;
	// ErrUnitNotFound si elle n'existe plus.
	SetUnitUpdatePolicy(ctx context.Context, unitID string, policy container.UpdatePolicy) error
	InsertProject(ctx context.Context, project Project) error
	UpdateProject(ctx context.Context, project Project) error
	GetProject(ctx context.Context, id string) (Project, error)
	DeleteProject(ctx context.Context, id string) error
	ProjectDeletionPlan(ctx context.Context, id string) (deletion.Plan, error)
	// ListIgnoredKeys rend les clés ignorées d'une machine, de toutes si
	// machineID est vide, par machine, genre et valeur.
	ListIgnoredKeys(ctx context.Context, machineID string) ([]IgnoredKey, error)
	// InsertIgnoredKey est sans effet si la clé y est déjà ; une machine
	// inconnue est refusée par ErrMachineInvalid.
	InsertIgnoredKey(ctx context.Context, key IgnoredKey) error
	DeleteIgnoredKey(ctx context.Context, key IgnoredKey) error
}

// Containers est ce que l'annuaire lit des conteneurs observés : les
// vivants d'une machine (de toutes si machineID est vide), et une fiche par
// son identifiant, archivée comprise.
type Containers interface {
	List(ctx context.Context, machineID string) ([]container.Container, error)
	Get(ctx context.Context, id string) (container.Container, error)
}

// ImageChecks est ce que l'annuaire lit des constats sur les images, dont
// l'empreinte tirée localement.
type ImageChecks interface {
	Checks(ctx context.Context, machineID string) (map[string]update.Check, error)
}

// Rematcher refait le rattachement des conteneurs d'une machine : il se
// lance après chaque écriture de l'annuaire. nil est toléré.
type Rematcher interface {
	Rematch(ctx context.Context, machineID string) error
}

// Assigner redit à une machine les images qu'elle ne doit plus interroger :
// c'est la politique des unités qui les désigne. nil est toléré.
type Assigner interface {
	Assign(ctx context.Context, machineID string)
}

// Watcher reçoit chaque changement visible : le direct s'y branche. nil
// est toléré.
type Watcher interface {
	DirectoryChanged()
}

type Directory struct {
	store      Store
	watcher    Watcher
	containers Containers
	checks     ImageChecks
	rematcher  Rematcher
	assigner   Assigner
	logger     *slog.Logger
	now        func() time.Time
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

// SetObserved branche ce que la machine montre : sans lui, l'écart voit
// chaque service déclaré comme absent.
func (d *Directory) SetObserved(containers Containers, checks ImageChecks) {
	d.containers = containers
	d.checks = checks
}

func (d *Directory) SetRematcher(rematcher Rematcher) {
	d.rematcher = rematcher
}

func (d *Directory) SetAssigner(assigner Assigner) {
	d.assigner = assigner
}

func (d *Directory) changed() {
	if d.watcher != nil {
		d.watcher.DirectoryChanged()
	}
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
	d.settle(ctx, created.MachineID)
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
	d.settle(ctx, before.MachineID, updated.MachineID)
	d.changed()
	return d.store.GetDeclaredService(ctx, id)
}

// settle rattache de nouveau les conteneurs des machines dites, une fois
// l'écriture validée, puis leur redit les images à ne pas interroger : la
// règle lit l'annuaire, elle ne peut pas le faire pendant la transaction, et
// la politique d'un conteneur est celle de l'unité où il vient d'arriver ou
// dont il vient de partir. Un échec n'annule pas l'écriture, que l'opérateur
// a faite ; le rapport suivant de la machine rattrapera.
func (d *Directory) settle(ctx context.Context, machineIDs ...string) {
	for index, machineID := range machineIDs {
		if index > 0 && machineID == machineIDs[0] {
			continue
		}
		if d.rematcher != nil {
			if err := d.rematcher.Rematch(ctx, machineID); err != nil {
				d.logger.Warn("rematch containers", "machine", machineID, "error", err)
			}
		}
		if d.assigner != nil {
			d.assigner.Assign(ctx, machineID)
		}
	}
}

// MatchUnits applique la règle de rapprochement aux conteneurs d'une
// machine : c'est ce que le suivi des services appelle à chaque rapport.
func (d *Directory) MatchUnits(ctx context.Context, machineID string, containers []container.Container) (map[string]container.UnitMatch, error) {
	declared, err := d.store.ListDeclaredServices(ctx, Filter{MachineID: machineID})
	if err != nil {
		return nil, err
	}
	return Match(declared, containers), nil
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
		UpdatePolicy:  definition.UpdatePolicy,
	}
}

// DeleteServicePlan dit ce que DeleteService ferait : les sondes et les
// composants qui le retiennent, les unités qui partent, les liens de projet
// et les conteneurs qui se détachent. Rien n'est écrit.
func (d *Directory) DeleteServicePlan(ctx context.Context, id string) (deletion.Plan, error) {
	if _, err := d.store.GetDeclaredService(ctx, id); err != nil {
		return deletion.Plan{}, err
	}
	return d.store.DeclaredServiceDeletionPlan(ctx, id)
}

// DeleteService retire le service. Des sondes ou des composants de statut
// qui le visent encore le retiennent : le refus porte le plan, et rien n'est
// supprimé en silence.
func (d *Directory) DeleteService(ctx context.Context, id string) error {
	found, err := d.store.GetDeclaredService(ctx, id)
	if err != nil {
		return err
	}
	if err := d.store.DeleteDeclaredService(ctx, id); err != nil {
		var refused *deletion.RefusedError
		if errors.As(err, &refused) {
			d.logger.Info("declared service deletion refused", "service_id", id, "blockers", len(refused.Plan.Blockers))
		}
		return err
	}
	d.logger.Info("declared service deleted", "service_id", id, "name", found.Name)
	d.settle(ctx, found.MachineID)
	d.changed()
	return nil
}

// Review valide un service repris de l'existant : « à vérifier » tombe, mais
// seulement si sa définition passe la validation que l'opérateur subirait en
// la créant. Sinon on validerait une unité mal reprise. Le refus est celui de
// la première règle violée ; modifier le service ne le valide pas. Valider
// deux fois ne change rien.
func (d *Directory) Review(ctx context.Context, id string) (Service, error) {
	found, err := d.store.GetDeclaredService(ctx, id)
	if err != nil {
		return Service{}, err
	}
	if !found.NeedsReview {
		return found, nil
	}
	definition := found.Definition().Complete()
	if err := definition.Validate(); err != nil {
		return Service{}, err
	}
	if err := d.store.MarkServiceReviewed(ctx, id, d.now()); err != nil {
		return Service{}, err
	}
	d.logger.Info("declared service reviewed", "service_id", id, "name", found.Name)
	d.changed()
	return d.store.GetDeclaredService(ctx, id)
}

// SetUnitUpdatePolicy règle ce que l'opérateur veut des mises à jour d'une
// unité : tous ses conteneurs la suivent, ceux qui seront recréés aussi. Elle
// ne tire ni ne redémarre rien. La machine est prévenue des images qu'elle ne
// doit plus interroger.
func (d *Directory) SetUnitUpdatePolicy(ctx context.Context, unitID string, policy container.UpdatePolicy) (Service, error) {
	if !container.IsUpdatePolicy(policy) {
		return Service{}, ErrUpdatePolicyInvalid
	}
	unit, err := d.store.GetUnit(ctx, unitID)
	if err != nil {
		return Service{}, err
	}
	if unit.Kind == UnitBinary && policy != container.PolicyWatch {
		return Service{}, ErrUpdatePolicyInvalid
	}
	if err := d.store.SetUnitUpdatePolicy(ctx, unitID, policy); err != nil {
		return Service{}, err
	}
	owner, err := d.store.GetDeclaredService(ctx, unit.ServiceID)
	if err != nil {
		return Service{}, err
	}
	d.logger.Info("unit update policy set", "service_id", owner.ID, "unit_id", unitID, "policy", policy)
	if d.assigner != nil {
		d.assigner.Assign(ctx, owner.MachineID)
	}
	d.changed()
	return owner, nil
}
