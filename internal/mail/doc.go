// Package mail envoie un message par le relais SMTP que l'opérateur a
// choisi : openCloud n'est jamais un serveur de courrier et ne gère aucune
// réputation d'envoi. Il compose le message, compose vers le relais par la
// garde de sortie, et traduit un refus en une erreur que l'appelant classe.
package mail
