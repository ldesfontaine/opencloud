package sshkeys

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
	// ReplaceSSHKeys remplace, en une transaction, tout ce que la machine avait.
	ReplaceSSHKeys(ctx context.Context, machineID string, report Report, at time.Time) error
	// MachineSSHKeys rend le relevé d'une machine ; machine.ErrNotFound si elle
	// n'existe pas.
	MachineSSHKeys(ctx context.Context, machineID string) (Snapshot, error)
	// SSHKeyMatrix rend le relevé de chaque machine, nommé, même sans relevé.
	SSHKeyMatrix(ctx context.Context) ([]Snapshot, error)
}

// Watcher reçoit chaque relevé écrit : le direct s'y branche. nil est toléré.
type Watcher interface {
	SSHKeysChanged(machineID string)
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
	if err := s.store.ReplaceSSHKeys(ctx, machineID, report, s.now()); err != nil {
		return err
	}
	if s.watcher != nil {
		s.watcher.SSHKeysChanged(machineID)
	}
	return nil
}

// Machine rend ce que l'agent d'une machine a relevé.
func (s *Service) Machine(ctx context.Context, machineID string) (Snapshot, error) {
	return s.store.MachineSSHKeys(ctx, machineID)
}

// Matrix rend le relevé de toutes les machines, pour la vue « quelle clé
// ouvre quoi ».
func (s *Service) Matrix(ctx context.Context) ([]Snapshot, error) {
	return s.store.SSHKeyMatrix(ctx)
}
