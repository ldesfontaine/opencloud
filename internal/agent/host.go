package agent

import (
	"context"
	"log/slog"
	"time"

	"github.com/ldesfontaine/opencloud/internal/hostinfo"
	"github.com/ldesfontaine/opencloud/internal/wait"
)

// Où tourne la machine et ses disques se relisent toutes les dix minutes : un
// disque ajouté ou une carte changée se voit sans que l'agent ait rien d'autre
// à surveiller. Lecture seule, aucun droit demandé.
const hostPeriod = 10 * time.Minute

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
