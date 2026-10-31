// Package verbrequest tient les demandes de verbe à confirmation : un jeton
// d'API qui appelle un geste qui ne se défait pas ne l'exécute jamais, il
// dépose une demande que l'opérateur confirme ou refuse dans l'interface.
// Le paquet ne rejoue rien : la route d'opérateur reste au serveur, qui la
// rappelle une fois la demande confirmée.
package verbrequest
