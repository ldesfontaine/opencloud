package agent

import (
	"context"
	"log/slog"
	"reflect"
	"sync"
	"time"

	"github.com/ldesfontaine/opencloud/internal/hostinfo"
	"github.com/ldesfontaine/opencloud/internal/wait"
)

// Où tourne la machine et ses disques se relisent toutes les dix minutes : un
// disque ajouté ou une carte changée se voit sans que l'agent ait rien d'autre
// à surveiller. Lecture seule, aucun droit demandé.
const hostPeriod = 10 * time.Minute

// hostReports garde le dernier rapport et dit s'il reste à livrer, comme
// roleReports : il part quand il a changé, et une fois à chaque ouverture du
// flux (le serveur garde un reflet, un flux neuf le lui redonne).
type hostReports struct {
	mu     sync.Mutex
	latest *hostinfo.Report
	// isDelivered : le dernier signal a emporté le rapport courant.
	isDelivered bool
}

// update garde le rapport et dit s'il diffère du précédent.
func (h *hostReports) update(report hostinfo.Report) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	changed := h.latest == nil || !reflect.DeepEqual(*h.latest, report)
	if changed {
		h.isDelivered = false
	}
	h.latest = &report
	return changed
}

// take rend le rapport s'il n'est pas encore parti, nil sinon.
func (h *hostReports) take() *hostinfo.Report {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.latest == nil || h.isDelivered {
		return nil
	}
	h.isDelivered = true
	return h.latest
}

// restore remet à livrer ce qu'un signal n'a pas pu emporter.
func (h *hostReports) restore(report *hostinfo.Report) {
	if report == nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.isDelivered = false
}

// markReplayed : un flux neuf redit le rapport.
func (h *hostReports) markReplayed() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.isDelivered = false
}

// hostLoop relit la machine au démarrage puis à chaque période, jusqu'à l'arrêt
// de l'agent. La lecture vit à part du flux : une coupure ne l'arrête pas.
func hostLoop(ctx context.Context, read func(context.Context) hostinfo.Report, reports *hostReports, period time.Duration, logger *slog.Logger) {
	for {
		report := read(ctx)
		if reports.update(report) {
			logger.Info("machine host read", "interfaces", len(report.Interfaces), "disks", len(report.Disks), "pools", len(report.Pools))
		}
		if !wait.For(ctx, period) {
			return
		}
	}
}
