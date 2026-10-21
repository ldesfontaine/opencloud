// Package action envoie les ordres aux agents : un run écrit avant de
// partir, signé par la clé du serveur (server.key), suivi jusqu'à la réponse
// de l'agent ou son expiration. La liste des ordres et leur enveloppe sont
// au protocole (agentwire) ; les commandes d'observation n'y passent pas.
package action
