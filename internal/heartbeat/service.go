package heartbeat

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	checkPeriod = 15 * time.Second
	purgePeriod = 24 * time.Hour
)

// Ce que le service attend de la base ; le package store le fournit.
type Store interface {
	InsertHeartbeat(ctx context.Context, heartbeat Heartbeat) error
	ListHeartbeats(ctx context.Context) ([]Heartbeat, error)
	GetHeartbeat(ctx context.Context, id string) (Heartbeat, error)
	GetHeartbeatByToken(ctx context.Context, token string) (Heartbeat, error)
	MachineExists(ctx context.Context, id string) (bool, error)
	CountHeartbeats(ctx context.Context) (total, attention int, err error)
	// SetHeartbeatToken remplace le jeton et son empreinte d'un coup.
	SetHeartbeatToken(ctx context.Context, id, token string) error
	DeleteHeartbeat(ctx context.Context, id string) error
	ListOverdueHeartbeats(ctx context.Context, now time.Time) ([]Heartbeat, error)
	// Transact relit le moniteur dans une transaction qui prend le verrou
	// d'écriture, laisse decide calculer la transition sur cet état frais,
	// puis écrit le ping, l'exécution et le nouvel état d'un coup. Deux pings
	// ou un ping et l'échéance ne se voient jamais un état périmé.
	Transact(ctx context.Context, id string, decide Decision) error
	ListPings(ctx context.Context, heartbeatID string, limit int) ([]Ping, error)
	ListRuns(ctx context.Context, heartbeatID string, limit int) ([]Run, error)
	PurgeHistory(ctx context.Context, pingsBefore, runsBefore time.Time) error
}

// Decision reçoit le moniteur tel qu'il est au moment d'écrire, et rend la
// transition à appliquer ; nil quand il n'y a plus rien à faire.
type Decision func(before Heartbeat) *Transition

// Transition est ce qu'un ping, une échéance ou une pause change d'un coup.
type Transition struct {
	// L'état du moniteur après.
	Heartbeat Heartbeat
	// Le ping reçu ; nil quand c'est l'échéance qui parle.
	Ping *Ping
	// L'exécution en cours, close avec ces valeurs ; nil s'il n'y en a pas.
	CloseRun *Run
	// Une exécution à ouvrir, ou à insérer déjà close.
	NewRun *Run
}

// closeOpenRun rend la clôture de l'exécution ouverte, s'il y en a une.
func closeOpenRun(before Heartbeat, now time.Time, outcome Outcome) *Run {
	if before.RunStartedAt.IsZero() {
		return nil
	}
	return &Run{HeartbeatID: before.ID, CompletedAt: now, Outcome: outcome}
}

// Alerter reçoit ce qui mérite une alerte : le moteur des alertes s'y
// branche ; nil est toléré.
type Alerter interface {
	Late(ctx context.Context, heartbeat Heartbeat)
	// Recovered : une fin réussie, après un retard, un échec ou un start.
	// Elle peut donc arriver sans rien à lever.
	Recovered(ctx context.Context, heartbeat Heartbeat)
	Failed(ctx context.Context, heartbeat Heartbeat, exitCode int)
	// Paused et Removed : ce que le moniteur portait ne compte plus.
	Paused(ctx context.Context, heartbeat Heartbeat)
	Removed(ctx context.Context, heartbeatID string)
}

// Watcher reçoit chaque changement d'un moniteur, quel qu'il soit : le
// direct s'y branche. Alerter ne reçoit que ce qui mérite une alerte.
// nil est toléré.
type Watcher interface {
	HeartbeatChanged(heartbeatID string)
}

type Service struct {
	store   Store
	alerter Alerter
	watcher Watcher
	logger  *slog.Logger
	// now est remplaçable dans les tests : les échéances se comparent à lui.
	now func() time.Time
}

func New(store Store, logger *slog.Logger) *Service {
	return &Service{store: store, logger: logger, now: time.Now}
}

