package directory

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/ldesfontaine/opencloud/internal/container"
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
	// DeleteDeclaredService compte ce qui vise le service et le supprime dans
	// la même transaction : si des sondes ou des composants le visent encore,
	// rien n'est supprimé et l'erreur est un *LinkedError.
	DeleteDeclaredService(ctx context.Context, id string) error
	// ListServiceLinks rend les sondes et les composants de statut qui
	// visent le service.
	ListServiceLinks(ctx context.Context, serviceID string) (Links, error)
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
	ListProjects(ctx context.Context) ([]Project, error)
	DeleteProject(ctx context.Context, id string) error
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

// DeleteService retire le service. Des sondes ou des composants de statut
// qui le visent encore le retiennent : l'erreur dit lesquels, et rien n'est
// supprimé en silence.
func (d *Directory) DeleteService(ctx context.Context, id string) error {
	found, err := d.store.GetDeclaredService(ctx, id)
	if err != nil {
		return err
	}
	if err := d.store.DeleteDeclaredService(ctx, id); err != nil {
		var linked *LinkedError
		if errors.As(err, &linked) {
			d.logger.Info("declared service deletion refused", "service_id", id,
				"probes", len(linked.Links.Probes), "components", len(linked.Links.Components))
		}
		return err
	}
	d.logger.Info("declared service deleted", "service_id", id, "name", found.Name)
	d.settle(ctx, found.MachineID)
	d.changed()
	return nil
}

// ServiceLinks dit ce qui vise le service : ses sondes et les composants de
// statut où il figure.
func (d *Directory) ServiceLinks(ctx context.Context, id string) (Links, error) {
	return d.store.ListServiceLinks(ctx, id)
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
	definition := definitionOf(found).Complete()
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

// Gaps compare ce qui est déclaré à ce que les machines montrent. Avec un
// projet, les conteneurs non déclarés ne sont pas dits : ils n'ont pas de
// projet.
func (d *Directory) Gaps(ctx context.Context, filter Filter) (GapReport, error) {
	declared, err := d.store.ListDeclaredServices(ctx, filter)
	if err != nil {
		return GapReport{}, err
	}
	containers, checks, err := d.observed(ctx, filter.MachineID)
	if err != nil {
		return GapReport{}, err
	}
	ignored, err := d.store.ListIgnoredKeys(ctx, filter.MachineID)
	if err != nil {
		return GapReport{}, err
	}
	report := Gaps(declared, containers, checks, ignored)
	if filter.ProjectID != "" {
		report.Undeclared = []Undeclared{}
	}
	return report, nil
}

// observed lit les conteneurs vivants et les constats d'images ; sans
// source branchée, la machine ne montre rien.
func (d *Directory) observed(ctx context.Context, machineID string) ([]container.Container, map[string]update.Check, error) {
	if d.containers == nil || d.checks == nil {
		return nil, nil, nil
	}
	containers, err := d.containers.List(ctx, machineID)
	if err != nil {
		return nil, nil, err
	}
	checks, err := d.checks.Checks(ctx, machineID)
	if err != nil {
		return nil, nil, err
	}
	return containers, checks, nil
}

// Propose tire de la fiche d'un conteneur non déclaré la définition à
// relire avant d'adopter. Un conteneur inconnu ou détruit est introuvable.
func (d *Directory) Propose(ctx context.Context, containerID string) (Proposal, error) {
	if d.containers == nil {
		return Proposal{}, ErrContainerNotFound
	}
	item, err := d.containers.Get(ctx, containerID)
	if errors.Is(err, container.ErrNotFound) {
		return Proposal{}, ErrContainerNotFound
	}
	if err != nil {
		return Proposal{}, err
	}
	if item.IsArchived() {
		return Proposal{}, ErrContainerNotFound
	}
	siblings, err := d.containers.List(ctx, item.MachineID)
	if err != nil {
		return Proposal{}, err
	}
	declared, err := d.store.ListDeclaredServices(ctx, Filter{MachineID: item.MachineID})
	if err != nil {
		return Proposal{}, err
	}
	return Propose(item, siblings, declared)
}

// Ignore dit que ce qui est présent sans être déclaré, c'est voulu. Le dire
// deux fois est sans effet. Cela ne change aucun rattachement.
func (d *Directory) Ignore(ctx context.Context, key IgnoredKey) error {
	key = key.Complete()
	if err := key.Validate(); err != nil {
		return err
	}
	key.CreatedAt = d.now()
	if err := d.store.InsertIgnoredKey(ctx, key); err != nil {
		return err
	}
	d.logger.Info("key ignored", "machine_id", key.MachineID, "kind", key.Kind, "value", key.Value)
	d.changed()
	return nil
}

// IgnoredKeys rend toutes les clés ignorées, par machine, genre et valeur.
func (d *Directory) IgnoredKeys(ctx context.Context) ([]IgnoredKey, error) {
	return d.store.ListIgnoredKeys(ctx, "")
}

func (d *Directory) Unignore(ctx context.Context, key IgnoredKey) error {
	key = key.Complete()
	if err := d.store.DeleteIgnoredKey(ctx, key); err != nil {
		return err
	}
	d.logger.Info("key no longer ignored", "machine_id", key.MachineID, "kind", key.Kind, "value", key.Value)
	d.changed()
	return nil
}

// ContainerLink est ce que la fiche d'un conteneur dit de l'annuaire :
// l'unité à laquelle il se rattache, ou, sans elle, la clé par laquelle on
// l'ignorerait.
type ContainerLink struct {
	ServiceID   string
	ServiceName string
	UnitID      string
	UnitName    string
	Level       container.MatchLevel
	Key         IgnoredKey
	Ignored     bool
}

func (d *Directory) ContainerLink(ctx context.Context, item container.Container) (ContainerLink, error) {
	link := ContainerLink{Key: keyOf(item)}
	if item.UnitID != "" {
		return d.linkToUnit(ctx, item, link)
	}
	ignored, err := d.store.ListIgnoredKeys(ctx, item.MachineID)
	if err != nil {
		return ContainerLink{}, err
	}
	link.Ignored = isIgnored(ignored, link.Key)
	return link, nil
}

// linkToUnit cherche le service qui porte l'unité sur toutes les machines :
// une fiche archivée garde son unité, et le service a pu changer de machine
// depuis.
func (d *Directory) linkToUnit(ctx context.Context, item container.Container, link ContainerLink) (ContainerLink, error) {
	declared, err := d.store.ListDeclaredServices(ctx, Filter{})
	if err != nil {
		return ContainerLink{}, err
	}
	for _, found := range declared {
		for _, unit := range found.Units {
			if unit.ID == item.UnitID {
				link.ServiceID, link.ServiceName = found.ID, found.Name
				link.UnitID, link.UnitName = unit.ID, unit.Name
				link.Level = item.MatchLevel
				return link, nil
			}
		}
	}
	return link, nil
}
