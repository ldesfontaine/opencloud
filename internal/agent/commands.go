package agent

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"sync"

	"github.com/ldesfontaine/opencloud/internal/agentwire"
	"github.com/ldesfontaine/opencloud/internal/container"
	"github.com/ldesfontaine/opencloud/internal/probe"
	"github.com/ldesfontaine/opencloud/internal/update"
)

// commandRunner exécute ce que le serveur pousse par le flux : ouvrir et
// fermer un suivi de journal, remplacer le jeu de sondes, régler les
// vérifications d'images, et passer les ordres signés à leur juge. Chaque
// suivi vit dans sa propre goroutine et meurt avec le flux ; les sondes,
// les vérifications et les ordres, eux, survivent aux reconnexions.
type commandRunner struct {
	ctx     context.Context
	client  *Client
	session string
	logs    container.LogSource
	probes  probe.Assignable
	updates update.Assignable
	orders  *orderRunner
	logger  *slog.Logger

	mu   sync.Mutex
	open map[string]context.CancelFunc
}

func newCommandRunner(ctx context.Context, client *Client, session string, logs container.LogSource, probes probe.Assignable, updates update.Assignable, orders *orderRunner, logger *slog.Logger) *commandRunner {
	return &commandRunner{ctx: ctx, client: client, session: session, logs: logs, probes: probes, updates: updates, orders: orders, logger: logger, open: make(map[string]context.CancelFunc)}
}

// handle lit un événement du flux. Un ordre part à son juge sans être lu
// ici : son genre ne compte qu'une fois sa signature vérifiée. Les
// commandes d'observation, elles, ne sont pas signées : elles ne font que
// lire la machine.
func (r *commandRunner) handle(name, data string) {
	if name == agentwire.EventOrder {
		r.orders.receive([]byte(data))
		return
	}
	command, err := agentwire.DecodeObservation(name, []byte(data))
	if errors.Is(err, agentwire.ErrUnknownCommand) {
		r.logger.Warn("unknown command", "name", name)
		return
	}
	if err != nil {
		r.logger.Warn("bad command", "name", name)
		return
	}
	switch command.Kind {
	case agentwire.KindLogs:
		if command.Logs.ID == "" {
			r.logger.Warn("bad command", "name", name)
			return
		}
		r.startLogs(*command.Logs)
	case agentwire.KindLogsStop:
		r.stopLogs(command.Logs.ID)
	case agentwire.KindProbes:
		// Le jeu de sondes remplace le précédent : ce qui n'y est plus s'arrête.
		// L'agent rejuge le jeu : ce qu'il écarte, il le dit dans son journal.
		refused := r.probes.Assign(*command.Probes)
		r.logger.Info("probes assigned", "count", len(command.Probes.Probes), "refused", refused)
	case agentwire.KindImages:
		// Les exclusions remplacent les précédentes ; « maintenant » lance un
		// passage complet.
		r.logger.Info("image checks assigned", "excluded", len(command.Images.Excluded), "now", command.Images.Now)
		r.updates.Assign(*command.Images)
	}
}

func (r *commandRunner) startLogs(request container.LogRequest) {
	ctx, cancel := context.WithCancel(r.ctx)
	r.mu.Lock()
	r.open[request.ID] = cancel
	r.mu.Unlock()
	go func() {
		defer r.stopLogs(request.ID)
		r.serveLogs(ctx, request)
	}()
}

func (r *commandRunner) stopLogs(requestID string) {
	r.mu.Lock()
	cancel, ok := r.open[requestID]
	delete(r.open, requestID)
	r.mu.Unlock()
	if ok {
		cancel()
	}
}

// serveLogs fait suivre chaque lot au serveur ; un serveur qui ne veut
// plus de la requête (404) arrête le suivi.
func (r *commandRunner) serveLogs(ctx context.Context, request container.LogRequest) {
	batches, err := r.logs.OpenLogs(ctx, request)
	if err != nil {
		// Clé figée : les agents installés l'envoient sous « service. ».
		_ = r.client.PostLogs(ctx, r.session, request.ID, container.LogBatch{Error: "service.logs_failed"})
		return
	}
	for batch := range batches {
		if err := r.client.PostLogs(ctx, r.session, request.ID, batch); err != nil {
			var refused *ServerError
			if errors.As(err, &refused) && refused.Status == http.StatusNotFound {
				return
			}
			if ctx.Err() == nil {
				r.logger.Warn("post logs", "request", request.ID, "error", err)
			}
			return
		}
	}
}
