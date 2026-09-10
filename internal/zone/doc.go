// Package zone tient les zones Cloudflare d'openCloud : une ligne en base par
// zone, son jeton dans un fichier à permissions strictes, et la table des
// machines qui portent ce jeton.
//
// Le jeton est écrit une fois et jamais réaffiché : le package le rend au
// catalogue, qui le dépose dans le fichier d'une action, et à personne
// d'autre. Il ne lance rien : c'est l'action « Poser le jeton DNS » qui pose,
// et l'observateur qui note ce qu'elle a conclu.
package zone
