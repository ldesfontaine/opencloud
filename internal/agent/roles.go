package agent

import (
	"context"
	"log/slog"
	"os"
	"time"

	"github.com/ldesfontaine/opencloud/internal/role"
	"github.com/ldesfontaine/opencloud/internal/wait"
)

// Ce que la machine est (un hôte Proxmox VE) se relit toutes les dix minutes :
// un paquet installé ou retiré se voit sans que l'agent ait rien d'autre à
// surveiller. Lecture seule, aucun droit demandé.
const rolesPeriod = 10 * time.Minute

// rolesLoop relit la machine au démarrage puis à chaque période, jusqu'à
// l'arrêt de l'agent. La lecture vit à part du flux : une coupure ne l'arrête pas.
func rolesLoop(ctx context.Context, detect func() role.Report, reports *roleReports, period time.Duration, logger *slog.Logger) {
	for {
		report := detect()
		if reports.update(report) {
			logger.Info("machine roles read", "roles", len(report.Roles))
		}
		if !wait.For(ctx, period) {
			return
		}
	}
}

// detectRoles lit la vraie machine, depuis sa racine.
func detectRoles() role.Report {
	return role.Detect(os.DirFS("/"))
}
