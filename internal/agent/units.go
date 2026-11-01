package agent

import (
	"context"
	"log/slog"
	"reflect"
	"sync"
	"time"

	"github.com/ldesfontaine/opencloud/internal/systemdunits"
	"github.com/ldesfontaine/opencloud/internal/wait"
)

// Les unités systemd se relisent chaque minute : un binaire qui tombe ou qui
// redémarre se voit vite. Lecture seule, aucun droit demandé : systemctl liste
// les unités à tout compte. Le rapport ne part que s'il a changé.
const unitsPeriod = time.Minute

// unitReports garde le dernier relevé et dit s'il reste à livrer, comme
// hostReports : il part quand il a changé, et une fois à chaque ouverture du
// flux (le serveur garde un reflet, un flux neuf le lui redonne).
type unitReports struct {
	mu     sync.Mutex
	latest *systemdunits.Report
	// isDelivered : le dernier signal a emporté le relevé courant.
	isDelivered bool
}

// update garde le relevé et dit s'il diffère du précédent.
func (u *unitReports) update(report systemdunits.Report) bool {
	u.mu.Lock()
	defer u.mu.Unlock()
	changed := u.latest == nil || !reflect.DeepEqual(*u.latest, report)
	if changed {
		u.isDelivered = false
	}
	u.latest = &report
	return changed
}

// take rend le relevé s'il n'est pas encore parti, nil sinon.
func (u *unitReports) take() *systemdunits.Report {
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.latest == nil || u.isDelivered {
		return nil
	}
	u.isDelivered = true
	return u.latest
}

// restore remet à livrer ce qu'un signal n'a pas pu emporter.
func (u *unitReports) restore(report *systemdunits.Report) {
	if report == nil {
		return
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	u.isDelivered = false
}

// markReplayed : un flux neuf redit le relevé.
func (u *unitReports) markReplayed() {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.isDelivered = false
}

// unitsLoop relève les unités au démarrage puis à chaque période, jusqu'à
// l'arrêt de l'agent. Le relevé vit à part du flux : une coupure ne l'arrête
// pas. Le journal ne dit que des comptes, jamais un nom ni un état.
func unitsLoop(ctx context.Context, read func(context.Context) systemdunits.Report, reports *unitReports, period time.Duration, logger *slog.Logger) {
	for {
		report := read(ctx)
		if reports.update(report) {
			logger.Info("systemd units read", "available", report.Available, "units", len(report.Units))
		}
		if !wait.For(ctx, period) {
			return
		}
	}
}

// readUnits lit la vraie machine.
func readUnits(ctx context.Context) systemdunits.Report {
	return systemdunits.Read(ctx, systemdunits.Exec)
}
