// Package container porte les conteneurs d'openCloud : ce qui tourne sur une
// machine. Aujourd'hui, un conteneur Docker que l'agent de la machine
// découvre et suit ; le modèle laisse la place à d'autres genres. Il reçoit
// ce que l'agent rapporte (inventaire, événements, mesures), écrit les
// fiches, les transitions et les échantillons, et relaie les journaux à la
// demande. Il ne parle ni HTTP, ni SQL, ni Docker. Un « service », lui,
// est celui de l'annuaire : voir le package directory.
package container