func (s *Service) SetClock(now func() time.Time) {
	s.now = now
}

func (s *Service) SetAlerter(alerter Alerter) {
	s.alerter = alerter
}

func (s *Service) SetWatcher(watcher Watcher) {
	s.watcher = watcher
}

func (s *Service) changed(heartbeatID string) {
	if s.watcher != nil {
		s.watcher.HeartbeatChanged(heartbeatID)
	}
}

// Create tire l'identifiant et le jeton, puis écrit le moniteur ; il naît
// « Nouveau » avec une première échéance : une tâche jamais branchée doit
// finir en retard, pas rester nouvelle pour toujours.
func (s *Service) Create(ctx context.Context, definition Definition) (Heartbeat, error) {
	if err := s.checkMachineExists(ctx, definition.MachineID); err != nil {
		return Heartbeat{}, err
	}
	if err := definition.Validate(); err != nil {
		return Heartbeat{}, err
	}
	id, err := NewID()
	if err != nil {
		return Heartbeat{}, fmt.Errorf("generate id: %w", err)
	}
	token, err := NewToken()
	if err != nil {
		return Heartbeat{}, fmt.Errorf("generate token: %w", err)
	}
	now := s.now()
	heartbeat := Heartbeat{
		ID:             id,
		Token:          token,
		Name:           strings.TrimSpace(definition.Name),
		MachineID:      definition.MachineID,
		Status:         StatusNew,
		Interval:       definition.Interval,
		Grace:          definition.Grace,
		NextDeadlineAt: now.Add(definition.Interval + definition.Grace),
		CreatedAt:      now,
	}
	if err := s.store.InsertHeartbeat(ctx, heartbeat); err != nil {
		return Heartbeat{}, err
	}
	s.logger.Info("heartbeat created", "heartbeat_id", id, "name", heartbeat.Name)
	s.changed(id)
	return s.store.GetHeartbeat(ctx, id)
}

// Update remplace le nom, la machine, l'intervalle et la marge d'une tâche.
// L'identifiant et le jeton ne changent jamais ici. Un intervalle ou une
// marge qui change décale l'échéance en cours d'autant, sans la réinitialiser
// à partir de maintenant : la tâche n'a pas pingé de plus ou de moins.
func (s *Service) Update(ctx context.Context, id string, definition Definition) (Heartbeat, error) {
	// La tâche d'abord : inconnue, elle est introuvable quoi que dise la
	// définition.
	if _, err := s.store.GetHeartbeat(ctx, id); err != nil {
		return Heartbeat{}, err
	}
	if err := s.checkMachineExists(ctx, definition.MachineID); err != nil {
		return Heartbeat{}, err
	}
	if err := definition.Validate(); err != nil {
		return Heartbeat{}, err
	}
	now := s.now()
	var before, after Heartbeat
	err := s.store.Transact(ctx, id, func(current Heartbeat) *Transition {
		before = current
		after = revise(current, definition, now)
		return &Transition{Heartbeat: after}
	})
	if err != nil {
		return Heartbeat{}, err
	}
	s.logger.Info("heartbeat updated", "heartbeat_id", id, "name", after.Name, "previous_status", string(before.Status), "status", string(after.Status))
	s.notifyAfterEdit(ctx, before, after)
	s.changed(id)
	return s.store.GetHeartbeat(ctx, id)
}

// La machine est facultative ; donnée, elle doit exister, sinon la clé
// étrangère la refuserait en erreur brute. Elle passe avant le reste de la
// définition : c'est l'ordre que l'API a toujours rendu.
func (s *Service) checkMachineExists(ctx context.Context, machineID string) error {
	if machineID == "" {
		return nil
	}
	exists, err := s.store.MachineExists(ctx, machineID)
	if err != nil {
		return err
	}
	if !exists {
		return ErrMachineInvalid
	}
	return nil
}

