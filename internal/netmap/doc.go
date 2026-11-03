// Package netmap dessine la carte du réseau de la vue d'ensemble : l'arbre
// hôte > machines > services, les zones que les données laissent déduire, les
// liens stockés entre services et les entrées d'Internet. Il ne lit ni la base
// ni les API : le serveur lui donne des faits, il rend un arbre. Aucune zone,
// aucun nom, aucune adresse n'y est écrit en dur.
package netmap
