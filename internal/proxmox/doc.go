// Package proxmox lit l'API d'un hôte Proxmox déclaré sur une machine, avec un
// jeton en lecture seule : les VM et conteneurs, les stockages, la dernière
// sauvegarde connue de chaque VM, rapprochés des machines enrôlées. Il ne
// donne aucun ordre à Proxmox, n'en tire aucune alerte et ne garde que le
// dernier relevé réussi.
package proxmox
