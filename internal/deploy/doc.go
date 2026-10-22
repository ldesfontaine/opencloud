// Package deploy déploie un service déclaré d'origine Compose sur sa machine :
// il génère le compose.yml depuis l'annuaire, vérifie ce qui peut l'être côté
// serveur et envoie l'ordre signé. Il ne garde jamais l'.env.
package deploy
