// Package tlsinspect relève le certificat qu'une adresse https présente,
// sans clé ni identifiant : un handshake TLS et rien d'autre. Il sert à dire
// à l'opérateur, avant de déclarer un pare-feu ou un hôte Proxmox, si
// l'adresse est joignable et quelle est l'empreinte à confirmer.
package tlsinspect
