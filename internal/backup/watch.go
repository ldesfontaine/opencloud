package backup

import (
	"context"
	"errors"
	"time"

	"github.com/ldesfontaine/opencloud/internal/audit"
	"github.com/ldesfontaine/opencloud/internal/refusal"
)

// Watch est la boucle de fond : toutes les quinze minutes, le plan est-il
// dû ? Un serveur arrêté à 3 h rattrape à son retour.
func (s *Service) Watch(ctx context.Context) {
	ticker := time.NewTicker(watchPeriod)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.RunIfDue(ctx)
		}
	}
}

// RunIfDue lance la sauvegarde si le plan est dû, sous l'acteur « backup » :
// c'est une règle du serveur qui l'a décidée, pas l'opérateur.
func (s *Service) RunIfDue(ctx context.Context) {
	plan, err := s.store.GetBackupPlan(ctx)
	if errors.Is(err, ErrNotFound) {
		return
	}
	if err != nil {
		s.logger.Error("read backup plan", "error", err)
		return
	}
	if !due(s.now(), plan) {
		return
	}
	if _, err := s.run(audit.WithActor(ctx, audit.Rule("backup"))); err != nil && ctx.Err() == nil {
		s.logRun(err)
	}
}

func (s *Service) logRun(err error) {
	if _, isRefusal := refusal.KeyOf(err); isRefusal {
		s.logger.Warn("nightly backup refused", "error", err)
		return
	}
	s.logger.Error("nightly backup failed", "error", err)
}

// due dit si la nuit doit lancer la sauvegarde : une tentative par nuit,
// la première à partir de 3 h (heure du serveur). Le plan est dû quand
// aucune tentative n'a eu lieu depuis le dernier 3 h passé, y compris celui
// d'hier si on n'y est pas encore : un serveur éteint à 3 h rattrape.
func due(now time.Time, plan Plan) bool {
	year, month, day := now.Date()
	threshold := time.Date(year, month, day, dailyAtHour, 0, 0, 0, now.Location())
	if now.Before(threshold) {
		threshold = time.Date(year, month, day-1, dailyAtHour, 0, 0, 0, now.Location())
	}
	return plan.LastAttemptAt.Before(threshold)
}
