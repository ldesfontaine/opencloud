// Package cloudflare parle à l'API v4 de Cloudflare, et à elle seule :
// vérifier un jeton, trouver une zone par son nom, lister, créer et supprimer
// un enregistrement DNS. Bibliothèque standard uniquement.
//
// Le jeton n'est jamais journalisé ni recopié dans une erreur : il passe dans
// l'en-tête Authorization, et nulle part ailleurs.
package cloudflare
