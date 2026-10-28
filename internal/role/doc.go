// Package role reconnaît ce qu'une machine est, à des signes locaux lisibles
// sans droit particulier (aujourd'hui : un hôte Proxmox VE), et garde le
// dernier rapport de son agent. Un rôle détecté n'ouvre aucun droit, n'active
// aucun ordre et ne déclare rien : il se montre, l'opérateur décide.
package role
