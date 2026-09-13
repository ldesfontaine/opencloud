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
	CountHeartbeats(ctx context.Context) (total, attention int, err error)
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

// Listener reçoit ce qui mérite une alerte ou un direct. Les fonctionnalités
// alertes et direct s'y brancheront ; nil est toléré.
type Listener interface {
	Late(heartbeat Heartbeat)
	Recovered(heartbeat Heartbeat)
	Failed(heartbeat Heartbeat, exitCode int)
}

// Watcher reçoit chaque changement d'un moniteur, quel qu'il soit : le
// direct s'y branche. Listener ne reçoit que ce qui mérite une alerte.
// nil est toléré.
type Watcher interface {
	HeartbeatChanged(heartbeatID string)
}

type Service struct {
	store    Store
	listener Listener
	watcher  Watcher
	logger   *slog.Logger
	// now est remplaçable dans les tests : les échéances se comparent à lui.
	now func() time.Time
}

func New(store Store, logger *slog.Logger) *Service {
	return &Service{store: store, logger: logger, now: time.Now}
}

func (s *Service) SetClock(now func() time.Time) {
	s.now = now
}

func (s *Service) SetListener(listener Listener) {
	s.listener = listener
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
// « Nouveau », sans échéance tant qu'aucun ping n'est arrivé.
func (s *Service) Create(ctx context.Context, definition Definition) (Heartbeat, error) {
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
	heartbeat := Heartbeat{
		ID:        id,
		Token:     token,
		Name:      strings.TrimSpace(definition.Name),
		MachineID: definition.MachineID,
		Status:    StatusNew,
		Interval:  definition.Interval,
		Grace:     definition.Grace,
		CreatedAt: s.now(),
	}
	if err := s.store.InsertHeartbeat(ctx, heartbeat); err != nil {
		return Heartbeat{}, err
	}
	s.logger.Info("heartbeat created", "heartbeat_id", id, "name", heartbeat.Name)
	s.changed(id)
	return s.store.GetHeartbeat(ctx, id)
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
	s.changed(id)
	return nil
}

// Pause arrête la surveillance : plus d'échéance, plus de retard possible.
// Une exécution ouverte est close en dépassement, pas abandonnée.
func (s *Service) Pause(ctx context.Context, id string) error {
	now := s.now()
	err := s.store.Transact(ctx, id, func(before Heartbeat) *Transition {
		after := before
		after.Status = StatusPaused
		after.NextDeadlineAt = time.Time{}
		after.RunStartedAt = time.Time{}
		return &Transition{Heartbeat: after, CloseRun: closeOpenRun(before, now, OutcomeTimeout)}
	})
	if err != nil {
		return err
	}
	s.logger.Info("heartbeat paused", "heartbeat_id", id)
	s.changed(id)
	return nil
}

// Resume rouvre une échéance complète depuis maintenant.
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

// Receive applique un ping au moniteur de ce jeton : il repousse l'échéance,
// ouvre ou clôt une exécution, et reprend un moniteur en pause. Le corps est
// tronqué à MaxPayloadBytes.
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
		after = transition.Heartbeat
		return &transition
	})
	if err != nil {
		return Heartbeat{}, err
	}
	s.logger.Info("heartbeat ping", "heartbeat_id", after.ID, "kind", string(ping.Kind), "status", string(after.Status), "source", ping.Source)
	s.notifyAfterPing(before, after, ping)
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

func (s *Service) notifyAfterPing(before, after Heartbeat, ping Ping) {
	if s.listener == nil {
		return
	}
	if after.Status == StatusFailed {
		s.listener.Failed(after, *ping.ExitCode)
		return
	}
	if NeedsAttention(before.Status) && after.Status == StatusOnTime {
		s.listener.Recovered(after)
	}
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
	if s.listener != nil {
		s.listener.Late(after)
	}
	s.changed(after.ID)
	return nil
}

// Purge efface le brut et les exécutions au-delà de leur rétention.
func (s *Service) Purge(ctx context.Context) error {
	now := s.now()
	return s.store.PurgeHistory(ctx, now.Add(-PingRetention), now.Add(-RunRetention))
}

// Watch est la boucle de fond : les échéances toutes les 15 s, la purge au
// départ puis une fois par jour. Elle s'arrête avec le contexte.
func (s *Service) Watch(ctx context.Context) {
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
