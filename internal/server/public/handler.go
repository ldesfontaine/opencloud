package public

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/ldesfontaine/opencloud/internal/lang"
	"github.com/ldesfontaine/opencloud/internal/live"
	"github.com/ldesfontaine/opencloud/internal/ratelimit"
	"github.com/ldesfontaine/opencloud/internal/status"
)

// La méthode « * » du montage de la racine : toutes, pour un fourre-tout.
const anyMethod = "*"

// Ce que la page attend du composant statut : l'instantané public et les
// réglages de la page (sa langue).
type StatusReader interface {
	Snapshot(ctx context.Context) (status.Snapshot, error)
	Page(ctx context.Context) (status.Page, error)
}

// Ce que le direct public attend du bus : un abonnement par visiteur. Ce
// bus ne porte que « status », à part de celui des onglets de
// l'administration.
type Live interface {
	Subscribe() *live.Subscription
}

type Options struct {
	Logger *slog.Logger
	Status StatusReader
	// Catalogs sert les chaînes de la page, dans sa langue.
	Catalogs lang.Catalogs
	Live     Live
	// Page rend la page HTML du front compilé, prête à servir (nom du
	// produit posé) : le front et sa livraison vivent à la racine.
	Page func() ([]byte, error)
	// ClientAddress rend l'adresse du client telle que le serveur la résout,
	// mandataires de confiance compris : la règle vit à la racine.
	ClientAddress func(r *http.Request) string
	// Clock remplace l'horloge des limiteurs, pour les tests ; nil = time.Now.
	Clock func() time.Time
}

type Handler struct {
	logger        *slog.Logger
	status        StatusReader
	catalogs      lang.Catalogs
	live          Live
	page          func() ([]byte, error)
	clientAddress func(r *http.Request) string
	// Les lectures et les ouvertures de flux, chacune son seau par source.
	readLimit   *ratelimit.BySource
	streamLimit *ratelimit.BySource
	// Les visiteurs en direct, et la part de chaque source dans ce pool.
	tabs         *ratelimit.Slots
	tabsBySource *ratelimit.SourceSlots
	// La durée maximale d'un flux public.
	streamLifetime time.Duration
}

func New(opts Options) *Handler {
	handler := &Handler{
		logger:         opts.Logger,
		status:         opts.Status,
		catalogs:       opts.Catalogs,
		live:           opts.Live,
		page:           opts.Page,
		clientAddress:  opts.ClientAddress,
		readLimit:      ratelimit.NewBySource(statusPerSourceRate, statusPerSourceBurst, statusIdle),
		streamLimit:    ratelimit.NewBySource(statusStreamRate, statusStreamBurst, statusIdle),
		tabs:           ratelimit.NewSlots(maxPublicSubscribers),
		tabsBySource:   ratelimit.NewSourceSlots(maxPublicStreamsPerSource),
		streamLifetime: maxPublicStreamLifetime,
	}
	if opts.Clock != nil {
		handler.readLimit.SetClock(opts.Clock)
		handler.streamLimit.SetClock(opts.Clock)
	}
	return handler
}

// Routes déclare les routes de la page publique. Le serveur les monte à leur
// place dans son arbre, que server/routes_test.go fige.
func (h *Handler) Routes(add func(method, pattern string, handler http.HandlerFunc)) {
	add("GET", "/statut", h.limitRead(h.servePage))
	add("GET", "/statut/{$}", h.limitRead(h.servePage))
	add("GET", "/statut/api/status", h.limitRead(h.serveStatus))
	add("GET", "/statut/api/i18n", h.limitRead(h.serveCatalog))
	add("GET", "/statut/api/events", h.serveEvents)
	add(anyMethod, "/statut/", h.serveUnknown)
}
