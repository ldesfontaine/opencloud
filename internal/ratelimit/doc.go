// Package ratelimit borne le débit par clé avec un seau à jetons en mémoire,
// balayé au passage, et les places qu'un flux tient à la fois. Une adresse
// cliente passe par BySource ou SourceSlots, qui la ramènent à sa source (une
// IPv4, un /64 en IPv6). Il ne sait rien de HTTP : le web répond 429.
package ratelimit
