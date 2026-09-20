// Package dockerapi parle au démon Docker de la machine où il tourne, par sa
// socket Unix et l'API Engine épinglée à une version. Lecture seule par
// construction : lister et inspecter les conteneurs, lister les réseaux,
// suivre les événements, lire une mesure et les journaux d'un conteneur.
// Aucun verbe qui change quelque chose.
// Il ne dépend que de la bibliothèque standard.
package dockerapi
