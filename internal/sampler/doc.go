// Package sampler mesure la machine où il tourne : processeur, charge,
// mémoire, swap, débit réseau, lus dans /proc, et chaque volume réel monté,
// par statfs. Il ne dépend de rien d'autre : l'agent et le serveur s'en
// servent tous deux. Il ne garde que les compteurs précédents, pour les
// deltas.
package sampler
