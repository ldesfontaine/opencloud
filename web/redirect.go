package web

import "net/http"

// Après une action, on revient à une page. Une requête HTMX ne suit pas une
// redirection classique : elle reçoit l'en-tête HX-Redirect et navigue.
func (s *Server) redirect(w http.ResponseWriter, r *http.Request, target string) {
	if r.Header.Get("HX-Request") == "true" {
		w.Header().Set("HX-Redirect", target)
		w.WriteHeader(http.StatusOK)
		return
	}
	http.Redirect(w, r, target, http.StatusSeeOther) // #nosec G710 -- les cibles commencent toutes par « / » : un chemin local, jamais un hôte.
}
