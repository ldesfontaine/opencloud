// Package egress est la garde de sortie d'openCloud : tout ce que le
// serveur ou l'agent compose vers une adresse donnée par l'opérateur, une
// cible de sonde ou l'URL d'un canal, passe par lui. Il résout lui-même,
// écarte le lien-local, puis compose sur l'adresse retenue ; la boucle
// locale et les adresses privées restent ouvertes, c'est l'usage voulu.
package egress
