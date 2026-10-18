// Package manifest est le fichier YAML de l'annuaire : le format, sa lecture
// stricte, son écriture déterministe et l'export de ce que l'opérateur a
// déclaré. Le fichier ne porte jamais un secret ni un état (pause, « à
// vérifier », historique). Il ne parle ni HTTP ni SQL ; le serveur et la
// commande l'exposent, les composants du domaine fournissent les données.
package manifest
