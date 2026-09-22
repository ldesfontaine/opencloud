// Package alert transforme ce que les composants constatent en alertes :
// un fait typé par objet, dédupliqué, aggravé ou résolu, tu par un silence
// ou une maintenance, acquitté par l'opérateur, et livré aux canaux par
// webhook. Il ne parle ni HTTP d'openCloud ni SQL : les composants lui
// donnent des faits, le store écrit, le serveur sert la page.
package alert