// revise porte la définition sur le moniteur et recalcule l'échéance si le
// rythme a changé. Une échéance devenue passée n'est pas jugée ici :
// CheckDeadlines s'en charge au prochain contrôle, comme pour toute autre.
func revise(before Heartbeat, definition Definition, now time.Time) Heartbeat {
	after := before
	after.Name = strings.TrimSpace(definition.Name)
	after.MachineID = definition.MachineID
	after.Interval = definition.Interval
	after.Grace = definition.Grace
	rhythmChanged := after.Interval != before.Interval || after.Grace != before.Grace
	if !rhythmChanged || before.IsPaused() {
		return after
	}
	if before.Status == StatusLate {
		return reviseLate(after, now)
	}
	if !before.NextDeadlineAt.IsZero() {
		delta := (after.Interval + after.Grace) - (before.Interval + before.Grace)
		after.NextDeadlineAt = before.NextDeadlineAt.Add(delta)
	}
	return after
}

// Une tâche en retard n'a plus d'échéance. Si le nouveau rythme laisse
// encore du temps depuis son dernier signe de vie, elle n'est plus en
// retard : l'échéance qu'elle aurait eue avec ce rythme revient.
func reviseLate(late Heartbeat, now time.Time) Heartbeat {
	lastSign := late.LastPingAt
	if late.NeverPinged() {
		lastSign = late.CreatedAt
	}
	deadline := lastSign.Add(late.Interval + late.Grace)
	if !deadline.After(now) {
		return late
	}
	late.NextDeadlineAt = deadline
	late.Status = restoredStatus(late)
	return late
}

// restoredStatus est l'état d'une tâche qui n'est plus en retard : celui
// que son dernier ping lui avait laissé.
func restoredStatus(heartbeat Heartbeat) Status {
	if heartbeat.NeverPinged() {
		return StatusNew
	}
	if heartbeat.LastExitCode != nil && *heartbeat.LastExitCode != 0 {
		return StatusFailed
	}
	return StatusOnTime
}

// notifyAfterEdit tient les alertes au courant : une tâche encore en retard
// ou en échec voit son alerte renommée, une tâche sortie du retard la lève.
func (s *Service) notifyAfterEdit(ctx context.Context, before, after Heartbeat) {
	if s.alerter == nil {
		return
	}
	switch {
	case after.Status == StatusLate:
		s.alerter.Late(ctx, after)
	case after.Status == StatusFailed && after.LastExitCode != nil:
		s.alerter.Failed(ctx, after, *after.LastExitCode)
	case before.Status == StatusLate:
		s.alerter.Recovered(ctx, after)
	}
}

// RegenerateToken tire un nouveau jeton : l'ancienne URL de ping cesse
// aussitôt de répondre. L'état, l'échéance et l'historique ne bougent pas.
func (s *Service) RegenerateToken(ctx context.Context, id string) error {
	found, err := s.store.GetHeartbeat(ctx, id)
	if err != nil {
		return err
	}
	token, err := NewToken()
	if err != nil {
		return fmt.Errorf("generate token: %w", err)
	}
	if err := s.store.SetHeartbeatToken(ctx, id, token); err != nil {
		return err
	}
	s.logger.Info("heartbeat token regenerated", "heartbeat_id", id, "name", found.Name)
	s.changed(id)
	return nil
}

func (s *Service) List(ctx context.Context) ([]Heartbeat, error) {
	return s.store.ListHeartbeats(ctx)
}

func (s *Service) Get(ctx context.Context, id string) (Heartbeat, error) {
	return s.store.GetHeartbeat(ctx, id)
}

// Count renvoie le nombre de moniteurs et ceux à traiter : en retard ou en
// échec.
func (s *Service) Count(ctx context.Context) (total, attention int, err error) {
	return s.store.CountHeartbeats(ctx)
}

// NeedsAttention dit si l'état compte parmi ce que l'opérateur doit voir.
func NeedsAttention(status Status) bool {
	return status == StatusLate || status == StatusFailed
}

