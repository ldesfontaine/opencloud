// Package probe vérifie depuis l'extérieur qu'une URL ou qu'un port
// répond : une sonde par cible, sur son propre intervalle, exécutée par
// l'agent de la machine qui la porte. Il ne parle ni HTTP d'openCloud ni
// SQL : le serveur pousse le jeu de sondes et reçoit les essais, le store
// les écrit. Les alertes viendront lire les changements d'état ; le
// certificat vu attend la fonctionnalité certificats.
package probe
