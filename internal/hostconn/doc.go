// Package hostconn relève les connexions TCP de l'hôte, sans droit élevé
// (étude services-modèle §2.3, signal 9, décision 5) : la table
// /proc/net/tcp de l'espace de noms réseau de l'agent, lisible par tout
// compte. Le rapport ne garde que le port joint et qui le joint (un compte de
// l'hôte, une adresse du réseau local, ou « Internet ») : jamais un processus,
// un port d'origine, une adresse publique ni un octet de la connexion.
//
// Les connexions entre conteneurs n'y sont pas : un conteneur a son propre
// espace de noms réseau, et y entrer demande CAP_SYS_PTRACE.
package hostconn
