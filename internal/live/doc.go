// Package live est le bus des changements que l'interface doit voir tout de
// suite : un sujet (machines, tâches, ressources, services, sondes, statut) publié par un composant, reçu par
// chaque onglet abonné. Il ne garde rien : un abonné lent voit ses sujets
// fusionnés, jamais perdus, et rien n'est rejoué après une coupure.
package live
