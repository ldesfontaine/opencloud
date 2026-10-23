package probe

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/ldesfontaine/opencloud/internal/audit"
	"github.com/ldesfontaine/opencloud/internal/deletion"
	"github.com/ldesfontaine/opencloud/internal/ident"
	"github.com/ldesfontaine/opencloud/internal/machine"
)

const (
	rollupPeriod = 5 * time.Minute
	purgePeriod  = 24 * time.Hour
	// Un jeu refusé par une file pleine repart à ce rythme.
	resendPeriod = 30 * time.Second
)

// Ce que le service attend de la base ; le paquet store le fournit.
type Store interface {
	InsertProbe(ctx context.Context, probe Probe) error
	// UpdateProbe remplace la définition d'une sonde en une instruction,
	// sans relire l'état : un essai qui arrive pendant l'édition ne se perd
	// pas, sauf si l'édition remet l'état à zéro.
	UpdateProbe(ctx context.Context, revision Revision) error
	// ListProbes rend les sondes d'une machine, toutes si machineID est vide.
	ListProbes(ctx context.Context, machineID string) ([]Probe, error)
	GetProbe(ctx context.Context, id string) (Probe, error)
	// CountProbeCertificates compte les certificats vus et ceux dont
	// l'échéance tombe avant l'instant donné.
	CountProbeCertificates(ctx context.Context, now, before time.Time) (Certificates, error)
	CountProbesOnMachine(ctx context.Context, machineID string) (int, error)
	MachineExists(ctx context.Context, id string) (bool, error)
	// DeclaredServiceExists dit si le service de l'annuaire existe.
	DeclaredServiceExists(ctx context.Context, id string) (bool, error)
	// DeleteProbe relit le plan dans sa transaction et refuse tant qu'il est
	// bloqué (RefuseDeletion).
	DeleteProbe(ctx context.Context, id string) error
	ProbeDeletionPlan(ctx context.Context, id string) (deletion.Plan, error)
	// SetProbeStatus met l'état et remet les compteurs à zéro : après une
	// pause, ce que la sonde valait ne vaut plus rien.
	SetProbeStatus(ctx context.Context, id string, status Status) error
	// ApplyProbeResults écrit les essais et l'état des sondes touchées, en une
	// transaction ; un essai déjà en base, rejoué, est ignoré. Rend les
	// sondes dont l'état a été écrit : une sonde révisée entre-temps n'y est pas.
	ApplyProbeResults(ctx context.Context, changes Changes) ([]Probe, error)
	// ListDays rend les jours d'une sonde, ou de toutes si probeID est vide.
	ListProbeDays(ctx context.Context, probeID string, from time.Time) ([]Day, error)
	// CountResults compte les essais et les succès d'une fenêtre, dans le brut.
	CountProbeResults(ctx context.Context, probeID string, from, to time.Time) (total, success int, err error)
	// CountProbeWindow compte une fenêtre de jours : l'agrégat des jours que
	// le brut ne couvre plus en entier, puis le brut.
	CountProbeWindow(ctx context.Context, probeID string, from, now time.Time) (total, success int, err error)
	// RollupDay réécrit un jour entier en une instruction idempotente.
	RollupProbeDay(ctx context.Context, dayStart, dayEnd time.Time) error
	PurgeProbeHistory(ctx context.Context, resultsBefore, daysBefore time.Time) error
}

// Commander pousse le jeu de sondes à l'agent d'une machine par son flux ;
// il refuse quand elle est hors ligne, et c'est sans gravité : elle recevra
// son jeu en revenant. Une file pleine rend machine.ErrBusy : le jeu est
// renvoyé au cycle suivant.
type Commander interface {
	AssignProbes(machineID string, assignment Assignment) error
}

// Assignable reçoit le jeu de sondes : le moteur de l'agent, qui le tient de
// sa commande. Il rend le nombre de tâches qu'il a écartées.
type Assignable interface {
	Assign(assignment Assignment) int
}

// Watcher reçoit chaque changement visible : le direct s'y branche. nil
// est toléré.
type Watcher interface {
	ProbesChanged(machineID string)
}

// Alerter reçoit chaque sonde dont l'état vient de bouger, ou dont le
// certificat est à rejuger, et chaque sonde supprimée : le moteur des
// alertes s'y branche. nil est toléré.
type Alerter interface {
	ProbeChanged(ctx context.Context, probe Probe)
	ProbeRemoved(ctx context.Context, probeID string)
}

