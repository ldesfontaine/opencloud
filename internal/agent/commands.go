package agent

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"sync"

	"github.com/ldesfontaine/opencloud/internal/probe"
	"github.com/ldesfontaine/opencloud/internal/service"
)

// commandRunner exécute ce que le serveur pousse par le flux : ouvrir et
// fermer un suivi de journal, remplacer le jeu de sondes. Chaque suivi vit
// dans sa propre goroutine et meurt avec le flux ; les sondes, elles,
// survivent aux reconnexions, leur moteur vit avec l'agent.
type commandRunner struct {
	ctx     context.Context
	client  *Client
	session string
	logs    service.LogSource
	probes  probe.Assignable
	logger  *slog.Logger

	mu   sync.Mutex
	open map[string]context.CancelFunc
}

func newCommandRunner(ctx context.Context, client *Client, session string, logs service.LogSource, probes probe.Assignable, logger *slog.Logger) *commandRunner {
	return &commandRunner{ctx: ctx, client: client, session: session, logs: logs, probes: probes, logger: logger, open: make(map[string]context.CancelFunc)}
}

func (r *commandRunner) handle(name, data string) {
	switch name {
	case service.CommandLogs:
		r.withLogRequest(name, data, r.startLogs)
	case service.CommandLogsStop:
		r.withLogRequest(name, data, func(request service.LogRequest) { r.stopLogs(request.ID) })
	case probe.CommandProbes:
		r.assignProbes(name, data)
	default:
		r.logger.Warn("unknown command", "name", name)
	}
}

func (r *commandRunner) withLogRequest(name, data string, apply func(service.LogRequest)) {
	var request service.LogRequest
	if err := json.Unmarshal([]byte(data), &request); err != nil || request.ID == "" {
		r.logger.Warn("bad command", "name", name)
		return
	}
	apply(request)
}

// Le jeu de sondes remplace le précédent : ce qui n'y est plus s'arrête.
func (r *commandRunner) assignProbes(name, data string) {
	var assignment probe.Assignment
	if err := json.Unmarshal([]byte(data), &assignment); err != nil {
		r.logger.Warn("bad command", "name", name)
		return
	}
	r.logger.Info("probes assigned", "count", len(assignment.Probes))
	r.probes.Assign(assignment)
}

func (r *commandRunner) startLogs(request service.LogRequest) {
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
func (r *commandRunner) serveLogs(ctx context.Context, request service.LogRequest) {
	batches, err := r.logs.OpenLogs(ctx, request)
	if err != nil {
		_ = r.client.PostLogs(ctx, r.session, request.ID, service.LogBatch{Error: "service.logs_failed"})
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
