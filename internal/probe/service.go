package probe

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"
)

const (
	rollupPeriod = 5 * time.Minute
	purgePeriod  = 24 * time.Hour
)

// Ce que le service attend de la base ; le paquet store le fournit.
type Store interface {
	InsertProbe(ctx context.Context, probe Probe) error
	// ListProbes rend les sondes d'une machine, toutes si machineID est vide.
	ListProbes(ctx context.Context, machineID string) ([]Probe, error)
	GetProbe(ctx context.Context, id string) (Probe, error)
	CountProbes(ctx context.Context) (total, attention int, err error)
	// CountProbeCertificates compte les certificats vus et ceux dont
	// l'échéance tombe avant l'instant donné.
	CountProbeCertificates(ctx context.Context, before time.Time) (Certificates, error)
	CountProbesOnMachine(ctx context.Context, machineID string) (int, error)
	DeleteProbe(ctx context.Context, id string) error
	// SetProbeStatus met l'état et remet les compteurs à zéro : après une
	// pause, ce que la sonde valait ne vaut plus rien.
	SetProbeStatus(ctx context.Context, id string, status Status) error
	// ApplyChanges écrit les essais et l'état des sondes touchées, en une
	// transaction ; un essai déjà en base, rejoué, est ignoré.
	ApplyProbeResults(ctx context.Context, changes Changes) error
	ListProbeResults(ctx context.Context, probeID string, limit int) ([]Result, error)
	// ListDays rend les jours d'une sonde, ou de toutes si probeID est vide.
	ListProbeDays(ctx context.Context, probeID string, from time.Time) ([]Day, error)
	// CountResults compte les essais et les succès d'une fenêtre, dans le brut.
	CountProbeResults(ctx context.Context, probeID string, from, to time.Time) (total, success int, err error)
	// CountDays fait la même chose depuis l'agrégat journalier.
	CountProbeDays(ctx context.Context, probeID string, from time.Time) (total, success int, err error)
	// RollupDay réécrit un jour entier en une instruction idempotente.
	RollupProbeDay(ctx context.Context, dayStart, dayEnd time.Time) error
	PurgeProbeHistory(ctx context.Context, resultsBefore, daysBefore time.Time) error
}

// Commander pousse une commande à l'agent d'une machine par son flux ; il
// refuse quand elle est hors ligne, et c'est sans gravité : elle recevra
// son jeu en revenant.
type Commander interface {
	Command(machineID, name string, payload any) error
}

// Assignable reçoit le jeu de sondes sans passer par le réseau : c'est le
// moteur de la machine openCloud, qui est son propre agent.
type Assignable interface {
	Assign(assignment Assignment)
}

// Watcher reçoit chaque changement visible : le direct s'y branche. nil
// est toléré.
type Watcher interface {
	ProbesChanged(machineID string)
}

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

// SetLocalRunner branche le moteur de la machine openCloud : son jeu de
// sondes ne sort jamais du processus.
func (s *Service) SetLocalRunner(machineID string, runner Assignable) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.local[machineID] = runner
}

func (s *Service) changed(machineID string) {
	if s.watcher != nil {
		s.watcher.ProbesChanged(machineID)
	}
}

// Create écrit la sonde, puis pousse à sa machine son jeu à jour. Elle
// naît « Nouvelle » : rien n'est su tant qu'aucun essai n'est revenu.
func (s *Service) Create(ctx context.Context, definition Definition) (Probe, error) {
	definition = definition.Complete()
	if err := definition.Validate(); err != nil {
		return Probe{}, err
	}
	carried, err := s.store.CountProbesOnMachine(ctx, definition.MachineID)
	if err != nil {
		return Probe{}, err
	}
	if carried >= MaxProbesPerMachine {
		return Probe{}, ErrTooMany
	}
	id, err := NewID()
	if err != nil {
		return Probe{}, fmt.Errorf("generate id: %w", err)
	}
	created := Probe{
		ID:                id,
		Name:              definition.Name,
		Kind:              definition.Kind,
		Target:            definition.Target,
		MachineID:         definition.MachineID,
		ServiceID:         definition.ServiceID,
		Status:            StatusNew,
		Interval:          definition.Interval,
		Timeout:           definition.Timeout,
		FailureThreshold:  definition.FailureThreshold,
		RecoveryThreshold: definition.RecoveryThreshold,
		Method:            definition.Method,
		ExpectedStatus:    definition.ExpectedStatus,
		ExpectedBody:      definition.ExpectedBody,
		FollowRedirects:   definition.FollowRedirects,
		TLS:               definition.TLS,
		CreatedAt:         s.now(),
	}
	if err := s.store.InsertProbe(ctx, created); err != nil {
		return Probe{}, err
	}
	s.logger.Info("probe created", "probe_id", id, "name", created.Name, "machine_id", created.MachineID, "target", created.Target)
	s.Assign(ctx, created.MachineID)
	s.changed(created.MachineID)
	return s.store.GetProbe(ctx, id)
}