type Service struct {
	store   Store
	watcher Watcher
	alerter Alerter
	auditor *audit.Recorder
	logger  *slog.Logger
	now     func() time.Time

	mu        sync.RWMutex
	commander Commander
	// Les machines dont le dernier jeu n'est pas parti, file pleine.
	unsent map[string]bool
	// creating tient compter-puis-insérer d'un seul tenant : deux créations
	// simultanées ne passent pas toutes deux sur la dernière place.
	creating sync.Mutex
}

func New(store Store, logger *slog.Logger) *Service {
	return &Service{store: store, logger: logger, now: time.Now, unsent: make(map[string]bool)}
}

func (s *Service) SetClock(now func() time.Time) {
	s.now = now
}

func (s *Service) SetWatcher(watcher Watcher) {
	s.watcher = watcher
}

func (s *Service) SetAlerter(alerter Alerter) {
	s.alerter = alerter
}

func (s *Service) SetCommander(commander Commander) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.commander = commander
}

func (s *Service) changed(machineID string) {
	if s.watcher != nil {
		s.watcher.ProbesChanged(machineID)
	}
}

// Create écrit la sonde, puis pousse à sa machine son jeu à jour. Elle
// naît « Nouvelle » : rien n'est su tant qu'aucun essai n'est revenu.
func (s *Service) Create(ctx context.Context, definition Definition) (Probe, error) {
	if err := s.checkMachineExists(ctx, definition.MachineID); err != nil {
		return Probe{}, err
	}
	definition = definition.Complete()
	if err := definition.Validate(); err != nil {
		return Probe{}, err
	}
	if err := s.checkServiceExists(ctx, definition); err != nil {
		return Probe{}, err
	}
	id, err := ident.New()
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
	err = s.writeUnderCeiling(ctx, created.MachineID, func() error {
		return s.store.InsertProbe(ctx, created)
	})
	if err != nil {
		return Probe{}, err
	}
	s.logger.Info("probe created", "probe_id", id, "name", created.Name, "machine_id", created.MachineID, "target", RedactTarget(created.Target))
	s.auditor.Record(ctx, probeEvent(audit.ProbeCreate, created))
	s.Assign(ctx, created.MachineID)
	s.changed(created.MachineID)
	return s.store.GetProbe(ctx, id)
}

// La machine passe avant le reste de la définition : c'est l'ordre que
// l'API a toujours rendu. Inconnue, la clé étrangère la refuserait en erreur
// brute ; vide, elle n'existe pas non plus.
func (s *Service) checkMachineExists(ctx context.Context, machineID string) error {
	exists, err := s.store.MachineExists(ctx, machineID)
	if err != nil {
		return err
	}
	if !exists {
		return ErrMachineInvalid
	}
	return nil
}

// Le rattachement est facultatif, mais s'il est donné il doit être vrai :
// la clé étrangère le refuserait en erreur brute. Le service peut tourner sur
// une autre machine que celle qui sonde.
func (s *Service) checkServiceExists(ctx context.Context, definition Definition) error {
	if definition.ServiceID == "" {
		return nil
	}
	exists, err := s.store.DeclaredServiceExists(ctx, definition.ServiceID)
	if err != nil {
		return err
	}
	if !exists {
		return ErrServiceInvalid
	}
	return nil
}

// writeUnderCeiling compte puis écrit d'un seul tenant : la sonde qui
// arrive sur la machine ne passe que s'il reste une place.
func (s *Service) writeUnderCeiling(ctx context.Context, machineID string, write func() error) error {
	s.creating.Lock()
	defer s.creating.Unlock()
	carried, err := s.store.CountProbesOnMachine(ctx, machineID)
	if err != nil {
		return err
	}
	if carried >= MaxProbesPerMachine {
		return ErrTooMany
	}
	return write()
}

