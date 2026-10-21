// Package audit tient le journal de qui a fait quoi : l'acteur, posé dans le
// contexte par celui qui le connaît (le serveur, MCP, la commande, une boucle,
// un agent), et le Recorder qui écrit un événement par geste. Il ne juge rien
// et ne lit aucun domaine : chaque composant dit ce qu'il vient d'écrire, sans
// secret. Un événement ne se modifie jamais ; il ne part qu'avec l'âge.
package audit
