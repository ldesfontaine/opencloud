package public

import (
	"net/http"

	"github.com/ldesfontaine/opencloud/internal/status"
)

// GET /statut : la seule page s'il n'y en a qu'une, l'adresse d'avant les
// pages multiples ; sinon 404. Jamais une liste : une page ne révèle pas
// les autres.
func (h *Handler) serveRoot(w http.ResponseWriter, r *http.Request) {
	pages, err := h.status.ListPages(r.Context())
	if err != nil {
		h.logger.Error("status pages", "error", err)
		http.Error(w, "error", http.StatusInternalServerError)
		return
	}
	if len(pages) != 1 {
		http.NotFound(w, nil)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	http.Redirect(w, r, pathOf(pages[0]), http.StatusFound)
}

// pathOf est l'adresse publique d'une page, sous /statut.
func pathOf(page status.Page) string {
	return "/statut/" + page.Slug
}

// GET /statut/<slug> : la page, sans rien de l'administration. Le front lit
// le slug dans l'adresse.
func (h *Handler) servePage(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.pageOf(w, r); !ok {
		return
	}
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