func (s *Service) List(ctx context.Context, machineID string) ([]Probe, error) {
	return s.store.ListProbes(ctx, machineID)
}

func (s *Service) Get(ctx context.Context, id string) (Probe, error) {
	return s.store.GetProbe(ctx, id)
}

// Count renvoie le nombre de sondes et celles à traiter : hors ligne.
func (s *Service) Count(ctx context.Context) (total, attention int, err error) {
	return s.store.CountProbes(ctx)
}

// NeedsAttention dit si l'état compte parmi ce que l'opérateur doit voir.
// Dégradé n'en est pas : la cible répond, c'est sa chaîne qui déplaît.
func NeedsAttention(status Status) bool {
	return status == StatusDown
}

// Certificates compte ce que la vue d'ensemble montre. Le seuil est celui
// du produit, publié au navigateur par la session : un seul chiffre, une
// seule source, et la même bascule à l'écran qu'au compteur.
func (s *Service) Certificates(ctx context.Context) (Certificates, error) {
	return s.store.CountProbeCertificates(ctx, s.now().Add(CertificateWarning*24*time.Hour))
}

func (s *Service) Results(ctx context.Context, id string, limit int) ([]Result, error) {
	return s.store.ListProbeResults(ctx, id, limit)
}

// Days rend les jours d'une sonde depuis la date donnée, les plus anciens
// d'abord ; probeID vide les rend pour toutes les sondes.
func (s *Service) Days(ctx context.Context, id string, span time.Duration) ([]Day, error) {
	return s.store.ListProbeDays(ctx, id, startOfDay(s.now().UTC().Add(-span)))
}

// Uptimes compte, par fenêtre, les essais et les succès. Le pourcentage se
// fait dans le navigateur, et une fenêtre sans essai n'en a pas.
func (s *Service) Uptimes(ctx context.Context, id string) ([]Uptime, error) {
	now := s.now().UTC()
	uptimes := make([]Uptime, 0, len(Windows))
	for _, window := range Windows {
		var total, success int
		var err error
		if window.Source == SourceDaily {
			total, success, err = s.store.CountProbeDays(ctx, id, startOfDay(now.Add(-window.Span)))
		} else {
			total, success, err = s.store.CountProbeResults(ctx, id, now.Add(-window.Span), now)
		}
		if err != nil {
			return nil, err
		}
		uptimes = append(uptimes, Uptime{Window: window.Name, Total: total, Success: success})
	}
	return uptimes, nil
}

// Delete efface la sonde, son histoire avec elle, et rend son jeu à la
// machine. Rien ne la recréera : une sonde ne naît que de l'interface.
func (s *Service) Delete(ctx context.Context, id string) error {
	found, err := s.store.GetProbe(ctx, id)
	if err != nil {
		return err
	}
	if err := s.store.DeleteProbe(ctx, id); err != nil {
		return err
	}
	s.logger.Info("probe deleted", "probe_id", id, "name", found.Name)
	s.Assign(ctx, found.MachineID)
	s.changed(found.MachineID)
	return nil
}

// Pause arrête les essais : la machine ne porte plus cette sonde.
func (s *Service) Pause(ctx context.Context, id string) error {
	return s.setStatus(ctx, id, StatusPaused, false)
}

// Resume la remet au jeu de sa machine. Elle repart « Nouvelle » : ce
// qu'elle valait avant la pause ne dit plus rien de la cible, et le
// premier essai, immédiat, le tranche.
func (s *Service) Resume(ctx context.Context, id string) error {
	return s.setStatus(ctx, id, StatusNew, true)
}

func (s *Service) setStatus(ctx context.Context, id string, status Status, wasPaused bool) error {
	found, err := s.store.GetProbe(ctx, id)
	if err != nil {
		return err
	}
	if wasPaused && !found.IsPaused() {
		return ErrNotPaused
	}
	if err := s.store.SetProbeStatus(ctx, id, status); err != nil {
		return err
	}
	s.logger.Info("probe status set", "probe_id", id, "status", string(status))
	s.Assign(ctx, found.MachineID)
	s.changed(found.MachineID)
	return nil
}

