// Package restic orchestre les sauvegardes restic vers un rest-server en
// écriture seule : la politique des routes REST, le choix du chemin (direct
// TLS épinglé ou relais du serveur), le relais lui-même, la commande restic
// et le fichier htpasswd. Ni SQL, ni flux SSE : il reçoit ce qu'il pousse et
// ce qu'il authentifie par des interfaces.
package restic