func (s *Service) Delete(ctx context.Context, id string) error {
	if err := s.store.DeleteHeartbeat(ctx, id); err != nil {
		return err
	}
	s.logger.Info("heartbeat deleted", "heartbeat_id", id)
	if s.alerter != nil {
		s.alerter.Removed(ctx, id)
	}
	s.changed(id)
	return nil
}

// Pause arrête la surveillance : plus d'échéance, plus de retard possible.
// Une exécution ouverte est close en dépassement, pas abandonnée.
func (s *Service) Pause(ctx context.Context, id string) error {
	now := s.now()
	var paused Heartbeat
	err := s.store.Transact(ctx, id, func(before Heartbeat) *Transition {
		after := before
		after.Status = StatusPaused
		after.NextDeadlineAt = time.Time{}
		after.RunStartedAt = time.Time{}
		paused = after
		return &Transition{Heartbeat: after, CloseRun: closeOpenRun(before, now, OutcomeTimeout)}
	})
	if err != nil {
		return err
	}
	s.logger.Info("heartbeat paused", "heartbeat_id", id)
	if s.alerter != nil {
		s.alerter.Paused(ctx, paused)
	}
	s.changed(id)
	return nil
}

// Resume rouvre une échéance complète depuis maintenant ; une tâche qui
// n'a jamais pingé redevient « Nouveau », avec cette échéance quand même.
func (s *Service) Resume(ctx context.Context, id string) error {
	now := s.now()
	notPaused := false
	err := s.store.Transact(ctx, id, func(before Heartbeat) *Transition {
		if !before.IsPaused() {
			notPaused = true
			return nil
		}
		after := before
		after.Status = StatusOnTime
		if before.NeverPinged() {
			after.Status = StatusNew
		}
		after.NextDeadlineAt = now.Add(before.Interval + before.Grace)
		return &Transition{Heartbeat: after}
	})
	if err != nil {
		return err
	}
	if notPaused {
		return ErrNotPaused
	}
	s.logger.Info("heartbeat resumed", "heartbeat_id", id)
	s.changed(id)
	return nil
}

func (s *Service) Pings(ctx context.Context, id string, limit int) ([]Ping, error) {
	return s.store.ListPings(ctx, id, limit)
}

func (s *Service) Runs(ctx context.Context, id string, limit int) ([]Run, error) {
	return s.store.ListRuns(ctx, id, limit)
}

// Receive applique un ping au moniteur de ce jeton : il repousse l'échéance
// et ouvre ou clôt une exécution. Un moniteur en pause note le ping, rien de
// plus : seule la reprise le remet en surveillance. Le corps est tronqué à
// MaxPayloadBytes.
func (s *Service) Receive(ctx context.Context, token string, ping Ping) (Heartbeat, error) {
	if ping.Kind == KindExitCode && (ping.ExitCode == nil || *ping.ExitCode < 0 || *ping.ExitCode > MaxExitCode) {
		return Heartbeat{}, ErrExitCodeInvalid
	}
	// Le jeton donne l'identifiant, qui ne change jamais ; l'état, lui, est
	// relu dans la transaction.
	found, err := s.store.GetHeartbeatByToken(ctx, token)
	if err != nil {
		return Heartbeat{}, err
	}
	now := s.now()
	ping.HeartbeatID = found.ID
	ping.ReceivedAt = now
	ping.Payload = truncate(ping.Payload)

	var before, after Heartbeat
	err = s.store.Transact(ctx, found.ID, func(current Heartbeat) *Transition {
		before = current
		transition := s.finishTransition(current, ping, now)
		if ping.Kind == KindStart {
			transition = s.startTransition(current, ping, now)
		}
		// Une tâche en pause garde le ping brut pour qu'on voie que le cron
		// tourne encore, sans que rien d'autre ne bouge : sinon un script
		// oublié la remettrait en surveillance, et ses alertes avec.
		if current.IsPaused() {
			transition = Transition{Heartbeat: current, Ping: &ping}
		}
		after = transition.Heartbeat
		return &transition
	})
	if err != nil {
		return Heartbeat{}, err
	}
	if before.IsPaused() {
		s.logger.Info("heartbeat ping while paused", "heartbeat_id", after.ID, "kind", string(ping.Kind), "source", ping.Source)
		s.changed(after.ID)
		return after, nil
	}
	s.logger.Info("heartbeat ping", "heartbeat_id", after.ID, "kind", string(ping.Kind), "status", string(after.Status), "source", ping.Source)
	s.notifyAfterPing(ctx, before, after, ping)
	s.changed(after.ID)
	return after, nil
}

