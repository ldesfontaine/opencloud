package server

import (
	"net/http"

	"github.com/ldesfontaine/opencloud/internal/server/sse"
)

const (
	maxEventSubscribers = 64
	codeBusy            = "busy"
)

// Le direct du navigateur : une connexion par onglet, qui reçoit le nom de
// ce qui a changé (machines, jobs) et rien d'autre ; le front relit. Un
// identifiant sur chaque événement fait que le navigateur revient avec
// Last-Event-ID : on lui dit alors « reconnected » et il relit tout, car rien
// n'est rejoué. Un commentaire toutes les 15 s tient la connexion ouverte
// derrière Traefik. Sans authentification, comme le reste de l'interface :
// le plafond d'abonnés empêche seulement d'épuiser le serveur. Il compte
// les onglets, pas les abonnés du bus : la page de statut y est abonnée
// aussi, et prendrait sinon une place.
func (s *Server) events(w http.ResponseWriter, r *http.Request) {
	if !s.eventTabs.Take() {
		s.writeAPIError(w, http.StatusServiceUnavailable, codeBusy)
		return
	}
	defer s.eventTabs.Release()
	// Le flux n'est jugé qu'à l'ouverture : si l'authentification a changé
	// depuis, il se ferme au ping suivant et le navigateur revient se faire
	// juger.
	sse.Stream(w, r, s.live.Subscribe(), func() bool { return s.authorizationLapsed(r) })
}
