// Package proxysecret lit le secret que le mandataire d'authentification
// partage avec le serveur. En mode forward, une adresse de confiance ne
// prouve pas que la requête vient du mandataire (la boucle locale est celle
// de tous les processus de la machine) : le secret, posé par le mandataire
// dans un en-tête que le client ne choisit pas, le prouve.
package proxysecret
