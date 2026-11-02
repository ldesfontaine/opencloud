package agent

import (
	"context"
	"log/slog"
	"time"

	"github.com/ldesfontaine/opencloud/internal/hostconn"
	"github.com/ldesfontaine/opencloud/internal/wait"
)

// La table des connexions de l'hôte se relit toutes les quinze secondes : un
// instantané manque ce qui dure moins que sa période, et un client qui revient
// toutes les minutes doit y tomber. Lecture seule de /proc/net/tcp, aucun
// droit demandé : tout compte la lit.
const connectionsPeriod = 15 * time.Second

// connectionsHeartbeat : un rapport inchangé repart quand même à cette cadence.
// Le serveur avance la dernière vue de ce que le rapport porte ; sans cela, une
// connexion qui dure passerait pour « ancienne ».
const connectionsHeartbeat = time.Minute

// connectionsLoop relit la table au démarrage puis à chaque période, jusqu'à
// l'arrêt de l'agent. La lecture vit à part du flux : une coupure ne l'arrête
// pas. Le journal ne dit que des comptes, jamais un port, un compte ou une
// adresse.
func connectionsLoop(ctx context.Context, read func() hostconn.Report, reports *connectionReports, period, heartbeat time.Duration, logger *slog.Logger) {
	lastDelivery := time.Now()
	for {
		report := read()
		switch {
		case reports.update(report):
			lastDelivery = time.Now()
			logger.Info("host connections read", "available", report.Available, "connections", len(report.Connections))
		case time.Since(lastDelivery) >= heartbeat:
			reports.markReplayed()
			lastDelivery = time.Now()
		}
		if !wait.For(ctx, period) {
			return
		}
	}
}

// readConnections lit la vraie machine.
func readConnections() hostconn.Report {
	return hostconn.Read(hostconn.Local())
}
