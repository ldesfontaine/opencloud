// Package agent est ce qui tourne sur une machine gérée : il s'enrôle une
// fois avec un jeton, garde son identité Ed25519 sur disque, tient un flux
// ouvert vers openCloud et lui donne signe de vie. Il ne mesure rien encore :
// les fonctionnalités suivantes lui apprendront quoi envoyer.
package agent