// Update remplace la définition de la sonde, sur place : l'identifiant,
// l'histoire et la date de création restent. Ce qui change ce que la sonde
// mesure la remet à « Nouvelle » ; le certificat n'est oublié que si la
// cible change. Ses machines, l'ancienne comme la nouvelle, reçoivent leur
// jeu à jour.
func (s *Service) Update(ctx context.Context, id string, definition Definition) (Probe, error) {
	before, err := s.store.GetProbe(ctx, id)
	if err != nil {
		return Probe{}, err
	}
	if err := s.checkMachineExists(ctx, definition.MachineID); err != nil {
		return Probe{}, err
	}
	definition = definition.Complete()
	if err := definition.Validate(); err != nil {
		return Probe{}, err
	}
	if err := s.checkServiceExists(ctx, definition); err != nil {
		return Probe{}, err
	}
	revision := s.revisionOf(before, definition)
	if err := s.writeRevision(ctx, before, revision); err != nil {
		return Probe{}, err
	}
	s.logger.Info("probe updated", "probe_id", id, "name", revision.Probe.Name, "machine_id", revision.Probe.MachineID,
		"target", RedactTarget(revision.Probe.Target), "state_reset", revision.ResetsState)
	s.auditor.Record(ctx, probeEvent(audit.ProbeUpdate, revision.Probe))
	updated, err := s.store.GetProbe(ctx, id)
	if err != nil {
		return Probe{}, err
	}
	// Remise à zéro : l'état est « Nouveau » et le moteur lève tout ; sinon
	// il ne fait que renommer ce qui est ouvert, sans rien notifier.
	if s.alerter != nil {
		s.alerter.ProbeChanged(ctx, updated)
	}
	s.Assign(ctx, updated.MachineID)
	s.changed(updated.MachineID)
	if before.MachineID != updated.MachineID {
		s.Assign(ctx, before.MachineID)
		s.changed(before.MachineID)
	}
	return updated, nil
}

func (s *Service) revisionOf(before Probe, definition Definition) Revision {
	revision := Revision{
		Probe:             before,
		ResetsState:       definition.ResetsState(before),
		ClearsCertificate: definition.ClearsCertificate(before),
	}
	revised := &revision.Probe
	revised.Name = definition.Name
	revised.Kind = definition.Kind
	revised.Target = definition.Target
	revised.MachineID = definition.MachineID
	revised.ServiceID = definition.ServiceID
	revised.Interval = definition.Interval
	revised.Timeout = definition.Timeout
	revised.FailureThreshold = definition.FailureThreshold
	revised.RecoveryThreshold = definition.RecoveryThreshold
	revised.Method = definition.Method
	revised.ExpectedStatus = definition.ExpectedStatus
	revised.ExpectedBody = definition.ExpectedBody
	revised.FollowRedirects = definition.FollowRedirects
	revised.TLS = definition.TLS
	if revision.ResetsState {
		revised.RevisedAt = s.now()
	}
	return revision
}

// Changer de machine est une arrivée : elle compte sur la nouvelle, et
// c'est elle qui est refusée si elle est pleine.
func (s *Service) writeRevision(ctx context.Context, before Probe, revision Revision) error {
	write := func() error { return s.store.UpdateProbe(ctx, revision) }
	if revision.Probe.MachineID == before.MachineID {
		return write()
	}
	return s.writeUnderCeiling(ctx, revision.Probe.MachineID, write)
}