// Un start clôt en dépassement l'exécution restée ouverte, puis en ouvre une.
func (s *Service) startTransition(before Heartbeat, ping Ping, now time.Time) Transition {
	after := before
	after.Status = StatusStarted
	after.LastPingAt = now
	after.NextDeadlineAt = now.Add(before.Interval + before.Grace)
	after.RunStartedAt = now
	return Transition{
		Heartbeat: after,
		Ping:      &ping,
		CloseRun:  closeOpenRun(before, now, OutcomeTimeout),
		NewRun:    &Run{HeartbeatID: before.ID, StartedAt: now, Outcome: OutcomeInProgress},
	}
}

// Une fin clôt l'exécution ouverte par un start, avec sa durée, ou en
// insère une déjà close quand la tâche n'a envoyé que sa fin.
func (s *Service) finishTransition(before Heartbeat, ping Ping, now time.Time) Transition {
	after := before
	after.Status = StatusOnTime
	after.LastPingAt = now
	after.NextDeadlineAt = now.Add(before.Interval + before.Grace)
	after.RunStartedAt = time.Time{}
	after.LastExitCode = ping.ExitCode
	// Le dernier résultat est celui de cette exécution : sans start, pas de durée.
	after.LastDuration = nil

	run := Run{HeartbeatID: before.ID, CompletedAt: now, ExitCode: ping.ExitCode, Outcome: OutcomeSuccess, Payload: ping.Payload}
	if ping.ExitCode != nil && *ping.ExitCode != 0 {
		run.Outcome = OutcomeFailure
		after.Status = StatusFailed
	}
	transition := Transition{Heartbeat: after, Ping: &ping}
	if before.RunStartedAt.IsZero() {
		transition.NewRun = &run
		return transition
	}
	duration := now.Sub(before.RunStartedAt)
	run.StartedAt = before.RunStartedAt
	run.Duration = &duration
	after.LastDuration = &duration
	transition.Heartbeat = after
	transition.CloseRun = &run
	return transition
}

func (s *Service) notifyAfterPing(ctx context.Context, before, after Heartbeat, ping Ping) {
	if s.alerter == nil {
		return
	}
	if after.Status == StatusFailed {
		s.alerter.Failed(ctx, after, *ping.ExitCode)
		return
	}
	if after.Status == StatusOnTime && mayHaveNeededAttention(before.Status) {
		s.alerter.Recovered(ctx, after)
	}
}

// Une fin réussie lève ce qui était ouvert. Après un start, l'état d'avant
// est « Démarré » : il ne dit plus si la tâche était en retard ou en échec.
// On prévient quand même, le moteur des alertes lève ce qu'il trouve et rien
// sinon.
func mayHaveNeededAttention(status Status) bool {
	return NeedsAttention(status) || status == StatusStarted
}

