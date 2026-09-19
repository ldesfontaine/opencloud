// Package service porte les services d'openCloud : ce qui tourne sur une
// machine. Aujourd'hui un service est un conteneur Docker que l'agent de la
// machine découvre et suit ; le modèle laisse la place à d'autres genres.
// Il reçoit ce que l'agent rapporte (inventaire, événements, mesures),
// écrit les fiches, les transitions et les échantillons, et relaie les
// journaux à la demande. Il ne parle ni HTTP, ni SQL, ni Docker.
package service
