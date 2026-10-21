package agentapi

import "time"

// Les deux routes ouvertes de l'agent, l'enrôlement et l'ouverture du flux,
// partagent un seau par source. Un agent ouvre son flux une fois par
// connexion, et revient au plus toutes les quelques secondes quand le
// serveur le coupe : une par seconde, dix d'un coup, laisse passer une
// flotte derrière la même adresse (ou le même /64 en IPv6, voir
// ratelimit.BySource) qui redémarre ; l'agent recule seul, d'une à soixante
// secondes, quand il est refusé. Au-delà, c'est quelqu'un qui essaie des
// preuves ou des jetons : chaque essai coûte une lecture de la base et une
// vérification de signature. Derrière Traefik sans trusted_proxies, tous
// les agents partagent un seul seau.
const (
	perSourceRate  = 1
	perSourceBurst = 10
	sourceIdle     = 10 * time.Minute
)
