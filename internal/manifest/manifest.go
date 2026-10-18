package manifest

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/ldesfontaine/opencloud/internal/directory"
	"github.com/ldesfontaine/opencloud/internal/heartbeat"
	"github.com/ldesfontaine/opencloud/internal/probe"
)

// Ce que le fichier lit de l'annuaire et écrit par ses gestes. Chaque
// interface est celle d'un composant du domaine : l'export passe par leurs
// lectures, jamais par la base, et l'application par les mêmes gestes que
// l'interface, pour que les agents reçoivent leurs sondes, que le
// rapprochement se refasse et que les alertes suivent.
type Directory interface {
	ListProjects(ctx context.Context) ([]directory.Project, error)
	CreateProject(ctx context.Context, definition directory.ProjectDefinition) (directory.Project, error)
	UpdateProject(ctx context.Context, id string, definition directory.ProjectDefinition) (directory.Project, error)
	DeleteProject(ctx context.Context, id string) error
	ListServices(ctx context.Context, filter directory.Filter) ([]directory.Service, error)
	GetService(ctx context.Context, id string) (directory.Service, error)
	CreateService(ctx context.Context, definition directory.ServiceDefinition) (directory.Service, error)
	UpdateService(ctx context.Context, id string, definition directory.ServiceDefinition) (directory.Service, error)
	DeleteService(ctx context.Context, id string) error
	// ServiceLinks dit ce qui vise un service : ses sondes et les composants
	// de statut où il figure.
	ServiceLinks(ctx context.Context, id string) (directory.Links, error)
	IgnoredKeys(ctx context.Context) ([]directory.IgnoredKey, error)
	Ignore(ctx context.Context, key directory.IgnoredKey) error
	Unignore(ctx context.Context, key directory.IgnoredKey) error
}

type Probes interface {
	// List rend les sondes de toutes les machines quand machineID est vide.
	List(ctx context.Context, machineID string) ([]probe.Probe, error)
	Get(ctx context.Context, id string) (probe.Probe, error)
	Create(ctx context.Context, definition probe.Definition) (probe.Probe, error)
	Update(ctx context.Context, id string, definition probe.Definition) (probe.Probe, error)
	Delete(ctx context.Context, id string) error
}

type Heartbeats interface {
	List(ctx context.Context) ([]heartbeat.Heartbeat, error)
	Get(ctx context.Context, id string) (heartbeat.Heartbeat, error)
	Create(ctx context.Context, definition heartbeat.Definition) (heartbeat.Heartbeat, error)
	Update(ctx context.Context, id string, definition heartbeat.Definition) (heartbeat.Heartbeat, error)
	Delete(ctx context.Context, id string) error
}

// Machines rend le nom de chaque machine, par identifiant.
type Machines interface {
	MachineNames(ctx context.Context) (map[string]string, error)
}

// History compte ce qu'une suppression emporte : l'historique d'une sonde
// ou d'une tâche, que le plan annonce avant qu'on confirme.
type History interface {
	CountProbeHistory(ctx context.Context, probeID string) (results, days int, err error)
	CountHeartbeatHistory(ctx context.Context, heartbeatID string) (pings, runs int, err error)
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
	Machines   Machines
	History    History
	Journal    Journal
}

// Manifest est le composant du fichier de l'annuaire.
type Manifest struct {
	deps   Dependencies
	logger *slog.Logger
	now    func() time.Time

	watcher  Watcher
	errorKey ErrorKeyFunc
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
		errorKey: func(error) string { return "" },
		lifetime: context.Background(),
	}
}

func (m *Manifest) SetClock(now func() time.Time) {
	m.now = now
}

func (m *Manifest) SetWatcher(watcher Watcher) {
	m.watcher = watcher
}

// SetErrorKeys donne la traduction d'une erreur du domaine en clé du
// catalogue : manifest ne connaît pas le serveur, qui a les clés.
func (m *Manifest) SetErrorKeys(errorKey ErrorKeyFunc) {
	m.errorKey = errorKey
}

// SetLifetime donne la vie du serveur : l'arrêt du processus ne coupe jamais
// un changement en cours, il empêche seulement le suivant.
func (m *Manifest) SetLifetime(lifetime context.Context) {
	m.lifetime = lifetime
}
