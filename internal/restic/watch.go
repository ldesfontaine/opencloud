package restic

import (
	"context"
	"sync"
	"time"

	"github.com/ldesfontaine/opencloud/internal/audit"
	"github.com/ldesfontaine/opencloud/internal/refusal"
)

const (
	watchPeriod = 15 * time.Minute
	// La nuit de restic commence à 2 h (heure du serveur) : avant celle des
	// plans par archives (3 h) et des tâches vzdump usuelles.
	dailyAtHour = 2
)

// Watch est la boucle de fond : toutes les quinze minutes, une sauvegarde
// est-elle due ? Un serveur arrêté à 2 h rattrape à son retour.
func (s *Service) Watch(ctx context.Context) {
	defer s.WaitBackground()
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

// RunIfDue mène un passage : les sauvegardes dues, une file par source, les
// sauvegardes d'une source l'une après l'autre (elles partagent un dépôt par
// cible), puis la rétention de chaque dépôt sauvegardé. Un seul passage à la
// fois ; celui qui arrive pendant qu'un autre
// court ne fait rien, le suivant rattrapera.
func (s *Service) RunIfDue(ctx context.Context) {
	if !s.passing.TryLock() {
		return
	}
	defer s.passing.Unlock()
	backups, err := s.store.ListResticBackups(ctx)
	if err != nil {
		s.logger.Error("list restic backups", "error", err)
		return
	}
	now := s.now()
	bySource := map[string][]Backup{}
	var sources []string
	for _, candidate := range backups {
		if !isDue(now, candidate.LastAttemptAt) {
			continue
		}
		if _, seen := bySource[candidate.SourceMachineID]; !seen {
			sources = append(sources, candidate.SourceMachineID)
		}
		bySource[candidate.SourceMachineID] = append(bySource[candidate.SourceMachineID], candidate)
	}
	// Une règle du serveur l'a décidé, pas l'opérateur.
	passCtx := audit.WithActor(ctx, audit.Rule("restic"))
	var lanes sync.WaitGroup
	var mu sync.Mutex
	saved := []Backup{}
	for _, source := range sources {
		lanes.Add(1)
		go func() {
			defer lanes.Done()
			succeeded := s.runLane(passCtx, bySource[source])
			mu.Lock()
			saved = append(saved, succeeded...)
			mu.Unlock()
		}()
	}
	lanes.Wait()
	// Les sauvegardes de la nuit finies, la cible purge chaque dépôt qui a
	// reçu un instantané.
	s.retainAfterNight(passCtx, saved)
}

// runLane mène les sauvegardes d'une source, l'une après l'autre, chacune
// jusqu'à sa fin ; un refus n'arrête pas les suivantes. Elle rend celles qui
// ont réussi.
func (s *Service) runLane(ctx context.Context, lane []Backup) []Backup {
	succeeded := []Backup{}
	for _, current := range lane {
		if ctx.Err() != nil {
			return succeeded
		}
		if !s.claimRepo(current.repo()) {
			continue
		}
		run, err := s.start(ctx, current)
		if err != nil {
			s.releaseRepo(current.repo())
			s.noteRefusal(ctx, current, err)
			continue
		}
		if s.follow(ctx, current, run) {
			succeeded = append(succeeded, current)
		}
		s.releaseRepo(current.repo())
	}
	return succeeded
}

// noteRefusal garde la tentative refusée avant tout ordre : la nuit est
// passée, la carte dit pourquoi, le passage suivant ne la rejoue pas.
func (s *Service) noteRefusal(ctx context.Context, current Backup, cause error) {
	key, isRefusal := refusal.KeyOf(cause)
	if !isRefusal {
		s.logger.Error("nightly restic backup failed", "backup_id", current.ID, "error", cause)
		key = reasonUnknown
	} else {
		s.logger.Warn("nightly restic backup refused", "backup_id", current.ID, "error", cause)
	}
	now := s.now()
	if err := s.store.NoteResticRunSent(ctx, current.ID, "", now); err != nil {
		s.logger.Error("note restic attempt", "backup_id", current.ID, "error", err)
		return
	}
	outcome := Outcome{State: StateRefused, Reason: key, FinishedAt: now}
	if err := s.store.RecordResticOutcome(ctx, current.ID, outcome); err != nil {
		s.logger.Error("record restic refusal", "backup_id", current.ID, "error", err)
	}
	s.changed()
}

// isDue : une tentative par nuit, la première à partir de 2 h. Aucune
// tentative depuis le dernier 2 h passé (celui d'hier avant 2 h) : due.
func isDue(now, lastAttempt time.Time) bool {
	return lastAttempt.Before(lastThreshold(now))
}

func lastThreshold(now time.Time) time.Time {
	year, month, day := now.Date()
	threshold := time.Date(year, month, day, dailyAtHour, 0, 0, 0, now.Location())
	if now.Before(threshold) {
		threshold = time.Date(year, month, day-1, dailyAtHour, 0, 0, 0, now.Location())
	}
	return threshold
}

// nextRun rend la prochaine tentative : maintenant si elle est due (le
// prochain passage la prendra), sinon le prochain 2 h.
func nextRun(now, lastAttempt time.Time) time.Time {
	if isDue(now, lastAttempt) {
		return now
	}
	return lastThreshold(now).AddDate(0, 0, 1)
}
