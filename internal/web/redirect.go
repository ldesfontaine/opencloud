package web

import "net/http"

// redirect envoie le navigateur ailleurs. Une requête htmx suivrait un 303
// par fetch et collerait la page dans la cible : on lui demande une vraie
// navigation par HX-Redirect. Les redirections ici changent des cookies, une
// page entière est ce qu'il faut.
func redirect(w http.ResponseWriter, r *http.Request, location string) {
	if r.Header.Get("HX-Request") == "true" {
		w.Header().Set("HX-Redirect", location)
		w.WriteHeader(http.StatusOK)
		return
	}
	http.Redirect(w, r, location, http.StatusSeeOther)
}
