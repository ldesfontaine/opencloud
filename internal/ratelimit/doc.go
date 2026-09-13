// Package ratelimit borne le débit de requêtes par clé, une IP ou un jeton,
// avec un seau à jetons en mémoire. Il ne sait rien de HTTP : le web
// choisit la clé et répond 429. Les seaux oubliés sont balayés.
package ratelimit
