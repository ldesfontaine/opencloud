package public

import (
	"context"
	"errors"
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

// Ce que la page attend du composant statut : la page à une adresse (sa
// langue), la liste des pages pour /statut seul, et l'instantané public
// d'une page.
type StatusReader interface {
	PageBySlug(ctx context.Context, slug string) (status.Page, error)
	ListPages(ctx context.Context) ([]status.Page, error)
	Snapshot(ctx context.Context, pageID string) (status.Snapshot, error)
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

// Routes déclare les routes des pages publiques, une par slug. Le serveur
// les monte à leur place dans son arbre, que server/routes_test.go fige.
func (h *Handler) Routes(add func(method, pattern string, handler http.HandlerFunc)) {
	add("GET", "/statut", h.limitRead(h.serveRoot))
	add("GET", "/statut/{$}", h.limitRead(h.serveRoot))
	add("GET", "/statut/{slug}", h.limitRead(h.servePage))
	add("GET", "/statut/{slug}/{$}", h.limitRead(h.servePage))
	add("GET", "/statut/{slug}/api/status", h.limitRead(h.serveStatus))
	add("GET", "/statut/{slug}/api/i18n", h.limitRead(h.serveCatalog))
	add("GET", "/statut/{slug}/api/events", h.serveEvents)
	add(anyMethod, "/statut/", h.serveUnknown)
}

// pageOf rend la page de l'adresse demandée. Un slug inconnu répond 404,
// comme tout chemin inconnu sous /statut : rien ne dit qu'une autre page
// existe. Faux : la réponse est écrite.
func (h *Handler) pageOf(w http.ResponseWriter, r *http.Request) (status.Page, bool) {
	page, err := h.status.PageBySlug(r.Context(), r.PathValue("slug"))
	if errors.Is(err, status.ErrNotFound) {
		http.NotFound(w, nil)
		return status.Page{}, false
	}
	if err != nil {
		h.logger.Error("status page", "error", err)
		http.Error(w, "error", http.StatusInternalServerError)
		return status.Page{}, false
	}
	return page, true
}
