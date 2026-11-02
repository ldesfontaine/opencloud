package agent

import (
	"context"
	"log/slog"
	"time"

	"github.com/ldesfontaine/opencloud/internal/systemdunits"
	"github.com/ldesfontaine/opencloud/internal/wait"
)

// Les unités systemd se relisent chaque minute : un binaire qui tombe ou qui
// redémarre se voit vite. Lecture seule, aucun droit demandé : systemctl liste
// les unités à tout compte. Le rapport ne part que s'il a changé.
const unitsPeriod = time.Minute

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
