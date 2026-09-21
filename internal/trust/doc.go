// Package trust tient les autorités racines contre lesquelles une chaîne
// TLS se vérifie : celles du système, plus celle que l'opérateur ajoute.
// Il ne compose vers personne et ne lit aucun certificat de cible ; il rend
// un jeu de racines, les sondes s'en servent.
package trust
