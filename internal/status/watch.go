package status

import (
	"context"
	"time"

	"github.com/ldesfontaine/opencloud/internal/live"
)

const (
	// Les fenêtres de maintenance s'ouvrent et se ferment à la minute près,
	// et un certificat franchit son seuil sans qu'aucun sujet ne le dise.
	tickPeriod  = 30 * time.Second
	purgePeriod = 24 * time.Hour
)

// Watch tient la page à jour : relit à chaque sujet du bus interne, ouvre
// et ferme les maintenances à l'heure, purge une fois par jour.
func (s *Service) Watch(ctx context.Context) {
	if err := s.Purge(ctx); err != nil {
		s.logger.Warn("purge incidents", "error", err)
	}
	s.Refresh(ctx)
	topics := s.listen(ctx)
	tick := time.NewTicker(tickPeriod)
	defer tick.Stop()
	purge := time.NewTicker(purgePeriod)
	defer purge.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			s.ApplyWindows(ctx)
			s.Refresh(ctx)
		case <-purge.C:
			if err := s.Purge(ctx); err != nil {
				s.logger.Warn("purge incidents", "error", err)
			}
		case _, open := <-topics:
			if !open {
				return
			}
			s.Refresh(ctx)
		}
	}
}

// listen rend un canal qui se réveille à chaque lot de sujets, sauf quand
// le lot ne porte que « status », qui est le nôtre.
func (s *Service) listen(ctx context.Context) <-chan struct{} {
	wake := make(chan struct{})
	if s.feed == nil {
		return wake
	}
	subscription := s.feed.Subscribe()
	go func() {
		defer close(wake)
		defer subscription.Close()
		for {
			batch, err := subscription.Next(ctx)
			if err != nil {
				return
			}
			if onlyStatus(batch) {
				continue
			}
			select {
			case wake <- struct{}{}:
			case <-ctx.Done():
				return
			}
		}
	}()
	return wake
}

func onlyStatus(batch []live.Topic) bool {
	for _, topic := range batch {
		if topic != live.TopicStatus {
			return false
		}
	}
	return true
}
