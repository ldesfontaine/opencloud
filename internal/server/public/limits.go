package public

import (
	"net/http"
	"time"

	"github.com/ldesfontaine/opencloud/internal/server/httpx"
)

// La page de statut est servie par le même binaire sur le même port, sans
// authentification. Toutes ses routes sont limitées par adresse résolue,
// comme /ping.
const (
	statusPerSourceRate  = 10
	statusPerSourceBurst = 20
	// Ouvrir un flux coûte plus qu'une lecture : une par seconde suffit à
	// un navigateur qui revient, cinq d'un coup à un cadre qui recharge.
	statusStreamRate  = 1
	statusStreamBurst = 5
	statusIdle        = 10 * time.Minute
	// Les visiteurs en direct, à part des onglets de l'administration.
	maxPublicSubscribers = 256
	// Ce qu'une même source (une IPv4, un /64 en IPv6) en tient à la fois :
	// quelques onglets et un cadre, pas le pool. Sans ce plafond, une adresse
	// qui ouvre un flux par seconde le vide en quatre minutes.
	maxPublicStreamsPerSource = 4
	// Un flux public se termine de lui-même : le navigateur se reconnecte
	// seul au bout de cinq secondes, et la place retourne au pool au lieu de
	// rester à une connexion que personne ne lit.
	maxPublicStreamLifetime = 10 * time.Minute
)

// limitRead compte la requête sur le seau de sa source et répond 429
// au-delà ; le visiteur reçoit un mot, jamais une clé de catalogue.
func (h *Handler) limitRead(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !h.readLimit.Allow(h.clientAddress(r)) {
			httpx.WriteTooMany(w)
			return
		}
		next(w, r)
	}
}
