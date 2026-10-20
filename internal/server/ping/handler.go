package ping

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/ldesfontaine/opencloud/internal/heartbeat"
)

// Ce que les pings attendent du composant heartbeat : recevoir un ping.
type HeartbeatReceiver interface {
	Receive(ctx context.Context, token string, ping heartbeat.Ping) (heartbeat.Heartbeat, error)
}

type Options struct {
	Logger     *slog.Logger
	Heartbeats HeartbeatReceiver
	// ClientAddress rend l'adresse du client telle que le serveur la résout,
	// mandataires de confiance compris : la règle vit à la racine.
	ClientAddress func(r *http.Request) string
	// Clock remplace l'horloge du limiteur, pour les tests ; nil = time.Now.
	Clock func() time.Time
}

type Handler struct {
	logger        *slog.Logger
	heartbeats    HeartbeatReceiver
	clientAddress func(r *http.Request) string
	limits        *pingLimits
}

func New(opts Options) *Handler {
	handler := &Handler{
		logger:        opts.Logger,
		heartbeats:    opts.Heartbeats,
		clientAddress: opts.ClientAddress,
		limits:        newPingLimits(),
	}
	if opts.Clock != nil {
		handler.limits.setClock(opts.Clock)
	}
	return handler
}

// Routes déclare les routes des pings. Le serveur les monte à leur place
// dans son arbre, que server/routes_test.go fige.
func (h *Handler) Routes(add func(method, pattern string, handler http.HandlerFunc)) {
	add("GET", "/ping/{token}", h.finish)
	add("POST", "/ping/{token}", h.finish)
	add("GET", "/ping/{token}/start", h.start)
	add("POST", "/ping/{token}/start", h.start)
	add("GET", "/ping/{token}/{code}", h.exitCode)
	add("POST", "/ping/{token}/{code}", h.exitCode)
}
