// Package ratelimit borne le débit par clé (une IP, un jeton) avec un seau à
// jetons en mémoire, et les places qu'un flux tient à la fois. Il ne sait rien
// de HTTP : le web choisit la clé et répond 429.
package ratelimit
