package agentapi

import "time"

// Un agent demande un défi par connexion, et revient au plus toutes les
// quelques secondes quand le serveur le coupe : une par seconde, dix d'un
// coup, laisse passer une flotte derrière la même adresse (ou le même /64
// en IPv6, voir ratelimit.BySource) qui redémarre : l'agent recule seul,
// d'une à soixante secondes, quand il est refusé.
// Au-delà, c'est quelqu'un qui cherche des identifiants de machine ou qui
// remplit la table des défis. Derrière Traefik sans trusted_proxies, tous
// les agents partagent un seul seau.
const (
	challengePerSourceRate  = 1
	challengePerSourceBurst = 10
	challengeIdle           = 10 * time.Minute
)
