package systemdunits

import (
	"context"
	"log/slog"
	"time"
)

// Snapshot est le dernier relevé d'une machine. ReportedAt est nul tant que
// son agent n'en a envoyé aucun (un agent plus ancien n'en envoie pas).
type Snapshot struct {
	MachineID   string
	MachineName string
	ReportedAt  time.Time
	Report
}

// Ce que le composant attend de la base ; le paquet store le fournit.
type Store interface {
	// ReplaceSystemdUnits remplace, en une transaction, tout ce que la machine avait.
	ReplaceSystemdUnits(ctx context.Context, machineID string, report Report, at time.Time) error
	// MachineSystemdUnits rend le relevé d'une machine ; machine.ErrNotFound si
	// elle n'existe pas.
	MachineSystemdUnits(ctx context.Context, machineID string) (Snapshot, error)
	// SystemdUnitsOfMachines rend le relevé de chaque machine, nommé, même sans relevé.
	SystemdUnitsOfMachines(ctx context.Context) ([]Snapshot, error)
}

// Watcher reçoit chaque relevé écrit : le direct s'y branche. nil est toléré.
type Watcher interface {
	SystemdUnitsChanged(machineID string)
}

type Service struct {
	store   Store
	watcher Watcher
	logger  *slog.Logger
	now     func() time.Time
}

func New(store Store, logger *slog.Logger) *Service {
	return &Service{store: store, logger: logger, now: time.Now}
}

func (s *Service) SetWatcher(watcher Watcher) {
	s.watcher = watcher
}

func (s *Service) SetClock(now func() time.Time) {
	s.now = now
}

// Record garde le relevé d'une machine à la place du précédent. Un rapport
// hors bornes est refusé en entier. Rien n'est journalisé à l'audit : c'est
// une lecture de l'agent, comme ses mesures.
func (s *Service) Record(ctx context.Context, machineID string, report Report) error {
	if err := report.Validate(); err != nil {
		return err
	}
	if err := s.store.ReplaceSystemdUnits(ctx, machineID, report, s.now()); err != nil {
		return err
	}
	if s.watcher != nil {
		s.watcher.SystemdUnitsChanged(machineID)
	}
	return nil
}

// Machine rend ce que l'agent d'une machine a relevé.
func (s *Service) Machine(ctx context.Context, machineID string) (Snapshot, error) {
	return s.store.MachineSystemdUnits(ctx, machineID)
}

// All rend le relevé de toutes les machines : l'annuaire en tire l'état des
// unités binaires déclarées.
func (s *Service) All(ctx context.Context) ([]Snapshot, error) {
	return s.store.SystemdUnitsOfMachines(ctx)
}
