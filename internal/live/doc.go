// Package live est le bus des changements que l'interface doit voir tout de
// suite : un sujet (machines, tâches, ressources, conteneurs, sondes, statut,
// alertes, annuaire) publié par un composant, reçu par chaque onglet abonné.
// Il ne garde rien : un abonné lent voit ses sujets fusionnés, jamais perdus,
// et rien n'est rejoué après une coupure. Les mesures, seul sujet qu'une
// machine au repos publie en continu, partent au plus une fois par 30 s.
package live
