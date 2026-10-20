package public

import "net/http"

// GET /statut : la page, sans rien de l'administration.
func (h *Handler) servePage(w http.ResponseWriter, r *http.Request) {
	page, err := h.page()
	if err != nil {
		h.logger.Error("status page not built", "error", err)
		http.Error(w, "front not built: run make front", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	if _, err := w.Write(page); err != nil {
		h.logger.Warn("write status page", "error", err)
	}
}

// Tout autre chemin sous /statut est un 404 court : pas de page 404 de
// l'administration ici.
func (h *Handler) serveUnknown(w http.ResponseWriter, _ *http.Request) {
	http.NotFound(w, nil)
}
