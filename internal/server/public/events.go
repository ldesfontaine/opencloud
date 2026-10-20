package public

import (
	"context"
	"net/http"

	"github.com/ldesfontaine/opencloud/internal/server/httpx"
	"github.com/ldesfontaine/opencloud/internal/server/sse"
)

// GET /statut/api/events : le direct public, sur son propre bus, qui ne
// porte que « status ». Plafonné à part des onglets de l'administration, par
// source d'abord : la part d'une source se vérifie avant de toucher au pool
// global, pour qu'un refus ne coûte une place à personne.
func (h *Handler) serveEvents(w http.ResponseWriter, r *http.Request) {
	address := h.clientAddress(r)
	if !h.streamLimit.Allow(address) {
		httpx.WriteTooMany(w)
		return
	}
	if !h.tabsBySource.Take(address) {
		h.logger.Warn("public status streams limit reached for this source", "source", address)
		httpx.WriteTooMany(w)
		return
	}
	defer h.tabsBySource.Release(address)
	if !h.tabs.Take() {
		http.Error(w, "busy", http.StatusServiceUnavailable)
		return
	}
	defer h.tabs.Release()
	// Fin propre à l'échéance : la réponse se termine, EventSource se reconnecte.
	ctx, cancel := context.WithTimeout(r.Context(), h.streamLifetime)
	defer cancel()
	sse.Stream(w, r.WithContext(ctx), h.live.Subscribe(), nil)
}
