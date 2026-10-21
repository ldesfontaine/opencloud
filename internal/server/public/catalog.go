package public

import (
	"net/http"
	"strings"
)

// Les seules clés de catalogue qui sortent en public.
const statusKeyPrefix = "status."

// GET /statut/<slug>/api/i18n : les chaînes de la page dans sa langue, et
// rien d'autre du catalogue.
func (h *Handler) serveCatalog(w http.ResponseWriter, r *http.Request) {
	page, ok := h.pageOf(w, r)
	if !ok {
		return
	}
	catalog := h.catalogs.For(page.Language)
	strings := map[string]string{}
	for _, key := range catalog.Keys() {
		if hasStatusPrefix(key) {
			strings[key] = catalog.Get(key)
		}
	}
	h.writeJSON(w, http.StatusOK, strings)
}

func hasStatusPrefix(key string) bool {
	return strings.HasPrefix(key, statusKeyPrefix)
}
