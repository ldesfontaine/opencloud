// Package agent est ce qui tourne sur une machine gérée : il s'enrôle une
// fois avec un jeton, garde son identité Ed25519 sur disque, tient un flux
// ouvert vers openCloud et lui donne signe de vie. Il mesure la machine
// toutes les 10 s et livre ses lectures avec chaque signal ; entre deux,
// elles attendent dans un tampon d'une heure au plus.
package agent
