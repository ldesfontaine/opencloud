// Package alert transforme ce que les composants constatent en alertes :
// un fait typé par objet, dédupliqué, aggravé ou résolu, tu par une fenêtre
// jugée à chaque envoi, acquitté par l'opérateur, et livré aux canaux par
// webhook, essai après essai jusqu'à un jour. Il ne parle ni HTTP
// d'openCloud ni SQL, et ne lit pas la page de statut : les composants lui
// donnent des faits, le store écrit, le serveur sert la page.
package alert
