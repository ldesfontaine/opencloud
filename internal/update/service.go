package update

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/ldesfontaine/opencloud/internal/service"
)

const (
	// Un constat qu'aucun passage n'a rafraîchi depuis un mois parle d'une
	// image que la machine n'a plus.
	CheckRetention = 30 * 24 * time.Hour
	purgePeriod    = 24 * time.Hour
)

// Ce que le composant attend de la base ; le package store le fournit.
type Store interface {
	ListServices(ctx context.Context, machineID string) ([]service.Service, error)
	GetService(ctx context.Context, id string) (service.Service, error)
	SetServiceUpdatePolicy(ctx context.Context, id string, policy service.UpdatePolicy) error
	// ApplyImageChecks écrase le constat de chaque (machine, image).
	ApplyImageChecks(ctx context.Context, checks []Check) error
	// ListImageChecks rend les constats d'une machine ; de toutes si vide.
	ListImageChecks(ctx context.Context, machineID string) ([]Check, error)
	PurgeImageChecks(ctx context.Context, before time.Time) error
}

// Commander pousse une commande à l'agent d'une machine par son flux ;
// il refuse quand elle est hors ligne.
type Commander interface {
	Command(machineID, name string, payload any) error
}

// Assignable reçoit les exclusions sans passer par le réseau : la boucle
// de la machine openCloud, qui est son propre agent.
type Assignable interface {
	Assign(assignment Assignment)
}

// Watcher reçoit chaque changement visible : le direct s'y branche. Un
// constat change une ligne de service, le sujet est le leur.
type Watcher interface {
	ServicesChanged(machineID string)
}

// Service est le composant côté serveur : il écrit les constats, tient
// la politique des fiches, et dit aux agents quoi ne pas interroger.
type Service struct {
	store   Store
	watcher Watcher
	logger  *slog.Logger
	now     func() time.Time

	mu        sync.RWMutex
	commander Commander
	local     map[string]Assignable
}

func New(store Store, logger *slog.Logger) *Service {
	return &Service{store: store, logger: logger, now: time.Now, local: make(map[string]Assignable)}
}

func (s *Service) SetClock(now func() time.Time) {
	s.now = now
}

func (s *Service) SetWatcher(watcher Watcher) {
	s.watcher = watcher
}

func (s *Service) SetCommander(commander Commander) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.commander = commander
}

func (s *Service) SetLocalRunner(machineID string, runner Assignable) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.local[machineID] = runner
}

// Record écrit ce qu'une machine a constaté sur ses images. Le rapport
// entier est refusé dès qu'un résultat est hors de ce qu'un agent peut
// avoir vu.
func (s *Service) Record(ctx context.Context, machineID string, report Report) error {
	if report.IsEmpty() {
		return nil
	}
	if err := validate(report, s.now()); err != nil {
		return err
	}
	checks := make([]Check, 0, len(report.Results))
	for _, result := range report.Results {
		checks = append(checks, checkOf(machineID, result))
	}
	if err := s.store.ApplyImageChecks(ctx, checks); err != nil {
		return err
	}
	if s.watcher != nil {
		s.watcher.ServicesChanged(machineID)
	}
	return nil
}

// Assign dit à une machine les images à ne jamais interroger : celles
// des services exclus. Une machine hors ligne le recevra à sa prochaine
// connexion, qui commence par là.
func (s *Service) Assign(ctx context.Context, machineID string) {
	if err := s.assign(ctx, machineID, false); err != nil {
		s.logger.Debug("assign image checks", "machine_id", machineID, "error", err)
	}
}

// CheckNow demande à une machine de vérifier toutes ses images sans
// attendre la cadence.
func (s *Service) CheckNow(ctx context.Context, machineID string) error {
	return s.assign(ctx, machineID, true)
}

func (s *Service) assign(ctx context.Context, machineID string, now bool) error {
	services, err := s.store.ListServices(ctx, machineID)
	if err != nil {
		return err
	}
	assignment := Assignment{Now: now}
	for _, found := range services {
		if found.UpdatePolicy == service.PolicyExcluded && !found.IsArchived() {
			assignment.Excluded = append(assignment.Excluded, found.Image)
		}
	}
	s.mu.RLock()
	runner, isLocal := s.local[machineID]
	commander := s.commander
	s.mu.RUnlock()
	if isLocal {
		runner.Assign(assignment)
		return nil
	}
	if commander == nil {
		return ErrMachineOffline
	}
	if err := commander.Command(machineID, CommandImages, assignment); err != nil {
		return ErrMachineOffline
	}
	return nil
}

// SetPolicy règle ce que l'opérateur veut des mises à jour d'un service,
// puis redit à sa machine ce qu'elle ne doit plus interroger.
func (s *Service) SetPolicy(ctx context.Context, serviceID string, policy service.UpdatePolicy) error {
	if !service.IsUpdatePolicy(policy) {
		return ErrBadPolicy
	}
	found, err := s.store.GetService(ctx, serviceID)
	if err != nil {
		return err
	}
	if err := s.store.SetServiceUpdatePolicy(ctx, serviceID, policy); err != nil {
		return err
	}
	s.Assign(ctx, found.MachineID)
	if s.watcher != nil {
		s.watcher.ServicesChanged(found.MachineID)
	}
	return nil
}

// Checks rend les constats d'une machine, ou de toutes, par image.
func (s *Service) Checks(ctx context.Context, machineID string) (map[string]Check, error) {
	checks, err := s.store.ListImageChecks(ctx, machineID)
	if err != nil {
		return nil, err
	}
	byImage := make(map[string]Check, len(checks))
	for _, check := range checks {
		byImage[keyOf(check.MachineID, check.Image)] = check
	}
	return byImage, nil
}

// keyOf est la clé d'un constat dans ce que Checks rend.
func keyOf(machineID, image string) string {
	return machineID + " " + image
}

// CheckFor rend le constat qui vaut pour une fiche, s'il y en a un.
func CheckFor(checks map[string]Check, found service.Service) (Check, bool) {
	check, ok := checks[keyOf(found.MachineID, found.Image)]
	return check, ok
}

// Count rend le nombre de services vivants qui ont une mise à jour et
// dont l'opérateur veut la voir : c'est ce que la vue d'ensemble compte.
func (s *Service) Count(ctx context.Context) (int, error) {
	services, err := s.store.ListServices(ctx, "")
	if err != nil {
		return 0, err
	}
	checks, err := s.Checks(ctx, "")
	if err != nil {
		return 0, err
	}
	count := 0
	for _, found := range services {
		if found.IsArchived() || found.UpdatePolicy != service.PolicyFollow {
			continue
		}
		if check, ok := CheckFor(checks, found); ok && check.HasUpdate() {
			count++
		}
	}
	return count, nil
}

func (s *Service) Purge(ctx context.Context) error {
	return s.store.PurgeImageChecks(ctx, s.now().Add(-CheckRetention))
}

// Watch est la boucle de fond : la purge au départ puis une fois par jour.
func (s *Service) Watch(ctx context.Context) {
	s.purge(ctx)
	ticker := time.NewTicker(purgePeriod)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.purge(ctx)
		}
	}
}

func (s *Service) purge(ctx context.Context) {
	if err := s.Purge(ctx); err != nil && !errors.Is(err, context.Canceled) {
		s.logger.Warn("purge image checks", "error", err)
	}
}
