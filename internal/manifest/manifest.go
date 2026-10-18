package manifest

import (
	"context"
	"log/slog"

	"github.com/ldesfontaine/opencloud/internal/directory"
	"github.com/ldesfontaine/opencloud/internal/heartbeat"
	"github.com/ldesfontaine/opencloud/internal/probe"
)

// Ce que le fichier lit de l'annuaire. Chaque interface est celle d'un
// composant du domaine : l'export passe par leurs lectures, jamais par la
// base.
type Directory interface {
	ListProjects(ctx context.Context) ([]directory.Project, error)
	ListServices(ctx context.Context, filter directory.Filter) ([]directory.Service, error)
	IgnoredKeys(ctx context.Context) ([]directory.IgnoredKey, error)
}

type Probes interface {
	// List rend les sondes de toutes les machines quand machineID est vide.
	List(ctx context.Context, machineID string) ([]probe.Probe, error)
}

type Heartbeats interface {
	List(ctx context.Context) ([]heartbeat.Heartbeat, error)
}

// Machines rend le nom de chaque machine, par identifiant.
type Machines interface {
	MachineNames(ctx context.Context) (map[string]string, error)
}

type Dependencies struct {
	Directory  Directory
	Probes     Probes
	Heartbeats Heartbeats
	Machines   Machines
}

// Manifest est le composant du fichier de l'annuaire.
type Manifest struct {
	deps   Dependencies
	logger *slog.Logger
}

func New(deps Dependencies, logger *slog.Logger) *Manifest {
	return &Manifest{deps: deps, logger: logger}
}
