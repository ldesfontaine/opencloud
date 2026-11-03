package agent

import (
	"context"
	"log/slog"
	"reflect"
	"sync"
	"time"

	"github.com/ldesfontaine/opencloud/internal/agentwire"
	"github.com/ldesfontaine/opencloud/internal/wait"
)

// La section restic se relit toutes les dix minutes, et dès qu'un ordre ou
// rest-server la change ; pas plus d'une fois par resticReportGap, car elle
// parcourt le dossier des dépôts et lance deux binaires.
const (
	resticPeriod    = 10 * time.Minute
	resticReportGap = 10 * time.Second
)

// resticReports garde le dernier rapport et dit s'il reste à livrer, comme
// roleReports : quand il a changé, et à chaque ouverture du flux.
type resticReports struct {
	mu          sync.Mutex
	latest      *agentwire.ResticReport
	isDelivered bool
}

func (r *resticReports) update(report agentwire.ResticReport) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	changed := r.latest == nil || !reflect.DeepEqual(*r.latest, report)
	if changed {
		r.isDelivered = false
	}
	r.latest = &report
	return changed
}

func (r *resticReports) take() *agentwire.ResticReport {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.latest == nil || r.isDelivered {
		return nil
	}
	r.isDelivered = true
	return r.latest
}

func (r *resticReports) restore(report *agentwire.ResticReport) {
	if report == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.isDelivered = false
}

func (r *resticReports) markReplayed() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.isDelivered = false
}

// resticLoop relit la section au démarrage, à chaque période et à chaque
// changement signalé, jusqu'à l'arrêt de l'agent. Un rapport hors bornes
// n'est pas envoyé : le serveur le refuserait en entier.
func resticLoop(ctx context.Context, orders *orderRunner, reports *resticReports, period time.Duration, logger *slog.Logger) {
	for {
		report, hasRestic := orders.resticReport(ctx)
		switch {
		case !hasRestic:
		case report.Validate() != nil:
			logger.Warn("restic report out of bounds, not sent", "error", report.Validate())
		case reports.update(report):
			logger.Info("restic report read")
		}
		timer := time.NewTimer(period)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		case <-orders.restic.changed:
			timer.Stop()
			if !wait.For(ctx, resticReportGap) {
				return
			}
		}
	}
}