// Assign pousse à une machine le jeu complet de ses sondes actives : le
// serveur tient la liste, l'agent l'exécute. Une machine hors ligne le
// recevra à sa prochaine connexion, qui commence par là.
func (s *Service) Assign(ctx context.Context, machineID string) {
	probes, err := s.store.ListProbes(ctx, machineID)
	if err != nil {
		s.logger.Error("list probes to assign", "machine_id", machineID, "error", err)
		return
	}
	tasks := make([]Task, 0, len(probes))
	for _, found := range probes {
		if found.IsPaused() {
			continue
		}
		tasks = append(tasks, found.Task())
	}
	s.mu.RLock()
	runner, isLocal := s.local[machineID]
	commander := s.commander
	s.mu.RUnlock()
	assignment := Assignment{Probes: tasks}
	if isLocal {
		runner.Assign(assignment)
		return
	}
	if commander == nil {
		return
	}
	if err := commander.Command(machineID, CommandProbes, assignment); err != nil {
		s.logger.Debug("assign probes", "machine_id", machineID, "probes", len(tasks), "error", err)
	}
}

// Record écrit ce qu'une machine a sondé. Le rapport entier est refusé dès
// qu'un essai est hors de ce qu'un agent peut avoir observé ; une sonde
// que la machine ne porte plus est ignorée, ses essais avec.
func (s *Service) Record(ctx context.Context, machineID string, report Report) error {
	if report.IsEmpty() {
		return nil
	}
	now := s.now()
	if err := validate(report, now); err != nil {
		return err
	}
	carried, err := s.store.ListProbes(ctx, machineID)
	if err != nil {
		return err
	}
	known := make(map[string]Probe, len(carried))
	for _, found := range carried {
		known[found.ID] = found
	}
	changes := planChanges(known, report, now)
	if len(changes.Results) == 0 && len(changes.Probes) == 0 {
		return nil
	}
	if err := s.store.ApplyProbeResults(ctx, changes); err != nil {
		return err
	}
	s.changed(machineID)
	return nil
}

// Rollup réécrit chaque jour que le brut couvre encore entièrement, en une
// instruction idempotente par jour : sans curseur, un redémarrage ou un
// rattrapage se corrige seul. Le premier jour couvert par la rétention
// l'est à moitié, et le réécrire le ferait mentir : il a déjà été agrégé
// quand il était entier.
func (s *Service) Rollup(ctx context.Context) error {
	now := s.now().UTC()
	first := startOfDay(now.Add(-ResultRetention)).AddDate(0, 0, 1)
	for day := first; !day.After(now); day = day.AddDate(0, 0, 1) {
		if err := s.store.RollupProbeDay(ctx, day, day.AddDate(0, 0, 1)); err != nil {
			return fmt.Errorf("rollup day %s: %w", day.Format(time.DateOnly), err)
		}
	}
	return nil
}

// Purge efface le brut et l'agrégat au-delà de leur rétention.
func (s *Service) Purge(ctx context.Context) error {
	now := s.now()
	return s.store.PurgeProbeHistory(ctx, now.Add(-ResultRetention), now.Add(-DayRetention))
}

// Watch est la boucle de fond : le rollup au départ puis toutes les 5 min,
// la purge au départ puis une fois par jour. Elle s'arrête avec le contexte.
func (s *Service) Watch(ctx context.Context) {
	s.rollupAndPurge(ctx, true)
	rollup := time.NewTicker(rollupPeriod)
	defer rollup.Stop()
	purge := time.NewTicker(purgePeriod)
	defer purge.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-rollup.C:
			s.rollupAndPurge(ctx, false)
		case <-purge.C:
			s.rollupAndPurge(ctx, true)
		}
	}
}

func (s *Service) rollupAndPurge(ctx context.Context, withPurge bool) {
	if err := s.Rollup(ctx); err != nil && ctx.Err() == nil {
		s.logger.Error("rollup probe days", "error", err)
	}
	if !withPurge {
		return
	}
	if err := s.Purge(ctx); err != nil && ctx.Err() == nil {
		s.logger.Warn("purge probe history", "error", err)
	}
}

func startOfDay(at time.Time) time.Time {
	return time.Date(at.Year(), at.Month(), at.Day(), 0, 0, 0, 0, time.UTC)
}
