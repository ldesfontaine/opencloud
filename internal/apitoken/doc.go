// Package apitoken tient les jetons d'API : ce qu'un programme présente en
// Authorization: Bearer pour lire l'API d'openCloud (mcp-maison, un script).
// Il crée, liste, révoque, vérifie et purge ; il ne sait rien de HTTP : le
// serveur dit quelles routes un jeton ouvre.
package apitoken
