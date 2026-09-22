package resource

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/ldesfontaine/opencloud/internal/sampler"
)

const (
	rollupPeriod = 5 * time.Minute
	purgePeriod  = 24 * time.Hour
)

// Ce que le service attend de la base ; le package store le fournit.
type Store interface {
	// InsertSamples écrit un lot ; un échantillon déjà en base, rejoué après
	// une coupure, est ignoré sans erreur.
	InsertSamples(ctx context.Context, machineID string, readings []sampler.Reading) error
	LatestSample(ctx context.Context, machineID string) (Sample, error)
	LatestSamples(ctx context.Context) ([]Sample, error)
	ListSamples(ctx context.Context, machineID string, from, to time.Time, step time.Duration) ([]sampler.Reading, error)
	ListHourly(ctx context.Context, machineID string, from, to time.Time) ([]sampler.Reading, error)
	ListDaily(ctx context.Context, machineID string, from, to time.Time) ([]sampler.Reading, error)
	// RollupHourly et RollupDaily agrègent un seau en une instruction
	// idempotente : repasser sur un seau donne le même résultat.
	RollupHourly(ctx context.Context, bucketStart, bucketEnd time.Time) error
	RollupDaily(ctx context.Context, bucketStart, bucketEnd time.Time) error
	PurgeSamples(ctx context.Context, rawBefore, hourlyBefore, dailyBefore time.Time) error
}

// Watcher reçoit chaque écriture visible : le direct s'y branche. nil est
// toléré.
type Watcher interface {
	ResourcesChanged(machineID string)
}

// Alerter reçoit la dernière lecture de chaque lot écrit, avec ses
// volumes : le moteur des alertes en fait un disque presque plein. nil
// est toléré.
type Alerter interface {
	ReadingRecorded(ctx context.Context, machineID string, reading sampler.Reading)
}

type Service struct {
	store   Store
	watcher Watcher
	alerter Alerter
	logger  *slog.Logger
	// now est remplaçable dans les tests : la fraîcheur se compare à lui.
	now func() time.Time
}

func New(store Store, logger *slog.Logger) *Service {
	return &Service{store: store, logger: logger, now: time.Now}
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

// Record écrit ce qu'une machine a mesuré. Le lot entier est refusé dès
// qu'une lecture est hors de ce qu'un agent peut mesurer.
func (s *Service) Record(ctx context.Context, machineID string, readings []sampler.Reading) error {
	if len(readings) == 0 {
		return nil
	}
	if len(readings) > MaxReadingsPerSignal {
		return ErrTooManyReadings
	}
	now := s.now()
	for _, reading := range readings {
		if err := validate(reading, now); err != nil {
			return err
		}
	}
	if err := s.store.InsertSamples(ctx, machineID, readings); err != nil {
		return err
	}
	if s.alerter != nil {
		s.alerter.ReadingRecorded(ctx, machineID, readings[len(readings)-1])
	}
	if s.watcher != nil {
		s.watcher.ResourcesChanged(machineID)
	}
	return nil
}

// Current rend le dernier échantillon d'une machine, disponible s'il a
// moins de StaleAfter ; sans échantillon, rien du tout, jamais des zéros.
func (s *Service) Current(ctx context.Context, machineID string) (Current, error) {
	sample, err := s.store.LatestSample(ctx, machineID)
	if errors.Is(err, ErrNoSample) {
		return Current{MachineID: machineID}, nil
	}
	if err != nil {
		return Current{}, err
	}
	return s.current(sample), nil
}

// CurrentAll rend la valeur courante de chaque machine qui a déjà mesuré.
func (s *Service) CurrentAll(ctx context.Context) ([]Current, error) {
	samples, err := s.store.LatestSamples(ctx)
	if err != nil {
		return nil, err
	}
	currents := make([]Current, 0, len(samples))
	for _, sample := range samples {
		currents = append(currents, s.current(sample))
	}
	return currents, nil
}

func (s *Service) current(sample Sample) Current {
	fresh := s.now().Sub(sample.SampledAt) <= StaleAfter
	return Current{MachineID: sample.MachineID, Available: fresh, Sample: &sample}
}

// History lit une fenêtre : le brut groupé par le pas de la fenêtre, ou
// les seaux déjà agrégés.
func (s *Service) History(ctx context.Context, machineID, windowName string) (Window, []sampler.Reading, error) {
	window, ok := WindowByName(windowName)
	if !ok {
		return Window{}, nil, ErrWindowUnknown
	}
	now := s.now()
	from := now.Add(-window.Span)
	var points []sampler.Reading
	var err error
	switch window.Source {
	case SourceHourly:
		points, err = s.store.ListHourly(ctx, machineID, from, now)
	case SourceDaily:
		points, err = s.store.ListDaily(ctx, machineID, from, now)
	default:
		points, err = s.store.ListSamples(ctx, machineID, from, now, window.Step)
	}
	if err != nil {
		return Window{}, nil, err
	}
	return window, points, nil
}

// Rollup rejoue tous les seaux horaires que le brut couvre encore, puis les
// seaux journaliers : sans curseur, un redémarrage ou un rattrapage se
// corrige seul. Le seau en cours est inclus, l'historique long voit donc
// l'heure et le jour qui avancent.
func (s *Service) Rollup(ctx context.Context) error {
	now := s.now().UTC()
	firstHour := now.Add(-RawRetention).Truncate(time.Hour)
	for bucket := firstHour; !bucket.After(now); bucket = bucket.Add(time.Hour) {
		if err := s.store.RollupHourly(ctx, bucket, bucket.Add(time.Hour)); err != nil {
			return fmt.Errorf("rollup hourly %s: %w", bucket.Format(time.RFC3339), err)
		}
	}
	firstDay := startOfDay(now.Add(-RawRetention))
	for bucket := firstDay; !bucket.After(now); bucket = bucket.Add(24 * time.Hour) {
		if err := s.store.RollupDaily(ctx, bucket, bucket.Add(24*time.Hour)); err != nil {
			return fmt.Errorf("rollup daily %s: %w", bucket.Format(time.RFC3339), err)
		}
	}
	return nil
}

func startOfDay(at time.Time) time.Time {
	return time.Date(at.Year(), at.Month(), at.Day(), 0, 0, 0, 0, time.UTC)
}

// Purge efface chaque étage au-delà de sa rétention.
func (s *Service) Purge(ctx context.Context) error {
	now := s.now()
	return s.store.PurgeSamples(ctx, now.Add(-RawRetention), now.Add(-HourlyRetention), now.Add(-DailyRetention))
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
		s.logger.Error("rollup samples", "error", err)
	}
	if !withPurge {
		return
	}
	if err := s.Purge(ctx); err != nil && ctx.Err() == nil {
		s.logger.Warn("purge samples", "error", err)
	}
}

// SampleLocal mesure la machine openCloud elle-même, à la même cadence que
// l'agent, et écrit sans passer par le réseau : c'est son rôle d'agent.
func (s *Service) SampleLocal(ctx context.Context, machineID string, probe *sampler.Sampler, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	s.sampleOnce(ctx, machineID, probe)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.sampleOnce(ctx, machineID, probe)
		}
	}
}

func (s *Service) sampleOnce(ctx context.Context, machineID string, probe *sampler.Sampler) {
	reading, ok, err := probe.Sample(s.now())
	if err != nil {
		s.logger.Warn("sample local machine", "error", err)
		return
	}
	if !ok {
		return
	}
	if err := s.Record(ctx, machineID, []sampler.Reading{reading}); err != nil && ctx.Err() == nil {
		s.logger.Error("record local sample", "error", err)
	}
}
