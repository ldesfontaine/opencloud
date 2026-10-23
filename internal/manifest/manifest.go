package manifest

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/ldesfontaine/opencloud/internal/deletion"
	"github.com/ldesfontaine/opencloud/internal/directory"
	"github.com/ldesfontaine/opencloud/internal/heartbeat"
	"github.com/ldesfontaine/opencloud/internal/probe"
	"github.com/ldesfontaine/opencloud/internal/reader"
)

// Ce que le fichier écrit par les gestes des composants du domaine, pour
// que les agents reçoivent leurs sondes, que le rapprochement se refasse et
// que les alertes suivent. Ce qu'il lit (services, projets, sondes, tâches,
// clés ignorées, noms des machines) passe par le reader.Reader : aucune règle en lecture. Les propositions, qui
// dérivent, et le plan d'une suppression, qui porte la règle du domaine,
// restent des lectures des composants.
type Directory interface {
	CreateProject(ctx context.Context, definition directory.ProjectDefinition) (directory.Project, error)
	UpdateProject(ctx context.Context, id string, definition directory.ProjectDefinition) (directory.Project, error)
	DeleteProject(ctx context.Context, id string) error
	// DeleteProjectPlan dit si une page de statut retient le projet.
	DeleteProjectPlan(ctx context.Context, id string) (deletion.Plan, error)
	CreateService(ctx context.Context, definition directory.ServiceDefinition) (directory.Service, error)
	UpdateService(ctx context.Context, id string, definition directory.ServiceDefinition) (directory.Service, error)
	DeleteService(ctx context.Context, id string) error
	// DeleteServicePlan, comme DeletePlan des sondes et des tâches, est le
	// plan que le geste de suppression rejoue : le fichier dit ce qu'il dit.
	DeleteServicePlan(ctx context.Context, id string) (deletion.Plan, error)
	// Proposals rend ce que l'export propose d'ajouter, depuis les conteneurs
	// que les machines montrent sans que rien ne les déclare. Rien n'est écrit.
	Proposals(ctx context.Context) ([]directory.Proposal, error)
	Ignore(ctx context.Context, key directory.IgnoredKey) error
	Unignore(ctx context.Context, key directory.IgnoredKey) error
}

type Probes interface {
	Create(ctx context.Context, definition probe.Definition) (probe.Probe, error)
	Update(ctx context.Context, id string, definition probe.Definition) (probe.Probe, error)
	Delete(ctx context.Context, id string) error
	DeletePlan(ctx context.Context, id string) (deletion.Plan, error)
}

type Heartbeats interface {
	Create(ctx context.Context, definition heartbeat.Definition) (heartbeat.Heartbeat, error)
	Update(ctx context.Context, id string, definition heartbeat.Definition) (heartbeat.Heartbeat, error)
	Delete(ctx context.Context, id string) error
	DeletePlan(ctx context.Context, id string) (deletion.Plan, error)
}

// Watcher est prévenu quand une application a touché l'annuaire : le direct
// s'y branche. nil est toléré.
type Watcher interface {
	DirectoryChanged()
}

type Dependencies struct {
	Directory  Directory
	Probes     Probes
	Heartbeats Heartbeats
	Reader     reader.Reader
	Journal    Journal
}

// Manifest est le composant du fichier de l'annuaire.
type Manifest struct {
	deps   Dependencies
	logger *slog.Logger
	now    func() time.Time

	watcher Watcher
	// lifetime est la vie du serveur : une fois finie, une application en
	// cours s'arrête entre deux changements et se journalise « interrompue ».
	lifetime context.Context
	// applying garde un seul apply à la fois. TryLock : le second répond
	// « occupé » plutôt que d'attendre derrière une requête qui dure.
	applying sync.Mutex
}

func New(deps Dependencies, logger *slog.Logger) *Manifest {
	return &Manifest{
		deps: deps, logger: logger, now: time.Now,
		lifetime: context.Background(),
	}
}

func (m *Manifest) SetClock(now func() time.Time) {
	m.now = now
}

func (m *Manifest) SetWatcher(watcher Watcher) {
	m.watcher = watcher
}

// SetLifetime donne la vie du serveur : l'arrêt du processus ne coupe jamais
// un changement en cours, il empêche seulement le suivant.
func (m *Manifest) SetLifetime(lifetime context.Context) {
	m.lifetime = lifetime
}
