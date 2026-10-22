package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"os"
	"sync"
	"time"

	"github.com/ldesfontaine/opencloud/internal/sshkeys"
	"github.com/ldesfontaine/opencloud/internal/wait"
)

// Le relevé des clés SSH autorisées se refait toutes les dix minutes : une clé
// ajoutée ou retirée à la main se voit sans que l'agent ait rien d'autre à
// surveiller. Il lit seulement ; rien n'est écrit sur la machine.
const (
	sshKeysPeriod = 10 * time.Minute
	passwdPath    = "/etc/passwd"
)

// sshReports garde le dernier relevé et dit s'il reste à livrer : il part
// quand il a changé, et une fois à chaque ouverture du flux (le serveur garde
// un reflet, un flux neuf le lui redonne).
type sshReports struct {
	mu        sync.Mutex
	latest    *sshkeys.Report
	latestSum string
	// delivered est l'empreinte du relevé que le dernier signal a emporté ;
	// vide, le relevé repart.
	delivered string
}

// update garde le relevé et dit s'il diffère du précédent.
func (r *sshReports) update(report sshkeys.Report) bool {
	sum := sshReportDigest(report)
	r.mu.Lock()
	defer r.mu.Unlock()
	changed := r.latest == nil || r.latestSum != sum
	r.latest, r.latestSum = &report, sum
	return changed
}

// take rend le relevé s'il n'est pas encore parti, nil sinon.
func (r *sshReports) take() *sshkeys.Report {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.latest == nil || r.latestSum == r.delivered {
		return nil
	}
	r.delivered = r.latestSum
	return r.latest
}

// restore remet à livrer ce qu'un signal n'a pas pu emporter.
func (r *sshReports) restore(report *sshkeys.Report) {
	if report == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.delivered = ""
}

// markReplayed : un flux neuf redit le relevé.
func (r *sshReports) markReplayed() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.delivered = ""
}

// sshReportDigest est l'empreinte d'un relevé, pour savoir s'il a changé.
func sshReportDigest(report sshkeys.Report) string {
	encoded, err := json.Marshal(report)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:])
}

// sshKeysLoop relève les clés au démarrage puis à chaque période, jusqu'à
// l'arrêt de l'agent. Le relevé vit à part du flux : une coupure ne l'arrête pas.
func sshKeysLoop(ctx context.Context, collect func() (sshkeys.Report, bool), reports *sshReports, period time.Duration, logger *slog.Logger) {
	for {
		if report, ok := collect(); ok && reports.update(report) {
			logger.Info("ssh keys read", "accounts", len(report.Accounts), "keys", countSSHKeys(report), "unreadable", countUnreadable(report))
		}
		if !wait.For(ctx, period) {
			return
		}
	}
}

func countSSHKeys(report sshkeys.Report) int {
	total := 0
	for _, account := range report.Accounts {
		total += len(account.Keys)
	}
	return total
}

// countUnreadable compte les comptes dont l'agent n'a pas pu lire les clés :
// c'est ce que le compte de l'agent ne lui laisse pas voir.
func countUnreadable(report sshkeys.Report) int {
	total := 0
	for _, account := range report.Accounts {
		if account.State == sshkeys.StateDenied {
			total++
		}
	}
	return total
}

// readSSHKeys lit la vraie machine : passwd, puis le dossier personnel de
// chaque compte. Faux si passwd ne s'ouvre pas : le dernier relevé reste.
func readSSHKeys() (sshkeys.Report, bool) {
	passwd, err := os.Open(passwdPath)
	if err != nil {
		return sshkeys.Report{}, false
	}
	defer passwd.Close()
	return sshkeys.ReadAll(passwd, sshkeys.OpenHome), true
}