// CheckDeadlines passe en retard tout moniteur dont l'échéance est dépassée
// et clôt en dépassement son exécution ouverte. La liste n'est qu'une
// présélection : l'échéance est revérifiée dans la transaction, un ping
// arrivé entre-temps l'a peut-être repoussée.
func (s *Service) CheckDeadlines(ctx context.Context) error {
	now := s.now()
	candidates, err := s.store.ListOverdueHeartbeats(ctx, now)
	if err != nil {
		return err
	}
	for _, candidate := range candidates {
		if err := s.expire(ctx, candidate.ID, now); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) expire(ctx context.Context, id string, now time.Time) error {
	var before, after Heartbeat
	expired := false
	err := s.store.Transact(ctx, id, func(current Heartbeat) *Transition {
		if current.NextDeadlineAt.IsZero() || !current.NextDeadlineAt.Before(now) {
			return nil
		}
		before = current
		after = current
		after.Status = StatusLate
		after.NextDeadlineAt = time.Time{}
		after.RunStartedAt = time.Time{}
		expired = true
		return &Transition{Heartbeat: after, CloseRun: closeOpenRun(current, now, OutcomeTimeout)}
	})
	if err != nil || !expired {
		return err
	}
	s.logger.Warn("heartbeat late", "heartbeat_id", before.ID, "name", before.Name, "previous_status", string(before.Status))
	if s.alerter != nil {
		s.alerter.Late(ctx, after)
	}
	s.changed(after.ID)
	return nil
}

// BackfillFirstDeadlines donne leur première échéance aux tâches créées
// avant qu'elles n'en aient une dès la création : nouvelles, jamais
// pingées, sans échéance. Celle-ci part de la création ; déjà dépassée,
// le contrôle suivant les passe en retard. Rejouée à chaque démarrage,
// elle ne trouve plus rien à faire.
func (s *Service) BackfillFirstDeadlines(ctx context.Context) error {
	heartbeats, err := s.store.ListHeartbeats(ctx)
	if err != nil {
		return err
	}
	for _, found := range heartbeats {
		if found.Status != StatusNew || !found.NeverPinged() || !found.NextDeadlineAt.IsZero() {
			continue
		}
		err := s.store.Transact(ctx, found.ID, func(current Heartbeat) *Transition {
			if current.Status != StatusNew || !current.NeverPinged() || !current.NextDeadlineAt.IsZero() {
				return nil
			}
			after := current
			after.NextDeadlineAt = current.CreatedAt.Add(current.Interval + current.Grace)
			return &Transition{Heartbeat: after}
		})
		if err != nil {
			return fmt.Errorf("backfill first deadline: %w", err)
		}
		s.logger.Info("heartbeat first deadline backfilled", "heartbeat_id", found.ID, "name", found.Name)
		s.changed(found.ID)
	}
	return nil
}

// Purge efface le brut et les exécutions au-delà de leur rétention.
func (s *Service) Purge(ctx context.Context) error {
	now := s.now()
	return s.store.PurgeHistory(ctx, now.Add(-PingRetention), now.Add(-RunRetention))
}

// Watch est la boucle de fond : le rattrapage des premières échéances au
// départ, les échéances toutes les 15 s, la purge au départ puis une fois
// par jour. Elle s'arrête avec le contexte.
func (s *Service) Watch(ctx context.Context) {
	if err := s.BackfillFirstDeadlines(ctx); err != nil {
		s.logger.Error("backfill heartbeat first deadlines", "error", err)
	}
	if err := s.Purge(ctx); err != nil {
		s.logger.Warn("purge heartbeat history", "error", err)
	}
	check := time.NewTicker(checkPeriod)
	defer check.Stop()
	purge := time.NewTicker(purgePeriod)
	defer purge.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-check.C:
			if err := s.CheckDeadlines(ctx); err != nil {
				s.logger.Error("check heartbeat deadlines", "error", err)
			}
		case <-purge.C:
			if err := s.Purge(ctx); err != nil {
				s.logger.Warn("purge heartbeat history", "error", err)
			}
		}
	}
}

// truncate coupe à MaxPayloadBytes sans laisser une rune UTF-8 entamée.
func truncate(payload string) string {
	if len(payload) > MaxPayloadBytes {
		payload = payload[:MaxPayloadBytes]
	}
	for len(payload) > 0 && !utf8.ValidString(payload) {
		payload = payload[:len(payload)-1]
	}
	return payload
}
