// Package link garde les liens entre instances : qui parle à qui, d'où vient
// la preuve, et quand le lien a été vu pour la dernière fois. Une extrémité
// est une instance (une machine et une clé stable), jamais un identifiant
// Docker. Le paquet ne parle ni HTTP ni SQL : le serveur l'expose, le store
// l'écrit, l'annuaire et les conteneurs lui disent quoi écrire.
package link
