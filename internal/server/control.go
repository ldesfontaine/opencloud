package server

import (
	"context"
	"net/http"
	"strings"
)

// controlPrefix est tout ce que la socket de commande sert : les routes du
// fichier de l'annuaire.
const controlPrefix = "/api/manifest/"

// ControlHandler est ce que serve sert sur state_dir/control.sock. Qui
// atteint la socket peut déjà lire la base (0600 dans un dossier 0700), comme
// pour « auth » : pas de garde d'opérateur ni de garde même-origine, aucun
// navigateur n'y passe. Rien d'autre que le fichier de l'annuaire n'y est
// servi.
func (s *Server) ControlHandler() http.Handler {
	return s.controlHandler
}

// controlMux n'enregistre que les routes de controlPrefix, chacune avec sa
// méthode : le motif sans méthode ouvrirait d'un coup toutes les méthodes sur
// une porte qui n'a ni garde d'opérateur ni garde même-origine. N'y ajoutez
// pas une route sans méthode, ni une route qui écrit sans y avoir réfléchi.
func (s *Server) controlMux() *http.ServeMux {
	mux := http.NewServeMux()
	for _, route := range s.routes() {
		if !strings.HasPrefix(route.pattern, controlPrefix) || route.method == anyMethod {
			continue
		}
		mux.HandleFunc(route.method+" "+route.pattern, markControlSocket(route.handler))
	}
	mux.HandleFunc("/", s.apiUnknown)
	return mux
}

// markControlSocket dit au gestionnaire qu'il répond à la commande locale :
// l'acteur d'un apply en dépend, et seule cette porte peut le dire.
func markControlSocket(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		next(w, r.WithContext(context.WithValue(r.Context(), controlSocketKey{}, struct{}{})))
	}
}
