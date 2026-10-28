package role

import (
	"context"
	"log/slog"
)

// Ce que le composant attend de la base ; le paquet store le fournit.
type Store interface {
	// ReplaceRoles remplace les rôles de la machine par ceux du rapport et
	// dit si quelque chose a changé.
	ReplaceRoles(ctx context.Context, machineID string, roles []Role) (changed bool, err error)
}

// Watcher reçoit chaque changement de rôle : le direct s'y branche. nil est
// toléré.
type Watcher interface {
	RolesChanged(machineID string)
}

type Service struct {
	store   Store
	watcher Watcher
	logger  *slog.Logger
}

func New(store Store, logger *slog.Logger) *Service {
	return &Service{store: store, logger: logger}
}

func (s *Service) SetWatcher(watcher Watcher) {
	s.watcher = watcher
}

// Record garde les rôles de la machine à la place des précédents. Un rapport
// hors bornes est refusé en entier. Rien n'est journalisé à l'audit : c'est
// une lecture de l'agent, qui ne change aucun réglage.
func (s *Service) Record(ctx context.Context, machineID string, report Report) error {
	if err := report.Validate(); err != nil {
		return err
	}
	changed, err := s.store.ReplaceRoles(ctx, machineID, report.Roles)
	if err != nil {
		return err
	}
	if changed {
		s.logger.Info("machine roles changed", "machine_id", machineID, "roles", len(report.Roles))
		if s.watcher != nil {
			s.watcher.RolesChanged(machineID)
		}
	}
	return nil
}