// Certificates compte ce que la vue d'ensemble montre. Le seuil est celui
// du produit, publié au navigateur par la session : un seul chiffre, une
// seule source, et la même bascule à l'écran qu'au compteur.
func (s *Service) Certificates(ctx context.Context) (Certificates, error) {
	now := s.now()
	return s.store.CountProbeCertificates(ctx, now, now.Add(CertificateWarning*24*time.Hour))
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
			total, success, err = s.store.CountProbeWindow(ctx, id, startOfDay(now.Add(-window.Span)), now)
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

// DeletePlan dit ce que Delete ferait : les composants de statut qui la
// retiennent, l'historique qui part, les alertes qui s'en détachent.
func (s *Service) DeletePlan(ctx context.Context, id string) (deletion.Plan, error) {
	if _, err := s.store.GetProbe(ctx, id); err != nil {
		return deletion.Plan{}, err
	}
	return s.store.ProbeDeletionPlan(ctx, id)
}

// Delete efface la sonde, son histoire avec elle, et rend son jeu à la
// machine. Rien ne la recréera : une sonde ne naît que de l'interface. Un
// composant de statut qui l'affiche la retient (RefuseDeletion).
func (s *Service) Delete(ctx context.Context, id string) error {
	found, err := s.store.GetProbe(ctx, id)
	if err != nil {
		return err
	}
	if err := s.store.DeleteProbe(ctx, id); err != nil {
		return err
	}
	s.logger.Info("probe deleted", "probe_id", id, "name", found.Name)
	s.auditor.Record(ctx, probeEvent(audit.ProbeDelete, found))
	if s.alerter != nil {
		s.alerter.ProbeRemoved(ctx, id)
	}
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
	// Une sonde mise en pause pour ses identifiants ne repart qu'avec une
	// cible changée.
	if wasPaused && targetHasCredentials(found.Kind, found.Target) {
		return ErrTargetCredentials
	}
	if err := s.store.SetProbeStatus(ctx, id, status); err != nil {
		return err
	}
	s.logger.Info("probe status set", "probe_id", id, "status", string(status))
	s.auditor.Record(ctx, probeEvent(pauseAction(wasPaused), found))
	if s.alerter != nil {
		found.Status = status
		s.alerter.ProbeChanged(ctx, found)
	}
	s.Assign(ctx, found.MachineID)
	s.changed(found.MachineID)
	return nil
}

// PauseTargetsWithCredentials met en pause chaque sonde dont la cible porte
// des identifiants, acceptés avant que la création les refuse. Ils restent
// en base, en clair, tant que l'opérateur ne change pas la cible : le journal
// le redit à chaque démarrage, cible masquée.
func (s *Service) PauseTargetsWithCredentials(ctx context.Context) error {
	probes, err := s.store.ListProbes(ctx, "")
	if err != nil {
		return fmt.Errorf("list probes: %w", err)
	}
	for _, found := range probes {
		if !targetHasCredentials(found.Kind, found.Target) {
			continue
		}
		if !found.IsPaused() {
			if err := s.Pause(ctx, found.ID); err != nil {
				return fmt.Errorf("pause probe %s: %w", found.ID, err)
			}
		}
		s.logger.Warn("probe paused, its target carries credentials: change the target to resume it",
			"probe_id", found.ID, "name", found.Name, "machine_id", found.MachineID, "target", RedactTarget(found.Target))
	}
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
	commander := s.commander
	s.mu.RUnlock()
	assignment := Assignment{Probes: tasks}
	if commander == nil {
		return
	}
	err = commander.AssignProbes(machineID, assignment)
	if errors.Is(err, machine.ErrBusy) {
		s.logger.Warn("assign probes later, command queue full", "machine_id", machineID, "probes", len(tasks))
		s.setUnsent(machineID, true)
		return
	}
	if err != nil {
		s.logger.Debug("assign probes", "machine_id", machineID, "probes", len(tasks), "error", err)
	}
	s.setUnsent(machineID, false)
}

func (s *Service) setUnsent(machineID string, isUnsent bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if isUnsent {
		s.unsent[machineID] = true
		return
	}
	delete(s.unsent, machineID)
}

// ResendUnsent repousse le jeu des machines dont la file était pleine.
func (s *Service) ResendUnsent(ctx context.Context) {
	s.mu.RLock()
	machineIDs := make([]string, 0, len(s.unsent))
	for machineID := range s.unsent {
		machineIDs = append(machineIDs, machineID)
	}
	s.mu.RUnlock()
	for _, machineID := range machineIDs {
		s.Assign(ctx, machineID)
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
	written, err := s.store.ApplyProbeResults(ctx, changes)
	if err != nil {
		return err
	}
	if s.alerter != nil {
		for _, touched := range written {
			s.alerter.ProbeChanged(ctx, touched)
		}
	}
	s.changed(machineID)
	return nil
}

// ReviewCertificates redonne au moteur des alertes chaque sonde active qui
// a vu un certificat : un seuil se franchit sans qu'aucun essai ne le dise,
// et une sonde à 24 h d'intervalle le dirait trop tard.
func (s *Service) ReviewCertificates(ctx context.Context) error {
	if s.alerter == nil {
		return nil
	}
	probes, err := s.store.ListProbes(ctx, "")
	if err != nil {
		return err
	}
	for _, found := range probes {
		if found.Certificate != nil && found.Status != StatusPaused && found.Status != StatusNew {
			s.alerter.ProbeChanged(ctx, found)
		}
	}
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

// Watch est la boucle de fond : le rollup et la revue des certificats au
// départ puis toutes les 5 min, la purge au départ puis une fois par jour.
// Elle s'arrête avec le contexte.
func (s *Service) Watch(ctx context.Context) {
	s.rollupAndPurge(ctx, true)
	rollup := time.NewTicker(rollupPeriod)
	defer rollup.Stop()
	purge := time.NewTicker(purgePeriod)
	defer purge.Stop()
	resend := time.NewTicker(resendPeriod)
	defer resend.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-resend.C:
			s.ResendUnsent(ctx)
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
	if err := s.ReviewCertificates(ctx); err != nil && ctx.Err() == nil {
		s.logger.Error("review certificates", "error", err)
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
