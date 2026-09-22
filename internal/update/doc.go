// Package update sait si l'image d'un service a une version plus
// récente. L'agent d'une machine interroge le registre de chaque image
// de ses conteneurs et rapporte des faits : l'empreinte tirée, celle que
// le tag pointe aujourd'hui, le tag plus récent écrit de la même façon.
// Le serveur les écrit, en déduit le type de mise à jour, fabrique la
// commande à copier et tient la politique de chaque service. Il ne
// tire ni ne relance jamais rien : il observe.
package update
