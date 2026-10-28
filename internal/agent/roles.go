package agent

import (
	"context"
	"log/slog"
	"os"
	"reflect"
	"sync"
	"time"

	"github.com/ldesfontaine/opencloud/internal/role"
	"github.com/ldesfontaine/opencloud/internal/wait"
)

// Ce que la machine est (un hôte Proxmox VE) se relit toutes les dix minutes :
// un paquet installé ou retiré se voit sans que l'agent ait rien d'autre à
// surveiller. Lecture seule, aucun droit demandé.
const rolesPeriod = 10 * time.Minute

// roleReports garde le dernier rapport et dit s'il reste à livrer : il part
// quand il a changé, et une fois à chaque ouverture du flux (le serveur garde
// un reflet, un flux neuf le lui redonne). Un rapport vide part aussi : c'est
// lui qui efface un rôle que la machine a perdu.
type roleReports struct {
	mu     sync.Mutex
	latest *role.Report
	// isDelivered : le dernier signal a emporté le rapport courant.
	isDelivered bool
}

// update garde le rapport et dit s'il diffère du précédent.
func (r *roleReports) update(report role.Report) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	changed := r.latest == nil || !reflect.DeepEqual(*r.latest, report)
	if changed {
		r.isDelivered = false
	}
	r.latest = &report
	return changed
}

// take rend le rapport s'il n'est pas encore parti, nil sinon.
func (r *roleReports) take() *role.Report {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.latest == nil || r.isDelivered {
		return nil
	}
	r.isDelivered = true
	return r.latest
}

// restore remet à livrer ce qu'un signal n'a pas pu emporter.
func (r *roleReports) restore(report *role.Report) {
	if report == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.isDelivered = false
}

// markReplayed : un flux neuf redit le rapport.
func (r *roleReports) markReplayed() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.isDelivered = false
}

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
